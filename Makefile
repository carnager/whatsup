PREFIX ?= /usr/local

whatsup: *.go go.mod go.sum qml/main.qml emoji/emoji.tsv assets/whatsup.svg
	go build -trimpath -ldflags="-s -w" -o whatsup .

install: whatsup
	install -Dm755 whatsup $(DESTDIR)$(PREFIX)/bin/whatsup
	install -Dm644 whatsup.desktop $(DESTDIR)$(PREFIX)/share/applications/whatsup.desktop
	install -Dm644 assets/whatsup.svg $(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/whatsup.svg

clean:
	rm -f whatsup

.PHONY: install clean
