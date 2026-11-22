package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// emoji.tsv is derived from https://unicode.org/Public/emoji/latest/emoji-test.txt
// (fully-qualified, up to Emoji 15.1 for font coverage, no skin-tone variants): group \t emoji \t CLDR name
//
//go:embed emoji/emoji.tsv
var emojiTSV string

type Emoji struct {
	Char  string
	Name  string // CLDR name, e.g. "face with tears of joy"
	Code  string // shortcode, e.g. "face_with_tears_of_joy"
	Group string
}

// common Slack/GitHub-style aliases
var emojiAliases = map[string]string{
	"+1": "👍", "thumbsup": "👍", "-1": "👎", "thumbsdown": "👎", "joy": "😂", "rofl": "🤣", "smile": "😄",
	"grin": "😁", "laughing": "😆", "sweat_smile": "😅", "wink": "😉", "blush": "😊", "heart_eyes": "😍",
	"kiss": "😘", "kissing_heart": "😘", "yum": "😋", "stuck_out_tongue": "😛", "sunglasses": "😎", "smirk": "😏",
	"thinking": "🤔", "neutral": "😐", "expressionless": "😑", "unamused": "😒", "rolling_eyes": "🙄",
	"grimacing": "😬", "relieved": "😌", "pensive": "😔", "sleepy": "😪", "sleeping": "😴", "mask": "😷",
	"nerd": "🤓", "confused": "😕", "worried": "😟", "slightly_frowning": "🙁", "open_mouth": "😮",
	"astonished": "😲", "flushed": "😳", "pleading": "🥺", "cry": "😢", "sob": "😭", "scream": "😱",
	"angry": "😠", "rage": "😡", "skull": "💀", "poop": "💩", "clown": "🤡", "ghost": "👻", "alien": "👽",
	"robot": "🤖", "see_no_evil": "🙈", "hear_no_evil": "🙉", "speak_no_evil": "🙊", "heart": "❤️",
	"broken_heart": "💔", "sparkling_heart": "💖", "100": "💯", "fire": "🔥", "tada": "🎉", "party": "🥳",
	"sparkles": "✨", "star": "⭐", "boom": "💥", "zzz": "💤", "wave": "👋", "ok_hand": "👌", "ok": "👌",
	"v": "✌️", "crossed_fingers": "🤞", "point_up": "☝️", "point_right": "👉", "point_left": "👈",
	"clap": "👏", "raised_hands": "🙌", "pray": "🙏", "muscle": "💪", "eyes": "👀", "facepalm": "🤦",
	"shrug": "🤷", "hug": "🤗", "hugs": "🤗", "shush": "🤫", "zipper_mouth": "🤐", "money": "🤑",
	"upside_down": "🙃", "melting": "🫠", "salute": "🫡", "check": "✅", "white_check_mark": "✅",
	"x": "❌", "warning": "⚠️", "question": "❓", "exclamation": "❗", "rocket": "🚀", "coffee": "☕",
	"beer": "🍺", "beers": "🍻", "wine": "🍷", "pizza": "🍕", "cake": "🍰", "birthday": "🎂", "gift": "🎁",
	"sun": "☀️", "moon": "🌙", "rainbow": "🌈", "snowflake": "❄️", "umbrella": "☔", "dog": "🐶", "cat": "🐱",
	"car": "🚗", "house": "🏠", "phone": "📱", "computer": "💻", "bulb": "💡", "moneybag": "💰",
	"calendar": "📅", "lock": "🔒", "key": "🔑", "bell": "🔔", "music": "🎵", "camera": "📷",
}

type EmojiDB struct {
	All    []Emoji
	Groups []string
	byChar map[string]*Emoji

	mu         sync.Mutex
	recent     []string
	recentPath string
}

