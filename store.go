package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// MsgStore keeps chats and messages locally. whatsmeow itself only stores
// crypto/session state, so history lives here.
type MsgStore struct {
	db *sql.DB
	mu sync.Mutex
}

type Chat struct {
	JID        string
	Name       string
	LastTS     int64
	LastText   string
	LastFromMe bool
	LastKind   string
	Unread     int
	Pinned     bool
	Archived   bool
	MutedUntil int64 // unix seconds; -1 = forever; 0 = not muted
}

func (c Chat) Muted(now int64) bool { return c.MutedUntil < 0 || c.MutedUntil > now }

type Msg struct {
	ID          string
	Chat        string
	Sender      string
	SenderName  string
	FromMe      bool
	TS          int64
	Text        string
	Kind        string // "", image, video, audio, voice, document, sticker
	Media       string // local file: thumbnail or full media
	Status      int    // own messages: 0 sent, 1 delivered, 2 read
	Raw         []byte // serialized waE2E.Message
	QuoteID     string
	QuoteSender string
	QuoteText   string
	Reactions   map[string]string // sender JID -> emoji
	Edited      bool
	Deleted     bool
	Forwarded   bool
}

const (
	StatusSent = iota
	StatusDelivered
	StatusRead
)

func OpenMsgStore(path string) (*MsgStore, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS chats (
	jid       TEXT PRIMARY KEY,
	name      TEXT NOT NULL DEFAULT '',
	last_ts   INTEGER NOT NULL DEFAULT 0,
	unread    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
	chat        TEXT NOT NULL,
	id          TEXT NOT NULL,
	sender      TEXT NOT NULL DEFAULT '',
	sender_name TEXT NOT NULL DEFAULT '',
	from_me     INTEGER NOT NULL DEFAULT 0,
	ts          INTEGER NOT NULL,
	text        TEXT NOT NULL DEFAULT '',
	media       TEXT NOT NULL DEFAULT '',
	status      INTEGER NOT NULL DEFAULT 0,
	read        INTEGER NOT NULL DEFAULT 0,
	raw         BLOB,
	PRIMARY KEY (chat, id)
);
CREATE INDEX IF NOT EXISTS messages_chat_ts ON messages(chat, ts);
CREATE TABLE IF NOT EXISTS link_previews (
	url     TEXT PRIMARY KEY,
	title   TEXT NOT NULL DEFAULT '',
	descr   TEXT NOT NULL DEFAULT '',
	site    TEXT NOT NULL DEFAULT '',
	image   TEXT NOT NULL DEFAULT '',
	video   INTEGER NOT NULL DEFAULT 0,
	fetched INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS chat_tombstones (
	jid  TEXT PRIMARY KEY,
	upto INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`)
	if err != nil {
		return nil, err
	}
	// additive migrations; "duplicate column" errors are expected on reruns
	for _, m := range []string{
		`ALTER TABLE chats ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE chats ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE chats ADD COLUMN muted_until INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN quote_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN quote_sender TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN quote_text TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN reactions TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN edited INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN forwarded INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
	return &MsgStore{db: db}, nil
}

func (s *MsgStore) exec(q string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Exec(q, args...)
}

// Setting returns a local app setting, "" if unset.
func (s *MsgStore) Setting(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v string
	s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *MsgStore) SetSetting(key, value string) {
	s.exec(`INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
}

// UpsertChat creates/updates a chat. unread < 0 keeps the current count.
// State older than a deletion of the chat is ignored.
func (s *MsgStore) UpsertChat(jid, name string, ts int64, unread int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts <= s.deletedUpto(jid) {
		return
	}
	s.db.Exec(`INSERT INTO chats(jid, name, last_ts, unread) VALUES(?,?,?,MAX(?,0))
		ON CONFLICT(jid) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			last_ts = MAX(chats.last_ts, excluded.last_ts),
			unread = CASE WHEN ? >= 0 THEN excluded.unread ELSE chats.unread END`,
		jid, name, ts, unread, unread)
}

func (s *MsgStore) SetChatName(jid, name string) {
	if name != "" {
		s.exec(`UPDATE chats SET name = ? WHERE jid = ?`, name, jid)
	}
}

func (s *MsgStore) SetPinned(jid string, v bool) {
	s.exec(`UPDATE chats SET pinned = ? WHERE jid = ?`, v, jid)
}
func (s *MsgStore) SetArchived(jid string, v bool) {
	s.exec(`UPDATE chats SET archived = ? WHERE jid = ?`, v, jid)
}
func (s *MsgStore) SetMuted(jid string, until int64) {
	s.exec(`UPDATE chats SET muted_until = ? WHERE jid = ?`, until, jid)
}
func (s *MsgStore) SetUnread(jid string, n int) {
	s.exec(`UPDATE chats SET unread = ? WHERE jid = ?`, n, jid)
}

// ClearChatName forgets a stored name, e.g. when a contact was removed.
func (s *MsgStore) ClearChatName(jid string) {
	s.exec(`UPDATE chats SET name = '' WHERE jid = ?`, jid)
}

// deletedUpto returns the timestamp up to which a chat was deleted or
// cleared. The caller holds s.mu.
func (s *MsgStore) deletedUpto(jid string) int64 {
	var upto int64
	s.db.QueryRow(`SELECT upto FROM chat_tombstones WHERE jid = ?`, jid).Scan(&upto)
	return upto
}

// RemoveChat deletes all messages up to and including upto and returns their
// IDs. Newer messages survive, since they arrived after the deletion. The
// chat row goes too when nothing is left, unless keepRow is set (clear chat).
// The tombstone stops history sync from restoring what was removed.
func (s *MsgStore) RemoveChat(jid string, upto int64, keepRow bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Exec(`INSERT INTO chat_tombstones(jid, upto) VALUES(?,?)
		ON CONFLICT(jid) DO UPDATE SET upto = MAX(upto, excluded.upto)`, jid, upto)
	var ids []string
	if rows, err := s.db.Query(`SELECT id FROM messages WHERE chat = ? AND ts <= ?`, jid, upto); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
	}
	s.db.Exec(`DELETE FROM messages WHERE chat = ? AND ts <= ?`, jid, upto)
	var left int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat = ?`, jid).Scan(&left)
	switch {
	case left > 0:
		s.db.Exec(`UPDATE chats SET
			last_ts = (SELECT MAX(ts) FROM messages WHERE chat = ?1),
			unread = (SELECT COUNT(*) FROM messages WHERE chat = ?1 AND read = 0 AND from_me = 0)
			WHERE jid = ?1`, jid)
	case keepRow:
		s.db.Exec(`UPDATE chats SET unread = 0 WHERE jid = ?`, jid)
	default:
		s.db.Exec(`DELETE FROM chats WHERE jid = ?`, jid)
	}
	return ids
}

// RemoveMessage drops one message entirely ("delete for me").
func (s *MsgStore) RemoveMessage(chat, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db.Exec(`DELETE FROM messages WHERE chat = ? AND id = ?`, chat, id)
	s.db.Exec(`UPDATE chats SET
		last_ts = COALESCE((SELECT MAX(ts) FROM messages WHERE chat = ?1), last_ts),
		unread = (SELECT COUNT(*) FROM messages WHERE chat = ?1 AND read = 0 AND from_me = 0)
		WHERE jid = ?1`, chat)
}

// MediaInUse reports whether a stored message still references a file.
func (s *MsgStore) MediaInUse(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE media = ?`, path).Scan(&n)
	return n > 0
}

// AddMessage stores a message. Returns true if it was new.
func (s *MsgStore) AddMessage(m Msg, countUnread bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.TS <= s.deletedUpto(m.Chat) {
		return false // removed on some device; don't let history bring it back
	}
	read := 1
	if countUnread && !m.FromMe {
		read = 0
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO messages(chat,id,sender,sender_name,from_me,ts,text,kind,media,status,read,raw,quote_id,quote_sender,quote_text,forwarded)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Chat, m.ID, m.Sender, m.SenderName, m.FromMe, m.TS, m.Text, m.Kind, m.Media, m.Status, read, m.Raw,
		m.QuoteID, m.QuoteSender, m.QuoteText, m.Forwarded)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	inc := 0
	if read == 0 {
		inc = 1
	}
	s.db.Exec(`INSERT INTO chats(jid, last_ts, unread) VALUES(?,?,?)
		ON CONFLICT(jid) DO UPDATE SET last_ts = MAX(chats.last_ts, excluded.last_ts), unread = chats.unread + excluded.unread`,
		m.Chat, m.TS, inc)
	// new activity unarchives a chat (unless muted), like the phone does
	if read == 0 {
		s.db.Exec(`UPDATE chats SET archived = 0 WHERE jid = ? AND muted_until = 0`, m.Chat)
	}
	return true
}

func (s *MsgStore) EditMessage(chat, id, text string) {
	s.exec(`UPDATE messages SET text = ?, edited = 1 WHERE chat = ? AND id = ?`, text, chat, id)
}

func (s *MsgStore) DeleteMessage(chat, id string) {
	s.exec(`UPDATE messages SET text = '', media = '', kind = '', deleted = 1, reactions = '' WHERE chat = ? AND id = ?`, chat, id)
}

func (s *MsgStore) SetMedia(chat, id, path string) {
	s.exec(`UPDATE messages SET media = ? WHERE chat = ? AND id = ?`, path, chat, id)
}

func (s *MsgStore) SetReaction(chat, id, sender, emoji string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if s.db.QueryRow(`SELECT reactions FROM messages WHERE chat = ? AND id = ?`, chat, id).Scan(&raw) != nil {
		return false
	}
	r := map[string]string{}
	json.Unmarshal([]byte(raw), &r)
	if emoji == "" {
		delete(r, sender)
	} else {
		r[sender] = emoji
	}
	out := ""
	if len(r) > 0 {
		b, _ := json.Marshal(r)
		out = string(b)
	}
	s.db.Exec(`UPDATE messages SET reactions = ? WHERE chat = ? AND id = ?`, out, chat, id)
	return true
}

func (s *MsgStore) SetStatus(chat string, ids []string, status int) {
	if len(ids) == 0 {
		return
	}
	q := `UPDATE messages SET status = MAX(status, ?) WHERE chat = ? AND from_me = 1 AND id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := []any{status, chat}
	for _, id := range ids {
		args = append(args, id)
	}
	s.exec(q, args...)
}

func (s *MsgStore) Chats() []Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT c.jid, c.name, c.last_ts, c.unread, c.pinned, c.archived, c.muted_until,
		COALESCE(m.text, ''), COALESCE(m.from_me, 0), COALESCE(m.kind, '')
		FROM chats c LEFT JOIN messages m ON m.rowid = (SELECT rowid FROM messages WHERE chat = c.jid AND deleted = 0 ORDER BY ts DESC LIMIT 1)
		WHERE c.last_ts > 0 ORDER BY c.pinned DESC, c.last_ts DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		rows.Scan(&c.JID, &c.Name, &c.LastTS, &c.Unread, &c.Pinned, &c.Archived, &c.MutedUntil, &c.LastText, &c.LastFromMe, &c.LastKind)
		out = append(out, c)
	}
	return out
}

