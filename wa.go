package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"google.golang.org/protobuf/proto"
)

// UI is what the WhatsApp layer needs from the frontend. All calls may come
// from arbitrary goroutines; implementations must hop to the Qt main thread.
type UI interface {
	ShowQR(code string)
	LoggedIn()
	LoggedOut()
	Status(text string)
	Changed(chat string)
	PresenceChanged(chat string)
	Incoming(m Msg)
}

type Presence struct {
	Online   bool
	LastSeen time.Time
}

type typing struct {
	sender string
	until  time.Time
}

type WA struct {
	cli      *whatsmeow.Client
	store    *MsgStore
	ui       UI
	mediaDir string
	avatars  *Avatars
	links    *Links

	nameMu    sync.Mutex
	resolving map[string]bool

	presMu     sync.Mutex
	presence   map[string]Presence
	typing     map[string]typing
	subscribed map[string]bool
	active     bool

	typeMu    sync.Mutex
	typeChat  string
	typeSent  time.Time
	typePause *time.Timer

	dlMu    sync.Mutex
	dlTried map[string]bool
	dlSem   chan struct{}
}

func NewWA(dataDir, cacheDir string, ms *MsgStore, ui UI) (*WA, error) {
	ctx := context.Background()
	logLevel := "WARN"
	if os.Getenv("WHATSUP_DEBUG") != "" {
		logLevel = "DEBUG"
	}
	container, err := sqlstore.New(ctx, "sqlite3",
		"file:"+filepath.Join(dataDir, "session.db")+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000",
		waLog.Stdout("DB", logLevel, true))
	if err != nil {
		return nil, err
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, err
	}
	w := &WA{
		cli:        whatsmeow.NewClient(dev, waLog.Stdout("WA", logLevel, true)),
		store:      ms,
		ui:         ui,
		mediaDir:   filepath.Join(cacheDir, "media"),
		resolving:  map[string]bool{},
		presence:   map[string]Presence{},
		typing:     map[string]typing{},
		subscribed: map[string]bool{},
		active:     true,
		dlTried:    map[string]bool{},
		dlSem:      make(chan struct{}, 3),
	}
	os.MkdirAll(w.mediaDir, 0o700)
	w.cli.AddEventHandler(w.handle)
	return w, nil
}

func (w *WA) Start() {
	if w.cli.Store.ID == nil {
		qr, _ := w.cli.GetQRChannel(context.Background())
		if err := w.cli.Connect(); err != nil {
			w.ui.Status("Connect failed: " + err.Error())
			return
		}
		go func() {
			for item := range qr {
				switch item.Event {
				case "code":
					w.ui.ShowQR(item.Code)
				case "success":
					w.ui.LoggedIn()
				case "timeout":
					w.ui.Status("QR code timed out, restart to try again")
				default:
					if item.Error != nil {
						w.ui.Status("Pairing error: " + item.Error.Error())
					}
				}
			}
		}()
		return
	}
	w.ui.LoggedIn()
	w.ui.Status("Connecting…")
	if err := w.cli.Connect(); err != nil {
		w.ui.Status("Connect failed: " + err.Error())
	}
}

func (w *WA) Stop() { w.cli.Disconnect() }

func (w *WA) ownJID() types.JID {
	if w.cli.Store.ID == nil {
		return types.EmptyJID
	}
	return w.cli.Store.ID.ToNonAD()
}

// canon maps LID-addressed JIDs to phone-number JIDs where known, so a
// conversation doesn't get split in two.
func (w *WA) canon(j types.JID) types.JID {
	j = j.ToNonAD()
	if j.Server == types.HiddenUserServer && w.cli.Store.LIDs != nil {
		if pn, err := w.cli.Store.LIDs.GetPNForLID(context.Background(), j); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD()
		}
	}
	return j
}

func (w *WA) contactName(j types.JID) string {
	if w.cli.Store.Contacts == nil {
		return ""
	}
	c, err := w.cli.Store.Contacts.GetContact(context.Background(), j)
	if err == nil && c.Found {
		for _, n := range []string{c.FullName, c.FirstName, c.BusinessName, c.PushName} {
			if n != "" {
				return n
			}
		}
	}
	return ""
}

type Contact struct {
	JID  string
	Name string
}

// Contacts returns address-book contacts.
func (w *WA) Contacts() []Contact {
	if w.cli.Store.Contacts == nil {
		return nil
	}
	all, err := w.cli.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		return nil
	}
	out := make([]Contact, 0, len(all))
	for j, c := range all {
		if j.Server != types.DefaultUserServer {
			continue
		}
		name := c.FullName
		if name == "" {
			name = c.FirstName
		}
		if name == "" {
			name = c.BusinessName
		}
		if name == "" {
			continue // only people in the address book
		}
		out = append(out, Contact{JID: j.String(), Name: name})
	}
	return out
}

