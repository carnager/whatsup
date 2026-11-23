package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mappu/miqt/qt6/mainthread"
)

const infoPage = 30 // items per "show more" in the info panel history

type infoDetail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// mediaFile resolves a viewer item to a local file. Besides message IDs it
// accepts "avatar:<jid>" for a full-size profile photo.
func (a *App) mediaFile(chat, id string) (string, error) {
	if jid, ok := strings.CutPrefix(id, "avatar:"); ok {
		p, err := a.avatars.Original(jid)
		if err == nil && p == "" {
			err = fmt.Errorf("no profile photo")
		}
		return p, err
	}
	return a.wa.DownloadMedia(chat, id)
}

// saveName is the suggested file name when saving a viewer item.
func (a *App) saveName(id, path string) string {
	jid, ok := strings.CutPrefix(id, "avatar:")
	if !ok {
		return displayName(path)
	}
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return -1
		}
		return r
	}, strings.TrimPrefix(a.wa.DisplayName(jid), "~"))
	return strings.TrimSpace(name) + " profile photo.jpg"
}

// mediaTile returns the picture for a gallery tile, fetching what's missing.
// Only the page on screen gets here, so history isn't downloaded wholesale.
func (a *App) mediaTile(chat string, m Msg) string {
	if m.Kind == "video" && !strings.HasSuffix(m.Media, "_thumb.jpg") {
		mi, ok := a.wa.MediaInfo(chat, m.ID)
		if !ok {
			return ""
		}
		if mi.Local != "" {
			if p := a.previews.Get(mi.Local, "video", func() { a.markDirty("*info") }); p != "" {
				return "file://" + p
			}
		} else if mi.Size <= 32<<20 {
			a.wa.AutoDownload(chat, m.ID)
		}
		return ""
	}
	if m.Media == "" {
		a.wa.AutoDownload(chat, m.ID)
		return ""
	}
	return "file://" + m.Media
}

// loadInfoMedia fills the Media, Files and Links tabs of the info panel.
func (a *App) loadInfoMedia(chat string) {
	counts := a.store.ChatCounts(chat)

	var mk []string
	var mr [][]any
	for _, m := range a.store.History(chat, a.infoLimits["media"], "image", "video") {
		mk = append(mk, m.ID)
		mr = append(mr, []any{m.ID, a.mediaTile(chat, m), m.Kind})
	}
	a.mediaModel.Update(mk, mr)

	var fk []string
	var fr [][]any
	for _, m := range a.store.History(chat, a.infoLimits["files"], "document") {
		name := strings.TrimPrefix(strings.SplitN(m.Text, "\n", 2)[0], "📄 ")
		info := ""
		if mi, ok := a.wa.MediaInfo(chat, m.ID); ok {
			info = fileLabel(mi)
			if mi.Name != "" {
				name = mi.Name
			}
		}
		fk = append(fk, m.ID)
		fr = append(fr, []any{m.ID, name, info, shortTime(m.TS)})
	}
	a.fileModel.Update(fk, fr)

	var lk []string
	var lr [][]any
	seen := map[string]bool{}
	for _, m := range a.store.LinkMessages(chat) {
		text := m.Text
		if strings.Contains(text, "```") {
			text = codeBlockRe.ReplaceAllString(text, "")
		}
		for i, u := range urlRe.FindAllString(text, -1) {
			if seen[u] {
				continue
			}
			seen[u] = true
			if len(lk) >= a.infoLimits["links"] {
				continue // still counted for the total
			}
			// the attached preview belongs to the message's first link.
			// Never fetch here: this list includes links others sent.
			var raw []byte
			if i == 0 {
				raw = a.store.Raw(chat, m.ID)
			}
			lp := a.links.Get(u, raw, false, nil)
			title, site, image := u, "", ""
			if pu, err := url.Parse(u); err == nil {
				site = strings.TrimPrefix(pu.Hostname(), "www.")
			}
			if lp != nil {
				if lp.Title != "" {
					title = lp.Title
				}
				if lp.Site != "" {
					site = lp.Site
				}
				if lp.Image != "" {
					image = "file://" + lp.Image
				}
			}
			lk = append(lk, u)
			lr = append(lr, []any{u, title, site, image, shortTime(m.TS)})
		}
	}
	a.linkModel.Update(lk, lr)

	a.set("infoMediaTotal", counts.Media)
	a.set("infoFilesTotal", counts.Files)
	a.set("infoLinksTotal", len(seen))
}

// infoDetails lists what is known locally about a chat.
func (a *App) infoDetails(chat string, isGroup bool) []infoDetail {
	var out []infoDetail
	if !isGroup {
		saved, push := a.wa.ContactDetails(chat)
		if push != "" && push != a.wa.DisplayName(chat) {
			out = append(out, infoDetail{"Profile name", "~" + push})
		}
		inBook := "No"
		if saved {
			inBook = "Yes"
		}
		out = append(out, infoDetail{"In your contacts", inBook})
	}
	if c := a.store.ChatCounts(chat); c.Messages > 0 {
		v := thousands(c.Messages) + " stored"
		if c.First > 0 {
			v += ", since " + time.Unix(c.First, 0).Format("2 January 2006")
		}
		out = append(out, infoDetail{"Messages", v})
	}
	return out
}

func (a *App) setInfoDetails(d []infoDetail) {
	b, _ := json.Marshal(d)
	a.set("infoDetails", string(b))
}

// loadCommonGroups fills the "groups in common" list of a contact.
func (a *App) loadCommonGroups(chat string) {
	groups, err := a.wa.GroupsInCommon(chat)
	if err != nil {
		return // not connected, say; the section just stays hidden
	}
	type row struct {
		jid, name string
		members   int
	}
	rows := make([]row, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, row{g.JID, a.wa.DisplayName(g.JID), g.Members})
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name) })
	mainthread.Start(func() {
		if a.current != chat {
			return
		}
		var keys []string
		var out [][]any
		for _, r := range rows {
			keys = append(keys, r.jid)
			out = append(out, []any{r.jid, r.name, plural(r.members, "member"),
				a.avatars.Get(r.jid), initials(r.name), nameColor(r.jid)})
		}
		a.common.Reset(keys, out)
	})
}
