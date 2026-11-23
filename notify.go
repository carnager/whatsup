package main

import (
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Notifier sends freedesktop desktop notifications, replacing the previous
// notification of the same chat instead of stacking them. Clicking one calls
// OnActivate with the chat JID.
type Notifier struct {
	mu         sync.Mutex
	conn       *dbus.Conn
	ids        map[string]uint32
	chats      map[uint32]string
	OnActivate func(chat string)
}

const notifyPath = "/org/freedesktop/Notifications"
const notifyIface = "org.freedesktop.Notifications"

func NewNotifier() *Notifier {
	n := &Notifier{ids: map[string]uint32{}, chats: map[uint32]string{}}
	c, err := dbus.SessionBus()
	if err != nil {
		return n
	}
	n.conn = c
	c.AddMatchSignal(dbus.WithMatchInterface(notifyIface), dbus.WithMatchObjectPath(notifyPath))
	ch := make(chan *dbus.Signal, 16)
	c.Signal(ch)
	go func() {
		for s := range ch {
			switch s.Name {
			case notifyIface + ".ActivationToken":
				// lets Wayland compositors accept our window raise
				if len(s.Body) == 2 {
					if tok, ok := s.Body[1].(string); ok {
						os.Setenv("XDG_ACTIVATION_TOKEN", tok)
					}
				}
			case notifyIface + ".ActionInvoked":
				if len(s.Body) != 2 {
					continue
				}
				id, _ := s.Body[0].(uint32)
				n.mu.Lock()
				chat, ok := n.chats[id]
				n.mu.Unlock()
				if ok && n.OnActivate != nil {
					n.OnActivate(chat)
				}
			}
		}
	}()
	return n
}

func (n *Notifier) Notify(chat, title, body, icon string) {
	if n.conn == nil {
		return
	}
	n.mu.Lock()
	replaces := n.ids[chat]
	n.mu.Unlock()
	if icon == "" {
		icon = "whatsup"
	}
	obj := n.conn.Object(notifyIface, notifyPath)
	var id uint32
	err := obj.Call(notifyIface+".Notify", 0,
		"WhatsUp", replaces, icon, title, body, []string{"default", "Open"},
		map[string]dbus.Variant{
			"desktop-entry": dbus.MakeVariant("whatsup"),
			"category":      dbus.MakeVariant("im.received"),
			// we play our own sound; don't let the daemon add a second one
			"suppress-sound": dbus.MakeVariant(true),
		}, int32(-1)).Store(&id)
	if err == nil {
		n.mu.Lock()
		n.ids[chat] = id
		n.chats[id] = chat
		n.mu.Unlock()
	}
}

// Clear closes the notification of a chat (e.g. after reading it here).
func (n *Notifier) Clear(chat string) {
	if n.conn == nil {
		return
	}
	n.mu.Lock()
	id, ok := n.ids[chat]
	delete(n.ids, chat)
	n.mu.Unlock()
	if ok {
		n.conn.Object(notifyIface, notifyPath).Call(notifyIface+".CloseNotification", 0, id)
	}
}