// ensureChatName fills in a missing chat name, fetching group info if needed.
func (w *WA) ensureChatName(chat types.JID) {
	key := chat.String()
	if w.store.ChatName(key) != "" {
		return
	}
	if chat.Server != types.GroupServer {
		if n := w.contactName(chat); n != "" {
			w.store.SetChatName(key, n)
		}
		return
	}
	w.nameMu.Lock()
	if w.resolving[key] {
		w.nameMu.Unlock()
		return
	}
	w.resolving[key] = true
	w.nameMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if info, err := w.cli.GetGroupInfo(ctx, chat); err == nil && info.Name != "" {
			w.store.SetChatName(key, info.Name)
			w.ui.Changed(key)
		}
	}()
}

func (w *WA) DisplayName(jid string) string {
	if own := w.ownJID(); !own.IsEmpty() && jid == own.String() {
		return "You"
	}
	if n := w.store.ChatName(jid); n != "" {
		return n
	}
	j, err := types.ParseJID(jid)
	if err != nil {
		return jid
	}
	if n := w.contactName(j); n != "" {
		return n
	}
	if n := w.store.SenderName(jid); n != "" {
		return "~" + n // profile name, not in address book
	}
	if j.Server == types.DefaultUserServer {
		return "+" + j.User
	}
	return j.User
}

func (w *WA) handle(evt any) {
	switch e := evt.(type) {
	case *events.Connected:
		w.ui.Status("Connected")
		w.presMu.Lock()
		w.subscribed = map[string]bool{} // subscriptions don't survive reconnects
		w.presMu.Unlock()
		go w.sendOwnPresence()
		go w.syncGroupNames()
	case *events.Disconnected:
		w.ui.Status("Disconnected, reconnecting…")
	case *events.LoggedOut:
		w.ui.LoggedOut()
	case *events.PairSuccess:
		w.ui.Status("Paired as " + e.ID.User + ", syncing history…")
	case *events.Message:
		w.onMessage(e, true)
	case *events.HistorySync:
		go w.onHistory(e)
	case *events.Receipt:
		w.onReceipt(e)
	case *events.Picture:
		if w.avatars != nil {
			w.avatars.Invalidate(w.canon(e.JID).String())
		}
	case *events.GroupInfo:
		if e.Name != nil {
			w.store.SetChatName(e.JID.String(), e.Name.Name)
			w.ui.Changed(e.JID.String())
		}
	case *events.PushName:
		j := w.canon(e.JID).String()
		if w.store.ChatName(j) == "" && e.NewPushName != "" {
			w.store.SetChatName(j, e.NewPushName)
			w.ui.Changed(j)
		}
	case *events.Presence:
		j := w.canon(e.From).String()
		w.presMu.Lock()
		w.presence[j] = Presence{Online: !e.Unavailable, LastSeen: e.LastSeen}
		w.presMu.Unlock()
		w.ui.PresenceChanged(j)
	case *events.ChatPresence:
		chat := w.canon(e.Chat).String()
		w.presMu.Lock()
		if e.State == types.ChatPresenceComposing {
			w.typing[chat] = typing{sender: w.canon(e.Sender).String(), until: time.Now().Add(7 * time.Second)}
			time.AfterFunc(7500*time.Millisecond, func() { w.ui.PresenceChanged(chat) })
		} else {
			delete(w.typing, chat)
		}
		w.presMu.Unlock()
		w.ui.PresenceChanged(chat)
	case *events.Pin:
		j := w.canon(e.JID).String()
		w.store.SetPinned(j, e.Action.GetPinned())
		w.ui.Changed(j)
	case *events.Archive:
		j := w.canon(e.JID).String()
		w.store.SetArchived(j, e.Action.GetArchived())
		w.ui.Changed(j)
	case *events.Mute:
		j := w.canon(e.JID).String()
		until := int64(0)
		if e.Action.GetMuted() {
			until = e.Action.GetMuteEndTimestamp()
			if until <= 0 {
				until = -1
			} else if until > 1e12 { // milliseconds
				until /= 1000
			}
		}
		w.store.SetMuted(j, until)
		w.ui.Changed(j)
	case *events.MarkChatAsRead:
		j := w.canon(e.JID).String()
		if e.Action.GetRead() {
			w.store.TakeUnread(j)
		} else {
			w.store.SetUnread(j, 1)
		}
		w.ui.Changed(j)
	case *events.DeleteChat:
		j := w.canon(e.JID).String()
		w.store.DeleteChat(j)
		w.ui.Changed(j)
	}
}

func (w *WA) syncGroupNames() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	groups, err := w.cli.GetJoinedGroups(ctx)
	if err != nil {
		return
	}
	for _, g := range groups {
		if g.Name != "" {
			w.store.SetChatName(g.JID.String(), g.Name)
		}
	}
	w.ui.Changed("")
}

func (w *WA) onReceipt(e *events.Receipt) {
	chat := w.canon(e.Chat).String()
	switch {
	case !e.IsFromMe && e.Type == types.ReceiptTypeDelivered:
		w.store.SetStatus(chat, e.MessageIDs, StatusDelivered)
	case !e.IsFromMe && (e.Type == types.ReceiptTypeRead || e.Type == types.ReceiptTypePlayed):
		w.store.SetStatus(chat, e.MessageIDs, StatusRead)
	case e.IsFromMe && (e.Type == types.ReceiptTypeRead || e.Type == types.ReceiptTypeReadSelf):
		w.store.TakeUnread(chat) // read on another device
	default:
		return
	}
	w.ui.Changed(chat)
}

