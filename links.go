package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// LinkPreview is the card shown under a message containing a URL.
type LinkPreview struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Desc  string `json:"desc"`
	Site  string `json:"site"`
	Image string `json:"image"` // local file
	Video bool   `json:"video"`
}

func (lp *LinkPreview) empty() bool { return lp == nil || lp.Title == "" && lp.Image == "" }

// Links builds link previews: from the preview the sender attached when
// there is one, otherwise by fetching the page (YouTube via oEmbed, which
// also avoids the EU cookie wall; everything else via OpenGraph tags).
// Results are cached in the message store, images on disk.
type Links struct {
	store *MsgStore
	dir   string

	mu   sync.Mutex
	mem  map[string]*LinkPreview // nil = known to have no preview
	busy map[string]bool
	sem  chan struct{}

	client *http.Client
}

func NewLinks(store *MsgStore, cacheDir string) *Links {
	l := &Links{
		store: store,
		dir:   filepath.Join(cacheDir, "links"),
		mem:   map[string]*LinkPreview{},
		busy:  map[string]bool{},
		sem:   make(chan struct{}, 3),
	}
	os.MkdirAll(l.dir, 0o700)
	// never fetch from loopback / LAN addresses: links in incoming messages
	// must not be able to poke at the user's network
	dialer := &net.Dialer{Timeout: 8 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("refusing to fetch from a local address")
		}
		return nil
	}}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialer.DialContext
	tr.Proxy = nil
	l.client = &http.Client{Transport: tr, Timeout: 12 * time.Second}
	return l
}

// firstURL returns the first http(s) link in a text.
func firstURL(text string) string {
	if strings.Contains(text, "```") {
		text = codeBlockRe.ReplaceAllString(text, "")
	}
	return urlRe.FindString(text)
}

// Get returns the preview for url without blocking. raw is the message that
// contained it (for sender-attached previews). With fetch set (own
// messages) a missing preview is fetched from the site and done is called
// once it is there; incoming links only show what the sender attached, so
// nobody learns our IP address by sending a link.
func (l *Links) Get(u string, raw []byte, fetch bool, done func()) *LinkPreview {
	l.mu.Lock()
	if lp, ok := l.mem[u]; ok {
		l.mu.Unlock()
		return lp
	}
	l.mu.Unlock()

	if lp, fetched, ok := l.store.LinkPreview(u); ok {
		if !lp.empty() {
			return l.remember(u, lp)
		}
		if fetch && time.Since(time.Unix(fetched, 0)) > 24*time.Hour {
			l.fetchAsync(u, done) // retry failures once a day
			return nil
		}
		if !fetch {
			// a failed fetch is no reason to hide what the sender attached
			if lp := senderPreview(u, raw, l.dir); lp != nil {
				l.store.SaveLinkPreview(lp)
				return l.remember(u, lp)
			}
			return nil
		}
		return l.remember(u, lp)
	}
	// the sender's own preview (no network needed); for our own YouTube
	// links the fetch gets a proper high-res thumbnail instead
	if !fetch || youtubeID(u) == "" {
		if lp := senderPreview(u, raw, l.dir); lp != nil {
			l.store.SaveLinkPreview(lp)
			return l.remember(u, lp)
		}
	}
	if fetch {
		l.fetchAsync(u, done)
	}
	return nil
}

// Fetch returns the preview for url, fetching it if needed (blocking).
func (l *Links) Fetch(ctx context.Context, u string) *LinkPreview {
	l.mu.Lock()
	if lp, ok := l.mem[u]; ok {
		l.mu.Unlock()
		return lp
	}
	l.mu.Unlock()
	if lp, fetched, ok := l.store.LinkPreview(u); ok && (!lp.empty() || time.Since(time.Unix(fetched, 0)) < time.Hour) {
		return l.remember(u, lp)
	}
	lp := l.fetch(ctx, u)
	l.store.SaveLinkPreview(lp)
	return l.remember(u, lp)
}

