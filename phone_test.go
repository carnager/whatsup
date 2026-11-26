package main

import "testing"

func TestNormalizeNumber(t *testing.T) {
	const de = "4915112345678"
	for _, c := range []struct{ in, own, want string }{
		{"171 234 5678", de, "491712345678"},
		{"171-234-5678", de, "491712345678"},
		{"0171 / 234 56 78", de, "491712345678"},
		{"0171.234.5678", de, "491712345678"},
		{"(0171) 2345678", de, "491712345678"},
		{"+49 (0)171 2345678", de, "491712345678"},
		{"+49 171 2345678", de, "491712345678"},
		{"0049 171 2345678", de, "491712345678"},
		{"491712345678", de, "491712345678"},
		{"30 1234567", de, "49301234567"},
		{"+1 (555) 123–4567", de, "15551234567"},
		{"tel:+43 664 1234567", de, "436641234567"},
		{"555 123 4567", "15551234567", "15551234567"},
		{"1712345678", "", "1712345678"}, // own number unknown yet
		{"0171234567", "", "0171234567"},
		{"0171 234567", "12025550100", "1171234567"},
		{"0171 234567", "351912345678", "351171234567"},
		{"12345", de, ""},
		{"call 0171 234", de, ""},
		{"+49 171 abc", de, ""},
	} {
		if got := normalizeNumber(c.in, c.own); got != c.want {
			t.Errorf("normalizeNumber(%q, %q) = %q, want %q", c.in, c.own, got, c.want)
		}
	}
}

func TestFormatNowPlaying(t *testing.T) {
	for _, c := range []struct{ artist, title, want string }{
		{"Artist", "Title", "np: Artist – Title"},
		{"", "Title", "np: Title"},
		{"A, B", "Two\nLines ", "np: A, B – Two Lines"},
		{"Artist", "", ""},
	} {
		if got := formatNowPlaying(c.artist, c.title); got != c.want {
			t.Errorf("formatNowPlaying(%q, %q) = %q, want %q", c.artist, c.title, got, c.want)
		}
	}
	if !isNowPlaying("/NP") || isNowPlaying("/np now") || isNowPlaying("np") {
		t.Error("isNowPlaying")
	}
}

func TestPhoneQuery(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"0171-234", "171234", true},
		{"+49 171", "49171", true},
		{"(0171) 23", "17123", true},
		{"12", "", false},
		{"anna", "", false},
		{"171 abc", "", false},
	} {
		got, ok := phoneQuery(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("phoneQuery(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