func (s *MsgStore) Chat(jid string) (Chat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Chat{JID: jid}
	err := s.db.QueryRow(`SELECT name, last_ts, unread, pinned, archived, muted_until FROM chats WHERE jid = ?`, jid).
		Scan(&c.Name, &c.LastTS, &c.Unread, &c.Pinned, &c.Archived, &c.MutedUntil)
	return c, err == nil
}

func (s *MsgStore) ChatName(jid string) string {
	c, _ := s.Chat(jid)
	return c.Name
}

// SenderName returns the most recent push name seen for a sender.
func (s *MsgStore) SenderName(jid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n string
	s.db.QueryRow(`SELECT sender_name FROM messages WHERE sender = ? AND sender_name != '' ORDER BY ts DESC LIMIT 1`, jid).Scan(&n)
	return n
}

// ChatCounts summarizes the stored history of a chat.
type ChatCounts struct {
	Messages, Media, Files int
	First                  int64
}

func (s *MsgStore) ChatCounts(chat string) ChatCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c ChatCounts
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(ts), 0),
		COALESCE(SUM(kind IN ('image','video')), 0), COALESCE(SUM(kind = 'document'), 0)
		FROM messages WHERE chat = ? AND deleted = 0`, chat).Scan(&c.Messages, &c.First, &c.Media, &c.Files)
	return c
}

// History returns the newest messages of the given kinds, newest first.
func (s *MsgStore) History(chat string, limit int, kinds ...string) []Msg {
	if len(kinds) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	args := []any{chat}
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id, kind, media, text, ts, from_me FROM messages
		WHERE chat = ? AND deleted = 0 AND kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`)
		ORDER BY ts DESC, rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Msg
	for rows.Next() {
		m := Msg{Chat: chat}
		if rows.Scan(&m.ID, &m.Kind, &m.Media, &m.Text, &m.TS, &m.FromMe) == nil {
			out = append(out, m)
		}
	}
	return out
}