func (w *WA) onHistory(e *events.HistorySync) {
	onDemand := e.Data.GetSyncType().String() == "ON_DEMAND"
	for _, conv := range e.Data.GetConversations() {
		chatJID, err := types.ParseJID(conv.GetID())
		if err != nil || chatJID.Server == types.BroadcastServer {
			continue
		}
		chat := w.canon(chatJID)
		for _, hm := range conv.GetMessages() {
			evt, err := w.cli.ParseWebMessage(chatJID, hm.GetMessage())
			if err != nil {
				continue
			}
			w.onMessage(evt, false)
		}
		name := conv.GetName()
		if name == "" {
			name = conv.GetDisplayName()
		}
		key := chat.String()
		unread := int(conv.GetUnreadCount())
		if onDemand {
			unread = -1
		}
		w.store.UpsertChat(key, name, int64(conv.GetConversationTimestamp()), unread)
		if !onDemand {
			w.store.SetArchived(key, conv.GetArchived())
			w.store.SetPinned(key, conv.GetPinned() > 0)
			if mt := int64(conv.GetMuteEndTime()); mt != 0 {
				if mt < 0 {
					mt = -1
				}
				w.store.SetMuted(key, mt)
			}
		}
		w.ensureChatName(chat)
	}
	w.ui.Changed("")
}

func contextInfo(m *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetContextInfo()
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetContextInfo()
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetContextInfo()
	case m.GetAudioMessage() != nil:
		return m.GetAudioMessage().GetContextInfo()
	case m.GetDocumentMessage() != nil:
		return m.GetDocumentMessage().GetContextInfo()
	case m.GetStickerMessage() != nil:
		return m.GetStickerMessage().GetContextInfo()
	}
	return nil
}

func (w *WA) onMessage(e *events.Message, live bool) {
	if e.Info.Chat.Server == types.BroadcastServer {
		return // status updates
	}
	chat := w.canon(e.Info.Chat)
	chatKey := chat.String()
	msg := e.Message
	if msg == nil {
		return
	}
	sender := w.canon(e.Info.Sender)

	if pm := msg.GetProtocolMessage(); pm != nil {
		switch pm.GetType() {
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			if t, _ := extractText(pm.GetEditedMessage()); t != "" {
				w.store.EditMessage(chatKey, pm.GetKey().GetID(), t)
				w.ui.Changed(chatKey)
			}
		case waE2E.ProtocolMessage_REVOKE:
			w.store.DeleteMessage(chatKey, pm.GetKey().GetID())
			w.ui.Changed(chatKey)
		}
		return
	}
	if r := msg.GetReactionMessage(); r != nil {
		s := sender.String()
		if e.Info.IsFromMe {
			s = w.ownJID().String()
		}
		if w.store.SetReaction(chatKey, r.GetKey().GetID(), s, r.GetText()) {
			w.ui.Changed(chatKey)
		}
		return
	}

	text, kind := extractText(msg)
	if text == "" && kind == "" {
		return // key distribution, poll votes, etc.
	}
	m := Msg{
		ID:     e.Info.ID,
		Chat:   chatKey,
		Sender: sender.String(),
		FromMe: e.Info.IsFromMe,
		TS:     e.Info.Timestamp.Unix(),
		Text:   text,
		Kind:   kind,
	}
	m.Raw, _ = proto.Marshal(msg)
	if m.FromMe {
		m.Status = StatusDelivered
		m.Sender = w.ownJID().String()
	} else {
		m.SenderName = w.contactName(sender)
		if m.SenderName == "" {
			m.SenderName = e.Info.PushName
		}
	}
	if ci := contextInfo(msg); ci != nil {
		m.Forwarded = ci.GetIsForwarded()
	}
	if ci := contextInfo(msg); ci != nil && ci.GetStanzaID() != "" {
		m.QuoteID = ci.GetStanzaID()
		if p, err := types.ParseJID(ci.GetParticipant()); err == nil {
			m.QuoteSender = w.canon(p).String()
		}
		qt, qk := extractText(ci.GetQuotedMessage())
		if qt == "" {
			qt = kindLabel(qk)
		}
		m.QuoteText = qt
	}
	var thumb []byte
	switch {
	case msg.GetImageMessage() != nil:
		thumb = msg.GetImageMessage().GetJPEGThumbnail()
	case msg.GetVideoMessage() != nil:
		thumb = msg.GetVideoMessage().GetJPEGThumbnail()
	case msg.GetDocumentMessage() != nil:
		thumb = msg.GetDocumentMessage().GetJPEGThumbnail()
	}
	if len(thumb) > 0 {
		p := filepath.Join(w.mediaDir, safeName(m.ID)+"_thumb.jpg")
		if os.WriteFile(p, thumb, 0o600) == nil {
			m.Media = p
		}
	}
	if !w.store.AddMessage(m, live) {
		return
	}
	if !live {
		return
	}
	w.ensureChatName(chat)
	if kind == "image" || kind == "sticker" {
		w.AutoDownload(chatKey, m.ID)
	}
	w.ui.Changed(chatKey)
	if !m.FromMe {
		w.presMu.Lock()
		delete(w.typing, chatKey)
		w.presMu.Unlock()
		w.ui.Incoming(m)
	}
}

