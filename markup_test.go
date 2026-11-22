package main

import (
	"strings"
	"testing"
)

func TestNormalizeMarkup(t *testing.T) {
	cases := map[string]string{
		"**bold** and __it__ and ~~gone~~": "*bold* and _it_ and ~gone~",
		"keep `**code**` as is":            "keep `**code**` as is",
		"```\n**x**\n```":                  "```\n**x**\n```",
		"snake_case_name stays":            "snake_case_name stays",
		"*already* fine":                   "*already* fine",
	}
	for in, want := range cases {
		if got := normalizeMarkup(in); got != want {
			t.Errorf("normalizeMarkup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderMarkup(t *testing.T) {
	st := markupStyle{codeBg: "#000", quoteBar: "#0f0", mention: "#00f"}
	check := func(in string, wants ...string) {
		t.Helper()
		got := renderMarkup(in, st, nil)
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("renderMarkup(%q) = %q, missing %q", in, got, w)
			}
		}
	}
	check("*a* *b*", "<b>a</b> <b>b</b>")
	check("**a** and _b_ ~c~", "<b>a</b>", "<i>b</i>", "<s>c</s>")
	check("__x__", "<i>x</i>")
	check("```\nfunc main() {\n  *x*\n}\n```", "white-space:pre-wrap", "*x*")
	check("use `go *build*` now", "go *build*")
	check("> quoted\nreply", `background-color:#0f0`, "quoted", "reply")
	check("- one\n- two", "•&nbsp;&nbsp;one", "•&nbsp;&nbsp;two")
	check("see https://example.com/a_b_c.", `href="https://example.com/a_b_c"`)
	if got := renderMarkup("snake_case_name", st, nil); strings.Contains(got, "<i>") {
		t.Errorf("snake_case got italicized: %q", got)
	}
	if got := renderMarkup("<script>", st, nil); strings.Contains(got, "<script>") {
		t.Errorf("html not escaped: %q", got)
	}
}