// LinkMessages returns messages that may contain a link, newest first.
func (s *MsgStore) LinkMessages(chat string) []Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, text, ts FROM messages
		WHERE chat = ? AND deleted = 0 AND text LIKE '%http%' ORDER BY ts DESC, rowid DESC`, chat)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Msg
	for rows.Next() {
		m := Msg{Chat: chat}
		if rows.Scan(&m.ID, &m.Text, &m.TS) == nil {
			out = append(out, m)
		}
	}
	return out
}

// UntypedMedia returns messages stored before kinds were tracked.
func (s *MsgStore) UntypedMedia() []Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT chat, id, media, raw FROM messages WHERE kind = '' AND raw IS NOT NULL AND deleted = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Msg
	for rows.Next() {
		var m Msg
		if rows.Scan(&m.Chat, &m.ID, &m.Media, &m.Raw) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *MsgStore) SetKind(chat, id, kind, text, media string) {
	s.exec(`UPDATE messages SET kind = ?, text = ?, media = ? WHERE chat = ? AND id = ?`, kind, text, media, chat, id)
}

const msgCols = `id, chat, sender, sender_name, from_me, ts, text, kind, media, status, raw, quote_id, quote_sender, quote_text, reactions, edited, deleted, forwarded`

func scanMsg(sc interface{ Scan(...any) error }) (Msg, error) {
	var m Msg
	var reactions string
	err := sc.Scan(&m.ID, &m.Chat, &m.Sender, &m.SenderName, &m.FromMe, &m.TS, &m.Text, &m.Kind, &m.Media, &m.Status, &m.Raw,
		&m.QuoteID, &m.QuoteSender, &m.QuoteText, &reactions, &m.Edited, &m.Deleted, &m.Forwarded)
	if reactions != "" {
		json.Unmarshal([]byte(reactions), &m.Reactions)
	}
	return m, err
}

// Messages returns the newest `limit` messages of a chat, oldest first.
func (s *MsgStore) Messages(chat string, limit int) []Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM
		(SELECT *, rowid AS rid FROM messages WHERE chat = ? ORDER BY ts DESC, rowid DESC LIMIT ?) ORDER BY ts ASC, rid ASC`, chat, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Msg
	for rows.Next() {
		if m, err := scanMsg(rows); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *MsgStore) Message(chat, id string) (Msg, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := scanMsg(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE chat = ? AND id = ?`, chat, id))
	return m, err == nil
}

func (s *MsgStore) LastMessage(chat string) (Msg, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := scanMsg(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE chat = ? ORDER BY ts DESC LIMIT 1`, chat))
	return m, err == nil
}

