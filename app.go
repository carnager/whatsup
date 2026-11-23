package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mappu/miqt/qt6/qml"
	"github.com/skip2/go-qrcode"
)

//go:embed qml/main.qml
var mainQML []byte

const pageSize = 200

type App struct {
	wa       *WA
	store    *MsgStore
	avatars  *Avatars
	notify   *Notifier
	emoji    *EmojiDB
	cacheDir string
	iconPath string
	tray     *Tray
	portal   *Portal
	previews *Previews
	links    *Links
	trayN    int

	engine      *qml.QQmlApplicationEngine
	root        *qt.QObject
	chats       *ListModel
	msgs        *ListModel
	emojis      *ListModel
	completions *ListModel
	pickModel   *ListModel // forward target picker
	members     *ListModel // group participants in the info panel
	mediaModel  *ListModel // shared media in the info panel
	fileModel   *ListModel // shared documents in the info panel
	linkModel   *ListModel // shared links in the info panel
	common      *ListModel // groups shared with a contact
	bridge      *qt.QAbstractListModel

	// main-thread state
	current      string
	limit        int
	active       bool
	qrN          int
	raiseN       int
	showArchived bool
	unreadAtOpen int
	contacts     []Contact
	contactsAt   time.Time
	infoChat     string
	infoLimits   map[string]int

	dirtyMu  sync.Mutex
	dirty    bool
	dirtyCur map[string]bool
}

func NewApp(qapp *qt.QApplication, ms *MsgStore, av *Avatars, em *EmojiDB, cacheDir string) *App {
	a := &App{
		store: ms, avatars: av, notify: NewNotifier(), emoji: em, cacheDir: cacheDir,
		dirtyCur: map[string]bool{}, limit: pageSize, active: true, previews: NewPreviews(),
	}
	a.chats = NewListModel("jid", "name", "preview", "time", "unread", "avatar", "initials", "tint",
		"pinned", "muted", "archived", "rowKind", "typing")
	a.msgs = NewListModel("msgId", "fromMe", "body", "plain", "time", "status", "kind", "media", "duration",
		"senderName", "senderColor", "showSender", "avatar", "initials", "dayHeader", "unreadMarker",
		"quoteId", "quoteName", "quoteText", "quoteColor", "reactions", "edited", "deleted", "first", "forwarded",
		"fileInfo", "preview", "snippet", "viewKind", "local",
		"linkUrl", "linkTitle", "linkDesc", "linkSite", "linkImage", "linkVideo")
	a.emojis = NewListModel("e", "name")
	a.completions = NewListModel("e", "code")
	a.pickModel = NewListModel("jid", "name", "sub", "avatar", "initials", "tint")
	a.members = NewListModel("jid", "name", "sub", "avatar", "initials", "tint", "role")
	a.mediaModel = NewListModel("msgId", "media", "kind")
	a.fileModel = NewListModel("msgId", "name", "info", "time")
	a.linkModel = NewListModel("url", "title", "site", "image", "time")
	a.common = NewListModel("jid", "name", "sub", "avatar", "initials", "tint")

	// QML -> Go calls go through setData on this one-row model, since we
	// can't declare Q_INVOKABLE methods from Go.
	a.bridge = qt.NewQAbstractListModel()
	a.bridge.OnRowCount(func(*qt.QModelIndex) int { return 1 })
	a.bridge.OnData(func(*qt.QModelIndex, int) *qt.QVariant { return qt.NewQVariant() })
	a.bridge.OnSetData(func(_ func(*qt.QModelIndex, *qt.QVariant, int) bool, _ *qt.QModelIndex, v *qt.QVariant, _ int) bool {
		var c struct{ Cmd, A, B, C string }
		if json.Unmarshal([]byte(v.ToString()), &c) == nil {
			a.command(c.Cmd, c.A, c.B, c.C)
		}
		return true
	})

	qapp.OnApplicationStateChanged(func(s qt.ApplicationState) {
		a.active = s == qt.ApplicationActive
		a.wa.SetActive(a.active)
		if a.active && a.current != "" {
			a.wa.MarkRead(a.current)
			a.notify.Clear(a.current)
			a.markDirty("")
		}
	})
	a.notify.OnActivate = func(chat string) {
		mainthread.Start(func() {
			a.showArchived = false
			a.openChat(chat)
			a.raiseN++
			a.set("raiseRequest", a.raiseN)
		})
	}

	a.engine = qml.NewQQmlApplicationEngine()
	ctx := a.engine.RootContext()
	ctx.SetContextProperty("chatModel", a.chats.M.QObject)
	ctx.SetContextProperty("messageModel", a.msgs.M.QObject)
	ctx.SetContextProperty("emojiModel", a.emojis.M.QObject)
	ctx.SetContextProperty("completionModel", a.completions.M.QObject)
	ctx.SetContextProperty("pickModel", a.pickModel.M.QObject)
	ctx.SetContextProperty("memberModel", a.members.M.QObject)
	ctx.SetContextProperty("mediaModel", a.mediaModel.M.QObject)
	ctx.SetContextProperty("fileModel", a.fileModel.M.QObject)
	ctx.SetContextProperty("linkModel", a.linkModel.M.QObject)
	ctx.SetContextProperty("commonModel", a.common.M.QObject)
	ctx.SetContextProperty("bridge", a.bridge.QObject)
	if p := os.Getenv("WHATSUP_QML"); p != "" { // live-edit the UI during development
		a.engine.Load(qt.QUrl_FromLocalFile(p))
	} else {
		a.engine.LoadData2(mainQML, qt.NewQUrl3("qrc:/whatsup/main.qml"))
	}
	roots := a.engine.RootObjects()
	if len(roots) == 0 {
		fatal(fmt.Errorf("failed to load QML UI (run with QT_FORCE_STDERR_LOGGING=1 for details)"))
	}
	a.root = roots[0]
	groups := append([]string{"Recent"}, em.Groups...)
	a.root.SetProperty("emojiGroups", qt.NewQVariant15(groups))
	a.setEmojis(em.Recent())

	av.onLoaded = func() { a.markDirty("*avatars") }
	go a.flusher()
	return a
}

