package main

import (
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *MsgStore {
	t.Helper()
	s, err := OpenMsgStore(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}

func TestRemoveChatStaysDeleted(t *testing.T) {
	s := testStore(t)
	const c = "1@s.whatsapp.net"
	s.AddMessage(Msg{ID: "a", Chat: c, TS: 100}, true)
	s.AddMessage(Msg{ID: "b", Chat: c, TS: 200}, true)

	if ids := s.RemoveChat(c, 200, false); len(ids) != 2 {
		t.Fatalf("removed %d messages, want 2", len(ids))
	}
	if _, ok := s.Chat(c); ok {
		t.Fatal("chat still exists")
	}
	// a later history sync must not bring it back
	s.AddMessage(Msg{ID: "a", Chat: c, TS: 100}, false)
	s.UpsertChat(c, "Old", 200, 0)
	if _, ok := s.Chat(c); ok {
		t.Fatal("history sync restored a deleted chat")
	}
	// a genuinely new message does
	if !s.AddMessage(Msg{ID: "n", Chat: c, TS: 300}, true) {
		t.Fatal("new message rejected")
	}
	if got := s.Messages(c, 10); len(got) != 1 || got[0].ID != "n" {
		t.Fatalf("messages after reopen: %+v", got)
	}
}

func TestRemoveChatKeepsNewerMessages(t *testing.T) {
	s := testStore(t)
	const c = "1@s.whatsapp.net"
	s.AddMessage(Msg{ID: "a", Chat: c, TS: 100}, true)
	s.AddMessage(Msg{ID: "b", Chat: c, TS: 300}, true)

	s.RemoveChat(c, 200, false)
	got, ok := s.Chat(c)
	if !ok || got.LastTS != 300 || got.Unread != 1 {
		t.Fatalf("chat after partial delete: %+v (exists %v)", got, ok)
	}
}

func TestClearChatKeepsRow(t *testing.T) {
	s := testStore(t)
	const c = "1@s.whatsapp.net"
	s.AddMessage(Msg{ID: "a", Chat: c, TS: 100}, true)

	s.RemoveChat(c, 100, true)
	got, ok := s.Chat(c)
	if !ok || got.Unread != 0 {
		t.Fatalf("cleared chat: %+v (exists %v)", got, ok)
	}
	if n := len(s.Messages(c, 10)); n != 0 {
		t.Fatalf("%d messages left", n)
	}
}

func TestInfoHistory(t *testing.T) {
	s := testStore(t)
	const c = "1@s.whatsapp.net"
	s.AddMessage(Msg{ID: "i", Chat: c, TS: 100, Kind: "image"}, false)
	s.AddMessage(Msg{ID: "v", Chat: c, TS: 200, Kind: "video"}, false)
	s.AddMessage(Msg{ID: "d", Chat: c, TS: 300, Kind: "document", Text: "📄 a.pdf"}, false)
	s.AddMessage(Msg{ID: "l", Chat: c, TS: 400, Text: "see https://example.com/x"}, false)
	s.AddMessage(Msg{ID: "x", Chat: c, TS: 500, Kind: "image"}, false)
	s.DeleteMessage(c, "x")

	got := s.ChatCounts(c)
	if got.Messages != 4 || got.Media != 2 || got.Files != 1 || got.First != 100 {
		t.Fatalf("counts: %+v", got)
	}
	if m := s.History(c, 1, "image", "video"); len(m) != 1 || m[0].ID != "v" {
		t.Fatalf("media history: %+v", m)
	}
	if m := s.LinkMessages(c); len(m) != 1 || m[0].ID != "l" {
		t.Fatalf("link messages: %+v", m)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{7: "7", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