func kindLabel(kind string) string {
	switch kind {
	case "image":
		return "📷 Photo"
	case "video":
		return "🎬 Video"
	case "audio":
		return "🎵 Audio"
	case "voice":
		return "🎤 Voice message"
	case "document":
		return "📄 Document"
	case "sticker":
		return "🏷 Sticker"
	}
	return ""
}

// extractText returns the displayable text and, for media, a media kind.
func extractText(m *waE2E.Message) (text, kind string) {
	if m == nil {
		return "", ""
	}
	switch {
	case m.GetConversation() != "":
		return m.GetConversation(), ""
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetText(), ""
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetCaption(), "image"
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetCaption(), "video"
	case m.GetAudioMessage() != nil:
		a := m.GetAudioMessage()
		if a.GetPTT() {
			return fmt.Sprintf("🎤 Voice message (%ds)", a.GetSeconds()), "voice"
		}
		return fmt.Sprintf("🎵 Audio (%ds)", a.GetSeconds()), "audio"
	case m.GetDocumentMessage() != nil:
		d := m.GetDocumentMessage()
		name := d.GetFileName()
		if name == "" {
			name = d.GetTitle()
		}
		return withPrefix("📄 "+name, d.GetCaption()), "document"
	case m.GetStickerMessage() != nil:
		return "🏷 Sticker", "sticker"
	case m.GetLocationMessage() != nil:
		l := m.GetLocationMessage()
		return fmt.Sprintf("📍 https://www.openstreetmap.org/?mlat=%f&mlon=%f", l.GetDegreesLatitude(), l.GetDegreesLongitude()), ""
	case m.GetLiveLocationMessage() != nil:
		l := m.GetLiveLocationMessage()
		return fmt.Sprintf("📍 Live location: https://www.openstreetmap.org/?mlat=%f&mlon=%f", l.GetDegreesLatitude(), l.GetDegreesLongitude()), ""
	case m.GetContactMessage() != nil:
		return "👤 " + m.GetContactMessage().GetDisplayName(), ""
	case m.GetPollCreationMessage() != nil:
		return pollText(m.GetPollCreationMessage()), ""
	case m.GetPollCreationMessageV3() != nil:
		return pollText(m.GetPollCreationMessageV3()), ""
	}
	return "", ""
}

func pollText(p *waE2E.PollCreationMessage) string {
	var b strings.Builder
	b.WriteString("📊 " + p.GetName())
	for _, o := range p.GetOptions() {
		b.WriteString("\n   ○ " + o.GetOptionName())
	}
	return b.String()
}

func withPrefix(prefix, caption string) string {
	if caption == "" {
		return prefix
	}
	return prefix + "\n" + caption
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, s)
}

// Backfill upgrades messages stored by older versions (no kind recorded) by
// re-reading their raw protobuf, so old photos show up as photos.
func (w *WA) Backfill() {
	n := 0
	for _, m := range w.store.UntypedMedia() {
		var msg waE2E.Message
		if proto.Unmarshal(m.Raw, &msg) != nil {
			continue
		}
		text, kind := extractText(&msg)
		if kind == "" {
			continue
		}
		media := m.Media
		var thumb []byte
		switch {
		case msg.GetImageMessage() != nil:
			thumb = msg.GetImageMessage().GetJPEGThumbnail()
		case msg.GetVideoMessage() != nil:
			thumb = msg.GetVideoMessage().GetJPEGThumbnail()
		case msg.GetDocumentMessage() != nil:
			thumb = msg.GetDocumentMessage().GetJPEGThumbnail()
		}
		if media == "" && len(thumb) > 0 {
			p := filepath.Join(w.mediaDir, safeName(m.ID)+"_thumb.jpg")
			if os.WriteFile(p, thumb, 0o600) == nil {
				media = p
			}
		}
		w.store.SetKind(m.Chat, m.ID, kind, text, media)
		n++
	}
	if n > 0 {
		w.ui.Changed("")
	}
}

// AutoDownload fetches media for inline display in the background (once).
func (w *WA) AutoDownload(chat, id string) {
	w.dlMu.Lock()
	if w.dlTried[chat+"/"+id] {
		w.dlMu.Unlock()
		return
	}
	w.dlTried[chat+"/"+id] = true
	w.dlMu.Unlock()
	go func() {
		w.dlSem <- struct{}{}
		defer func() { <-w.dlSem }()
		if !w.cli.IsConnected() {
			w.dlMu.Lock()
			delete(w.dlTried, chat+"/"+id) // retry later
			w.dlMu.Unlock()
			return
		}
		if p, err := w.DownloadMedia(chat, id); err == nil {
			w.store.SetMedia(chat, id, p)
			w.ui.Changed(chat)
		}
	}()
}

