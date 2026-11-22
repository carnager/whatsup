package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// WhatsApp text formatting:
//   *bold*  _italic_  ~strike~  `inline code`  ```code block```
//   > quote   - list / * list   1. numbered list
// The doubled markdown-style markers (**bold**, __italic__, ~~strike~~) are
// rendered too, and normalized to WhatsApp's single markers when sending.

var (
	codeBlockRe  = regexp.MustCompile("(?s)```\n?(.+?)\n?```")
	inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")
	urlRe        = regexp.MustCompile(`https?://[^\s<>"]+[^\s<>".,;:!?)\]]`)
	mentionRe    = regexp.MustCompile(`@(\d{6,})`)

	// a marker must sit at a word boundary: start/space/punctuation outside,
	// non-space right inside (like WhatsApp)
	dblBoldRe   = regexp.MustCompile(`(^|[^\w*])\*\*(\S(?:[^*\n]*\S)?)\*\*([^\w*]|$)`)
	boldRe      = regexp.MustCompile(`(^|[^\w*])\*(\S(?:[^*\n]*\S)?)\*([^\w*]|$)`)
	dblItalicRe = regexp.MustCompile(`(^|[^\w_])__(\S(?:[^_\n]*\S)?)__([^\w_]|$)`)
	italicRe    = regexp.MustCompile(`(^|[^\w_])_(\S(?:[^_\n]*\S)?)_([^\w_]|$)`)
	dblStrikeRe = regexp.MustCompile(`(^|[^\w~])~~(\S(?:[^~\n]*\S)?)~~([^\w~]|$)`)
	strikeRe    = regexp.MustCompile(`(^|[^\w~])~(\S(?:[^~\n]*\S)?)~([^\w~]|$)`)

	listRe     = regexp.MustCompile(`^[-*] +(.*)$`)
	numListRe  = regexp.MustCompile(`^(\d{1,3})\. +(.*)$`)
	quoteLnRe  = regexp.MustCompile(`^&gt; ?(.*)$`)
	placeRe    = regexp.MustCompile("\x00(\\d+)\x00")
	codeSpanRe = regexp.MustCompile("(?s)```.*?```|`[^`\n]+`")
)

// replaceAll applies a boundary-matching regex until nothing changes, since
// adjacent matches share their boundary character.
func replaceAll(re *regexp.Regexp, s, repl string) string {
	for i := 0; i < 4; i++ {
		n := re.ReplaceAllString(s, repl)
		if n == s {
			break
		}
		s = n
	}
	return s
}

