package main

import (
	"bytes"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestQuoteThumbFromReply(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenMsgStore(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	w := &WA{store: st, mediaDir: dir}
	raw, _ := proto.Marshal(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: proto.String("nice"),
		ContextInfo: &waE2E.ContextInfo{
			StanzaID: proto.String("GONE"),
			QuotedMessage: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
				JPEGThumbnail: []byte("jpeg"),
			}},
		},
	}})
	p, video := w.QuoteThumb("c", Msg{QuoteID: "GONE", Raw: raw}, func(string) string { return "" })
	if !video || p == "" {
		t.Fatalf("got %q, video=%v", p, video)
	}
	if b, _ := os.ReadFile(p); string(b) != "jpeg" {
		t.Fatalf("thumbnail content %q", b)
	}
}

func TestVideoThumb(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "my clip #1.mp4")
	if err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=10:duration=1",
		"-pix_fmt", "yuv420p", in).Run(); err != nil {
		t.Fatal(err)
	}
	frame, thumb, wd, ht := videoThumb(dir, in)
	if frame == "" || wd != 720 || ht != 406 {
		t.Fatalf("frame %q, %dx%d", frame, wd, ht)
	}
	img, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil || img.Bounds().Dx() != 72 {
		t.Fatalf("thumbnail: %v", err)
	}

	bad := filepath.Join(dir, "bad.mp4")
	os.WriteFile(bad, []byte("not a video"), 0o600)
	if f, _, _, _ := videoThumb(dir, bad); f != "" {
		t.Fatalf("frame from a broken video: %q", f)
	}
}