// DownloadMedia fetches the full media of a stored message into the cache dir.
func (w *WA) DownloadMedia(chat, id string) (string, error) {
	raw := w.store.Raw(chat, id)
	if raw == nil {
		return "", fmt.Errorf("no media")
	}
	var m waE2E.Message
	if err := proto.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	var dl whatsmeow.DownloadableMessage
	var mt, fname string
	switch {
	case m.GetImageMessage() != nil:
		dl, mt = m.GetImageMessage(), m.GetImageMessage().GetMimetype()
	case m.GetVideoMessage() != nil:
		dl, mt = m.GetVideoMessage(), m.GetVideoMessage().GetMimetype()
	case m.GetAudioMessage() != nil:
		dl, mt = m.GetAudioMessage(), m.GetAudioMessage().GetMimetype()
	case m.GetDocumentMessage() != nil:
		dl, mt, fname = m.GetDocumentMessage(), m.GetDocumentMessage().GetMimetype(), m.GetDocumentMessage().GetFileName()
	case m.GetStickerMessage() != nil:
		dl, mt = m.GetStickerMessage(), m.GetStickerMessage().GetMimetype()
	default:
		return "", fmt.Errorf("not downloadable")
	}
	path := w.mediaPath(id, mt, fname)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	data, err := w.cli.Download(ctx, dl)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o600)
}

func extFor(mt string) string {
	base, _, _ := strings.Cut(mt, ";")
	switch base {
	case "image/jpeg":
		return ".jpg"
	case "audio/ogg":
		return ".ogg"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	}
	if exts, _ := mime.ExtensionsByType(base); len(exts) > 0 {
		return exts[len(exts)-1]
	}
	return ".bin"
}

// ---- sending ----

func (w *WA) quoteContext(chat, quoteID string) *waE2E.ContextInfo {
	if quoteID == "" {
		return nil
	}
	q, ok := w.store.Message(chat, quoteID)
	if !ok {
		return nil
	}
	var qm waE2E.Message
	if q.Raw == nil || proto.Unmarshal(q.Raw, &qm) != nil {
		qm = waE2E.Message{Conversation: proto.String(q.Text)}
	}
	participant := q.Sender
	if q.FromMe {
		participant = w.ownJID().String()
	}
	return &waE2E.ContextInfo{
		StanzaID:      proto.String(q.ID),
		Participant:   proto.String(participant),
		QuotedMessage: &qm,
	}
}

func (w *WA) send(chat string, msg *waE2E.Message, local Msg) error {
	to, err := types.ParseJID(chat)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := w.cli.SendMessage(ctx, to, msg)
	if err != nil {
		return err
	}
	w.StopTyping(chat)
	local.ID, local.Chat, local.Sender, local.FromMe = resp.ID, chat, w.ownJID().String(), true
	local.TS, local.Status = resp.Timestamp.Unix(), StatusSent
	local.Raw, _ = proto.Marshal(msg)
	if ci := contextInfo(msg); ci != nil && ci.GetStanzaID() != "" {
		if q, ok := w.store.Message(chat, ci.GetStanzaID()); ok {
			local.QuoteID, local.QuoteSender, local.QuoteText = q.ID, q.Sender, q.Text
			if local.QuoteText == "" {
				local.QuoteText = kindLabel(q.Kind)
			}
		}
	}
	w.store.AddMessage(local, false)
	w.store.SetArchived(chat, false)
	w.ui.Changed(chat)
	return nil
}

// SendText sends a text message; a link in it gets a preview card attached
// (unless withPreview is false), like the official clients do.
func (w *WA) SendText(chat, text, quoteID string, withPreview bool) error {
	text = normalizeMarkup(text)
	ci := w.quoteContext(chat, quoteID)
	var lp *LinkPreview
	if u := firstURL(text); u != "" && withPreview && w.links != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		lp = w.links.Fetch(ctx, u)
		cancel()
	}
	var msg *waE2E.Message
	switch {
	case lp != nil:
		et := &waE2E.ExtendedTextMessage{
			Text: proto.String(text), MatchedText: proto.String(lp.URL), ContextInfo: ci,
			Title: proto.String(lp.Title), Description: proto.String(lp.Desc),
			PreviewType: waE2E.ExtendedTextMessage_NONE.Enum(),
		}
		if lp.Video {
			et.PreviewType = waE2E.ExtendedTextMessage_VIDEO.Enum()
		}
		if lp.Image != "" {
			et.JPEGThumbnail = thumbJPEG(lp.Image)
		}
		msg = &waE2E.Message{ExtendedTextMessage: et}
	case ci != nil:
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: ci}}
	default:
		msg = &waE2E.Message{Conversation: proto.String(text)}
	}
	return w.send(chat, msg, Msg{Text: text})
}