// normalizeMarkup converts markdown-style doubled markers into WhatsApp's
// single markers, leaving code untouched.
func normalizeMarkup(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range codeSpanRe.FindAllStringIndex(s, -1) {
		b.WriteString(normalizeSpan(s[last:loc[0]]))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(normalizeSpan(s[last:]))
	return b.String()
}

func normalizeSpan(s string) string {
	s = replaceAll(dblBoldRe, s, "$1*$2*$3")
	s = replaceAll(dblItalicRe, s, "${1}_${2}_${3}")
	return replaceAll(dblStrikeRe, s, "$1~$2~$3")
}

// stripMarkup removes formatting markers for plain-text previews.
func stripMarkup(s string) string {
	s = codeBlockRe.ReplaceAllString(s, "$1")
	s = inlineCodeRe.ReplaceAllString(s, "$1")
	for _, re := range []*regexp.Regexp{dblBoldRe, boldRe, dblItalicRe, italicRe, dblStrikeRe, strikeRe} {
		s = replaceAll(re, s, "$1$2$3")
	}
	return s
}

type markupStyle struct {
	codeBg, quoteBar, mention string
}

// renderMarkup turns message text into Qt rich text.
func renderMarkup(s string, st markupStyle, mentionName func(num string) string) string {
	var slots []string
	slot := func(h string) string {
		slots = append(slots, h)
		return fmt.Sprintf("\x00%d\x00", len(slots)-1)
	}
	mono := `font-family:'JetBrains Mono','Fira Code','DejaVu Sans Mono',monospace;`

	// code first: nothing inside is formatted
	s = codeBlockRe.ReplaceAllStringFunc(s, func(m string) string {
		code := codeBlockRe.FindStringSubmatch(m)[1]
		return slot(`<table width="100%" cellpadding="8" cellspacing="0" style="margin:3px 0; background-color:` + st.codeBg + `"><tr><td>` +
			`<span style="` + mono + ` white-space:pre-wrap; font-size:13px">` + html.EscapeString(code) + `</span></td></tr></table>`)
	})
	s = inlineCodeRe.ReplaceAllStringFunc(s, func(m string) string {
		code := inlineCodeRe.FindStringSubmatch(m)[1]
		return slot(`<span style="` + mono + ` background-color:` + st.codeBg + `; font-size:13px">&nbsp;` + html.EscapeString(code) + `&nbsp;</span>`)
	})

	s = html.EscapeString(s)
	s = urlRe.ReplaceAllStringFunc(s, func(u string) string {
		return slot(`<a href="` + u + `">` + u + `</a>`)
	})
	if mentionName != nil {
		s = mentionRe.ReplaceAllStringFunc(s, func(m string) string {
			name := mentionName(m[1:])
			if name == "" {
				return m
			}
			return slot(`<b style="color:` + st.mention + `">@` + html.EscapeString(name) + `</b>`)
		})
	}

	s = replaceAll(dblBoldRe, s, "$1<b>$2</b>$3")
	s = replaceAll(boldRe, s, "$1<b>$2</b>$3")
	s = replaceAll(dblItalicRe, s, "$1<i>$2</i>$3")
	s = replaceAll(italicRe, s, "$1<i>$2</i>$3")
	s = replaceAll(dblStrikeRe, s, "$1<s>$2</s>$3")
	s = replaceAll(strikeRe, s, "$1<s>$2</s>$3")

	// line-level: quotes and lists
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if quoteLnRe.MatchString(ln) {
			var q []string
			for ; i < len(lines) && quoteLnRe.MatchString(lines[i]); i++ {
				q = append(q, quoteLnRe.FindStringSubmatch(lines[i])[1])
			}
			i--
			out = append(out, slot(`<table cellspacing="0" cellpadding="0" style="margin:2px 0"><tr>`+
				`<td width="3" style="background-color:`+st.quoteBar+`"></td><td width="8"></td>`+
				`<td><span style="color:gray">`+strings.Join(q, "<br>")+`</span></td></tr></table>`))
			continue
		}
		if m := listRe.FindStringSubmatch(ln); m != nil {
			ln = "&nbsp;&nbsp;•&nbsp;&nbsp;" + m[1]
		} else if m := numListRe.FindStringSubmatch(ln); m != nil {
			ln = "&nbsp;&nbsp;" + m[1] + ".&nbsp;&nbsp;" + m[2]
		}
		out = append(out, ln)
	}
	var b strings.Builder
	for i, ln := range out {
		b.WriteString(ln)
		// block slots (tables) bring their own line break, before and after
		if i < len(out)-1 && !isBlockSlot(ln, slots) && !isBlockSlot(out[i+1], slots) {
			b.WriteString("<br>")
		}
	}
	s = b.String()

	// restore slots (they may nest, e.g. a link inside a quote)
	for i := 0; i < 3 && strings.Contains(s, "\x00"); i++ {
		s = placeRe.ReplaceAllStringFunc(s, func(m string) string {
			var n int
			fmt.Sscan(placeRe.FindStringSubmatch(m)[1], &n)
			return slots[n]
		})
	}
	return s
}

func isBlockSlot(ln string, slots []string) bool {
	m := placeRe.FindStringSubmatch(ln)
	if m == nil || m[0] != ln {
		return false
	}
	var n int
	fmt.Sscan(m[1], &n)
	return strings.HasPrefix(slots[n], "<table")
}
