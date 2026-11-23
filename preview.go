package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// MediaInfo describes the attachment of a stored message.
type MediaInfo struct {
	Mime, Name string
	Size       uint64
	Pages      uint32
	Local      string // downloaded file, "" if not (yet) cached
}

func (w *WA) mediaPath(id, mt, fname string) string {
	if fname == "" {
		fname = id + extFor(mt)
	} else {
		fname = id + "_" + fname
	}
	return filepath.Join(w.mediaDir, safeName(fname))
}

func (w *WA) MediaInfo(chat, id string) (MediaInfo, bool) {
	raw := w.store.Raw(chat, id)
	if raw == nil {
		return MediaInfo{}, false
	}
	var m waE2E.Message
	if proto.Unmarshal(raw, &m) != nil {
		return MediaInfo{}, false
	}
	var mi MediaInfo
	switch {
	case m.GetDocumentMessage() != nil:
		d := m.GetDocumentMessage()
		mi = MediaInfo{Mime: d.GetMimetype(), Name: d.GetFileName(), Size: d.GetFileLength(), Pages: d.GetPageCount()}
		mi.Local = w.mediaPath(id, mi.Mime, mi.Name)
	case m.GetVideoMessage() != nil:
		v := m.GetVideoMessage()
		mi = MediaInfo{Mime: v.GetMimetype(), Size: v.GetFileLength()}
		mi.Local = w.mediaPath(id, mi.Mime, "")
	case m.GetImageMessage() != nil:
		v := m.GetImageMessage()
		mi = MediaInfo{Mime: v.GetMimetype(), Size: v.GetFileLength()}
		mi.Local = w.mediaPath(id, mi.Mime, "")
	default:
		return MediaInfo{}, false
	}
	if _, err := os.Stat(mi.Local); err != nil {
		mi.Local = ""
	}
	mi.Mime, _, _ = strings.Cut(mi.Mime, ";")
	return mi, true
}

// previewType classifies a file for inline previews.
func previewType(mime, name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case mime == "application/pdf" || ext == ".pdf":
		return "pdf"
	case strings.HasPrefix(mime, "text/"), mime == "application/json", mime == "application/xml",
		mime == "application/x-yaml", mime == "application/yaml", mime == "application/x-sh",
		mime == "application/javascript", mime == "application/toml":
		return "text"
	}
	switch ext {
	case ".txt", ".md", ".log", ".csv", ".json", ".xml", ".yaml", ".yml", ".toml", ".ini", ".conf",
		".go", ".py", ".js", ".ts", ".c", ".h", ".cpp", ".rs", ".java", ".sh", ".lua", ".sql", ".html", ".css":
		return "text"
	}
	return ""
}

// fileLabel is the "PDF · 3 pages · 1.2 MB" line under a document name.
func fileLabel(mi MediaInfo) string {
	var parts []string
	ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(mi.Name)), ".")
	if ext == "" {
		_, sub, _ := strings.Cut(mi.Mime, "/")
		ext = strings.ToUpper(sub)
	}
	if ext != "" && len(ext) <= 6 {
		parts = append(parts, ext)
	}
	if mi.Pages > 0 {
		parts = append(parts, plural(int(mi.Pages), "page"))
	}
	if mi.Size > 0 {
		parts = append(parts, humanSize(mi.Size))
	}
	return strings.Join(parts, " · ")
}

func humanSize(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// textSnippet returns the first lines of a text file.
func textSnippet(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 1200)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if bytes.IndexByte(buf, 0) >= 0 {
		return "" // binary after all
	}
	for !utf8.Valid(buf) && len(buf) > 0 { // cut a split rune
		buf = buf[:len(buf)-1]
	}
	lines := strings.Split(strings.ReplaceAll(string(buf), "\r\n", "\n"), "\n")
	if len(lines) > 10 {
		lines = lines[:10]
	}
	for i, l := range lines {
		if r := []rune(l); len(r) > 90 {
			lines[i] = string(r[:90]) + "…"
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n ")
}

// Previews renders first pages / frames of downloaded files in the background,
// using poppler's pdftoppm and ffmpeg when they're installed.
type Previews struct {
	mu   sync.Mutex
	busy map[string]bool
	sem  chan struct{}
}

func NewPreviews() *Previews {
	return &Previews{busy: map[string]bool{}, sem: make(chan struct{}, 2)}
}

func previewPath(local string) string { return local + ".preview.jpg" }

// Get returns the preview image for a local file, or "" while it's being
// made (done is called once it's ready) or when it can't be made.
func (pv *Previews) Get(local, typ string, done func()) string {
	switch typ {
	case "pdf", "video":
	default:
		return ""
	}
	out := previewPath(local)
	if st, err := os.Stat(out); err == nil {
		if st.Size() == 0 {
			return "" // failed before
		}
		return out
	}
	pv.mu.Lock()
	defer pv.mu.Unlock()
	if pv.busy[out] {
		return ""
	}
	pv.busy[out] = true
	go func() {
		pv.sem <- struct{}{}
		defer func() { <-pv.sem }()
		ok := makePreview(local, typ, out)
		if !ok {
			os.WriteFile(out, nil, 0o600) // don't retry
		}
		pv.mu.Lock()
		delete(pv.busy, out)
		pv.mu.Unlock()
		if ok {
			done()
		}
	}()
	return ""
}

// AttachmentFrame grabs a frame of a video picked for sending. It lives in the
// cache, never next to the user's file, and blocks until it's ready.
func (pv *Previews) AttachmentFrame(cacheDir, in string) string {
	st, err := os.Stat(in)
	if err != nil {
		return ""
	}
	dir := filepath.Join(cacheDir, "attachments")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%d", in, st.Size(), st.ModTime().UnixNano()))
	out := filepath.Join(dir, hex.EncodeToString(sum[:16])+".jpg")
	if !fileEmpty(out) {
		return out
	}
	pv.sem <- struct{}{}
	defer func() { <-pv.sem }()
	if !makePreview(in, "video", out) {
		return ""
	}
	return out
}

func makePreview(in, typ, out string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tmp := out + ".tmp"
	var cmd *exec.Cmd
	switch typ {
	case "pdf":
		if _, err := exec.LookPath("pdftoppm"); err != nil {
			return false
		}
		// pdftoppm appends the extension itself
		cmd = exec.CommandContext(ctx, "pdftoppm", "-jpeg", "-f", "1", "-l", "1", "-singlefile", "-scale-to", "720", in, tmp)
		tmp += ".jpg"
	case "video":
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return false
		}
		tmp += ".jpg"
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error", "-ss", "0.5", "-i", in,
			"-frames:v", "1", "-vf", "scale='min(720,iw)':-2", tmp)
	}
	err := cmd.Run()
	if typ == "video" && (err != nil || fileEmpty(tmp)) { // clip shorter than the seek
		err = exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error", "-i", in,
			"-frames:v", "1", "-vf", "scale='min(720,iw)':-2", tmp).Run()
	}
	if err != nil || fileEmpty(tmp) {
		os.Remove(tmp)
		return false
	}
	return os.Rename(tmp, out) == nil
}

func fileEmpty(p string) bool {
	st, err := os.Stat(p)
	return err != nil || st.Size() == 0
}