// OldestMessage returns the oldest stored message of a chat (for on-demand history requests).
func (s *MsgStore) OldestMessage(chat string) (Msg, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := scanMsg(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE chat = ? ORDER BY ts ASC LIMIT 1`, chat))
	return m, err == nil
}

func (s *MsgStore) Raw(chat, id string) []byte {
	m, _ := s.Message(chat, id)
	return m.Raw
}

// TakeUnread marks all messages of a chat read and returns the ones that were unread,
// so read receipts can be sent.
func (s *MsgStore) TakeUnread(chat string) []Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, sender, ts FROM messages WHERE chat = ? AND read = 0 AND from_me = 0`, chat)
	var out []Msg
	if err == nil {
		for rows.Next() {
			m := Msg{Chat: chat}
			rows.Scan(&m.ID, &m.Sender, &m.TS)
			out = append(out, m)
		}
		rows.Close()
	}
	s.db.Exec(`UPDATE messages SET read = 1 WHERE chat = ? AND read = 0`, chat)
	s.db.Exec(`UPDATE chats SET unread = 0 WHERE jid = ?`, chat)
	return out
}

// LinkPreview returns a cached link preview and when it was fetched.
func (s *MsgStore) LinkPreview(url string) (*LinkPreview, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lp := &LinkPreview{URL: url}
	var fetched int64
	err := s.db.QueryRow(`SELECT title, descr, site, image, video, fetched FROM link_previews WHERE url = ?`, url).
		Scan(&lp.Title, &lp.Desc, &lp.Site, &lp.Image, &lp.Video, &fetched)
	if err != nil {
		return nil, 0, false
	}
	if lp.Image != "" {
		if _, err := os.Stat(lp.Image); err != nil {
			lp.Image = "" // cache was cleaned
		}
	}
	return lp, fetched, true
}

func (s *MsgStore) SaveLinkPreview(lp *LinkPreview) {
	s.exec(`INSERT OR REPLACE INTO link_previews (url, title, descr, site, image, video, fetched) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		lp.URL, lp.Title, lp.Desc, lp.Site, lp.Image, lp.Video, time.Now().Unix())
}
