package main

import (
	"path/filepath"
	"testing"
)

func TestNormalizeNumber(t *testing.T) {
	cases := []struct{ in, own, want string }{
		{"+49 171 234-567", "4915100000", "49171234567"},
		{"0049 171 234567", "4915100000", "49171234567"},
		{"0171 234567", "4915100000", "49171234567"},
		{"0171 234567", "12025550100", "1171234567"},
		{"0171 234567", "351912345678", "351171234567"},
		{"49171234567", "12025550100", "49171234567"},
		{"0171234567", "", "0171234567"},
	}
	for _, c := range cases {
		if got := normalizeNumber(c.in, c.own); got != c.want {
			t.Errorf("normalizeNumber(%q, %q) = %q, want %q", c.in, c.own, got, c.want)
		}
	}
}

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
