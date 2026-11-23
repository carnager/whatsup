// WhatsUp — a small native Qt6 WhatsApp client built on whatsmeow.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	qt "github.com/mappu/miqt/qt6"
	_ "github.com/mattn/go-sqlite3"
)

func xdgDir(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return filepath.Join(d, "whatsup")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback, "whatsup")
}

func main() {
	dataDir := xdgDir("XDG_DATA_HOME", ".local/share")
	cacheDir := xdgDir("XDG_CACHE_HOME", ".cache")
	for _, d := range []string{dataDir, cacheDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fatal(err)
		}
	}

	// our QML styles everything itself; the Basic style keeps menus/dialogs
	// from mixing in colours from the system widget theme
	if os.Getenv("QT_QUICK_CONTROLS_STYLE") == "" {
		os.Setenv("QT_QUICK_CONTROLS_STYLE", "Basic")
	}
	qapp := qt.NewQApplication(os.Args)
	qt.QCoreApplication_SetApplicationName("WhatsUp")
	qt.QGuiApplication_SetDesktopFileName("whatsup")
	iconPath := installIcon(cacheDir)
	qt.QGuiApplication_SetWindowIcon(qt.NewQIcon4(iconPath))

	ms, err := OpenMsgStore(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		fatal(err)
	}
	av := NewAvatars(cacheDir)
	app := NewApp(qapp, ms, av, NewEmojiDB(dataDir), cacheDir)
	app.set("messageSound", qt.QUrl_FromLocalFile(messageSoundPath(dataDir, cacheDir)).ToString())
	wa, err := NewWA(dataDir, cacheDir, ms, app)
	if err != nil {
		fatal(err)
	}
	app.wa = wa
	app.iconPath = iconPath
	app.EnableTray()
	app.portal = NewPortal()
	app.set("portalOK", app.portal != nil)
	wa.avatars = av
	app.links = NewLinks(ms, cacheDir)
	wa.links = app.links
	av.wa = wa
	go av.Run()
	go wa.Backfill()
	app.Start()
	if j := os.Getenv("WHATSUP_OPEN"); j != "" { // dev: preselect a chat
		app.openChat(j)
	}
	if os.Getenv("WHATSUP_OFFLINE") == "" {
		go wa.Start()
	}

	code := qt.QApplication_Exec()
	wa.Stop()
	os.Exit(code)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "whatsup:", err)
	os.Exit(1)
}