func (a *App) set(name string, v any) {
	var qv *qt.QVariant
	switch v := v.(type) {
	case string:
		qv = qt.NewQVariant14(v)
	case bool:
		qv = qt.NewQVariant8(v)
	case int:
		qv = qt.NewQVariant4(v)
	}
	a.root.SetProperty(name, qv)
}

// withMedia downloads the full media of a message, then runs f on the main thread.
func (a *App) withMedia(chat, id string, f func(path string)) {
	a.Status("Downloading…")
	a.async("Download", func() error {
		p, err := a.mediaFile(chat, id)
		if err != nil {
			return err
		}
		a.Status("")
		mainthread.Start(func() { f(p) })
		return nil
	})
}

// async runs a network action off the main thread and reports failures.
func (a *App) async(what string, f func() error) {
	go func() {
		if err := f(); err != nil {
			a.Status(what + " failed: " + err.Error())
		}
	}()
}

func localPath(u string) string {
	if strings.HasPrefix(u, "file://") {
		if p, err := url.PathUnescape(strings.TrimPrefix(u, "file://")); err == nil {
			return p
		}
	}
	return u
}

func (a *App) command(cmd, x, y, z string) {
	chat := a.current
	switch cmd {
	case "open":
		a.openChat(x)
	case "send":
		text := strings.TrimSpace(x)
		if text != "" && chat != "" {
			a.async("Send", func() error { return a.wa.SendText(chat, text, y, z != "nopreview") })
		}
	case "sendFile": // x = file url, y = caption, z = reply id
		p := localPath(x)
		a.Status("Sending " + filepath.Base(p) + "…")
		a.async("Send", func() error {
			err := a.wa.SendFile(chat, p, strings.TrimSpace(y), z)
			if err == nil {
				a.Status("")
			}
			return err
		})
	case "edit":
		if t := strings.TrimSpace(y); t != "" {
			a.async("Edit", func() error { return a.wa.Edit(chat, x, t) })
		}
	case "delete":
		a.async("Delete", func() error { return a.wa.Revoke(chat, x) })
	case "react":
		a.emoji.Use(y)
		a.async("Reaction", func() error { return a.wa.React(chat, x, y) })
	case "typing":
		if chat != "" {
			go a.wa.Typing(chat)
		}
	case "more":
		if a.msgs.Len() >= a.limit {
			a.limit += pageSize
			a.refreshMessages()
		}
	case "fetch":
		a.Status("Requesting older messages from phone…")
		a.async("History request", func() error { return a.wa.RequestOlder(chat) })
	case "openMedia": // open full media in the default app
		a.withMedia(chat, x, func(p string) { qt.QDesktopServices_OpenUrl(qt.QUrl_FromLocalFile(p)) })
	case "openWith": // let the user pick the application
		a.withMedia(chat, x, func(p string) {
			if a.portal == nil {
				qt.QDesktopServices_OpenUrl(qt.QUrl_FromLocalFile(p))
				return
			}
			go func() {
				if err := a.portal.OpenWith(p); err != nil {
					a.Status("Open with failed: " + err.Error())
				}
			}()
		})
	case "view": // full media for the built-in viewer -> viewerReady = "id\nurl"
		id := x
		a.async("Download", func() error {
			p, err := a.mediaFile(chat, id)
			if err != nil {
				mainthread.Start(func() { a.set("viewerReady", ""); a.set("viewerReady", id+"\n") })
				return err
			}
			// reset first: the same picture opened twice must still notify QML
			mainthread.Start(func() { a.set("viewerReady", ""); a.set("viewerReady", id+"\n"+qt.QUrl_FromLocalFile(p).ToString()) })
			return nil
		})
	case "pickFiles": // native file chooser via the portal; results -> pickedFiles
		if a.portal == nil {
			return
		}
		go func() {
			u, err := a.portal.OpenFiles("Send files")
			if err != nil && err != errCancelled {
				a.Status("File chooser failed: " + err.Error())
			}
			if len(u) > 0 {
				mainthread.Start(func() {
					a.set("pickedFiles", "")
					a.set("pickedFiles", strings.Join(u, "\n"))
				})
			}
		}()
	case "loadAudio": // download voice/audio for inline playback
		a.async("Download", func() error {
			p, err := a.wa.DownloadMedia(chat, x)
			if err != nil {
				return err
			}
			a.store.SetMedia(chat, x, p)
			a.markDirty(chat)
			return nil
		})
	case "save":
		a.async("Save", func() error {
			p, err := a.mediaFile(chat, x)
			if err != nil {
				return err
			}
			name := a.saveName(x, p)
			dir := qt.QStandardPaths_WritableLocation(qt.QStandardPaths__DownloadLocation)
			var dst string
			if a.portal != nil {
				dst, err = a.portal.SaveFile("Save file", name, dir)
				if err == errCancelled {
					return nil
				}
				if err != nil {
					return err
				}
			} else {
				dst = uniquePath(filepath.Join(dir, name))
			}
			if err := copyFile(p, dst); err != nil {
				return err
			}
			a.Status("Saved to " + dst)
			return nil
		})
	case "linkPreview": // composer: preview for the link being typed -> composerLink (JSON)
		u := x
		show := func(lp *LinkPreview) {
			if lp == nil {
				lp = &LinkPreview{}
			}
			out := *lp
			out.URL = u
			if out.Image != "" {
				out.Image = "file://" + out.Image
			}
			b, _ := json.Marshal(out)
			a.set("composerLink", string(b))
		}
		if u == "" {
			a.set("composerLink", "")
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			lp := a.links.Fetch(ctx, u)
			cancel()
			mainthread.Start(func() { show(lp) })
		}()
	case "copyImage": // full picture -> clipboard
		a.withMedia(chat, x, func(p string) {
			img := qt.NewQImage8(p)
			if img.IsNull() {
				a.Status("Couldn't read this image")
				return
			}
			qt.QGuiApplication_Clipboard().SetImage(img)
			a.Status("Image copied")
		})
	case "copy":
		qt.QGuiApplication_Clipboard().SetText(x)
	case "paste": // image on clipboard -> temp file, reported back synchronously
		a.set("pastedFile", "")
		md := qt.QGuiApplication_Clipboard().MimeData()
		if md != nil && md.HasImage() {
			img := qt.QGuiApplication_Clipboard().Image()
			p := filepath.Join(a.cacheDir, fmt.Sprintf("paste_%d.png", time.Now().UnixNano()))
			if img != nil && !img.IsNull() && img.Save(p) {
				a.set("pastedFile", "file://"+p)
			}
		}
	case "link":
		qt.QDesktopServices_OpenUrl(qt.NewQUrl3(x))
	case "search":
		a.refreshChats()
	case "toggleArchived":
		a.showArchived = !a.showArchived
		a.refreshChats()
	case "pin":
		a.async("Pin", func() error { return a.wa.SetPinned(x, y == "1") })
	case "archive":
		if y == "1" && x == a.current {
			a.closeChat()
		}
		a.async("Archive", func() error { return a.wa.SetArchived(x, y == "1") })
	case "deleteChat":
		a.async("Delete chat", func() error { return a.wa.DeleteChat(x) })
	case "mute": // y = hours, "-1" forever, "0" unmute
		var d time.Duration
		switch y {
		case "-1":
			d = -1
		case "0":
		default:
			var h int
			fmt.Sscan(y, &h)
			d = time.Duration(h) * time.Hour
		}
		a.async("Mute", func() error { return a.wa.SetMuted(x, d) })
	case "markRead":
		a.wa.MarkRead(x)
		a.markDirty("")
	case "newChat": // phone number
		a.Status("Looking up +" + strings.TrimLeft(x, "+") + "…")
		go func() {
			jid, err := a.wa.CheckNumber(x)
			if err != nil {
				a.Status(err.Error())
				return
			}
			a.Status("")
			mainthread.Start(func() {
				a.openChat(jid)
				a.set("numberResolved", "")
				a.set("numberResolved", jid)
			})
		}()
	case "emojiGroup":
		if x == "Recent" {
			a.setEmojis(a.emoji.Recent())
		} else {
			a.setEmojis(a.emoji.Group(x))
		}
	case "emojiSearch":
		if strings.TrimSpace(x) == "" {
			a.setEmojis(a.emoji.Recent())
		} else {
			a.setEmojis(a.emoji.Search(x, 400))
		}
	case "emojiUse":
		a.emoji.Use(x)
	case "complete":
		var keys []string
		var rows [][]any
		if len([]rune(x)) >= 2 {
			for _, e := range a.emoji.Search(x, 8) {
				keys = append(keys, e.Char+e.Code)
				rows = append(rows, []any{e.Char, e.Code})
			}
		}
		a.completions.Reset(keys, rows)
	case "pickSearch": // forward target list
		a.refreshPick(x)
	case "forward": // x = message id, y = comma separated chat jids
		dsts := strings.Split(y, ",")
		a.Status(fmt.Sprintf("Forwarding to %s…", plural(len(dsts), "chat")))
		a.async("Forward", func() error {
			err := a.wa.Forward(chat, x, dsts)
			if err == nil {
				a.Status("Forwarded")
			}
			return err
		})
	case "info":
		a.loadInfo(chat)
	case "infoMore": // x = media, files or links
		if a.infoLimits != nil {
			a.infoLimits[x] += infoPage
			a.loadInfoMedia(chat)
		}
	case "exact": // ":code:" typed completely -> resolve synchronously
		a.set("exactEmoji", a.emoji.Exact(x))
	}
}