func (w *WA) SendFile(chat, path, caption, quoteID string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 100<<20 {
		return fmt.Errorf("file too large (max 100 MB)")
	}
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	base, _, _ := strings.Cut(mt, ";")
	ci := w.quoteContext(chat, quoteID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	local := Msg{Text: caption}
	var msg *waE2E.Message
	switch {
	case base == "image/jpeg" || base == "image/png" || base == "image/webp":
		up, err := w.cli.Upload(ctx, data, whatsmeow.MediaImage)
		if err != nil {
			return err
		}
		thumb, wd, ht := makeThumb(data)
		msg = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(base),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
			JPEGThumbnail: thumb, Width: proto.Uint32(uint32(wd)), Height: proto.Uint32(uint32(ht)),
			ContextInfo: ci,
		}}
		local.Kind, local.Media = "image", copyToCache(w.mediaDir, data, filepath.Ext(path))
	case strings.HasPrefix(base, "video/"):
		up, err := w.cli.Upload(ctx, data, whatsmeow.MediaVideo)
		if err != nil {
			return err
		}
		msg = &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(base),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
			ContextInfo: ci,
		}}
		local.Kind = "video"
		if caption == "" {
			local.Text = "🎬 " + filepath.Base(path)
		}
	case strings.HasPrefix(base, "audio/"):
		up, err := w.cli.Upload(ctx, data, whatsmeow.MediaAudio)
		if err != nil {
			return err
		}
		msg = &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype: proto.String(base),
			URL:      &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
			ContextInfo: ci,
		}}
		local.Kind, local.Text = "audio", "🎵 "+filepath.Base(path)
	default:
		up, err := w.cli.Upload(ctx, data, whatsmeow.MediaDocument)
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		msg = &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			FileName: proto.String(name), Title: proto.String(name), Caption: proto.String(caption), Mimetype: proto.String(mt),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
			ContextInfo: ci,
		}}
		local.Kind, local.Text = "document", withPrefix("📄 "+name, caption)
	}
	return w.send(chat, msg, local)
}

// Forward sends a stored message to other chats, marked as forwarded.
// Media is not re-uploaded: the existing upload (URL + keys) is reused.
func (w *WA) Forward(srcChat, id string, dsts []string) error {
	src, ok := w.store.Message(srcChat, id)
	if !ok {
		return fmt.Errorf("message not found")
	}
	var firstErr error
	for _, dst := range dsts {
		msg := forwardCopy(src)
		local := Msg{Text: src.Text, Kind: src.Kind, Forwarded: true}
		if src.Kind == "image" || src.Kind == "sticker" {
			local.Media = src.Media
		}
		if err := w.send(dst, msg, local); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func forwardCopy(src Msg) *waE2E.Message {
	var m waE2E.Message
	if src.Raw == nil || proto.Unmarshal(src.Raw, &m) != nil {
		m = waE2E.Message{Conversation: proto.String(src.Text)}
	}
	score := uint32(1)
	if old := contextInfo(&m); old != nil && old.GetIsForwarded() {
		score = old.GetForwardingScore() + 1
	}
	ci := &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(score)}
	switch {
	case m.Conversation != nil:
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: m.Conversation, ContextInfo: ci}}
	case m.ExtendedTextMessage != nil:
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: m.ExtendedTextMessage.Text, ContextInfo: ci}}
	case m.ImageMessage != nil:
		m.ImageMessage.ContextInfo = ci
		return &waE2E.Message{ImageMessage: m.ImageMessage}
	case m.VideoMessage != nil:
		m.VideoMessage.ContextInfo = ci
		return &waE2E.Message{VideoMessage: m.VideoMessage}
	case m.AudioMessage != nil:
		m.AudioMessage.ContextInfo = ci
		return &waE2E.Message{AudioMessage: m.AudioMessage}
	case m.DocumentMessage != nil:
		m.DocumentMessage.ContextInfo = ci
		return &waE2E.Message{DocumentMessage: m.DocumentMessage}
	case m.StickerMessage != nil:
		m.StickerMessage.ContextInfo = ci
		return &waE2E.Message{StickerMessage: m.StickerMessage}
	case m.LocationMessage != nil:
		m.LocationMessage.ContextInfo = ci
		return &waE2E.Message{LocationMessage: m.LocationMessage}
	case m.ContactMessage != nil:
		m.ContactMessage.ContextInfo = ci
		return &waE2E.Message{ContactMessage: m.ContactMessage}
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(src.Text), ContextInfo: ci}}
}

type Participant struct {
	JID   string
	Admin string // "", "Admin", "Owner"
}

type ChatInfo struct {
	About        string
	Created      time.Time
	Participants []Participant
}

// Info fetches the "about" text of a contact, or description and members of a group.
func (w *WA) Info(chat string) (ChatInfo, error) {
	var out ChatInfo
	j, err := types.ParseJID(chat)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if j.Server == types.GroupServer {
		gi, err := w.cli.GetGroupInfo(ctx, j)
		if err != nil {
			return out, err
		}
		out.About, out.Created = gi.Topic, gi.GroupCreated
		for _, p := range gi.Participants {
			pj := p.JID
			if !p.PhoneNumber.IsEmpty() {
				pj = p.PhoneNumber
			}
			role := ""
			if p.IsSuperAdmin {
				role = "Owner"
			} else if p.IsAdmin {
				role = "Admin"
			}
			out.Participants = append(out.Participants, Participant{JID: w.canon(pj).String(), Admin: role})
		}
		return out, nil
	}
	infos, err := w.cli.GetUserInfo(ctx, []types.JID{j})
	if err != nil {
		return out, err
	}
	for _, ui := range infos {
		out.About = ui.Status
	}
	return out, nil
}