func (l *Links) remember(u string, lp *LinkPreview) *LinkPreview {
	if lp.empty() {
		lp = nil
	}
	l.mu.Lock()
	l.mem[u] = lp
	l.mu.Unlock()
	return lp
}

func (l *Links) fetchAsync(u string, done func()) {
	l.mu.Lock()
	if l.busy[u] {
		l.mu.Unlock()
		return
	}
	l.busy[u] = true
	l.mu.Unlock()
	go func() {
		l.sem <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		lp := l.fetch(ctx, u)
		cancel()
		<-l.sem
		l.store.SaveLinkPreview(lp)
		l.remember(u, lp)
		l.mu.Lock()
		delete(l.busy, u)
		l.mu.Unlock()
		if !lp.empty() && done != nil {
			done()
		}
	}()
}

func (l *Links) fetch(ctx context.Context, u string) *LinkPreview {
	lp := &LinkPreview{URL: u}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
		return lp
	}
	lp.Site = strings.TrimPrefix(pu.Hostname(), "www.")
	if id := youtubeID(u); id != "" {
		l.fetchYouTube(ctx, id, lp)
		return lp
	}

	resp, err := l.get(ctx, u, "text/html,application/xhtml+xml;q=0.9,image/*;q=0.8,*/*;q=0.5")
	if err != nil {
		return lp
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "image/") { // direct link to a picture
		lp.Title = filepath.Base(pu.Path)
		lp.Image = l.saveImage(io.LimitReader(resp.Body, 10<<20), u)
		return lp
	}
	if !strings.Contains(ct, "html") {
		return lp
	}
	r, err := charset.NewReader(io.LimitReader(resp.Body, 1<<20), ct)
	if err != nil {
		return lp
	}
	meta, title := parseHead(r)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(meta[k]); v != "" {
				return v
			}
		}
		return ""
	}
	lp.Title = pick("og:title", "twitter:title")
	if lp.Title == "" {
		lp.Title = strings.TrimSpace(title)
	}
	lp.Desc = pick("og:description", "twitter:description", "description")
	if s := pick("og:site_name"); s != "" {
		lp.Site = s
	}
	lp.Video = strings.HasPrefix(pick("og:type"), "video") || pick("og:video", "og:video:url") != ""
	if img := pick("og:image", "og:image:url", "og:image:secure_url", "twitter:image", "twitter:image:src"); img != "" {
		if iu, err := resp.Request.URL.Parse(img); err == nil {
			lp.Image = l.download(ctx, iu.String())
		}
	}
	lp.Title = clip(lp.Title, 200)
	lp.Desc = clip(lp.Desc, 300)
	return lp
}

func (l *Links) get(ctx context.Context, u, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// sites tailor their OpenGraph tags to chat-app crawlers
	req.Header.Set("User-Agent", "WhatsApp/2.24 (WhatsUp desktop; link preview)")
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en;q=0.8, *;q=0.5")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.New(resp.Status)
	}
	return resp, nil
}

