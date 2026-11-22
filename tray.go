package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	qt "github.com/mappu/miqt/qt6"
)

//go:embed assets/whatsup.svg
var appIconSVG []byte

// installIcon writes the app icon to the cache dir so Qt and the
// notification daemon can load it by path.
func installIcon(cacheDir string) string {
	p := filepath.Join(cacheDir, "whatsup.svg")
	if old, err := os.ReadFile(p); err != nil || string(old) != string(appIconSVG) {
		os.WriteFile(p, appIconSVG, 0o644)
	}
	return p
}

// Tray is the system tray (StatusNotifierItem) icon with an unread badge.
type Tray struct {
	icon   *qt.QSystemTrayIcon
	base   *qt.QIcon
	unread int
}

func NewTray(iconPath string, toggle func()) *Tray {
	if !qt.QSystemTrayIcon_IsSystemTrayAvailable() {
		return nil
	}
	t := &Tray{base: qt.NewQIcon4(iconPath), unread: -1}
	t.icon = qt.NewQSystemTrayIcon2(t.base)
	menu := qt.NewQMenu2()
	menu.AddActionWithText("Show / Hide WhatsUp").OnTriggered(toggle)
	menu.AddSeparator()
	menu.AddActionWithText("Quit").OnTriggered(func() { qt.QCoreApplication_Quit() })
	t.icon.SetContextMenu(menu)
	t.icon.OnActivated(func(r qt.QSystemTrayIcon__ActivationReason) {
		if r == qt.QSystemTrayIcon__Trigger || r == qt.QSystemTrayIcon__MiddleClick {
			toggle()
		}
	})
	t.icon.SetToolTip("WhatsUp")
	t.SetUnread(0)
	t.icon.Show()
	return t
}

// SetUnread redraws the icon with a red count badge.
func (t *Tray) SetUnread(n int) {
	if t == nil || n == t.unread {
		return
	}
	t.unread = n
	if n == 0 {
		t.icon.SetIcon(t.base)
		t.icon.SetToolTip("WhatsUp")
		return
	}
	const size = 64
	pm := t.base.Pixmap2(size, size)
	p := qt.NewQPainter2(pm.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	label := fmt.Sprint(n)
	if n > 99 {
		label = "99+"
	}
	w := 34
	if len(label) > 2 {
		w = 44
	}
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qt.NewQBrush3(qt.NewQColor7("#ff3b30")))
	p.DrawRoundedRect2(size-w, 0, w, 34, 17, 17)
	f := qt.NewQFont()
	f.SetPixelSize(22)
	f.SetBold(true)
	p.SetFont(f)
	p.SetPen(qt.NewQColor7("#ffffff"))
	p.DrawText7(size-w, 0, w, 34, int(qt.AlignCenter), label)
	p.End()
	t.icon.SetIcon(qt.NewQIcon2(pm))
	t.icon.SetToolTip(fmt.Sprintf("WhatsUp — %d unread", n))
}
