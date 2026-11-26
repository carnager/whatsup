package main

import (
	"errors"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// "/np" sends what Melody is playing. Only Melody is asked, so a video in a
// browser tab can never end up in a chat.
const melodyBus = "org.mpris.MediaPlayer2.melody"

func isNowPlaying(text string) bool { return strings.EqualFold(text, "/np") }

func melodyNowPlaying() (string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return "", err
	}
	obj := conn.Object(melodyBus, "/org/mpris/MediaPlayer2")
	get := func(name string) (dbus.Variant, error) {
		var v dbus.Variant
		done := make(chan error, 1)
		go func() {
			done <- obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", name).Store(&v)
		}()
		select {
		case err := <-done:
			return v, err
		case <-time.After(2 * time.Second):
			return v, errors.New("Melody isn't responding")
		}
	}
	status, err := get("PlaybackStatus")
	if err != nil {
		return "", errors.New("Melody isn't running")
	}
	if s, _ := status.Value().(string); s == "Stopped" {
		return "", errors.New("nothing is playing in Melody")
	}
	mv, err := get("Metadata")
	if err != nil {
		return "", err
	}
	meta, _ := mv.Value().(map[string]dbus.Variant)
	str := func(k string) string {
		switch v := meta[k].Value().(type) {
		case string:
			return v
		case []string:
			return strings.Join(v, ", ")
		}
		return ""
	}
	np := formatNowPlaying(str("xesam:artist"), str("xesam:title"))
	if np == "" {
		return "", errors.New("nothing is playing in Melody")
	}
	return np, nil
}

// formatNowPlaying gives "np: Artist – Title" on one line.
func formatNowPlaying(artist, title string) string {
	oneLine := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	artist, title = oneLine(artist), oneLine(title)
	if title == "" {
		return ""
	}
	if artist == "" {
		return "np: " + title
	}
	return "np: " + artist + " – " + title
}