// EnableTray creates the tray icon; when it exists, closing the window hides it.
func (a *App) EnableTray() {
	a.tray = NewTray(a.iconPath, func() {
		a.trayN++
		a.set("trayToggle", a.trayN)
	})
	a.set("trayAvailable", a.tray != nil)
}

func (a *App) refreshPick(q string) {
	q = strings.ToLower(strings.TrimSpace(q))
	var keys []string
	var rows [][]any
	seen := map[string]bool{}
	add := func(jid, name, sub string) {
		if seen[jid] || (q != "" && !strings.Contains(strings.ToLower(name), q) && !strings.Contains(jid, q)) {
			return
		}
		seen[jid] = true
		keys = append(keys, jid)
		rows = append(rows, []any{jid, name, sub, a.avatars.Get(jid), initials(name), nameColor(jid)})
	}
	for _, c := range a.store.Chats() {
		name := c.Name
		if name == "" {
			name = a.wa.DisplayName(c.JID)
		}
		sub := "Chat"
		if strings.HasSuffix(c.JID, "@g.us") {
			sub = "Group"
		}
		add(c.JID, name, sub)
	}
	if q != "" {
		for _, ct := range a.contactList() {
			add(ct.JID, ct.Name, "Contact")
		}
	}
	if len(keys) > 300 {
		keys, rows = keys[:300], rows[:300]
	}
	a.pickModel.Reset(keys, rows)
}

