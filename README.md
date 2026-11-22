# WhatsUp

A native WhatsApp desktop client for Linux: Go with [whatsmeow](https://github.com/tulir/whatsmeow)
for the protocol and Qt 6 / QtQuick (through [miqt](https://github.com/mappu/miqt)) for the UI.
No Electron, no web view.

It links to your phone like WhatsApp Web does: scan the QR code under
*Settings → Linked devices → Link a device*.

## Features

- Chats and groups with history sync, replies, reactions, edits, deletes and forwarding
- WhatsApp formatting (`*bold*`, `_italic_`, `~strike~`, code, quotes, lists), plus `**`/`__`/`~~`
- Emoji picker and `:name` completion
- Photos, videos and documents inline: image viewer with zoom, video player, PDF, text and
  video previews; copy images to the clipboard; "Open with…" and "Save as…"
- Link previews, YouTube included. Incoming links only show the preview the sender attached,
  so nobody learns your IP address by sending a link; your own links are fetched
- Contact and group info with shared media, avatars, notifications and a tray icon
- Native file dialogs through xdg-desktop-portal

## Install

Arch Linux: `whatsup-git` from the [mooned repository](https://repo.mooned.de), or
`cd packaging/arch && makepkg -si`.

From source (Go, Qt 6 base/declarative/multimedia with headers, a C++ compiler):

    make
    sudo make install

Optional at runtime: `ffmpeg` (video previews), `pdftoppm` from poppler (PDF previews),
`xdg-desktop-portal` (file dialogs).

Data lives in `~/.local/share/whatsup` (session and message store) and media in
`~/.cache/whatsup`.

## License

MIT