func NewEmojiDB(dataDir string) *EmojiDB {
	db := &EmojiDB{byChar: map[string]*Emoji{}, recentPath: filepath.Join(dataDir, "recent-emoji")}
	for _, line := range strings.Split(emojiTSV, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		name := strings.TrimSpace(f[2])
		code := strings.NewReplacer(" ", "_", ":", "", ",", "", "’", "", "“", "", "”", "", ".", "", "!", "").Replace(strings.ToLower(name))
		db.All = append(db.All, Emoji{Char: f[1], Name: name, Code: code, Group: f[0]})
		if len(db.Groups) == 0 || db.Groups[len(db.Groups)-1] != f[0] {
			db.Groups = append(db.Groups, f[0])
		}
	}
	for i := range db.All {
		db.byChar[db.All[i].Char] = &db.All[i]
	}
	if b, err := os.ReadFile(db.recentPath); err == nil {
		db.recent = strings.Fields(string(b))
	}
	if len(db.recent) == 0 {
		db.recent = []string{"👍", "❤️", "😂", "😮", "😢", "🙏", "🎉", "🔥"}
	}
	return db
}

func (db *EmojiDB) Recent() []Emoji {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []Emoji
	for _, c := range db.recent {
		if e, ok := db.byChar[c]; ok {
			out = append(out, *e)
		} else {
			out = append(out, Emoji{Char: c, Name: c})
		}
	}
	return out
}

func (db *EmojiDB) Use(char string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	r := []string{char}
	for _, c := range db.recent {
		if c != char && len(r) < 32 {
			r = append(r, c)
		}
	}
	db.recent = r
	os.WriteFile(db.recentPath, []byte(strings.Join(r, " ")), 0o600)
}

func (db *EmojiDB) Group(g string) []Emoji {
	if g == "Recent" {
		return db.Recent()
	}
	var out []Emoji
	for _, e := range db.All {
		if e.Group == g {
			out = append(out, e)
		}
	}
	return out
}

// Exact resolves a complete shortcode like "joy" or "face_with_tears_of_joy".
func (db *EmojiDB) Exact(code string) string {
	code = strings.ToLower(code)
	if c, ok := emojiAliases[code]; ok {
		return c
	}
	for _, e := range db.All {
		if e.Code == code {
			return e.Char
		}
	}
	return ""
}

// Search ranks emoji for a query: alias/code exact > prefix > word prefix > substring.
func (db *EmojiDB) Search(q string, limit int) []Emoji {
	q = strings.ToLower(strings.TrimSpace(strings.Trim(q, ":")))
	q = strings.ReplaceAll(q, " ", "_")
	if q == "" {
		return nil
	}
	type scored struct {
		e     Emoji
		score int
		idx   int
	}
	seen := map[string]bool{}
	var res []scored
	add := func(e Emoji, s, idx int) {
		if seen[e.Char] {
			return
		}
		seen[e.Char] = true
		res = append(res, scored{e, s, idx})
	}
	for alias, c := range emojiAliases {
		s := 0
		switch {
		case alias == q:
			s = 100
		case strings.HasPrefix(alias, q):
			s = 80
		}
		if s > 0 {
			e := Emoji{Char: c, Code: alias, Name: alias}
			if full, ok := db.byChar[c]; ok {
				e.Name = full.Name
			}
			add(e, s-len(alias), 0)
		}
	}
	for i, e := range db.All {
		s := 0
		switch {
		case e.Code == q:
			s = 95
		case strings.HasPrefix(e.Code, q):
			s = 70
		case strings.Contains("_"+e.Code, "_"+q):
			s = 50
		case strings.Contains(e.Code, q):
			s = 20
		}
		if s > 0 {
			add(e, s, i)
		}
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].score != res[j].score {
			return res[i].score > res[j].score
		}
		if res[i].idx != res[j].idx {
			return res[i].idx < res[j].idx
		}
		return res[i].e.Code < res[j].e.Code
	})
	out := make([]Emoji, 0, min(limit, len(res)))
	for i := 0; i < len(res) && i < limit; i++ {
		out = append(out, res[i].e)
	}
	return out
}