func (l *Links) fetchYouTube(ctx context.Context, id string, lp *LinkPreview) {
	lp.Site, lp.Video = "YouTube", true
	watch := "https://www.youtube.com/watch?v=" + id
	if resp, err := l.get(ctx, "https://www.youtube.com/oembed?format=json&url="+url.QueryEscape(watch), "application/json"); err == nil {
		var o struct {
			Title  string `json:"title"`
			Author string `json:"author_name"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&o)
		resp.Body.Close()
		lp.Title, lp.Desc = o.Title, o.Author
	}
	// maxres isn't there for every video
	for _, q := range []string{"maxresdefault", "sddefault", "hqdefault"} {
		if p := l.download(ctx, "https://i.ytimg.com/vi/"+id+"/"+q+".jpg"); p != "" {
			lp.Image = p
			break
		}
	}
}

func (l *Links) download(ctx context.Context, u string) string {
	resp, err := l.get(ctx, u, "image/*")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	return l.saveImage(io.LimitReader(resp.Body, 10<<20), u)
}

// saveImage decodes, scales down to at most 720px wide and stores a JPEG.
func (l *Links) saveImage(r io.Reader, key string) string {
	data, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	b := img.Bounds()
	if b.Dx() < 16 || b.Dy() < 16 {
		return "" // tracking pixels and the like
	}
	if b.Dx() > 720 {
		dst := image.NewRGBA(image.Rect(0, 0, 720, max(1, 720*b.Dy()/b.Dx())))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
		img = dst
	}
	h := sha1.Sum([]byte(key))
	p := filepath.Join(l.dir, hex.EncodeToString(h[:])+".jpg")
	var buf bytes.Buffer
	if jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}) != nil || os.WriteFile(p, buf.Bytes(), 0o600) != nil {
		return ""
	}
	return p
}

// senderPreview extracts the preview the sending client attached.
func senderPreview(u string, raw []byte, dir string) *LinkPreview {
	if raw == nil {
		return nil
	}
	var m waE2E.Message
	if proto.Unmarshal(raw, &m) != nil {
		return nil
	}
	et := m.GetExtendedTextMessage()
	if et == nil || et.GetTitle() == "" && len(et.GetJPEGThumbnail()) == 0 {
		return nil
	}
	if mt := et.GetMatchedText(); mt != "" && mt != u && !strings.HasPrefix(u, mt) {
		return nil
	}
	lp := &LinkPreview{URL: u, Title: et.GetTitle(), Desc: et.GetDescription(),
		Video: et.GetPreviewType() == waE2E.ExtendedTextMessage_VIDEO}
	if pu, err := url.Parse(u); err == nil {
		lp.Site = strings.TrimPrefix(pu.Hostname(), "www.")
	}
	if youtubeID(u) != "" {
		lp.Site, lp.Video = "YouTube", true
	}
	if t := et.GetJPEGThumbnail(); len(t) > 0 {
		h := sha1.Sum([]byte("sender:" + u))
		p := filepath.Join(dir, hex.EncodeToString(h[:])+".jpg")
		if os.WriteFile(p, t, 0o600) == nil {
			lp.Image = p
		}
	}
	return lp
}

// parseHead collects <meta> tags and the <title> of an HTML document.
func parseHead(r io.Reader) (map[string]string, string) {
	meta := map[string]string{}
	var title string
	z := html.NewTokenizer(r)
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return meta, title
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "meta":
				var key, content string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch string(k) {
					case "property", "name", "itemprop":
						if key == "" || strings.Contains(string(v), ":") {
							key = strings.ToLower(string(v))
						}
					case "content":
						content = string(v)
					}
				}
				if key != "" {
					if _, dup := meta[key]; !dup {
						meta[key] = content
					}
				}
			case "title":
				inTitle = title == ""
			case "body":
				if meta["og:title"] != "" || title != "" {
					return meta, title
				}
			}
		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "head":
				return meta, title
			}
		}
	}
}

func youtubeID(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(pu.Hostname()), "www.")
	valid := func(id string) string {
		if len(id) == 11 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == "" {
			return id
		}
		return ""
	}
	switch host {
	case "youtu.be":
		return valid(strings.Trim(pu.Path, "/"))
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if v := pu.Query().Get("v"); v != "" {
			return valid(v)
		}
		for _, p := range []string{"/shorts/", "/live/", "/embed/", "/v/"} {
			if strings.HasPrefix(pu.Path, p) {
				return valid(strings.SplitN(strings.TrimPrefix(pu.Path, p), "/", 2)[0])
			}
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// thumbJPEG makes the small inline thumbnail sent along with a preview.
func thumbJPEG(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := img.Bounds()
	w := min(b.Dx(), 320)
	dst := image.NewRGBA(image.Rect(0, 0, w, max(1, w*b.Dy()/b.Dx())))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	var buf bytes.Buffer
	jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 75})
	return buf.Bytes()
}