func (a *App) loadInfo(chat string) {
	if chat == "" {
		return
	}
	isGroup := strings.HasSuffix(chat, "@g.us")
	name := a.wa.DisplayName(chat)
	sub := ""
	if u, ok := strings.CutSuffix(chat, "@s.whatsapp.net"); ok {
		sub = "+" + u
	}
	if chat != a.infoChat || a.infoLimits == nil {
		a.infoChat = chat
		a.infoLimits = map[string]int{"media": infoPage, "files": infoPage, "links": infoPage}
		a.common.Reset(nil, nil)
	}
	details := a.infoDetails(chat, isGroup)
	a.setInfoDetails(details)
	a.set("infoName", name)
	a.set("infoSub", sub)
	a.set("infoAbout", "")
	a.set("infoCreated", "")
	a.set("infoAvatar", a.avatars.Get(chat))
	a.set("infoInitials", initials(name))
	a.set("infoColor", nameColor(chat))
	a.members.Reset(nil, nil)

	a.loadInfoMedia(chat)
	if isGroup {
		a.common.Reset(nil, nil)
	} else {
		go a.loadCommonGroups(chat)
	}

	go func() {
		if p, err := a.avatars.Full(chat); err == nil && p != "" {
			mainthread.Start(func() {
				if a.current == chat {
					a.set("infoAvatar", "file://"+p)
				}
			})
		}
	}()
	go func() {
		info, err := a.wa.Info(chat)
		if err != nil {
			a.Status("Couldn't load info: " + err.Error())
			return
		}
		mainthread.Start(func() {
			if a.current != chat {
				return
			}
			a.set("infoAbout", info.About)
			if len(info.Business) > 0 {
				a.setInfoDetails(append(append([]infoDetail{}, info.Business...), details...))
			}
			if !info.Created.IsZero() {
				a.set("infoCreated", "Created "+info.Created.Format("2 January 2006"))
			}
			if isGroup {
				a.set("infoSub", "Group · "+plural(len(info.Participants), "member"))
				own := a.wa.ownJID().String()
				order := map[string]int{"Owner": 0, "Admin": 1, "": 2}
				ps := info.Participants
				sort.SliceStable(ps, func(i, j int) bool {
					if (ps[i].JID == own) != (ps[j].JID == own) {
						return ps[i].JID == own
					}
					if order[ps[i].Admin] != order[ps[j].Admin] {
						return order[ps[i].Admin] < order[ps[j].Admin]
					}
					return strings.ToLower(a.wa.DisplayName(ps[i].JID)) < strings.ToLower(a.wa.DisplayName(ps[j].JID))
				})
				var keys []string
				var rows [][]any
				for _, p := range ps {
					n := a.wa.DisplayName(p.JID)
					sub := ""
					if u, _, ok := strings.Cut(p.JID, "@"); ok && strings.HasSuffix(p.JID, "@s.whatsapp.net") && !strings.HasPrefix(n, "+") {
						sub = "+" + u
					}
					keys = append(keys, p.JID)
					rows = append(rows, []any{p.JID, n, sub, a.avatars.Get(p.JID), initials(n), nameColor(p.JID), p.Admin})
				}
				a.members.Reset(keys, rows)
			}
		})
	}()
}

