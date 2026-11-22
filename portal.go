package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// xdg-desktop-portal: native file chooser / save dialog and the
// "open with" app chooser of whatever desktop is running.

const (
	portalDest = "org.freedesktop.portal.Desktop"
	portalPath = "/org/freedesktop/portal/desktop"
)

var errCancelled = errors.New("cancelled")

type Portal struct{ conn *dbus.Conn }

func NewPortal() *Portal {
	c, err := dbus.SessionBus()
	if err != nil {
		return nil
	}
	// is a FileChooser implementation actually there?
	var v dbus.Variant
	err = c.Object(portalDest, portalPath).Call("org.freedesktop.DBus.Properties.Get", 0,
		"org.freedesktop.portal.FileChooser", "version").Store(&v)
	if err != nil {
		return nil
	}
	return &Portal{conn: c}
}

// request calls a portal method that answers through a Request object and
// waits for its Response signal.
func (p *Portal) request(method string, opts map[string]dbus.Variant, args ...any) (map[string]dbus.Variant, error) {
	token := fmt.Sprintf("whatsup%d", rand.Uint32())
	opts["handle_token"] = dbus.MakeVariant(token)
	sender := strings.ReplaceAll(strings.TrimPrefix(p.conn.Names()[0], ":"), ".", "_")
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)

	// subscribe before calling, so the response can't be missed
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response")}
	if err := p.conn.AddMatchSignal(match...); err != nil {
		return nil, err
	}
	defer p.conn.RemoveMatchSignal(match...)
	ch := make(chan *dbus.Signal, 4)
	p.conn.Signal(ch)
	defer p.conn.RemoveSignal(ch)

	var handle dbus.ObjectPath
	if err := p.conn.Object(portalDest, portalPath).Call(method, 0, append(args, opts)...).Store(&handle); err != nil {
		return nil, err
	}
	timeout := time.After(30 * time.Minute)
	for {
		select {
		case s := <-ch:
			if s.Path != path && s.Path != handle || len(s.Body) < 2 {
				continue
			}
			code, _ := s.Body[0].(uint32)
			res, _ := s.Body[1].(map[string]dbus.Variant)
			if code != 0 {
				return nil, errCancelled
			}
			return res, nil
		case <-timeout:
			return nil, errCancelled
		}
	}
}

func uris(res map[string]dbus.Variant) []string {
	var out []string
	if v, ok := res["uris"]; ok {
		v.Store(&out)
	}
	return out
}

// OpenFiles shows the native file chooser; returns file:// URLs.
func (p *Portal) OpenFiles(title string) ([]string, error) {
	res, err := p.request("org.freedesktop.portal.FileChooser.OpenFile", map[string]dbus.Variant{
		"multiple": dbus.MakeVariant(true),
		"modal":    dbus.MakeVariant(true),
	}, "", title)
	if err != nil {
		return nil, err
	}
	return uris(res), nil
}

// SaveFile asks where to save a file called name; returns the chosen path.
func (p *Portal) SaveFile(title, name, folder string) (string, error) {
	opts := map[string]dbus.Variant{
		"current_name": dbus.MakeVariant(name),
		"modal":        dbus.MakeVariant(true),
	}
	if folder != "" {
		opts["current_folder"] = dbus.MakeVariant(append([]byte(folder), 0))
	}
	res, err := p.request("org.freedesktop.portal.FileChooser.SaveFile", opts, "", title)
	if err != nil {
		return "", err
	}
	u := uris(res)
	if len(u) == 0 {
		return "", errCancelled
	}
	pu, err := url.Parse(u[0])
	if err != nil || pu.Scheme != "file" {
		return "", fmt.Errorf("unsupported location %q", u[0])
	}
	return pu.Path, nil
}

// OpenWith shows the desktop's application chooser for a local file.
func (p *Portal) OpenWith(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = p.request("org.freedesktop.portal.OpenURI.OpenFile", map[string]dbus.Variant{
		"ask": dbus.MakeVariant(true),
	}, "", dbus.UnixFD(f.Fd()))
	if err == errCancelled {
		return nil
	}
	return err
}

// displayName strips the message-id prefix the media cache puts on documents.
func displayName(p string) string {
	name := filepath.Base(p)
	if i := strings.Index(name, "_"); i > 0 && i < len(name)-1 {
		name = name[i+1:]
	}
	return name
}