func copyToCache(dir string, data []byte, ext string) string {
	p := filepath.Join(dir, fmt.Sprintf("sent_%d%s", time.Now().UnixNano(), ext))
	if os.WriteFile(p, data, 0o600) != nil {
		return ""
	}
	return p
}

func makeThumb(data []byte) ([]byte, int, int) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0
	}
	b := img.Bounds()
	tw, th := 72, 72
	if b.Dx() > b.Dy() {
		th = max(1, 72*b.Dy()/b.Dx())
	} else {
		tw = max(1, 72*b.Dx()/b.Dy())
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	var buf bytes.Buffer
	jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 70})
	return buf.Bytes(), b.Dx(), b.Dy()
}

func (w *WA) senderOf(chat, id string) (types.JID, Msg, bool) {
	m, ok := w.store.Message(chat, id)
	if !ok {
		return types.EmptyJID, m, false
	}
	if m.FromMe {
		return w.ownJID(), m, true
	}
	s, _ := types.ParseJID(m.Sender)
	return s, m, true
}

func (w *WA) React(chat, id, emoji string) error {
	sender, m, ok := w.senderOf(chat, id)
	if !ok {
		return fmt.Errorf("message not found")
	}
	if m.Reactions[w.ownJID().String()] == emoji {
		emoji = "" // same reaction again removes it
	}
	to, _ := types.ParseJID(chat)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.cli.SendMessage(ctx, to, w.cli.BuildReaction(to, sender, id, emoji)); err != nil {
		return err
	}
	w.store.SetReaction(chat, id, w.ownJID().String(), emoji)
	w.ui.Changed(chat)
	return nil
}

func (w *WA) Edit(chat, id, text string) error {
	to, _ := types.ParseJID(chat)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.cli.SendMessage(ctx, to, w.cli.BuildEdit(to, id, &waE2E.Message{Conversation: proto.String(text)})); err != nil {
		return err
	}
	w.store.EditMessage(chat, id, text)
	w.ui.Changed(chat)
	return nil
}

func (w *WA) Revoke(chat, id string) error {
	sender, _, ok := w.senderOf(chat, id)
	if !ok {
		return fmt.Errorf("message not found")
	}
	to, _ := types.ParseJID(chat)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.cli.SendMessage(ctx, to, w.cli.BuildRevoke(to, sender, id)); err != nil {
		return err
	}
	w.store.DeleteMessage(chat, id)
	w.ui.Changed(chat)
	return nil
}

// ---- presence ----

func (w *WA) SetActive(active bool) {
	w.presMu.Lock()
	changed := w.active != active
	w.active = active
	w.presMu.Unlock()
	if changed {
		go w.sendOwnPresence()
	}
}

func (w *WA) sendOwnPresence() {
	if !w.cli.IsConnected() {
		return
	}
	w.presMu.Lock()
	p := types.PresenceUnavailable
	if w.active {
		p = types.PresenceAvailable
	}
	w.presMu.Unlock()
	w.cli.SendPresence(context.Background(), p)
}

// Subscribe asks for presence updates of a 1:1 chat partner.
func (w *WA) Subscribe(chat string) {
	j, err := types.ParseJID(chat)
	if err != nil || j.Server != types.DefaultUserServer || !w.cli.IsConnected() {
		return
	}
	w.presMu.Lock()
	if w.subscribed[chat] {
		w.presMu.Unlock()
		return
	}
	w.subscribed[chat] = true
	w.presMu.Unlock()
	go w.cli.SubscribePresence(context.Background(), j)
}

// PresenceText describes the chat partner's state: typing, online, last seen.
func (w *WA) PresenceText(chat string) (text string, typing bool) {
	w.presMu.Lock()
	t, isTyping := w.typing[chat]
	p, hasPresence := w.presence[chat]
	w.presMu.Unlock()
	if isTyping && time.Now().Before(t.until) {
		if strings.HasSuffix(chat, "@g.us") {
			name := w.DisplayName(t.sender)
			if f := strings.Fields(name); len(f) > 0 {
				name = f[0]
			}
			return name + " is typing…", true
		}
		return "typing…", true
	}
	if !hasPresence {
		return "", false
	}
	if p.Online {
		return "online", false
	}
	if p.LastSeen.IsZero() {
		return "", false
	}
	return "last seen " + lastSeen(p.LastSeen), false
}

func lastSeen(t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return "today at " + t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "yesterday at " + t.Format("15:04")
	default:
		return t.Format("02.01.2006 15:04")
	}
}

