package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// Avatars fetches profile pictures lazily, crops them to circles and caches
// them as PNGs on disk. Get never blocks; it returns "" until a picture is known.
type Avatars struct {
	dir string
	wa  *WA

	mu     sync.Mutex
	known  map[string]string    // jid -> file URL ("" = no picture)
	failed map[string]time.Time // transient failures, retried later
	queued map[string]bool
	queue  chan string

	onLoaded func()
}

func NewAvatars(cacheDir string) *Avatars {
	a := &Avatars{
		dir:    filepath.Join(cacheDir, "avatars"),
		known:  map[string]string{},
		failed: map[string]time.Time{},
		queued: map[string]bool{},
		queue:  make(chan string, 4096),
	}
	os.MkdirAll(a.dir, 0o700)
	return a
}

func (a *Avatars) path(jid string) string { return filepath.Join(a.dir, safeName(jid)+".png") }

// Get returns a file:// URL for the avatar of jid, or "" if unknown/none.
func (a *Avatars) Get(jid string) string {
	if jid == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if u, ok := a.known[jid]; ok {
		return u
	}
	// cached on disk?
	p := a.path(jid)
	if st, err := os.Stat(p); err == nil {
		u := ""
		if st.Size() > 0 {
			u = "file://" + p
		}
		a.known[jid] = u
		if time.Since(st.ModTime()) > 7*24*time.Hour {
			a.enqueueLocked(jid)
		}
		return u
	}
	if t, ok := a.failed[jid]; ok && time.Since(t) < 5*time.Minute {
		return ""
	}
	a.enqueueLocked(jid)
	return ""
}

func (a *Avatars) enqueueLocked(jid string) {
	if a.queued[jid] {
		return
	}
	select {
	case a.queue <- jid:
		a.queued[jid] = true
	default:
	}
}

// Invalidate forces a refetch (profile picture changed).
func (a *Avatars) Invalidate(jid string) {
	os.Remove(a.path(jid))
	a.mu.Lock()
	delete(a.known, jid)
	delete(a.failed, jid)
	a.enqueueLocked(jid)
	a.mu.Unlock()
}

func (a *Avatars) Run() {
	for jid := range a.queue {
		url, ok := a.fetch(jid)
		a.mu.Lock()
		delete(a.queued, jid)
		if ok {
			a.known[jid] = url
			delete(a.failed, jid)
		} else {
			a.failed[jid] = time.Now()
		}
		a.mu.Unlock()
		if ok && url != "" && a.onLoaded != nil {
			a.onLoaded()
		}
		time.Sleep(200 * time.Millisecond) // be gentle with the servers
	}
}

func (a *Avatars) fetch(jid string) (string, bool) {
	if a.wa == nil || !a.wa.cli.IsConnected() {
		return "", false
	}
	j, err := types.ParseJID(jid)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := a.wa.cli.GetProfilePictureInfo(ctx, j, &whatsmeow.GetProfilePictureParams{Preview: true})
	p := a.path(jid)
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) || (err == nil && info == nil) {
		os.WriteFile(p, nil, 0o600) // remember "no picture"
		return "", true
	}
	if err != nil {
		return "", false
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", false
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, circleCrop(img)); err != nil {
		return "", false
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, buf.Bytes(), 0o600) != nil || os.Rename(tmp, p) != nil {
		return "", false
	}
	// cache-bust so QML reloads a changed picture
	return "file://" + p + "?" + time.Now().Format("150405"), true
}

// Full returns a large, circle-cropped profile picture (cached for a day).
func (a *Avatars) Full(jid string) (string, error) {
	p := filepath.Join(a.dir, safeName(jid)+"_full.png")
	if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) < 24*time.Hour {
		if st.Size() == 0 {
			return "", nil
		}
		return p, nil
	}
	if a.wa == nil || !a.wa.cli.IsConnected() {
		return "", errors.New("not connected")
	}
	j, err := types.ParseJID(jid)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := a.wa.cli.GetProfilePictureInfo(ctx, j, &whatsmeow.GetProfilePictureParams{})
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) || (err == nil && info == nil) {
		os.WriteFile(p, nil, 0o600)
		return "", nil
	}
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, circleCrop(img)); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, buf.Bytes(), 0o600)
}

type circleMask struct{ r int }

func (c circleMask) ColorModel() color.Model { return color.AlphaModel }
func (c circleMask) Bounds() image.Rectangle { return image.Rect(0, 0, 2*c.r, 2*c.r) }
func (c circleMask) At(x, y int) color.Color {
	// 2x2 supersampling for a soft edge
	hits := 0
	for _, o := range [][2]float64{{.25, .25}, {.75, .25}, {.25, .75}, {.75, .75}} {
		dx, dy := float64(x)+o[0]-float64(c.r), float64(y)+o[1]-float64(c.r)
		if dx*dx+dy*dy <= float64(c.r*c.r) {
			hits++
		}
	}
	return color.Alpha{uint8(hits * 255 / 4)}
}

func circleCrop(src image.Image) image.Image {
	b := src.Bounds()
	size := min(b.Dx(), b.Dy())
	off := image.Pt(b.Min.X+(b.Dx()-size)/2, b.Min.Y+(b.Dy()-size)/2)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.DrawMask(dst, dst.Bounds(), src, off, circleMask{size / 2}, image.Point{}, draw.Over)
	return dst
}