func uniquePath(p string) string {
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(c); err != nil {
			return c
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (a *App) setEmojis(list []Emoji) {
	keys := make([]string, len(list))
	rows := make([][]any, len(list))
	for i, e := range list {
		keys[i] = e.Char
		rows[i] = []any{e.Char, e.Name}
	}
	a.emojis.Reset(keys, rows)
}

// ---- UI interface (called from whatsmeow goroutines) ----

func (a *App) ShowQR(code string) {
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		return
	}
	p := filepath.Join(a.cacheDir, "qr.png")
	if os.WriteFile(p, png, 0o600) != nil {
		return
	}
	mainthread.Start(func() {
		a.qrN++
		a.set("qrSource", fmt.Sprintf("file://%s?%d", p, a.qrN))
	})
}

func (a *App) LoggedIn() {
	mainthread.Start(func() { a.set("qrSource", ""); a.set("loggedIn", true) })
}

func (a *App) LoggedOut() {
	mainthread.Start(func() {
		a.set("loggedIn", false)
		a.set("statusText", "Logged out from phone — delete ~/.local/share/whatsup/session.db and restart to link again")
	})
}

func (a *App) Status(text string) {
	mainthread.Start(func() { a.set("statusText", text) })
}

func (a *App) Changed(chat string)         { a.markDirty(chat) }
func (a *App) PresenceChanged(chat string) { a.markDirty("~" + chat) }

// ChatRemoved closes a chat that was deleted here or on another device.
// A chat with messages newer than the deletion stays open.
func (a *App) ChatRemoved(chat string) {
	mainthread.Start(func() {
		if _, ok := a.store.Chat(chat); !ok {
			if a.current == chat {
				a.closeChat()
			}
			a.notify.Clear(chat)
		}
		a.markDirty(chat)
	})
}

func (a *App) Incoming(m Msg) {
	mainthread.Start(func() {
		if a.active && a.current == m.Chat {
			return
		}
		if c, ok := a.store.Chat(m.Chat); ok && c.Muted(time.Now().Unix()) {
			return
		}
		title := a.wa.DisplayName(m.Chat)
		body := m.Text
		if body == "" {
			body = kindLabel(m.Kind)
		}
		if strings.HasSuffix(m.Chat, "@g.us") {
			name := m.SenderName
			if name == "" {
				name = a.wa.DisplayName(m.Sender)
			}
			body = name + ": " + body
		}
		icon := localPath(a.avatars.Get(m.Chat))
		if i := strings.Index(icon, "?"); i > 0 {
			icon = icon[:i]
		}
		if icon == "" {
			icon = a.iconPath
		}
		go a.notify.Notify(m.Chat, title, body, icon)
	})
}

// ---- refresh coalescing ----

func (a *App) markDirty(chat string) {
	a.dirtyMu.Lock()
	a.dirty = true
	if chat == "" {
		chat = "*"
	}
	a.dirtyCur[chat] = true
	a.dirtyMu.Unlock()
}

func (a *App) flusher() {
	for range time.Tick(120 * time.Millisecond) {
		a.dirtyMu.Lock()
		if !a.dirty {
			a.dirtyMu.Unlock()
			continue
		}
		d := a.dirtyCur
		a.dirty = false
		a.dirtyCur = map[string]bool{}
		a.dirtyMu.Unlock()
		mainthread.Wait(func() {
			if d["*contacts"] {
				a.contactsAt = time.Time{} // reload the address book
			}
			cur := a.current
			if cur != "" && (d["*"] || d[cur]) && a.active {
				a.wa.MarkRead(cur)
				a.notify.Clear(cur)
			}
			a.refreshChats()
			if cur == "" {
				return
			}
			if d["*"] || d["*avatars"] || d[cur] || d["~"+cur] {
				a.refreshHeader()
			}
			if d["*"] || d["*avatars"] || d[cur] {
				a.refreshMessages()
			}
			if d["*info"] || ((d["*"] || d[cur]) && a.infoLimits != nil && a.infoChat == cur && a.root.Property("infoOpen").ToBool()) {
				a.loadInfoMedia(cur)
			}
		})
	}
}

// ---- chats ----

func (a *App) Start() {
	a.refreshChats()
}

func (a *App) contactList() []Contact {
	if time.Since(a.contactsAt) > time.Minute {
		a.contacts = a.wa.Contacts()
		sort.Slice(a.contacts, func(i, j int) bool {
			return strings.ToLower(a.contacts[i].Name) < strings.ToLower(a.contacts[j].Name)
		})
		a.contactsAt = time.Now()
	}
	return a.contacts
}

var digitsRe = regexp.MustCompile(`^\+?[0-9 ()/-]{6,}$`)

func (a *App) refreshChats() {
	q := strings.ToLower(strings.TrimSpace(a.root.Property("searchText").ToString()))
	filter := a.root.Property("chatFilter").ToString()
	now := time.Now().Unix()
	pass := func(c Chat) bool {
		switch filter {
		case "unread":
			return c.Unread > 0
		case "groups":
			return strings.HasSuffix(c.JID, "@g.us")
		}
		return true
	}
	var keys []string
	var rows [][]any
	total, archivedCount, archivedUnread := 0, 0, 0
	all := a.store.Chats()
	have := map[string]bool{}
	row := func(c Chat, name string) []any {
		preview := strings.ReplaceAll(c.LastText, "\n", " ")
		if preview == "" {
			preview = kindLabel(c.LastKind)
			if preview == "" && c.LastTS > 0 {
				preview = "📷 Photo"
			}
		}
		preview = stripMarkup(preview)
		if c.LastFromMe && preview != "" {
			preview = "You: " + preview
		}
		if r := []rune(preview); len(r) > 120 {
			preview = string(r[:120])
		}
		typingText, isTyping := a.wa.PresenceText(c.JID)
		if isTyping {
			preview = typingText
		}
		return []any{c.JID, name, preview, shortTime(c.LastTS), c.Unread,
			a.avatars.Get(c.JID), initials(name), nameColor(c.JID), c.Pinned, c.Muted(now), c.Archived, "chat", isTyping}
	}
	for _, c := range all {
		have[c.JID] = true
		if !c.Muted(now) && !c.Archived {
			total += c.Unread
		}
		if c.Archived {
			archivedCount++
			if c.Unread > 0 {
				archivedUnread++
			}
		}
	}
	nameOf := func(c Chat) string {
		if c.Name != "" {
			return c.Name
		}
		return a.wa.DisplayName(c.JID)
	}
	switch {
	case q != "":
		for _, c := range all {
			name := nameOf(c)
			if strings.Contains(strings.ToLower(name), q) || strings.Contains(c.JID, q) {
				keys = append(keys, c.JID)
				rows = append(rows, row(c, name))
			}
		}
		n := 0
		for _, ct := range a.contactList() {
			if have[ct.JID] || n >= 50 {
				continue
			}
			if strings.Contains(strings.ToLower(ct.Name), q) || strings.Contains(ct.JID, q) {
				keys = append(keys, "contact:"+ct.JID)
				rows = append(rows, []any{ct.JID, ct.Name, "+" + strings.Split(ct.JID, "@")[0], "", 0,
					a.avatars.Get(ct.JID), initials(ct.Name), nameColor(ct.JID), false, false, false, "contact", false})
				n++
			}
		}
		if digitsRe.MatchString(q) {
			num := normalizeNumber(q, a.wa.ownJID().User)
			keys = append(keys, "number:"+num)
			rows = append(rows, []any{num, "Message +" + num, "Start a new chat with this number", "", 0,
				"", "+", "#7bc862", false, false, false, "number", false})
		}
	case a.showArchived:
		keys = append(keys, "back")
		rows = append(rows, []any{"", "Archived chats", "", "", 0, "", "", "", false, false, false, "back", false})
		for _, c := range all {
			if c.Archived {
				keys = append(keys, c.JID)
				rows = append(rows, row(c, nameOf(c)))
			}
		}
	default:
		if archivedCount > 0 && filter == "" {
			keys = append(keys, "archive")
			rows = append(rows, []any{"", "Archived", plural(archivedCount, "chat"), "", archivedUnread,
				"", "", "", false, false, false, "archive", false})
		}
		for _, c := range all {
			if !c.Archived && pass(c) {
				keys = append(keys, c.JID)
				rows = append(rows, row(c, nameOf(c)))
			}
		}
	}
	a.chats.Update(keys, rows)
	a.set("totalUnread", total)
	a.tray.SetUnread(total)
	a.set("showArchived", a.showArchived)
}

func (a *App) refreshHeader() {
	if a.current == "" {
		return
	}
	name := a.wa.DisplayName(a.current)
	pres, typing := a.wa.PresenceText(a.current)
	sub := pres
	if sub == "" {
		if strings.HasSuffix(a.current, "@g.us") {
			sub = "Group"
		} else if u, _, ok := strings.Cut(a.current, "@"); ok && strings.HasSuffix(a.current, "@s.whatsapp.net") {
			sub = "+" + u
		}
	}
	c, _ := a.store.Chat(a.current)
	a.set("currentName", name)
	a.set("currentSubtitle", sub)
	a.set("currentTyping", typing)
	a.set("currentAvatar", a.avatars.Get(a.current))
	a.set("currentInitials", initials(name))
	a.set("currentColor", nameColor(a.current))
	a.set("currentPinned", c.Pinned)
	a.set("currentArchived", c.Archived)
	a.set("currentMuted", c.Muted(time.Now().Unix()))
}

func (a *App) openChat(jid string) {
	if jid == "" {
		return
	}
	if jid == a.current {
		a.set("currentJid", jid)
		return
	}
	a.current = jid
	a.limit = pageSize
	// a new conversation joins the chat list with its first message
	c, _ := a.store.Chat(jid)
	a.unreadAtOpen = c.Unread
	a.set("currentJid", jid)
	a.refreshHeader()
	a.wa.Subscribe(jid)
	a.wa.MarkRead(jid)
	a.notify.Clear(jid)
	a.msgs.Reset(nil, nil)
	a.refreshMessages()
	a.markDirty("")
}

func (a *App) closeChat() {
	a.current = ""
	a.set("currentJid", "")
	a.msgs.Reset(nil, nil)
}

// ---- messages ----

var durRe = regexp.MustCompile(`\((\d+)s\)`)

func (a *App) refreshMessages() {
	msgs := a.store.Messages(a.current, a.limit) // oldest first
	isGroup := strings.HasSuffix(a.current, "@g.us")
	own := a.wa.ownJID().String()
	n := len(msgs)

	// unread marker: above the oldest of the last N incoming messages
	markerID := ""
	if a.unreadAtOpen > 0 {
		cnt := 0
		for i := n - 1; i >= 0; i-- {
			if !msgs[i].FromMe {
				cnt++
				if cnt == a.unreadAtOpen {
					markerID = msgs[i].ID
					break
				}
			}
		}
	}

	keys := make([]string, 0, n)
	rows := make([][]any, 0, n)
	// model is newest-first (the QML ListView is bottom-to-top)
	for i := n - 1; i >= 0; i-- {
		m := msgs[i]
		t := time.Unix(m.TS, 0)
		var prev *Msg
		if i > 0 {
			prev = &msgs[i-1]
		}
		day := ""
		if prev == nil || !sameDay(t, time.Unix(prev.TS, 0)) {
			day = dayLabel(t)
		}
		first := prev == nil || day != "" || prev.FromMe != m.FromMe || prev.Sender != m.Sender ||
			m.TS-prev.TS > 600 || m.ID == markerID
		showSender := isGroup && !m.FromMe && first
		senderName, avatar := "", ""
		if showSender {
			senderName = m.SenderName
			if senderName == "" {
				senderName = a.wa.DisplayName(m.Sender)
			}
			avatar = a.avatars.Get(m.Sender)
		}
		kind := m.Kind
		if kind == "" && m.Media != "" {
			kind = "image" // rows from before kinds were stored
		}
		media := ""
		if m.Media != "" {
			media = "file://" + m.Media
		}
		if (kind == "image" || kind == "sticker") && n-i <= 40 && !m.Deleted &&
			(m.Media == "" || strings.HasSuffix(m.Media, "_thumb.jpg")) {
			a.wa.AutoDownload(a.current, m.ID)
		}
		// attachments: file details, inline previews, how to view them
		fileInfo, preview, snippet, viewKind, local := "", "", "", "", ""
		switch kind {
		case "image", "sticker":
			viewKind = "image"
		case "video":
			viewKind = "video"
			// history-synced videos come without thumbnails: grab a frame
			// from the (downloaded) video instead
			if !m.Deleted && !strings.HasSuffix(m.Media, "_thumb.jpg") {
				media = ""
				if mi, ok := a.wa.MediaInfo(a.current, m.ID); ok {
					fileInfo = fileLabel(MediaInfo{Size: mi.Size})
					if mi.Local != "" {
						chat := a.current
						if p := a.previews.Get(mi.Local, "video", func() { a.markDirty(chat) }); p != "" {
							media = "file://" + p
						}
					} else if mi.Size <= 32<<20 && n-i <= 40 {
						a.wa.AutoDownload(a.current, m.ID)
					}
				}
			}
		}
		if kind == "document" && !m.Deleted {
			if mi, ok := a.wa.MediaInfo(a.current, m.ID); ok {
				name := mi.Name
				typ := previewType(mi.Mime, name)
				fileInfo = fileLabel(mi)
				if typ == "image" || typ == "video" {
					viewKind = typ
				}
				if mi.Local != "" {
					local = "file://" + mi.Local
					chat := a.current
					switch typ {
					case "image":
						preview = local
					case "text":
						snippet = textSnippet(mi.Local)
					case "pdf", "video":
						if p := a.previews.Get(mi.Local, typ, func() { a.markDirty(chat) }); p != "" {
							preview = "file://" + p
						}
					}
				} else if typ != "" && typ != "audio" && mi.Size <= 16<<20 && n-i <= 40 {
					a.wa.AutoDownload(a.current, m.ID) // small enough to preview
				}
				if preview == "" && strings.HasSuffix(m.Media, "_thumb.jpg") {
					preview = media // thumbnail sent along
				}
			}
		}
		// link preview card
		var lp *LinkPreview
		if !m.Deleted && (kind == "" || kind == "image" || kind == "video") {
			if u := firstURL(m.Text); u != "" {
				chat := a.current
				lp = a.links.Get(u, m.Raw, m.FromMe, func() { a.markDirty(chat) })
			}
		}
		if lp == nil {
			lp = &LinkPreview{}
		}
		linkImage := ""
		if lp.Image != "" {
			linkImage = "file://" + lp.Image
		}
		text := m.Text
		duration := ""
		switch kind {
		case "voice", "audio":
			if mm := durRe.FindStringSubmatch(text); mm != nil {
				var s int
				fmt.Sscan(mm[1], &s)
				duration = fmt.Sprintf("%d:%02d", s/60, s%60)
			}
			if kind == "voice" || strings.HasPrefix(text, "🎵 Audio (") {
				text = ""
			}
		case "sticker":
			text = ""
		case "":
			if text == "" && m.Media == "" && !m.Deleted {
				text = "📷 Photo"
			}
		}
		quoteName, quoteColor := "", ""
		if m.QuoteID != "" {
			if m.QuoteSender == own {
				quoteName = "You"
			} else {
				quoteName = a.wa.DisplayName(m.QuoteSender)
			}
			quoteColor = nameColor(m.QuoteSender)
		}
		unreadMarker := ""
		if m.ID == markerID {
			unreadMarker = fmt.Sprintf("%d unread message", a.unreadAtOpen)
			if a.unreadAtOpen != 1 {
				unreadMarker += "s"
			}
		}
		keys = append(keys, m.ID)
		rows = append(rows, []any{m.ID, m.FromMe, a.formatBody(text), text, t.Format("15:04"), m.Status, kind, media, duration,
			senderName, nameColor(m.Sender), showSender, avatar, initials(senderName), day, unreadMarker,
			m.QuoteID, quoteName, m.QuoteText, quoteColor, reactionSummary(m.Reactions), m.Edited, m.Deleted, first, m.Forwarded,
			fileInfo, preview, snippet, viewKind, local,
			lp.URL, lp.Title, lp.Desc, lp.Site, linkImage, lp.Video})
	}
	a.msgs.Update(keys, rows)
	a.set("hasMore", n >= a.limit)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func reactionSummary(r map[string]string) string {
	if len(r) == 0 {
		return ""
	}
	count := map[string]int{}
	var order []string
	for _, e := range r {
		if count[e] == 0 {
			order = append(order, e)
		}
		count[e]++
	}
	sort.Slice(order, func(i, j int) bool {
		if count[order[i]] != count[order[j]] {
			return count[order[i]] > count[order[j]]
		}
		return order[i] < order[j]
	})
	var b strings.Builder
	for i, e := range order {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(e)
		if count[e] > 1 {
			fmt.Fprintf(&b, "%d", count[e])
		}
	}
	return b.String()
}

// ---- formatting helpers ----

func (a *App) formatBody(s string) string {
	if s == "" {
		return ""
	}
	if isJumboEmoji(s) {
		return `<span style="font-size:36px">` + html.EscapeString(s) + `</span>`
	}
	st := markupStyle{codeBg: "#0b0e11", quoteBar: "#2fc27a", mention: "#62c6ff"}
	if !a.root.Property("isDark").ToBool() {
		st = markupStyle{codeBg: "#eef1f4", quoteBar: "#1f9d5c", mention: "#0b7bc2"}
	}
	return renderMarkup(s, st, func(num string) string {
		name := a.wa.DisplayName(num + "@s.whatsapp.net")
		if strings.HasPrefix(name, "+") {
			return ""
		}
		return name
	})
}

// isJumboEmoji reports whether s consists only of a few emoji (shown large).
func isJumboEmoji(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	pictos := 0
	for _, r := range s {
		switch {
		case r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0x1f3fb && r <= 0x1f3ff) || r == ' ':
			// joiners, variation selectors, skin tones
		case r >= 0x1f000 || (r >= 0x2600 && r <= 0x27bf) || (r >= 0x2b00 && r <= 0x2bff) || unicode.Is(unicode.So, r):
			pictos++
		default:
			return false
		}
	}
	return pictos > 0 && pictos <= 3
}

func nameColor(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	palette := []string{"#e17076", "#7bc862", "#e5ca77", "#65aadd", "#a695e7", "#ee7aae", "#6ec9cb", "#faa774"}
	return palette[h.Sum32()%uint32(len(palette))]
}

func initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "#"
	}
	return string(out)
}

func shortTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	t := time.Unix(ts, 0)
	now := time.Now()
	switch {
	case sameDay(t, now):
		return t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Monday")
	default:
		return t.Format("02.01.06")
	}
}

func dayLabel(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	case t.Year() == now.Year():
		return t.Format("Monday, 2 January")
	default:
		return t.Format("2 January 2006")
	}
}

func sameDay(a, b time.Time) bool {
	y1, m1, d1 := a.Date()
	y2, m2, d2 := b.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}