// Typing sends "composing" (throttled) and "paused" after a few idle seconds.
func (w *WA) Typing(chat string) {
	j, err := types.ParseJID(chat)
	if err != nil || !w.cli.IsConnected() {
		return
	}
	w.typeMu.Lock()
	defer w.typeMu.Unlock()
	if w.typeChat != chat || time.Since(w.typeSent) > 4*time.Second {
		w.typeChat, w.typeSent = chat, time.Now()
		go w.cli.SendChatPresence(context.Background(), j, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	}
	if w.typePause != nil {
		w.typePause.Stop()
	}
	w.typePause = time.AfterFunc(5*time.Second, func() { w.StopTyping(chat) })
}

func (w *WA) StopTyping(chat string) {
	w.typeMu.Lock()
	if w.typeChat != chat {
		w.typeMu.Unlock()
		return
	}
	w.typeChat = ""
	if w.typePause != nil {
		w.typePause.Stop()
	}
	w.typeMu.Unlock()
	if j, err := types.ParseJID(chat); err == nil && w.cli.IsConnected() {
		w.cli.SendChatPresence(context.Background(), j, types.ChatPresencePaused, types.ChatPresenceMediaText)
	}
}

// ---- chat state (synced to other devices via app state) ----

func (w *WA) SetPinned(chat string, pin bool) error {
	j, _ := types.ParseJID(chat)
	if err := w.cli.SendAppState(context.Background(), appstate.BuildPin(j, pin)); err != nil {
		return err
	}
	w.store.SetPinned(chat, pin)
	w.ui.Changed(chat)
	return nil
}

func (w *WA) SetArchived(chat string, archive bool) error {
	j, _ := types.ParseJID(chat)
	last, _ := w.store.LastMessage(chat)
	sender := types.EmptyJID
	if !last.FromMe {
		sender, _ = types.ParseJID(last.Sender)
	}
	key := w.cli.BuildMessageKey(j, sender, last.ID)
	if err := w.cli.SendAppState(context.Background(), appstate.BuildArchive(j, archive, time.Unix(last.TS, 0), key)); err != nil {
		return err
	}
	w.store.SetArchived(chat, archive)
	if archive {
		w.store.SetPinned(chat, false)
	}
	w.ui.Changed(chat)
	return nil
}

// SetMuted mutes for d (0 = unmute, <0 = forever).
func (w *WA) SetMuted(chat string, d time.Duration) error {
	j, _ := types.ParseJID(chat)
	var patch appstate.PatchInfo
	until := int64(0)
	switch {
	case d == 0:
		patch = appstate.BuildMute(j, false, 0)
	case d < 0:
		forever := int64(-1)
		patch = appstate.BuildMuteAbs(j, true, &forever)
		until = -1
	default:
		patch = appstate.BuildMute(j, true, d)
		until = time.Now().Add(d).Unix()
	}
	if err := w.cli.SendAppState(context.Background(), patch); err != nil {
		return err
	}
	w.store.SetMuted(chat, until)
	w.ui.Changed(chat)
	return nil
}

// MarkRead sends read receipts for everything unread in a chat.
func (w *WA) MarkRead(chat string) {
	unread := w.store.TakeUnread(chat)
	if len(unread) == 0 {
		return
	}
	chatJID, err := types.ParseJID(chat)
	if err != nil {
		return
	}
	bySender := map[string][]types.MessageID{}
	for _, m := range unread {
		bySender[m.Sender] = append(bySender[m.Sender], m.ID)
	}
	go func() {
		for s, ids := range bySender {
			var sender types.JID
			if chatJID.Server == types.GroupServer {
				sender, _ = types.ParseJID(s)
			}
			w.cli.MarkRead(context.Background(), ids, time.Now(), chatJID, sender)
		}
	}()
}

// RequestOlder asks the primary phone for older messages of a chat. They
// arrive asynchronously as an on-demand HistorySync event.
func (w *WA) RequestOlder(chat string) error {
	oldest, ok := w.store.OldestMessage(chat)
	if !ok || w.cli.Store.ID == nil {
		return fmt.Errorf("nothing to anchor on")
	}
	chatJID, err := types.ParseJID(chat)
	if err != nil {
		return err
	}
	sender, _ := types.ParseJID(oldest.Sender)
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chatJID, Sender: sender, IsFromMe: oldest.FromMe, IsGroup: chatJID.Server == types.GroupServer},
		ID:            oldest.ID,
		Timestamp:     time.Unix(oldest.TS, 0),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = w.cli.SendMessage(ctx, w.ownJID(), w.cli.BuildHistorySyncRequest(info, 50),
		whatsmeow.SendRequestExtra{Peer: true})
	return err
}

// CheckNumber resolves a phone number to a WhatsApp JID.
func (w *WA) CheckNumber(phone string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := w.cli.IsOnWhatsApp(ctx, []string{"+" + strings.TrimLeft(phone, "+")})
	if err != nil {
		return "", err
	}
	for _, r := range res {
		if r.IsIn {
			return r.JID.ToNonAD().String(), nil
		}
	}
	return "", fmt.Errorf("+%s is not on WhatsApp", strings.TrimLeft(phone, "+"))
}
