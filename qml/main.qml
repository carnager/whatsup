import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import QtQuick.Dialogs
import QtMultimedia

ApplicationWindow {
    id: root
    width: 1200
    height: 800
    minimumWidth: 760
    minimumHeight: 480
    visible: true
    title: totalUnread > 0 ? "WhatsUp (" + totalUnread + ")" : "WhatsUp"
    color: t.bg
    font.family: "Inter"
    font.pixelSize: 14

    // ---- state set from Go ----
    property string currentJid: ""
    property string currentName: ""
    property string currentSubtitle: ""
    property bool currentTyping: false
    property string currentAvatar: ""
    property string currentInitials: ""
    property string currentColor: "#888"
    property bool currentPinned: false
    property bool currentArchived: false
    property bool currentMuted: false
    property bool muteAll: false
    property string messageSound: ""
    property int soundRequest: 0
    property string statusText: ""
    property string qrSource: ""
    property bool loggedIn: false
    property bool hasMore: false
    property bool showArchived: false
    property int totalUnread: 0
    property var emojiGroups: []
    property string pastedFile: ""
    property string exactEmoji: ""
    property int raiseRequest: 0
    property string searchText: search.text
    property string chatFilter: ""
    readonly property bool isDark: t.dark
    property bool trayAvailable: false
    property bool portalOK: false
    property string pickedFiles: ""
    onPickedFilesChanged: if (pickedFiles !== "") addAttachments(pickedFiles.split("\n"))
    property string viewerReady: ""
    // link preview for the message being typed
    property string composerLink: ""                 // JSON from Go
    readonly property var cLink: composerLink !== "" ? JSON.parse(composerLink) : null
    property string linkUrl: ""                      // first URL in the composer
    property string linkDismissed: ""                // user closed the preview for this URL
    readonly property bool showComposerLink: linkUrl !== "" && linkUrl !== linkDismissed && editId === ""
    readonly property bool linkLoading: cLink === null || cLink.url !== linkUrl
    readonly property bool linkHasData: !linkLoading && (cLink.title !== "" || cLink.image !== "")
    readonly property string linkHost: linkUrl.replace(/^https?:\/\/(www\.)?/i, "").split(/[\/?#]/)[0]
    Timer {
        id: linkTimer
        interval: 250
        onTriggered: {
            var m = composer.text.match(/https?:\/\/[^\s<>"]+[^\s<>".,;:!?)\]]/)
            var u = m ? m[0] : ""
            if (u === root.linkUrl) return
            root.linkUrl = u
            root.call("linkPreview", u)
        }
    }
    onViewerReadyChanged: if (viewerReady !== "") viewer.ready(viewerReady)
    function pickFiles() { if (portalOK) call("pickFiles"); else fileDialog.open() }
    property int trayToggle: 0
    onTrayToggleChanged: {
        if (root.visible && root.active) root.hide()
        else { root.show(); root.raise(); root.requestActivate() }
    }
    // with a tray icon, closing the window only hides it
    onClosing: (close) => { if (trayAvailable) { close.accepted = false; root.hide() } }
    property bool infoOpen: false
    property string infoName: ""
    property string infoSub: ""
    property string infoAbout: ""
    property string infoCreated: ""
    property string infoAvatar: ""
    property string infoInitials: ""
    property string infoColor: "#888"
    property string infoDetails: "[]"
    property int infoMediaTotal: 0
    property int infoFilesTotal: 0
    property int infoLinksTotal: 0
    property string infoTab: "media"
    // the chosen tab, or the first one that has something in it
    readonly property string infoTabShown:
        (infoTab === "media" && infoMediaTotal > 0) || (infoTab === "files" && infoFilesTotal > 0)
            || (infoTab === "links" && infoLinksTotal > 0) ? infoTab
        : infoMediaTotal > 0 ? "media" : infoFilesTotal > 0 ? "files" : "links"

    // ---- local UI state ----
    property string replyId: ""
    property string replyName: ""
    property string replyText: ""
    property string replyThumb: ""
    property bool replyVideo: false
    property string editId: ""
    property var attachments: []
    property var attachFrames: ({})                  // video url -> still ("" if none)
    property string attachFrame: ""                  // "url\nstill" from Go
    onAttachFrameChanged: {
        var i = attachFrame.indexOf("\n")
        if (i < 0) return
        var m = Object.assign({}, attachFrames)
        m[attachFrame.substring(0, i)] = attachFrame.substring(i + 1)
        attachFrames = m
    }
    property string flashId: ""

    onRaiseRequestChanged: { root.show(); root.raise(); root.requestActivate(); composer.forceActiveFocus() }
    onCurrentJidChanged: {
        cancelCompose(); attachments = []; composer.text = ""
        msgList.scrollToEnd()
        if (currentJid !== "" && !search.activeFocus) composer.forceActiveFocus()
        if (currentJid === "") infoOpen = false
        else if (infoOpen) call("info")
    }
    onInfoOpenChanged: if (infoOpen) call("info")
    property string numberResolved: ""
    onNumberResolvedChanged: if (numberResolved !== "") { search.text = ""; composer.forceActiveFocus() }
    onChatFilterChanged: call("search")
    onSearchTextChanged: call("search")
    onStatusTextChanged: if (statusText !== "") statusTimer.restart()
    Timer { id: statusTimer; interval: 8000; onTriggered: root.statusText = "" }
    onSoundRequestChanged: messageChime.play()
    SoundEffect { id: messageChime; source: root.messageSound }

    // ---- call into Go ----
    function call(cmd, a, b, c) {
        bridge.setData(bridge.index(0, 0), JSON.stringify({cmd: cmd, a: a || "", b: b || "", c: c || ""}))
    }

    // role ids of the Go models (Qt.UserRole + 1 + index)
    readonly property var msgRole: ({msgId: 257, fromMe: 258, plain: 260, time: 261, kind: 263, media: 264, deleted: 279,
                                     preview: 283, viewKind: 285})
    function clock(ms) { var s = Math.floor(ms / 1000); return Math.floor(s / 60) + ":" + ("0" + s % 60).slice(-2) }
    function msgData(i, r) { return messageModel.data(messageModel.index(i, 0), msgRole[r]) }
    function chatJidAt(i) { return chatModel.data(chatModel.index(i, 0), 257) }
    function chatKindAt(i) { return chatModel.data(chatModel.index(i, 0), 268) }

    // ================================================================ theme
    SystemPalette { id: sys; colorGroup: SystemPalette.Active }
    QtObject {
        id: t
        readonly property bool dark: sys.window.hslLightness < 0.5
        readonly property color bg:        dark ? "#0e1114" : "#eef1f4"   // conversation backdrop
        readonly property color panel:     dark ? "#161a1f" : "#ffffff"   // sidebar, header, composer
        readonly property color field:     dark ? "#1f252c" : "#f0f2f5"   // inputs
        readonly property color raised:    dark ? "#222930" : "#ffffff"   // incoming bubbles, popups
        readonly property color hover:     dark ? "#1c2228" : "#f3f5f7"
        readonly property color selected:  dark ? "#232c35" : "#e7f5ed"
        readonly property color raisedHover: dark ? "#2f3842" : "#eef1f4"   // hover on popups/menus
        readonly property color line:      dark ? "#262d35" : "#e3e7eb"
        readonly property color text:      dark ? "#e8ecf0" : "#111418"
        readonly property color text2:     dark ? "#9aa5b1" : "#5b6573"
        readonly property color text3:     dark ? "#6c7784" : "#8a94a1"
        readonly property color accent:    dark ? "#2fc27a" : "#1f9d5c"
        readonly property color accentSoft: dark ? "#163a29" : "#d9f3e5"
        readonly property color accentFg:  "#ffffff"
        readonly property color outBubble: dark ? "#1b4733" : "#d6f5e2"
        readonly property color outText:   dark ? "#e6f4ec" : "#0e2618"
        readonly property color link:      dark ? "#62c6ff" : "#0b7bc2"
        readonly property color read:      dark ? "#5cc8ff" : "#1a8fd6"
        readonly property color danger:    dark ? "#ff6b6b" : "#d93636"
        readonly property color dot:       dark ? "#0affffff" : "#0b000000"
    }
    // Controls (menus, dialogs, scrollbars, tooltips) take these colours
    palette.window: t.raised
    palette.windowText: t.text
    palette.base: t.field
    palette.text: t.text
    palette.button: t.field
    palette.buttonText: t.text
    palette.highlight: t.accent
    palette.highlightedText: t.accentFg
    palette.placeholderText: t.text3
    palette.mid: t.line
    palette.light: t.hover
    palette.dark: t.line
    palette.midlight: t.hover
    palette.toolTipBase: t.raised
    palette.toolTipText: t.text

    // ================================================================ icons (Lucide, ISC licence)
    readonly property var icons: ({
        search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
        compose: '<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62l.84-2.873a2 2 0 0 1 .506-.852z"/>',
        more: '<circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/>',
        smile: '<circle cx="12" cy="12" r="10"/><path d="M8 14s1.5 2 4 2 4-2 4-2"/><line x1="9" x2="9.01" y1="9" y2="9"/><line x1="15" x2="15.01" y1="9" y2="9"/>',
        clip: '<path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"/>',
        send: '<path d="M3.714 3.048a.498.498 0 0 0-.683.627l2.843 7.627a2 2 0 0 1 0 1.396l-2.842 7.627a.498.498 0 0 0 .682.627l18-8.5a.5.5 0 0 0 0-.904z"/><path d="M6 12h16"/>',
        x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
        pin: '<path d="M12 17v5"/><path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"/>',
        mute: '<path d="M8.7 3A6 6 0 0 1 18 8a21.3 21.3 0 0 0 .6 5"/><path d="M17 17H3s3-2 3-9a4.67 4.67 0 0 1 .3-1.7"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/><path d="m2 2 20 20"/>',
        bell: '<path d="M10.268 21a2 2 0 0 0 3.464 0"/><path d="M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326"/>',
        archive: '<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>',
        back: '<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>',
        check: '<path d="M20 6 9 17l-5-5"/>',
        checks: '<path d="M18 6 7 17l-5-5"/><path d="m22 10-7.5 7.5L13 16"/>',
        play: '<polygon points="6 3 20 12 6 21 6 3" fill="currentColor"/>',
        pause: '<rect x="14" y="4" width="4" height="16" rx="1" fill="currentColor"/><rect x="6" y="4" width="4" height="16" rx="1" fill="currentColor"/>',
        reply: '<polyline points="9 17 4 12 9 7"/><path d="M20 18v-2a4 4 0 0 0-4-4H4"/>',
        copy: '<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
        trash: '<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/>',
        download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>',
        open: '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
        down: '<path d="m6 9 6 6 6-6"/>',
        file: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
        edit: '<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/>',
        chat: '<path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/>',
        mic: '<path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" x2="12" y1="19" y2="22"/>',
        read: '<path d="M18 6 7 17l-5-5"/><path d="m22 10-7.5 7.5L13 16"/>',
        forward: '<polyline points="15 17 20 12 15 7"/><path d="M4 18v-2a4 4 0 0 1 4-4h12"/>',
        info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
        users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>',
        image: '<rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>',
        video: '<path d="m16 13 5.223 3.482a.5.5 0 0 0 .777-.416V7.87a.5.5 0 0 0-.752-.432L16 10.5"/><rect x="2" y="6" width="14" height="12" rx="2"/>',
        music: '<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>',
        filetext: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
        archivefile: '<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>',
        left: '<path d="m15 18-6-6 6-6"/>',
        right: '<path d="m9 18 6-6-6-6"/>',
        zoomin: '<circle cx="11" cy="11" r="8"/><line x1="21" x2="16.65" y1="21" y2="16.65"/><line x1="11" x2="11" y1="8" y2="14"/><line x1="8" x2="14" y1="11" y2="11"/>',
        zoomout: '<circle cx="11" cy="11" r="8"/><line x1="21" x2="16.65" y1="21" y2="16.65"/><line x1="8" x2="14" y1="11" y2="11"/>',
        volume: '<path d="M11 4.702a.705.705 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.705.705 0 0 0 11 19.298z"/><path d="M16 9a5 5 0 0 1 0 6"/><path d="M19.364 18.364a9 9 0 0 0 0-12.728"/>',
        volumeoff: '<path d="M11 4.702a.705.705 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.705.705 0 0 0 11 19.298z"/><line x1="22" x2="16" y1="9" y2="15"/><line x1="16" x2="22" y1="9" y2="15"/>',
        appwindow: '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="M10 4v4"/><path d="M2 8h20"/><path d="M6 4v4"/>'
    })
    // icon for an attachment, from its file name
    function fileIcon(name) {
        var e = name.toLowerCase().split(".").pop()
        if (["jpg","jpeg","png","gif","webp","bmp","svg","heic","avif"].indexOf(e) >= 0) return "image"
        if (["mp4","mkv","webm","mov","avi","m4v","3gp"].indexOf(e) >= 0) return "video"
        if (["mp3","ogg","opus","m4a","flac","wav","aac"].indexOf(e) >= 0) return "music"
        if (["zip","tar","gz","xz","7z","rar","zst","bz2"].indexOf(e) >= 0) return "archivefile"
        return "filetext"
    }
    function iconUrl(name, color) {
        var c = color.toString()
        var svg = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="' + c +
                  '" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" color="' + c + '">' +
                  (icons[name] || "").replace(/currentColor/g, c) + '</svg>'
        return "data:image/svg+xml;utf8," + encodeURIComponent(svg)
    }

    component Icon: Image {
        property string name: ""
        property color tint: t.text2
        property int size: 20
        width: size
        height: size
        sourceSize.width: size * 2
        sourceSize.height: size * 2
        source: root.iconUrl(name, tint)
        smooth: true
    }

    // Mouse-wheel scrolling: a fixed distance per notch with a short ease
    // (Qt's default flick-based wheel scrolling is sluggish). Touchpads with
    // pixel deltas scroll 1:1.
    component WheelScroll: WheelHandler {
        id: ws
        required property Flickable flick
        property real step: 200          // px per wheel notch
        property real dest: 0
        signal scrolled()                // after a scroll settled
        signal started()
        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
        function clamp(y) {
            var lo = flick.originY - flick.topMargin
            var hi = flick.originY + flick.contentHeight + flick.bottomMargin - flick.height
            return Math.max(lo, Math.min(Math.max(lo, hi), y))
        }
        // Wayland attaches a small pixelDelta to mouse-wheel notches too; only
        // touchpads (which report a scroll phase) get 1:1 pixel scrolling
        onWheel: (ev) => scrollBy(ev.angleDelta.y, ev.phase !== Qt.NoScrollPhase || ev.angleDelta.y === 0 ? ev.pixelDelta.y : 0)
        function scrollBy(angle, pixels) {
            if (!anim.running) { dest = flick.contentY; started() }
            if (pixels !== 0) {
                anim.stop()
                flick.contentY = clamp(flick.contentY - pixels)
                dest = flick.contentY
                settle.restart()
                return
            }
            dest = clamp(dest - angle / 120 * step)
            anim.to = dest
            anim.restart()
        }
        property NumberAnimation anim: NumberAnimation {
            target: ws.flick; property: "contentY"
            duration: 160; easing.type: Easing.OutCubic
            onFinished: ws.done()
        }
        property Timer settle: Timer { interval: 120; onTriggered: ws.done() }
        // the content size may be re-estimated while scrolling: snap back inside
        function done() {
            var y = clamp(flick.contentY)
            if (Math.abs(y - flick.contentY) > 0.5) flick.contentY = y
            scrolled()
        }
    }

    component IconButton: AbstractButton {
        id: ib
        property string iconName: ""
        property int size: 38
        property int iconSize: 20
        property string tip: ""
        property color tint: t.text2
        implicitWidth: size
        implicitHeight: size
        hoverEnabled: true
        focusPolicy: Qt.NoFocus
        ToolTip.visible: tip !== "" && hovered
        ToolTip.text: tip
        ToolTip.delay: 600
        background: Rectangle { radius: ib.size / 2; color: ib.down ? t.selected : (ib.hovered ? t.hover : "transparent") }
        contentItem: Item {
            Icon { anchors.centerIn: parent; name: ib.iconName; size: ib.iconSize; tint: ib.hovered ? t.text : ib.tint }
        }
    }

    component Avatar: Item {
        id: avatar
        property string source: ""
        property string initials: ""
        property color tint: "#888"
        property string icon: ""
        Rectangle {
            anchors.fill: parent
            radius: width / 2
            color: avatar.tint
            visible: img.status !== Image.Ready
            Text {
                visible: avatar.icon === ""
                anchors.centerIn: parent
                text: avatar.initials
                color: "white"
                font.pixelSize: parent.height * 0.36
                font.weight: Font.DemiBold
            }
            Icon {
                visible: avatar.icon !== ""
                anchors.centerIn: parent
                name: avatar.icon
                size: parent.height * 0.46
                tint: t.accent
            }
        }
        Image {
            id: img
            anchors.fill: parent
            source: avatar.source
            sourceSize.width: width * 2
            sourceSize.height: height * 2
            smooth: true
            mipmap: true
            asynchronous: true
            cache: false
        }
    }

    component Chip: AbstractButton {
        id: chip
        property bool active: false
        implicitHeight: 30
        implicitWidth: label.implicitWidth + 26
        hoverEnabled: true
        focusPolicy: Qt.NoFocus
        background: Rectangle {
            radius: 15
            color: chip.active ? t.accentSoft : (chip.hovered ? t.hover : t.field)
        }
        contentItem: Text {
            id: label
            text: chip.text
            color: chip.active ? t.accent : t.text2
            font.pixelSize: 13
            font.weight: Font.Medium
            horizontalAlignment: Text.AlignHCenter
            verticalAlignment: Text.AlignVCenter
        }
    }

    component MenuEntry: MenuItem {
        id: me
        property string iconName: ""
        property bool destructive: false
        implicitHeight: visible ? 38 : 0
        implicitWidth: 220
        contentItem: Row {
            spacing: 12
            leftPadding: 4
            Icon {
                anchors.verticalCenter: parent.verticalCenter
                name: me.iconName; size: 17; visible: me.iconName !== ""
                tint: me.destructive ? t.danger : t.text2
            }
            Text {
                anchors.verticalCenter: parent.verticalCenter
                text: me.text
                color: me.destructive ? t.danger : (me.enabled ? t.text : t.text3)
                font.pixelSize: 14
            }
        }
        background: Rectangle { radius: 8; color: me.highlighted ? t.raisedHover : "transparent"; anchors.fill: parent; anchors.margins: 2 }
    }

    component ThemedMenu: Menu {
        padding: 6
        background: Rectangle {
            implicitWidth: 230
            radius: 12
            color: t.raised
            border.width: 1
            border.color: t.line
        }
        delegate: MenuEntry {}
    }

    Shortcut { sequence: "Ctrl+K"; onActivated: { search.forceActiveFocus(); search.selectAll() } }
    Shortcut { sequence: "Ctrl+Q"; onActivated: Qt.quit() }
    Shortcut { sequence: "Alt+Down"; onActivated: chatList.step(1) }
    Shortcut { sequence: "Alt+Up"; onActivated: chatList.step(-1) }
    Shortcut { sequence: "Ctrl+E"; onActivated: emojiPicker.toggle(false, "") }

    // ================================================================ helpers
    function cancelCompose() {
        if (editId !== "") composer.text = ""
        linkUrl = ""; linkDismissed = ""; composerLink = ""
        replyId = ""; replyName = ""; replyText = ""; replyThumb = ""; replyVideo = false; editId = ""
    }
    function replyTo(m) {
        editId = ""
        replyId = m.msgId; replyName = m.who
        replyText = m.plain !== "" ? m.plain
                  : m.kind === "image" ? "📷 Photo" : m.kind === "video" ? "🎬 Video" : m.kind
        replyThumb = m.visualMedia ? m.media : ""
        replyVideo = m.kind === "video"
        composer.forceActiveFocus()
    }
    function startEdit(id, text) {
        replyId = ""; replyThumb = ""; replyVideo = false
        editId = id
        composer.text = text
        composer.cursorPosition = text.length
        composer.forceActiveFocus()
    }
    function editLastOwn() {
        for (var i = 0; i < messageModel.rowCount(); i++) {
            if (msgData(i, "fromMe") && msgData(i, "kind") === "" && !msgData(i, "deleted")) {
                startEdit(msgData(i, "msgId"), msgData(i, "plain"))
                return true
            }
        }
        return false
    }
    function jumpTo(id) {
        for (var i = 0; i < messageModel.rowCount(); i++) {
            if (msgData(i, "msgId") === id) {
                msgList.follow = false
                msgList.positionViewAtIndex(i, ListView.Center)
                flashId = ""; flashId = id
                return
            }
        }
        statusText = "Quoted message is not loaded"
    }
    function addAttachments(urls) {
        var list = attachments.slice()
        for (var i = 0; i < urls.length; i++) {
            var u = urls[i].toString()
            list.push(u)
            if (fileIcon(u) === "video" && attachFrames[u] === undefined) call("attachFrame", u)
        }
        attachments = list
        composer.forceActiveFocus()
    }
    function fileName(u) { var s = decodeURIComponent(u.toString()); return s.substring(s.lastIndexOf("/") + 1) }
    function insertEmoji(e) {
        var pos = composer.cursorPosition
        composer.insert(pos, e)
        composer.cursorPosition = pos + e.length
        call("emojiUse", e)
    }
    function send() {
        var text = composer.text.trim()
        if (editId !== "") {
            if (text !== "") call("edit", editId, text)
            composer.text = ""
            cancelCompose()
            return
        }
        if (attachments.length > 0) {
            for (var i = 0; i < attachments.length; i++)
                call("sendFile", attachments[i], i === 0 ? text : "", i === 0 ? replyId : "")
            attachments = []
        } else if (text !== "") {
            call("send", text, replyId, linkUrl !== "" && linkUrl === linkDismissed ? "nopreview" : "")
        } else return
        composer.text = ""
        cancelCompose()
        msgList.scrollToEnd()
    }

    // ================================================================ layout
    RowLayout {
        anchors.fill: parent
        spacing: 0

        // ------------------------------------------------ sidebar
        Rectangle {
            Layout.fillHeight: true
            Layout.preferredWidth: 360
            color: t.panel

            ColumnLayout {
                anchors.fill: parent
                spacing: 0

                // title row
                RowLayout {
                    Layout.fillWidth: true
                    Layout.preferredHeight: 64
                    Layout.leftMargin: 20
                    Layout.rightMargin: 10
                    spacing: 2
                    Text {
                        Layout.fillWidth: true
                        text: root.showArchived ? "Archived" : "Chats"
                        color: t.text
                        font.pixelSize: 22
                        font.weight: Font.Bold
                        font.letterSpacing: -0.3
                    }
                    IconButton {
                        iconName: root.muteAll ? "mute" : "bell"
                        tint: root.muteAll ? t.accent : t.text2
                        tip: root.muteAll ? "All chats muted — click to unmute" : "Mute all chats"
                        onClicked: root.call("muteAll", root.muteAll ? "0" : "1")
                    }
                    IconButton {
                        iconName: "compose"
                        tip: "New chat — search a contact or type a phone number"
                        onClicked: { search.forceActiveFocus(); search.selectAll() }
                    }
                }

                // search
                Rectangle {
                    Layout.fillWidth: true
                    Layout.leftMargin: 14
                    Layout.rightMargin: 14
                    Layout.preferredHeight: 40
                    radius: 20
                    color: t.field
                    border.width: search.activeFocus ? 1.5 : 0
                    border.color: t.accent
                    Icon {
                        id: searchIcon
                        anchors.left: parent.left
                        anchors.leftMargin: 14
                        anchors.verticalCenter: parent.verticalCenter
                        name: "search"; size: 17
                        tint: search.activeFocus ? t.accent : t.text3
                    }
                    TextField {
                        id: search
                        anchors.left: searchIcon.right
                        anchors.right: clearSearch.left
                        anchors.verticalCenter: parent.verticalCenter
                        leftPadding: 10
                        placeholderText: "Search or start a new chat"
                        placeholderTextColor: t.text3
                        color: t.text
                        font.pixelSize: 14
                        background: null
                        // refreshing happens in root.onSearchTextChanged, once the binding has updated
                        Keys.onEscapePressed: { text = ""; composer.forceActiveFocus() }
                        Keys.onDownPressed: chatList.forceActiveFocus()
                        onAccepted: if (chatList.count > 0) { chatList.activate(0); composer.forceActiveFocus() }
                    }
                    IconButton {
                        id: clearSearch
                        anchors.right: parent.right
                        anchors.rightMargin: 4
                        anchors.verticalCenter: parent.verticalCenter
                        visible: search.text !== ""
                        width: visible ? 32 : 0
                        size: 32; iconSize: 15; iconName: "x"
                        onClicked: search.text = ""
                    }
                }

                // filter chips
                Row {
                    Layout.fillWidth: true
                    Layout.leftMargin: 14
                    Layout.topMargin: 10
                    Layout.bottomMargin: 6
                    spacing: 6
                    visible: search.text === "" && !root.showArchived
                    Chip { text: "All"; active: root.chatFilter === ""; onClicked: root.chatFilter = "" }
                    Chip { text: "Unread"; active: root.chatFilter === "unread"; onClicked: root.chatFilter = "unread" }
                    Chip { text: "Groups"; active: root.chatFilter === "groups"; onClicked: root.chatFilter = "groups" }
                }

                ListView {
                    id: chatList
                    WheelScroll { flick: chatList }
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    Layout.topMargin: 4
                    clip: true
                    model: chatModel
                    boundsBehavior: Flickable.StopAtBounds
                    ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }
                    keyNavigationEnabled: false

                    function activate(i) {
                        var kind = chatKindAt(i), jid = chatJidAt(i)
                        if (kind === "archive" || kind === "back") root.call("toggleArchived")
                        else if (kind === "number") root.call("newChat", jid)
                        else { root.call("open", jid); if (search.text !== "") search.text = "" }
                    }
                    function step(d) {
                        var cur = -1
                        for (var i = 0; i < count; i++)
                            if (chatJidAt(i) === root.currentJid) { cur = i; break }
                        var n = cur + d
                        while (n >= 0 && n < count && chatKindAt(n) !== "chat") n += d
                        if (n < 0 || n >= count) return
                        root.call("open", chatJidAt(n))
                        positionViewAtIndex(n, ListView.Contain)
                    }
                    Keys.onDownPressed: step(1)
                    Keys.onUpPressed: step(-1)
                    Keys.onReturnPressed: composer.forceActiveFocus()

                    property real savedY: 0
                    Connections {
                        target: chatModel
                        function onModelAboutToBeReset() { chatList.savedY = chatList.contentY }
                        function onModelReset() { chatList.contentY = chatList.savedY }
                    }

                    Text {
                        anchors.centerIn: parent
                        visible: chatList.count === 0
                        text: search.text !== "" ? "No chats or contacts found" : (root.chatFilter === "unread" ? "No unread chats" : "No chats yet")
                        color: t.text3
                        font.pixelSize: 14
                    }

                    delegate: Item {
                        id: row
                        required property int index
                        required property string jid
                        required property string name
                        required property string preview
                        required property string time
                        required property int unread
                        required property string avatar
                        required property string initials
                        required property string tint
                        required property bool pinned
                        required property bool muted
                        required property bool archived
                        required property string rowKind
                        required property bool typing
                        width: ListView.view.width
                        height: rowKind === "back" ? 52 : 74
                        readonly property bool current: rowKind === "chat" && jid === root.currentJid
                        readonly property bool special: rowKind === "archive" || rowKind === "number"

                        Rectangle {
                            anchors.fill: parent
                            anchors.leftMargin: 8
                            anchors.rightMargin: 8
                            anchors.topMargin: 1
                            anchors.bottomMargin: 1
                            radius: 12
                            color: row.current ? t.selected : (ma.containsMouse ? t.hover : "transparent")
                            Rectangle {
                                visible: row.current
                                width: 3; height: 28; radius: 1.5
                                anchors.verticalCenter: parent.verticalCenter
                                color: t.accent
                            }
                        }

                        // "← Archived" back row
                        Row {
                            visible: row.rowKind === "back"
                            anchors.verticalCenter: parent.verticalCenter
                            x: 22
                            spacing: 14
                            Icon { name: "back"; size: 18; tint: t.text2; anchors.verticalCenter: parent.verticalCenter }
                            Text { text: "Back to chats"; color: t.text2; font.pixelSize: 14; font.weight: Font.Medium; anchors.verticalCenter: parent.verticalCenter }
                        }

                        Avatar {
                            id: av
                            visible: row.rowKind !== "back"
                            width: 50; height: 50
                            anchors.left: parent.left
                            anchors.leftMargin: 18
                            anchors.verticalCenter: parent.verticalCenter
                            source: row.avatar
                            initials: row.initials
                            tint: row.special ? t.accentSoft : row.tint
                            icon: row.rowKind === "archive" ? "archive" : (row.rowKind === "number" ? "chat" : "")
                        }
                        Text {
                            id: nameText
                            visible: row.rowKind !== "back"
                            anchors.left: av.right
                            anchors.leftMargin: 14
                            anchors.right: timeText.left
                            anchors.rightMargin: 8
                            y: 16
                            text: row.name
                            elide: Text.ElideRight
                            color: t.text
                            font.pixelSize: 15
                            font.weight: row.unread > 0 ? Font.Bold : Font.DemiBold
                        }
                        Text {
                            id: timeText
                            visible: row.rowKind === "chat"
                            anchors.right: parent.right
                            anchors.rightMargin: 22
                            anchors.baseline: nameText.baseline
                            text: row.time
                            color: row.unread > 0 && !row.muted ? t.accent : t.text3
                            font.pixelSize: 12
                            font.weight: row.unread > 0 ? Font.DemiBold : Font.Normal
                        }
                        Text {
                            visible: row.rowKind !== "back"
                            anchors.left: nameText.left
                            anchors.right: icons.left
                            anchors.rightMargin: 8
                            anchors.top: nameText.bottom
                            anchors.topMargin: 5
                            text: row.preview
                            textFormat: Text.PlainText
                            maximumLineCount: 1
                            elide: Text.ElideRight
                            color: row.typing ? t.accent : (row.unread > 0 ? t.text : t.text2)
                            font.italic: row.typing
                            font.pixelSize: 14
                        }
                        Row {
                            id: icons
                            anchors.right: parent.right
                            anchors.rightMargin: 20
                            y: 42
                            spacing: 6
                            Icon { visible: row.muted; name: "mute"; size: 15; tint: t.text3; anchors.verticalCenter: parent.verticalCenter }
                            Icon { visible: row.pinned; name: "pin"; size: 15; tint: t.text3; anchors.verticalCenter: parent.verticalCenter; rotation: 45 }
                            Rectangle {
                                visible: row.unread > 0
                                height: 20
                                width: Math.max(20, badgeText.implicitWidth + 12)
                                radius: 10
                                color: row.muted || row.rowKind === "archive" ? t.text3 : t.accent
                                Text {
                                    id: badgeText
                                    anchors.centerIn: parent
                                    text: row.unread > 999 ? "999+" : row.unread
                                    color: t.accentFg
                                    font.pixelSize: 11
                                    font.weight: Font.Bold
                                }
                            }
                        }
                        MouseArea {
                            id: ma
                            anchors.fill: parent
                            hoverEnabled: true
                            acceptedButtons: Qt.LeftButton | Qt.RightButton
                            onClicked: (mouse) => {
                                if (mouse.button === Qt.RightButton) {
                                    if (row.rowKind === "chat") chatMenu.openFor(row.jid, row.pinned, row.muted, row.archived, row.unread)
                                    return
                                }
                                chatList.activate(row.index)
                                if (row.rowKind === "chat" || row.rowKind === "contact") composer.forceActiveFocus()
                            }
                        }
                    }
                }

                // status line
                Rectangle {
                    Layout.fillWidth: true
                    Layout.preferredHeight: root.statusText !== "" ? 34 : 0
                    visible: root.statusText !== ""
                    color: t.panel
                    Rectangle { width: parent.width; height: 1; color: t.line }
                    Text {
                        anchors.fill: parent
                        anchors.leftMargin: 16
                        anchors.rightMargin: 16
                        verticalAlignment: Text.AlignVCenter
                        text: root.statusText
                        color: t.text2
                        font.pixelSize: 12
                        elide: Text.ElideMiddle
                    }
                }
            }
        }

        Rectangle { Layout.fillHeight: true; Layout.preferredWidth: 1; color: t.line }

        // ------------------------------------------------ conversation
        Item {
            Layout.fillWidth: true
            Layout.fillHeight: true

            // subtle dotted backdrop
            Rectangle { anchors.fill: parent; color: t.bg }
            Image {
                anchors.fill: parent
                fillMode: Image.Tile
                readonly property color c: t.dot
                source: "data:image/svg+xml;utf8," + encodeURIComponent(
                    '<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22"><circle cx="2" cy="2" r="1.1" fill="rgba(' +
                    Math.round(c.r * 255) + ',' + Math.round(c.g * 255) + ',' + Math.round(c.b * 255) + ',' + c.a.toFixed(3) + ')"/></svg>')
            }

            // empty state
            Column {
                anchors.centerIn: parent
                spacing: 14
                visible: root.currentJid === ""
                Rectangle {
                    anchors.horizontalCenter: parent.horizontalCenter
                    width: 88; height: 88; radius: 44
                    color: t.accentSoft
                    Icon { anchors.centerIn: parent; name: "chat"; size: 40; tint: t.accent }
                }
                Text { anchors.horizontalCenter: parent.horizontalCenter; text: "WhatsUp"; color: t.text; font.pixelSize: 26; font.weight: Font.Bold; font.letterSpacing: -0.4 }
                Text { anchors.horizontalCenter: parent.horizontalCenter; text: "Pick a chat on the left, or press Ctrl+K to search."; color: t.text2; font.pixelSize: 14 }
            }

            ColumnLayout {
                anchors.fill: parent
                spacing: 0
                visible: root.currentJid !== ""

                // ---- header
                Rectangle {
                    Layout.fillWidth: true
                    Layout.preferredHeight: 64
                    color: t.panel
                    Avatar {
                        id: hav
                        width: 42; height: 42
                        anchors.left: parent.left
                        anchors.leftMargin: 18
                        anchors.verticalCenter: parent.verticalCenter
                        source: root.currentAvatar
                        initials: root.currentInitials
                        tint: root.currentColor
                    }
                    MouseArea {
                        anchors.left: parent.left
                        anchors.right: headerIcons.left
                        height: parent.height
                        cursorShape: Qt.PointingHandCursor
                        onClicked: root.infoOpen = !root.infoOpen
                    }
                    Column {
                        anchors.left: hav.right
                        anchors.leftMargin: 14
                        anchors.right: headerIcons.left
                        anchors.rightMargin: 8
                        anchors.verticalCenter: parent.verticalCenter
                        spacing: 2
                        Text { width: parent.width; text: root.currentName; color: t.text; font.pixelSize: 16; font.weight: Font.DemiBold; elide: Text.ElideRight }
                        Text {
                            width: parent.width
                            text: root.currentSubtitle
                            color: root.currentTyping || root.currentSubtitle === "online" ? t.accent : t.text2
                            font.pixelSize: 12
                            elide: Text.ElideRight
                            visible: text !== ""
                        }
                    }
                    Row {
                        id: headerIcons
                        anchors.right: parent.right
                        anchors.rightMargin: 12
                        anchors.verticalCenter: parent.verticalCenter
                        spacing: 2
                        Icon { visible: root.currentMuted; name: "mute"; size: 16; tint: t.text3; anchors.verticalCenter: parent.verticalCenter }
                        Item { width: 6; height: 1; visible: root.currentMuted }
                        IconButton {
                            iconName: "info"
                            tip: root.currentJid.endsWith("@g.us") ? "Group info" : "Contact info"
                            tint: root.infoOpen ? t.accent : t.text2
                            onClicked: root.infoOpen = !root.infoOpen
                        }
                        IconButton {
                            iconName: "more"
                            tip: "Chat options"
                            onClicked: chatMenu.openFor(root.currentJid, root.currentPinned, root.currentMuted, root.currentArchived, 0)
                        }
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: t.line }
                }

                // ---- messages (bottom-to-top: index 0 is the newest)
                Item {
                    Layout.fillWidth: true
                    Layout.fillHeight: true

                    ListView {
                        id: msgList
                        WheelScroll { id: msgWheel; flick: msgList; onStarted: msgList.follow = false; onScrolled: msgList.follow = msgList.fromEnd() < 6 }
                        anchors.fill: parent
                        clip: true
                        model: messageModel
                        verticalLayoutDirection: ListView.BottomToTop
                        spacing: 2
                        boundsBehavior: Flickable.StopAtBounds
                        ScrollBar.vertical: ScrollBar {
                            policy: ScrollBar.AsNeeded
                            onPressedChanged: msgList.follow = !pressed && msgList.fromEnd() < 6
                        }
                        bottomMargin: 12
                        topMargin: 12
                        // the top (oldest end) is the flickable's beginning
                        onAtYBeginningChanged: if (atYBeginning && count > 0 && root.hasMore) root.call("more")
                        readonly property real maxBubble: Math.min(width * 0.7, 640)
                        // distance between the viewport bottom and the newest message
                        function fromEnd() { return originY + contentHeight + bottomMargin - contentY - height }
                        readonly property bool atBottom: count === 0 || follow
                        // at the bottom → stay at the bottom when content grows
                        property bool follow: true
                        // only user scrolling (drag, flick, wheel, scrollbar) changes follow
                        onMovementStarted: follow = false
                        onMovementEnded: follow = fromEnd() < 6
                        onContentHeightChanged: if (follow) Qt.callLater(scrollToEnd)
                        onHeightChanged: if (follow) Qt.callLater(scrollToEnd)
                        function scrollToEnd() {
                            follow = true
                            positionViewAtBeginning()
                            if (fromEnd() > 0.5 && contentHeight > height) contentY = originY + contentHeight + bottomMargin - height
                        }

                        footer: Item { // shown above the oldest message
                            width: msgList.width
                            height: msgList.count > 0 && !root.hasMore ? 50 : 0
                            visible: height > 0
                            AbstractButton {
                                id: olderBtn
                                anchors.centerIn: parent
                                hoverEnabled: true
                                implicitHeight: 30
                                implicitWidth: olderText.implicitWidth + 28
                                background: Rectangle { radius: 15; color: olderBtn.hovered ? t.selected : t.raised; border.width: 1; border.color: t.line }
                                contentItem: Text { id: olderText; text: "Load older messages from phone"; color: t.text2; font.pixelSize: 12; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                                onClicked: root.call("fetch")
                            }
                        }

                        delegate: Column {
                            id: msg
                            required property string msgId
                            required property bool fromMe
                            required property string body
                            required property string plain
                            required property string time
                            required property int status
                            required property string kind
                            required property string media
                            required property string duration
                            required property string senderName
                            required property string senderColor
                            required property bool showSender
                            required property string avatar
                            required property string initials
                            required property string dayHeader
                            required property string unreadMarker
                            required property string quoteId
                            required property string quoteName
                            required property string quoteText
                            required property string quoteColor
                            required property string quoteThumb
                            required property bool quoteVideo
                            required property string reactions
                            required property bool edited
                            required property bool deleted
                            required property bool first
                            required property bool forwarded
                            required property string fileInfo
                            required property string preview
                            required property string snippet
                            required property string viewKind
                            required property string local
                            required property string linkUrl
                            required property string linkTitle
                            required property string linkDesc
                            required property string linkSite
                            required property string linkImage
                            required property bool linkVideo
                            readonly property string fileName: kind === "document" ? plain.split("\n")[0].replace(/^📄 /, "") : ""
                            // tap on media: built-in viewer for pictures/videos, else default app
                            function activate() {
                                if (viewKind !== "") viewer.show(msgId, viewKind, kind === "document" ? preview : media, fileName)
                                else root.call("openMedia", msgId)
                            }
                            width: ListView.view.width
                            spacing: 0
                            readonly property bool group: root.currentJid.endsWith("@g.us")
                            readonly property bool sticker: kind === "sticker" && !deleted
                            readonly property bool visualMedia: (kind === "image" || kind === "video" || kind === "sticker") && !deleted
                            readonly property bool mediaOnly: visualMedia && body === "" && quoteId === "" && !showSender
                            readonly property string who: fromMe ? "You" : (senderName !== "" ? senderName : root.currentName)
                            readonly property color fg: fromMe ? t.outText : t.text

                            function menu() { msgMenu.openFor(msg) }

                            // day separator
                            Item {
                                width: parent.width
                                height: msg.dayHeader !== "" ? 46 : 0
                                visible: height > 0
                                Rectangle {
                                    anchors.centerIn: parent
                                    width: dayText.implicitWidth + 24
                                    height: 26
                                    radius: 13
                                    color: t.raised
                                    border.width: 1
                                    border.color: t.line
                                    Text { id: dayText; anchors.centerIn: parent; text: msg.dayHeader; color: t.text2; font.pixelSize: 12; font.weight: Font.Medium }
                                }
                            }
                            // unread separator
                            Item {
                                width: parent.width
                                height: msg.unreadMarker !== "" ? 40 : 0
                                visible: height > 0
                                Rectangle { anchors.verticalCenter: parent.verticalCenter; x: 24; width: parent.width - 48; height: 1; color: t.accent; opacity: 0.5 }
                                Rectangle {
                                    anchors.centerIn: parent
                                    width: unreadText.implicitWidth + 20; height: 22; radius: 11
                                    color: t.accentSoft
                                    Text { id: unreadText; anchors.centerIn: parent; text: msg.unreadMarker; color: t.accent; font.pixelSize: 12; font.weight: Font.DemiBold }
                                }
                            }

                            Item { width: 1; height: msg.first ? 6 : 0 }

                            Item {
                                width: parent.width
                                height: bubble.height + (reactionPill.visible ? 18 : 0)
                                // hover over the whole row, so the action button beside the bubble stays reachable
                                HoverHandler { id: bubbleHover }

                                Avatar {
                                    visible: msg.showSender
                                    width: 32; height: 32
                                    x: 16
                                    anchors.top: bubble.top
                                    source: msg.avatar
                                    initials: msg.initials
                                    tint: msg.senderColor
                                }

                                Rectangle {
                                    id: bubble
                                    readonly property real pad: msg.sticker || msg.mediaOnly ? 4 : 10
                                    x: msg.fromMe ? parent.width - width - 20 : (msg.group ? 58 : 20)
                                    width: content.width + 2 * pad
                                    height: content.height + 2 * pad - (msg.sticker ? 0 : 2)
                                    radius: 14
                                    color: msg.sticker ? "transparent" : (msg.fromMe ? t.outBubble : t.raised)
                                    border.width: root.flashId === msg.msgId ? 2 : 0
                                    border.color: t.accent

                                    // the corner pointing at the sender is sharp on the first bubble of a run
                                    Rectangle {
                                        visible: msg.first && !msg.sticker
                                        width: 14; height: 14
                                        x: msg.fromMe ? parent.width - width : 0
                                        color: parent.color
                                        radius: 3
                                        z: -1
                                    }

                                    Timer {
                                        running: root.flashId === msg.msgId
                                        interval: 1600
                                        onTriggered: root.flashId = ""
                                    }
                                    TapHandler {
                                        acceptedButtons: Qt.RightButton
                                        onTapped: msg.menu()
                                    }
                                    TapHandler {
                                        acceptedButtons: Qt.LeftButton
                                        onDoubleTapped: if (!msg.deleted) root.replyTo(msg)
                                    }

                                    Column {
                                        id: content
                                        x: bubble.pad
                                        y: bubble.pad - (msg.sticker ? 0 : 2)
                                        spacing: 5
                                        width: Math.max(sender.visible ? Math.min(sender.implicitWidth, msgList.maxBubble) : 0,
                                                        quote.visible ? Math.min(Math.max(quoteNameText.implicitWidth, quoteBody.implicitWidth) + 22 + (quoteThumbBox.visible ? 54 : 0), msgList.maxBubble) : 0,
                                                        mediaBox.visible ? mediaBox.width : 0,
                                                        docCard.visible ? docCard.width : 0,
                                                        linkCard.visible ? linkCard.width : 0,
                                                        audio.visible ? audio.width : 0,
                                                        textBody.visible ? textBody.width : 0,
                                                        deletedText.visible ? deletedText.implicitWidth : 0,
                                                        meta.visible && !meta.overlay ? meta.implicitWidth : 0,
                                                        fwdLabel.visible ? fwdLabel.implicitWidth : 0)

                                        Row {
                                            id: fwdLabel
                                            visible: msg.forwarded && !msg.deleted
                                            spacing: 5
                                            Icon { name: "forward"; size: 14; tint: t.text3; anchors.verticalCenter: parent.verticalCenter }
                                            Text { text: "Forwarded"; color: t.text3; font.pixelSize: 12; font.italic: true; anchors.verticalCenter: parent.verticalCenter }
                                        }

                                        Text {
                                            id: sender
                                            visible: msg.showSender && !msg.sticker
                                            text: msg.senderName
                                            color: msg.senderColor
                                            font.pixelSize: 13
                                            font.weight: Font.DemiBold
                                            elide: Text.ElideRight
                                            width: Math.min(implicitWidth, msgList.maxBubble)
                                        }

                                        // quoted message
                                        Rectangle {
                                            id: quote
                                            visible: msg.quoteId !== "" && !msg.deleted
                                            width: parent.width
                                            height: Math.max(quoteCol.height + 12, quoteThumbBox.visible ? 56 : 0)
                                            radius: 8
                                            color: msg.fromMe ? Qt.rgba(0, 0, 0, t.dark ? 0.22 : 0.06) : t.field
                                            clip: true
                                            Rectangle { width: 4; height: parent.height; color: msg.quoteColor }
                                            Column {
                                                id: quoteCol
                                                x: 12
                                                anchors.verticalCenter: parent.verticalCenter
                                                width: parent.width - 18 - (quoteThumbBox.visible ? 54 : 0)
                                                spacing: 1
                                                Text { id: quoteNameText; text: msg.quoteName; color: msg.quoteColor; font.pixelSize: 12; font.weight: Font.DemiBold; elide: Text.ElideRight; width: parent.width }
                                                Text { id: quoteBody; text: msg.quoteText; color: t.text2; font.pixelSize: 13; maximumLineCount: 2; elide: Text.ElideRight; wrapMode: Text.Wrap; width: parent.width; textFormat: Text.PlainText }
                                            }
                                            Image {
                                                id: quoteThumbBox
                                                visible: msg.quoteThumb !== "" && status !== Image.Error
                                                anchors.right: parent.right
                                                anchors.top: parent.top
                                                anchors.bottom: parent.bottom
                                                width: 56
                                                source: msg.quoteThumb
                                                sourceSize.width: 112; sourceSize.height: 112
                                                fillMode: Image.PreserveAspectCrop
                                                asynchronous: true
                                                Rectangle {
                                                    visible: msg.quoteVideo
                                                    anchors.centerIn: parent
                                                    width: 24; height: 24; radius: 12
                                                    color: Qt.rgba(0, 0, 0, 0.6)
                                                    Icon { anchors.centerIn: parent; anchors.horizontalCenterOffset: 1; name: "play"; size: 13; tint: "white" }
                                                }
                                            }
                                            TapHandler { onTapped: root.jumpTo(msg.quoteId) }
                                            HoverHandler { cursorShape: Qt.PointingHandCursor }
                                        }

                                        // images, video thumbnails, stickers
                                        Item {
                                            id: mediaBox
                                            visible: msg.visualMedia
                                            width: msg.sticker ? 170 : Math.min(330, msgList.maxBubble)
                                            height: msg.sticker ? 170 : (img.status === Image.Ready && img.implicitWidth > 0
                                                    ? Math.min(width * img.implicitHeight / img.implicitWidth, 440) : width * 0.62)
                                            Rectangle {
                                                anchors.fill: parent; radius: 10
                                                color: t.field
                                                visible: img.status !== Image.Ready && !msg.sticker
                                                Icon { anchors.centerIn: parent; visible: msg.kind !== "video"; name: "download"; size: 26; tint: t.text3 }
                                            }
                                            Image {
                                                id: img
                                                anchors.fill: parent
                                                source: msg.media
                                                asynchronous: true
                                                fillMode: msg.sticker ? Image.PreserveAspectFit : Image.PreserveAspectCrop
                                                smooth: true
                                                mipmap: true
                                            }
                                            Rectangle {
                                                visible: msg.kind === "video"
                                                anchors.centerIn: parent
                                                width: 56; height: 56; radius: 28
                                                color: Qt.rgba(0, 0, 0, 0.55)
                                                Icon { anchors.centerIn: parent; anchors.horizontalCenterOffset: 2; name: "play"; size: 24; tint: "white" }
                                            }
                                            TapHandler { onTapped: msg.activate() }
                                            HoverHandler { cursorShape: Qt.PointingHandCursor }
                                        }

                                        // voice messages / audio: inline player
                                        Row {
                                            id: audio
                                            visible: (msg.kind === "voice" || msg.kind === "audio") && !msg.deleted
                                            spacing: 10
                                            property bool wantPlay: false
                                            MediaPlayer {
                                                id: player
                                                source: audio.visible && msg.media !== "" && !msg.media.endsWith("_thumb.jpg") ? msg.media : ""
                                                audioOutput: AudioOutput {}
                                                onSourceChanged: if (audio.wantPlay && source != "") { audio.wantPlay = false; play() }
                                            }
                                            AbstractButton {
                                                id: playBtn
                                                width: 42; height: 42
                                                anchors.verticalCenter: parent.verticalCenter
                                                hoverEnabled: true
                                                background: Rectangle { radius: 21; color: playBtn.hovered ? Qt.lighter(t.accent, 1.1) : t.accent }
                                                contentItem: Item {
                                                    BusyIndicator { anchors.centerIn: parent; running: audio.wantPlay; visible: running; width: 30; height: 30 }
                                                    Icon {
                                                        visible: !audio.wantPlay
                                                        anchors.centerIn: parent
                                                        anchors.horizontalCenterOffset: player.playbackState === MediaPlayer.PlayingState ? 0 : 1
                                                        name: player.playbackState === MediaPlayer.PlayingState ? "pause" : "play"
                                                        size: 18; tint: t.accentFg
                                                    }
                                                }
                                                onClicked: {
                                                    if (player.source == "") { audio.wantPlay = true; root.call("loadAudio", msg.msgId); return }
                                                    if (player.playbackState === MediaPlayer.PlayingState) player.pause(); else player.play()
                                                }
                                            }
                                            Column {
                                                anchors.verticalCenter: parent.verticalCenter
                                                spacing: 0
                                                Slider {
                                                    id: seek
                                                    width: 200
                                                    height: 22
                                                    from: 0
                                                    to: Math.max(1, player.duration)
                                                    value: player.position
                                                    enabled: player.source != ""
                                                    onMoved: player.position = value
                                                    background: Rectangle {
                                                        x: seek.leftPadding; y: seek.topPadding + seek.availableHeight / 2 - 2
                                                        width: seek.availableWidth; height: 4; radius: 2
                                                        color: msg.fromMe ? Qt.rgba(1, 1, 1, 0.18) : t.line
                                                        Rectangle { width: seek.visualPosition * parent.width; height: 4; radius: 2; color: t.accent }
                                                    }
                                                    handle: Rectangle {
                                                        x: seek.leftPadding + seek.visualPosition * (seek.availableWidth - 12)
                                                        y: seek.topPadding + seek.availableHeight / 2 - 6
                                                        width: 12; height: 12; radius: 6
                                                        color: t.accent
                                                    }
                                                }
                                                Row {
                                                    spacing: 5
                                                    leftPadding: 4
                                                    Icon { name: "mic"; size: 12; tint: t.text3; visible: msg.kind === "voice"; anchors.verticalCenter: parent.verticalCenter }
                                                    Text {
                                                        text: player.playbackState !== MediaPlayer.StoppedState
                                                              ? Math.floor(player.position / 60000) + ":" + ("0" + Math.floor(player.position / 1000) % 60).slice(-2)
                                                              : msg.duration
                                                        color: t.text3
                                                        font.pixelSize: 11
                                                    }
                                                }
                                            }
                                        }

                                        Text {
                                            id: deletedText
                                            visible: msg.deleted
                                            text: "This message was deleted"
                                            color: t.text3
                                            font.italic: true
                                            font.pixelSize: 14
                                        }

                                        // document: inline preview + file card
                                        Rectangle {
                                            id: docCard
                                            visible: msg.kind === "document" && !msg.deleted
                                            width: Math.min(320, msgList.maxBubble)
                                            height: docCol.height
                                            radius: 10
                                            clip: true
                                            color: msg.fromMe ? Qt.rgba(0, 0, 0, t.dark ? 0.22 : 0.06) : t.field
                                            readonly property string icon: root.fileIcon(msg.fileName)
                                            Column {
                                                id: docCol
                                                width: parent.width
                                                // first page / frame / picture
                                                Item {
                                                    width: parent.width
                                                    height: docImg.status === Image.Ready && docImg.implicitWidth > 0
                                                            ? Math.min(width * docImg.implicitHeight / docImg.implicitWidth, 260) : 0
                                                    visible: height > 0
                                                    clip: true
                                                    Image {
                                                        id: docImg
                                                        width: parent.width
                                                        height: width * implicitHeight / Math.max(1, implicitWidth)
                                                        source: msg.kind === "document" ? msg.preview : ""
                                                        asynchronous: true
                                                        smooth: true
                                                        mipmap: true
                                                    }
                                                    Rectangle { // fade into the card for cut-off pages
                                                        visible: docImg.height > parent.height + 1
                                                        anchors.bottom: parent.bottom
                                                        width: parent.width; height: 36
                                                        gradient: Gradient {
                                                            GradientStop { position: 0; color: "transparent" }
                                                            GradientStop { position: 1; color: Qt.rgba(0, 0, 0, 0.18) }
                                                        }
                                                    }
                                                    Rectangle {
                                                        visible: msg.viewKind === "video"
                                                        anchors.centerIn: parent
                                                        width: 48; height: 48; radius: 24
                                                        color: Qt.rgba(0, 0, 0, 0.55)
                                                        Icon { anchors.centerIn: parent; anchors.horizontalCenterOffset: 2; name: "play"; size: 20; tint: "white" }
                                                    }
                                                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: t.line }
                                                }
                                                // text files: the first lines
                                                Rectangle {
                                                    width: parent.width
                                                    visible: msg.snippet !== ""
                                                    height: visible ? snippetText.height + 20 : 0
                                                    color: Qt.rgba(0, 0, 0, t.dark ? 0.18 : 0.04)
                                                    Text {
                                                        id: snippetText
                                                        x: 12; y: 10
                                                        width: parent.width - 24
                                                        text: msg.snippet
                                                        textFormat: Text.PlainText
                                                        color: t.text2
                                                        font.family: "JetBrains Mono, Fira Code, DejaVu Sans Mono, monospace"
                                                        font.pixelSize: 11
                                                        lineHeight: 1.15
                                                        elide: Text.ElideRight
                                                        wrapMode: Text.NoWrap
                                                        maximumLineCount: 10
                                                    }
                                                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: t.line }
                                                }
                                                Item {
                                                    width: parent.width
                                                    height: 58
                                                    Rectangle {
                                                        id: docIcon
                                                        x: 10; anchors.verticalCenter: parent.verticalCenter
                                                        width: 38; height: 38; radius: 9
                                                        color: t.accentSoft
                                                        Icon { anchors.centerIn: parent; name: docCard.icon; size: 19; tint: t.accent }
                                                    }
                                                    Column {
                                                        anchors.left: docIcon.right; anchors.leftMargin: 12
                                                        anchors.right: docDl.left; anchors.rightMargin: 8
                                                        anchors.verticalCenter: parent.verticalCenter
                                                        spacing: 2
                                                        Text {
                                                            width: parent.width
                                                            text: msg.fileName
                                                            color: msg.fg
                                                            font.pixelSize: 14
                                                            font.weight: Font.Medium
                                                            elide: Text.ElideMiddle
                                                        }
                                                        Text {
                                                            width: parent.width
                                                            visible: text !== ""
                                                            text: msg.fileInfo
                                                            color: t.text3
                                                            font.pixelSize: 12
                                                            elide: Text.ElideRight
                                                        }
                                                    }
                                                    IconButton {
                                                        id: docDl
                                                        anchors.right: parent.right; anchors.rightMargin: 6
                                                        anchors.verticalCenter: parent.verticalCenter
                                                        size: 34; iconSize: 18
                                                        iconName: "download"; tip: "Save…"
                                                        onClicked: root.call("save", msg.msgId)
                                                    }
                                                }
                                            }
                                            TapHandler {
                                                // the save button handles its own clicks
                                                onTapped: (ev) => { if (ev.position.x < docDl.x + docCol.x || ev.position.y < docCol.height - 58) msg.activate() }
                                            }
                                            HoverHandler { cursorShape: Qt.PointingHandCursor }
                                        }

                                        // hidden measurer: unwrapped text width
                                        Text { id: measure; visible: false; text: textBody.text; textFormat: Text.RichText; font.pixelSize: 15; font.family: root.font.family }
                                        TextEdit {
                                            id: textBody
                                            readonly property string shown: msg.kind === "document" ? msg.body.split("<br>").slice(1).join("<br>") : msg.body
                                            visible: shown !== "" && !msg.deleted
                                            width: Math.min(measure.implicitWidth + 2, msgList.maxBubble)
                                            text: "<style>a { color: " + t.link + "; text-decoration: none; }</style>" + shown
                                            textFormat: TextEdit.RichText
                                            wrapMode: TextEdit.Wrap
                                            readOnly: true
                                            selectByMouse: true
                                            color: msg.fg
                                            selectionColor: t.accent
                                            selectedTextColor: t.accentFg
                                            font.pixelSize: 15
                                            onLinkActivated: (link) => root.call("link", link)
                                            HoverHandler { cursorShape: textBody.hoveredLink !== "" ? Qt.PointingHandCursor : Qt.IBeamCursor }
                                        }

                                        // link preview card
                                        Rectangle {
                                            id: linkCard
                                            visible: msg.linkUrl !== "" && (msg.linkTitle !== "" || msg.linkImage !== "")
                                            // big picture for videos and wide images, a square thumb otherwise
                                            readonly property bool big: msg.linkImage !== "" && (msg.linkVideo ||
                                                    lImg.status === Image.Ready && lImg.implicitWidth >= 400 && lImg.implicitWidth >= lImg.implicitHeight * 1.3)
                                            width: Math.min(big ? 360 : 340, msgList.maxBubble)
                                            height: lCol.height
                                            radius: 10
                                            clip: true
                                            color: msg.fromMe ? Qt.rgba(0, 0, 0, t.dark ? 0.22 : 0.06) : t.field
                                            Rectangle { width: 3; height: parent.height; color: t.accent; visible: !linkCard.big }
                                            Column {
                                                id: lCol
                                                width: parent.width
                                                Item {
                                                    width: parent.width
                                                    height: linkCard.big ? Math.round(width * 9 / 16) : 0
                                                    visible: linkCard.big
                                                    clip: true
                                                    Image {
                                                        id: lImg
                                                        anchors.fill: parent
                                                        source: msg.linkImage
                                                        fillMode: Image.PreserveAspectCrop
                                                        asynchronous: true
                                                        smooth: true
                                                        mipmap: true
                                                    }
                                                    Rectangle {
                                                        visible: msg.linkVideo
                                                        anchors.centerIn: parent
                                                        width: 64; height: 44; radius: 12
                                                        color: msg.linkSite === "YouTube" ? "#e62117" : Qt.rgba(0, 0, 0, 0.6)
                                                        Icon { anchors.centerIn: parent; anchors.horizontalCenterOffset: 1; name: "play"; size: 20; tint: "white" }
                                                    }
                                                }
                                                Item {
                                                    width: parent.width
                                                    height: Math.max(lText.height, linkCard.big || msg.linkImage === "" ? 0 : 64) + 20
                                                    Image {
                                                        id: lThumb
                                                        visible: !linkCard.big && msg.linkImage !== ""
                                                        anchors.right: parent.right; anchors.rightMargin: 10
                                                        anchors.verticalCenter: parent.verticalCenter
                                                        width: 64; height: 64
                                                        source: visible ? msg.linkImage : ""
                                                        sourceSize.width: 128
                                                        fillMode: Image.PreserveAspectCrop
                                                        asynchronous: true
                                                    }
                                                    Column {
                                                        id: lText
                                                        x: linkCard.big ? 12 : 14
                                                        anchors.verticalCenter: parent.verticalCenter
                                                        width: parent.width - x - (lThumb.visible ? 64 + 22 : 12)
                                                        spacing: 3
                                                        Text {
                                                            width: parent.width
                                                            text: msg.linkSite
                                                            color: t.text3; font.pixelSize: 11
                                                            elide: Text.ElideRight; textFormat: Text.PlainText
                                                        }
                                                        Text {
                                                            width: parent.width
                                                            visible: text !== ""
                                                            text: msg.linkTitle
                                                            color: msg.fg; font.pixelSize: 14; font.weight: Font.DemiBold
                                                            wrapMode: Text.Wrap; maximumLineCount: 2; elide: Text.ElideRight
                                                            textFormat: Text.PlainText
                                                        }
                                                        Text {
                                                            width: parent.width
                                                            visible: text !== ""
                                                            text: msg.linkDesc
                                                            color: t.text2; font.pixelSize: 12
                                                            wrapMode: Text.Wrap; maximumLineCount: linkCard.big ? 2 : 3; elide: Text.ElideRight
                                                            textFormat: Text.PlainText
                                                        }
                                                    }
                                                }
                                            }
                                            TapHandler { onTapped: root.call("link", msg.linkUrl) }
                                            HoverHandler { cursorShape: Qt.PointingHandCursor }
                                            ToolTip.visible: lHover.hovered
                                            ToolTip.delay: 800
                                            ToolTip.text: msg.linkUrl
                                            HoverHandler { id: lHover }
                                        }

                                        Row {
                                            id: meta
                                            // over images without caption, the time floats on the picture
                                            readonly property bool overlay: msg.mediaOnly && !msg.sticker
                                            anchors.right: parent.right
                                            spacing: 3
                                            visible: !msg.sticker && !overlay
                                            Text { visible: msg.edited && !msg.deleted; text: "edited ·"; color: t.text3; font.pixelSize: 11; anchors.verticalCenter: parent.verticalCenter }
                                            Text { text: msg.time; color: t.text3; font.pixelSize: 11; anchors.verticalCenter: parent.verticalCenter }
                                            Icon {
                                                visible: msg.fromMe && !msg.deleted
                                                anchors.verticalCenter: parent.verticalCenter
                                                name: msg.status === 0 ? "check" : "checks"
                                                size: 15
                                                tint: msg.status === 2 ? t.read : t.text3
                                            }
                                        }
                                    }

                                    // time on top of caption-less images / stickers
                                    Rectangle {
                                        visible: msg.mediaOnly || msg.sticker
                                        anchors.right: parent.right
                                        anchors.bottom: parent.bottom
                                        anchors.margins: 10
                                        width: ovRow.implicitWidth + 12
                                        height: 20
                                        radius: 10
                                        color: Qt.rgba(0, 0, 0, 0.5)
                                        Row {
                                            id: ovRow
                                            anchors.centerIn: parent
                                            spacing: 3
                                            Text { text: msg.time; color: "white"; font.pixelSize: 11; anchors.verticalCenter: parent.verticalCenter }
                                            Icon {
                                                visible: msg.fromMe
                                                anchors.verticalCenter: parent.verticalCenter
                                                name: msg.status === 0 ? "check" : "checks"; size: 14
                                                tint: msg.status === 2 ? t.read : "white"
                                            }
                                        }
                                    }

                                    // hover: quick action button
                                    IconButton {
                                        visible: bubbleHover.hovered && !msg.deleted
                                        anchors.top: parent.top
                                        anchors.topMargin: 2
                                        x: msg.fromMe ? -width - 4 : parent.width + 4
                                        size: 30; iconSize: 16; iconName: "down"
                                        tip: "Reply, react, …"
                                        onClicked: msg.menu()
                                    }
                                }

                                // reactions
                                Rectangle {
                                    id: reactionPill
                                    visible: msg.reactions !== "" && !msg.deleted
                                    anchors.top: bubble.bottom
                                    anchors.topMargin: -6
                                    x: msg.fromMe ? bubble.x + bubble.width - width - 10 : bubble.x + 10
                                    height: 26
                                    width: reactText.implicitWidth + 16
                                    radius: 13
                                    color: t.raised
                                    border.width: 1
                                    border.color: t.line
                                    Text { id: reactText; anchors.centerIn: parent; text: msg.reactions; color: t.text; font.pixelSize: 13 }
                                    TapHandler { onTapped: msg.menu() }
                                }
                            }
                        }
                    }

                    // jump to latest
                    AbstractButton {
                        id: jumpBtn
                        anchors.right: parent.right
                        anchors.bottom: parent.bottom
                        anchors.margins: 18
                        width: 42; height: 42
                        opacity: msgList.atBottom ? 0 : 1
                        visible: opacity > 0
                        Behavior on opacity { NumberAnimation { duration: 150 } }
                        hoverEnabled: true
                        background: Rectangle {
                            radius: 21
                            color: jumpBtn.hovered ? t.hover : t.raised
                            border.width: 1
                            border.color: t.line
                        }
                        contentItem: Item { Icon { anchors.centerIn: parent; name: "down"; size: 20; tint: t.text2 } }
                        onClicked: msgList.scrollToEnd()
                    }

                    // drag & drop
                    DropArea {
                        id: drop
                        anchors.fill: parent
                        onDropped: (d) => { if (d.hasUrls) root.addAttachments(d.urls) }
                        Rectangle {
                            anchors.fill: parent
                            anchors.margins: 16
                            visible: drop.containsDrag
                            radius: 18
                            color: Qt.rgba(t.accent.r, t.accent.g, t.accent.b, 0.10)
                            border.width: 2
                            border.color: t.accent
                            Column {
                                anchors.centerIn: parent
                                spacing: 10
                                Icon { anchors.horizontalCenter: parent.horizontalCenter; name: "clip"; size: 34; tint: t.accent }
                                Text { text: "Drop files to send"; color: t.accent; font.pixelSize: 18; font.weight: Font.DemiBold }
                            }
                        }
                    }
                }

                // ---- composer
                Rectangle {
                    Layout.fillWidth: true
                    Layout.preferredHeight: composerCol.implicitHeight + 24
                    color: t.panel
                    Rectangle { anchors.top: parent.top; width: parent.width; height: 1; color: t.line }

                    ColumnLayout {
                        id: composerCol
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        anchors.leftMargin: 14
                        anchors.rightMargin: 14
                        spacing: 8

                        // reply / edit banner
                        Rectangle {
                            Layout.fillWidth: true
                            visible: root.replyId !== "" || root.editId !== ""
                            implicitHeight: 50
                            radius: 10
                            color: t.field
                            clip: true
                            Rectangle { width: 4; height: parent.height; color: t.accent }
                            Icon {
                                id: bannerIcon
                                x: 16; anchors.verticalCenter: parent.verticalCenter
                                name: root.editId !== "" ? "edit" : "reply"; size: 18; tint: t.accent
                            }
                            Column {
                                anchors.left: bannerIcon.right
                                anchors.leftMargin: 12
                                anchors.right: bannerThumb.left
                                anchors.rightMargin: 8
                                anchors.verticalCenter: parent.verticalCenter
                                spacing: 2
                                Text {
                                    text: root.editId !== "" ? "Editing message" : "Replying to " + root.replyName
                                    color: t.accent; font.pixelSize: 12; font.weight: Font.DemiBold
                                }
                                Text {
                                    width: parent.width
                                    text: root.editId !== "" ? "Enter to save · Esc to cancel" : root.replyText
                                    color: t.text2; font.pixelSize: 13; elide: Text.ElideRight; maximumLineCount: 1
                                    textFormat: Text.PlainText
                                }
                            }
                            Image {
                                id: bannerThumb
                                readonly property bool shown: root.editId === "" && root.replyThumb !== "" && status !== Image.Error
                                visible: shown
                                anchors.right: bannerClose.left
                                anchors.rightMargin: shown ? 6 : 0
                                anchors.verticalCenter: parent.verticalCenter
                                width: shown ? 40 : 0; height: 40
                                source: root.replyThumb
                                sourceSize.width: 80; sourceSize.height: 80
                                fillMode: Image.PreserveAspectCrop
                                asynchronous: true
                                Rectangle {
                                    visible: root.replyVideo
                                    anchors.centerIn: parent
                                    width: 20; height: 20; radius: 10
                                    color: Qt.rgba(0, 0, 0, 0.6)
                                    Icon { anchors.centerIn: parent; anchors.horizontalCenterOffset: 1; name: "play"; size: 11; tint: "white" }
                                }
                            }
                            IconButton {
                                id: bannerClose
                                anchors.right: parent.right
                                anchors.rightMargin: 6
                                anchors.verticalCenter: parent.verticalCenter
                                iconName: "x"; size: 30; iconSize: 16
                                onClicked: root.cancelCompose()
                            }
                        }

                        // link preview of the message being typed
                        Rectangle {
                            Layout.fillWidth: true
                            visible: root.showComposerLink
                            implicitHeight: 64
                            radius: 10
                            color: t.field
                            clip: true
                            Rectangle { width: 4; height: parent.height; color: t.accent }
                            Image {
                                id: clImg
                                x: 12; anchors.verticalCenter: parent.verticalCenter
                                width: visible ? 48 : 0; height: 48
                                visible: root.linkHasData && root.cLink.image !== ""
                                source: visible ? root.cLink.image : ""
                                sourceSize.width: 96
                                fillMode: Image.PreserveAspectCrop
                                asynchronous: true
                            }
                            Column {
                                anchors.left: clImg.right
                                anchors.leftMargin: 12
                                anchors.right: clClose.left
                                anchors.verticalCenter: parent.verticalCenter
                                spacing: 2
                                Text {
                                    width: parent.width
                                    text: root.linkLoading ? "Loading preview…"
                                          : !root.linkHasData ? "No preview available"
                                          : root.cLink.title !== "" ? root.cLink.title : root.linkHost
                                    color: root.linkHasData ? t.text : t.text2
                                    font.pixelSize: 13; font.weight: root.linkHasData ? Font.DemiBold : Font.Normal
                                    elide: Text.ElideRight; textFormat: Text.PlainText
                                }
                                Text {
                                    width: parent.width
                                    visible: text !== ""
                                    text: root.linkHasData ? root.cLink.desc : ""
                                    color: t.text2; font.pixelSize: 12
                                    elide: Text.ElideRight; maximumLineCount: 1; textFormat: Text.PlainText
                                }
                                Text {
                                    width: parent.width
                                    text: root.linkHasData && root.cLink.site !== "" ? root.cLink.site : root.linkHost
                                    color: t.text3; font.pixelSize: 11
                                    elide: Text.ElideRight; textFormat: Text.PlainText
                                }
                            }
                            IconButton {
                                id: clClose
                                anchors.right: parent.right
                                anchors.rightMargin: 6
                                anchors.verticalCenter: parent.verticalCenter
                                iconName: "x"; size: 30; iconSize: 16; tip: "Send without preview"
                                onClicked: root.linkDismissed = root.linkUrl
                            }
                        }

                        // pending attachments
                        Flow {
                            Layout.fillWidth: true
                            visible: root.attachments.length > 0
                            spacing: 6
                            Repeater {
                                model: root.attachments
                                delegate: Rectangle {
                                    id: att
                                    required property string modelData
                                    required property int index
                                    readonly property string type: root.fileIcon(modelData)
                                    readonly property string frame: root.attachFrames[modelData] === undefined ? "" : root.attachFrames[modelData]
                                    // pictures and videos get a real preview; anything
                                    // that can't be shown stays a file chip
                                    readonly property bool tile: type === "image" ? attThumb.status !== Image.Error
                                                               : type === "video" && root.attachFrames[modelData] !== ""
                                    readonly property real aspect: attThumb.status === Image.Ready && attThumb.implicitHeight > 0
                                                                   ? attThumb.implicitWidth / attThumb.implicitHeight : 1
                                    height: tile ? 132 : 44
                                    width: tile ? Math.max(96, Math.min(126 * aspect + 6, 240)) : chipRow.implicitWidth + 18
                                    radius: tile ? 10 : 12
                                    color: t.field
                                    border.width: 1
                                    border.color: t.line
                                    clip: true

                                    Icon {
                                        anchors.centerIn: parent
                                        visible: att.tile && attThumb.status !== Image.Ready
                                        name: att.type; size: 28; tint: t.text3
                                    }
                                    Image {
                                        id: attThumb
                                        visible: att.tile
                                        anchors.fill: parent
                                        anchors.margins: 3
                                        source: att.type === "image" ? att.modelData : att.frame
                                        sourceSize.height: 264
                                        fillMode: Image.PreserveAspectCrop
                                        asynchronous: true
                                    }
                                    Rectangle {
                                        visible: att.tile && att.type === "video"
                                        anchors.centerIn: parent
                                        width: 40; height: 40; radius: 20
                                        color: Qt.rgba(0, 0, 0, 0.6)
                                        Icon { anchors.centerIn: parent; anchors.horizontalCenterOffset: 2; name: "play"; size: 18; tint: "white" }
                                    }
                                    Rectangle {
                                        visible: att.tile
                                        anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom
                                        anchors.margins: 3
                                        height: 22
                                        color: Qt.rgba(0, 0, 0, 0.6)
                                        Text {
                                            anchors.fill: parent; anchors.leftMargin: 6; anchors.rightMargin: 6
                                            verticalAlignment: Text.AlignVCenter
                                            text: root.fileName(att.modelData)
                                            color: "white"; font.pixelSize: 11
                                            elide: Text.ElideMiddle; textFormat: Text.PlainText
                                        }
                                    }
                                    Rectangle {
                                        visible: att.tile
                                        anchors.right: parent.right; anchors.top: parent.top
                                        anchors.margins: 6
                                        width: 24; height: 24; radius: 12
                                        color: tileCloseHover.hovered ? Qt.rgba(0, 0, 0, 0.85) : Qt.rgba(0, 0, 0, 0.6)
                                        Icon { anchors.centerIn: parent; name: "x"; size: 14; tint: "white" }
                                        HoverHandler { id: tileCloseHover; cursorShape: Qt.PointingHandCursor }
                                        TapHandler {
                                            onTapped: { var l = root.attachments.slice(); l.splice(att.index, 1); root.attachments = l }
                                        }
                                    }

                                    Row {
                                        id: chipRow
                                        visible: !att.tile
                                        anchors.verticalCenter: parent.verticalCenter
                                        x: 6
                                        spacing: 8
                                        Rectangle {
                                            width: 32; height: 32; radius: 7
                                            anchors.verticalCenter: parent.verticalCenter
                                            color: t.accentSoft
                                            Icon { anchors.centerIn: parent; name: att.type; size: 16; tint: t.accent }
                                        }
                                        Text { text: root.fileName(att.modelData); color: t.text; font.pixelSize: 13; anchors.verticalCenter: parent.verticalCenter; elide: Text.ElideMiddle; width: Math.min(implicitWidth, 260) }
                                        Icon {
                                            name: "x"; size: 14; tint: t.text3
                                            anchors.verticalCenter: parent.verticalCenter
                                            TapHandler {
                                                onTapped: { var l = root.attachments.slice(); l.splice(att.index, 1); root.attachments = l }
                                            }
                                        }
                                    }
                                }
                            }
                        }

                        RowLayout {
                            Layout.fillWidth: true
                            spacing: 10

                            // input pill with emoji + attach inside
                            Rectangle {
                                Layout.fillWidth: true
                                Layout.preferredHeight: Math.max(46, Math.min(composer.implicitHeight + 4, 170))
                                radius: 23
                                color: t.field
                                border.width: composer.activeFocus ? 1.5 : 0
                                border.color: Qt.rgba(t.accent.r, t.accent.g, t.accent.b, 0.6)

                                IconButton {
                                    id: emojiBtn
                                    anchors.left: parent.left
                                    anchors.leftMargin: 4
                                    anchors.bottom: parent.bottom
                                    anchors.bottomMargin: 4
                                    iconName: "smile"; tip: "Emoji (Ctrl+E)"
                                    tint: emojiPicker.visible ? t.accent : t.text2
                                    onClicked: emojiPicker.toggle(false, "")
                                }
                                IconButton {
                                    id: attachBtn
                                    anchors.right: parent.right
                                    anchors.rightMargin: 4
                                    anchors.bottom: parent.bottom
                                    anchors.bottomMargin: 4
                                    iconName: "clip"; tip: "Attach files (or drop / paste them)"
                                    onClicked: root.pickFiles()
                                }

                                ScrollView {
                                    anchors.left: emojiBtn.right
                                    anchors.right: attachBtn.left
                                    anchors.top: parent.top
                                    anchors.bottom: parent.bottom
                                    TextArea {
                                        id: composer
                                        placeholderText: !root.loggedIn ? "Not connected"
                                                         : root.attachments.length > 0 ? "Add a caption…" : "Message"
                                        placeholderTextColor: t.text3
                                        color: t.text
                                        selectionColor: t.accent
                                        selectedTextColor: t.accentFg
                                        wrapMode: TextArea.Wrap
                                        font.pixelSize: 15
                                        background: null
                                        topPadding: 12
                                        bottomPadding: 12
                                        leftPadding: 4
                                        rightPadding: 4
                                        selectByMouse: true

                                        // :shortcode completion
                                        property string token: ""
                                        onTextChanged: {
                                            if (text !== "" && root.editId === "") root.call("typing")
                                            linkTimer.restart()
                                            var before = text.substring(0, cursorPosition)
                                            var full = before.match(/(^|\s):([a-z0-9_+\-]+):$/i)
                                            if (full) {
                                                root.call("exact", full[2])
                                                if (root.exactEmoji !== "") {
                                                    var e = root.exactEmoji
                                                    var start = cursorPosition - full[2].length - 2
                                                    token = ""
                                                    root.call("complete", "")
                                                    remove(start, cursorPosition)
                                                    insert(start, e)
                                                    root.call("emojiUse", e)
                                                    return
                                                }
                                            }
                                            var m = before.match(/(^|\s):([a-z0-9_+\-]{2,})$/i)
                                            token = m ? m[2] : ""
                                            root.call("complete", token)
                                            completion.currentIndex = 0
                                        }
                                        function acceptCompletion(i) {
                                            var e = completionModel.data(completionModel.index(i, 0), 257)
                                            var start = cursorPosition - token.length - 1
                                            token = ""
                                            root.call("complete", "")
                                            remove(start, cursorPosition)
                                            insert(start, e)
                                            root.call("emojiUse", e)
                                        }
                                        readonly property bool completing: token !== "" && completion.count > 0

                                        Keys.onPressed: (event) => {
                                            if (completing) {
                                                if (event.key === Qt.Key_Down) { completion.currentIndex = Math.min(completion.count - 1, completion.currentIndex + 1); event.accepted = true; return }
                                                if (event.key === Qt.Key_Up) { completion.currentIndex = Math.max(0, completion.currentIndex - 1); event.accepted = true; return }
                                                if (event.key === Qt.Key_Tab || event.key === Qt.Key_Return || event.key === Qt.Key_Enter) {
                                                    acceptCompletion(completion.currentIndex); event.accepted = true; return
                                                }
                                                if (event.key === Qt.Key_Escape) { token = ""; root.call("complete", ""); event.accepted = true; return }
                                            }
                                            if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter) && !(event.modifiers & Qt.ShiftModifier)) {
                                                root.send(); event.accepted = true; return
                                            }
                                            if (event.key === Qt.Key_Escape) {
                                                if (emojiPicker.visible) emojiPicker.close()
                                                else if (root.replyId !== "" || root.editId !== "") root.cancelCompose()
                                                else root.attachments = []
                                                event.accepted = true; return
                                            }
                                            if (event.key === Qt.Key_Up && text === "" && root.attachments.length === 0) {
                                                event.accepted = root.editLastOwn(); return
                                            }
                                            if (event.key === Qt.Key_V && (event.modifiers & Qt.ControlModifier)) {
                                                root.call("paste")
                                                if (root.pastedFile !== "") { root.addAttachments([root.pastedFile]); event.accepted = true }
                                            }
                                        }
                                    }
                                }

                                // :shortcode completion popup
                                Rectangle {
                                    visible: composer.completing
                                    anchors.bottom: parent.top
                                    anchors.bottomMargin: 8
                                    x: 40
                                    width: 320
                                    height: Math.min(completion.count, 8) * 36 + 10
                                    radius: 12
                                    color: t.raised
                                    border.width: 1
                                    border.color: t.line
                                    ListView {
                                        id: completion
                                        anchors.fill: parent
                                        anchors.margins: 5
                                        model: completionModel
                                        interactive: false
                                        delegate: Rectangle {
                                            required property int index
                                            required property string e
                                            required property string code
                                            width: ListView.view.width
                                            height: 36
                                            radius: 8
                                            readonly property bool current: index === completion.currentIndex
                                            color: current ? t.accentSoft : (cHover.hovered ? t.raisedHover : "transparent")
                                            HoverHandler { id: cHover }
                                            Rectangle {
                                                visible: parent.current
                                                width: 3; height: 18; radius: 1.5
                                                anchors.verticalCenter: parent.verticalCenter
                                                color: t.accent
                                            }
                                            Row {
                                                anchors.verticalCenter: parent.verticalCenter
                                                x: 12
                                                spacing: 12
                                                Text { text: e; font.pixelSize: 20; anchors.verticalCenter: parent.verticalCenter }
                                                Text {
                                                    text: ":" + code + ":"
                                                    color: parent.parent.current ? t.accent : t.text
                                                    font.pixelSize: 14
                                                    font.weight: parent.parent.current ? Font.DemiBold : Font.Normal
                                                    anchors.verticalCenter: parent.verticalCenter
                                                }
                                            }
                                            Text {
                                                visible: parent.current
                                                anchors.right: parent.right
                                                anchors.rightMargin: 10
                                                anchors.verticalCenter: parent.verticalCenter
                                                text: "Tab ↹"
                                                color: t.accent
                                                font.pixelSize: 11
                                                opacity: 0.8
                                            }
                                            TapHandler { onTapped: composer.acceptCompletion(index) }
                                        }
                                    }
                                }
                            }

                            AbstractButton {
                                id: sendBtn
                                Layout.alignment: Qt.AlignBottom
                                Layout.preferredWidth: 46
                                Layout.preferredHeight: 46
                                enabled: composer.text.trim() !== "" || root.attachments.length > 0
                                hoverEnabled: true
                                focusPolicy: Qt.NoFocus
                                onClicked: root.send()
                                background: Rectangle {
                                    radius: 23
                                    color: !sendBtn.enabled ? t.field : (sendBtn.hovered ? Qt.lighter(t.accent, 1.1) : t.accent)
                                    Behavior on color { ColorAnimation { duration: 120 } }
                                }
                                contentItem: Item {
                                    Icon {
                                        anchors.centerIn: parent
                                        anchors.horizontalCenterOffset: root.editId !== "" ? 0 : 1
                                        name: root.editId !== "" ? "check" : "send"
                                        size: 20
                                        tint: sendBtn.enabled ? t.accentFg : t.text3
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    
        // ------------------------------------------------ contact / group info
        Rectangle {
            Layout.fillHeight: true
            Layout.preferredWidth: 1
            visible: root.infoOpen
            color: t.line
        }
        Rectangle {
            id: infoPanel
            Layout.fillHeight: true
            Layout.preferredWidth: 340
            visible: root.infoOpen
            color: t.panel

            RowLayout {
                id: infoHeader
                anchors.left: parent.left
                anchors.right: parent.right
                height: 64
                anchors.leftMargin: 10
                anchors.rightMargin: 16
                spacing: 6
                IconButton { iconName: "x"; tip: "Close"; onClicked: root.infoOpen = false }
                Text {
                    Layout.fillWidth: true
                    text: root.currentJid.endsWith("@g.us") ? "Group info" : "Contact info"
                    color: t.text; font.pixelSize: 16; font.weight: Font.DemiBold
                }
            }
            Rectangle { anchors.top: infoHeader.bottom; width: parent.width; height: 1; color: t.line }

            Flickable {
                id: infoFlick
                WheelScroll { flick: infoFlick }
                anchors.top: infoHeader.bottom
                anchors.topMargin: 1
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.bottom: parent.bottom
                clip: true
                contentHeight: infoCol.implicitHeight + 24
                boundsBehavior: Flickable.StopAtBounds
                ScrollBar.vertical: ScrollBar {}

                Column {
                    id: infoCol
                    width: parent.width
                    spacing: 0

                    // identity
                    Column {
                        width: parent.width
                        topPadding: 28
                        bottomPadding: 22
                        spacing: 6
                        Avatar {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: 140; height: 140
                            source: root.infoAvatar
                            initials: root.infoInitials
                            tint: root.infoColor
                            TapHandler {
                                enabled: root.infoAvatar !== ""
                                onTapped: viewer.show("avatar:" + root.currentJid, "image", root.infoAvatar, "Profile photo")
                            }
                            HoverHandler { enabled: root.infoAvatar !== ""; cursorShape: Qt.PointingHandCursor }
                        }
                        Item { width: 1; height: 10 }
                        Text {
                            width: parent.width - 40
                            anchors.horizontalCenter: parent.horizontalCenter
                            horizontalAlignment: Text.AlignHCenter
                            text: root.infoName
                            color: t.text; font.pixelSize: 20; font.weight: Font.DemiBold
                            wrapMode: Text.Wrap
                        }
                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: root.infoSub
                            color: t.text2; font.pixelSize: 14
                            visible: text !== ""
                            TapHandler { onTapped: { root.call("copy", root.infoSub); root.statusText = "Copied " + root.infoSub } }
                            HoverHandler { cursorShape: Qt.PointingHandCursor }
                        }
                    }

                    // quick actions
                    Row {
                        anchors.horizontalCenter: parent.horizontalCenter
                        spacing: 10
                        bottomPadding: 18
                        Repeater {
                            model: [
                                { icon: "mute", label: root.currentMuted ? "Unmute" : "Mute", act: "mute" },
                                { icon: "pin", label: root.currentPinned ? "Unpin" : "Pin", act: "pin" },
                                { icon: "archive", label: root.currentArchived ? "Unarchive" : "Archive", act: "archive" }
                            ]
                            delegate: AbstractButton {
                                id: qa
                                required property var modelData
                                width: 92; height: 64
                                hoverEnabled: true
                                background: Rectangle { radius: 12; color: qa.hovered ? t.selected : t.field }
                                contentItem: Column {
                                    spacing: 6
                                    topPadding: 10
                                    Icon { anchors.horizontalCenter: parent.horizontalCenter; name: qa.modelData.icon; size: 18; tint: t.accent }
                                    Text { anchors.horizontalCenter: parent.horizontalCenter; text: qa.modelData.label; color: t.text; font.pixelSize: 12 }
                                }
                                onClicked: {
                                    var j = root.currentJid
                                    if (qa.modelData.act === "mute") root.call("mute", j, root.currentMuted ? "0" : "-1")
                                    else if (qa.modelData.act === "pin") root.call("pin", j, root.currentPinned ? "0" : "1")
                                    else root.call("archive", j, root.currentArchived ? "0" : "1")
                                }
                            }
                        }
                    }

                    component Section: Rectangle {
                        width: parent ? parent.width : 0
                        height: 8
                        color: t.bg
                    }

                    // about / description
                    Section { visible: aboutCol.visible }
                    Column {
                        id: aboutCol
                        visible: root.infoAbout !== "" || root.infoCreated !== ""
                        width: parent.width
                        padding: 20
                        spacing: 6
                        Text {
                            text: root.currentJid.endsWith("@g.us") ? "Description" : "About"
                            color: t.text3; font.pixelSize: 12; font.weight: Font.DemiBold
                            visible: root.infoAbout !== ""
                        }
                        TextEdit {
                            width: parent.width - 40
                            visible: root.infoAbout !== ""
                            text: root.infoAbout
                            color: t.text; font.pixelSize: 14
                            wrapMode: TextEdit.Wrap
                            readOnly: true
                            selectByMouse: true
                            selectionColor: t.accent
                        }
                        Text { text: root.infoCreated; color: t.text3; font.pixelSize: 12; visible: text !== ""; topPadding: 4 }
                    }

                    // details
                    Section { visible: detailCol.visible }
                    Column {
                        id: detailCol
                        readonly property var rows: JSON.parse(root.infoDetails)
                        visible: rows.length > 0
                        width: parent.width
                        padding: 20
                        spacing: 12
                        Repeater {
                            model: detailCol.rows
                            delegate: Column {
                                required property var modelData
                                width: detailCol.width - 40
                                spacing: 2
                                Text { text: modelData.label; color: t.text3; font.pixelSize: 12; font.weight: Font.DemiBold }
                                TextEdit {
                                    width: parent.width
                                    text: modelData.value
                                    color: t.text; font.pixelSize: 14
                                    wrapMode: TextEdit.Wrap
                                    readOnly: true
                                    selectByMouse: true
                                    selectionColor: t.accent
                                }
                            }
                        }
                    }

                    // shared media, files and links
                    Section { visible: historyCol.visible }
                    Column {
                        id: historyCol
                        visible: root.infoMediaTotal + root.infoFilesTotal + root.infoLinksTotal > 0
                        width: parent.width
                        padding: 20
                        topPadding: 10
                        spacing: 10

                        Row {
                            id: historyTabs
                            width: historyCol.width - 40
                            Repeater {
                                model: [
                                    { key: "media", label: "Media", n: root.infoMediaTotal },
                                    { key: "files", label: "Files", n: root.infoFilesTotal },
                                    { key: "links", label: "Links", n: root.infoLinksTotal }
                                ]
                                delegate: AbstractButton {
                                    id: tabBtn
                                    required property var modelData
                                    readonly property bool current: root.infoTabShown === modelData.key
                                    width: historyTabs.width / 3; height: 40
                                    enabled: modelData.n > 0
                                    hoverEnabled: true
                                    onClicked: root.infoTab = modelData.key
                                    background: Item {
                                        Rectangle { anchors.fill: parent; anchors.bottomMargin: 2; radius: 8; color: tabBtn.hovered && !tabBtn.current ? t.hover : "transparent" }
                                        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 2; radius: 1; color: tabBtn.current ? t.accent : t.line }
                                    }
                                    contentItem: Text {
                                        text: tabBtn.modelData.label + (tabBtn.modelData.n > 0 ? "  " + tabBtn.modelData.n : "")
                                        color: tabBtn.current ? t.accent : (tabBtn.enabled ? t.text2 : t.text3)
                                        font.pixelSize: 13; font.weight: tabBtn.current ? Font.DemiBold : Font.Normal
                                        horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter
                                    }
                                }
                            }
                        }

                        GridView {
                            id: mediaGrid
                            visible: root.infoTabShown === "media"
                            width: historyCol.width - 40
                            height: visible ? Math.ceil(count / 3) * cellHeight : 0
                            interactive: false
                            model: mediaModel
                            cellWidth: Math.floor(width / 3)
                            cellHeight: cellWidth
                            delegate: Item {
                                required property string msgId
                                required property string media
                                required property string kind
                                width: mediaGrid.cellWidth
                                height: mediaGrid.cellHeight
                                Rectangle {
                                    anchors.fill: parent
                                    anchors.margins: 2
                                    radius: 4
                                    color: t.field
                                    visible: tileImg.status !== Image.Ready
                                    Icon { anchors.centerIn: parent; visible: kind !== "video"; name: "image"; size: 20; tint: t.text3 }
                                }
                                Image {
                                    id: tileImg
                                    anchors.fill: parent
                                    anchors.margins: 2
                                    source: media
                                    fillMode: Image.PreserveAspectCrop
                                    asynchronous: true
                                    sourceSize.width: 200
                                    smooth: true
                                }
                                Icon { visible: kind === "video"; anchors.centerIn: parent; name: "play"; size: 22; tint: tileImg.status === Image.Ready ? "white" : t.text3 }
                                TapHandler { onTapped: viewer.showGallery(msgId, kind === "video" ? "video" : "image", media) }
                                HoverHandler { cursorShape: Qt.PointingHandCursor }
                            }
                        }

                        ListView {
                            id: fileList
                            visible: root.infoTabShown === "files"
                            width: historyCol.width - 40
                            height: visible ? contentHeight : 0
                            interactive: false
                            model: fileModel
                            delegate: Item {
                                id: fileRow
                                required property string msgId
                                required property string name
                                required property string info
                                required property string time
                                width: ListView.view.width
                                height: 56
                                Rectangle { anchors.fill: parent; anchors.leftMargin: -8; anchors.rightMargin: -8; radius: 10; color: fileMa.containsMouse ? t.hover : "transparent" }
                                MouseArea {
                                    id: fileMa
                                    anchors.fill: parent
                                    hoverEnabled: true
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.call("openMedia", fileRow.msgId)
                                }
                                Rectangle {
                                    id: fileIconBox
                                    width: 38; height: 38; radius: 9
                                    anchors.verticalCenter: parent.verticalCenter
                                    color: t.accentSoft
                                    Icon { anchors.centerIn: parent; name: root.fileIcon(fileRow.name); size: 18; tint: t.accent }
                                }
                                Column {
                                    anchors.left: fileIconBox.right; anchors.leftMargin: 10
                                    anchors.right: fileSave.left; anchors.rightMargin: 4
                                    anchors.verticalCenter: parent.verticalCenter
                                    spacing: 1
                                    Text { width: parent.width; text: fileRow.name; color: t.text; font.pixelSize: 13; elide: Text.ElideMiddle; textFormat: Text.PlainText }
                                    Text { width: parent.width; text: [fileRow.info, fileRow.time].filter(function(x) { return x !== "" }).join(" · "); color: t.text3; font.pixelSize: 11; elide: Text.ElideRight }
                                }
                                IconButton {
                                    id: fileSave
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    iconName: "download"; size: 32; iconSize: 16
                                    tip: root.portalOK ? "Save as…" : "Save to Downloads"
                                    onClicked: root.call("save", fileRow.msgId)
                                }
                            }
                        }

                        ListView {
                            id: linkList
                            visible: root.infoTabShown === "links"
                            width: historyCol.width - 40
                            height: visible ? contentHeight : 0
                            interactive: false
                            model: linkModel
                            delegate: Item {
                                id: linkRow
                                required property string url
                                required property string title
                                required property string site
                                required property string image
                                required property string time
                                width: ListView.view.width
                                height: 62
                                Rectangle { anchors.fill: parent; anchors.leftMargin: -8; anchors.rightMargin: -8; radius: 10; color: linkMa.containsMouse ? t.hover : "transparent" }
                                MouseArea {
                                    id: linkMa
                                    anchors.fill: parent
                                    hoverEnabled: true
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.call("link", linkRow.url)
                                    ToolTip.visible: containsMouse
                                    ToolTip.delay: 600
                                    ToolTip.text: linkRow.url
                                }
                                Rectangle {
                                    id: linkThumb
                                    width: 46; height: 46; radius: 8
                                    anchors.verticalCenter: parent.verticalCenter
                                    color: t.accentSoft
                                    clip: true
                                    Icon { anchors.centerIn: parent; visible: linkImg.status !== Image.Ready; name: "open"; size: 18; tint: t.accent }
                                    Image {
                                        id: linkImg
                                        anchors.fill: parent
                                        source: linkRow.image
                                        sourceSize.width: 92
                                        fillMode: Image.PreserveAspectCrop
                                        asynchronous: true
                                    }
                                }
                                Column {
                                    anchors.left: linkThumb.right; anchors.leftMargin: 10
                                    anchors.right: linkCopy.left; anchors.rightMargin: 4
                                    anchors.verticalCenter: parent.verticalCenter
                                    spacing: 1
                                    Text { width: parent.width; text: linkRow.title; color: t.text; font.pixelSize: 13; elide: Text.ElideRight; textFormat: Text.PlainText }
                                    Text { width: parent.width; text: [linkRow.site, linkRow.time].filter(function(x) { return x !== "" }).join(" · "); color: t.text3; font.pixelSize: 11; elide: Text.ElideRight; textFormat: Text.PlainText }
                                }
                                IconButton {
                                    id: linkCopy
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    iconName: "copy"; size: 32; iconSize: 16; tip: "Copy link"
                                    onClicked: { root.call("copy", linkRow.url); root.statusText = "Link copied" }
                                }
                            }
                        }

                        AbstractButton {
                            id: moreBtn
                            readonly property int loaded: root.infoTabShown === "media" ? mediaGrid.count : root.infoTabShown === "files" ? fileList.count : linkList.count
                            readonly property int total: root.infoTabShown === "media" ? root.infoMediaTotal : root.infoTabShown === "files" ? root.infoFilesTotal : root.infoLinksTotal
                            visible: loaded < total
                            width: historyCol.width - 40; height: 36
                            hoverEnabled: true
                            onClicked: root.call("infoMore", root.infoTabShown)
                            background: Rectangle { radius: 18; color: moreBtn.hovered ? t.selected : t.field }
                            contentItem: Text {
                                text: "Show more (" + (moreBtn.total - moreBtn.loaded) + ")"
                                color: t.text; font.pixelSize: 13
                                horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter
                            }
                        }
                    }

                    // groups shared with this contact
                    Section { visible: commonList.count > 0 }
                    Column {
                        visible: commonList.count > 0
                        width: parent.width
                        topPadding: 16
                        bottomPadding: 8
                        Row {
                            leftPadding: 20
                            spacing: 8
                            bottomPadding: 6
                            Icon { name: "users"; size: 15; tint: t.text3; anchors.verticalCenter: parent.verticalCenter }
                            Text { text: commonList.count === 1 ? "1 group in common" : commonList.count + " groups in common"; color: t.text3; font.pixelSize: 12; font.weight: Font.DemiBold }
                        }
                        ListView {
                            id: commonList
                            width: parent.width
                            height: contentHeight
                            interactive: false
                            model: commonModel
                            delegate: Item {
                                id: cg
                                required property string jid
                                required property string name
                                required property string sub
                                required property string avatar
                                required property string initials
                                required property string tint
                                width: ListView.view.width
                                height: 58
                                Rectangle { anchors.fill: parent; anchors.leftMargin: 8; anchors.rightMargin: 8; radius: 10; color: cgMa.containsMouse ? t.hover : "transparent" }
                                Avatar {
                                    id: cgAv
                                    x: 20; anchors.verticalCenter: parent.verticalCenter
                                    width: 40; height: 40
                                    source: cg.avatar; initials: cg.initials; tint: cg.tint
                                }
                                Column {
                                    anchors.left: cgAv.right; anchors.leftMargin: 12
                                    anchors.right: parent.right; anchors.rightMargin: 20
                                    anchors.verticalCenter: parent.verticalCenter
                                    Text { width: parent.width; text: cg.name; color: t.text; font.pixelSize: 14; elide: Text.ElideRight }
                                    Text { width: parent.width; text: cg.sub; color: t.text3; font.pixelSize: 12; elide: Text.ElideRight }
                                }
                                MouseArea {
                                    id: cgMa
                                    anchors.fill: parent
                                    hoverEnabled: true
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.call("open", cg.jid)
                                }
                            }
                        }
                    }

                    // group members
                    Section { visible: memberList.count > 0 }
                    Column {
                        visible: memberList.count > 0
                        width: parent.width
                        topPadding: 16
                        Row {
                            leftPadding: 20
                            spacing: 8
                            bottomPadding: 6
                            Icon { name: "users"; size: 15; tint: t.text3; anchors.verticalCenter: parent.verticalCenter }
                            Text { text: memberList.count + " members"; color: t.text3; font.pixelSize: 12; font.weight: Font.DemiBold }
                        }
                        ListView {
                            id: memberList
                            width: parent.width
                            height: contentHeight
                            interactive: false
                            model: memberModel
                            delegate: Item {
                                id: mem
                                required property string jid
                                required property string name
                                required property string sub
                                required property string avatar
                                required property string initials
                                required property string tint
                                required property string role
                                width: ListView.view.width
                                height: 58
                                Rectangle { anchors.fill: parent; anchors.leftMargin: 8; anchors.rightMargin: 8; radius: 10; color: memMa.containsMouse ? t.hover : "transparent" }
                                Avatar {
                                    id: memAv
                                    x: 20; anchors.verticalCenter: parent.verticalCenter
                                    width: 40; height: 40
                                    source: mem.avatar; initials: mem.initials; tint: mem.tint
                                }
                                Column {
                                    anchors.left: memAv.right; anchors.leftMargin: 12
                                    anchors.right: roleTag.left; anchors.rightMargin: 8
                                    anchors.verticalCenter: parent.verticalCenter
                                    Text { width: parent.width; text: mem.name; color: t.text; font.pixelSize: 14; elide: Text.ElideRight }
                                    Text { width: parent.width; text: mem.sub; color: t.text3; font.pixelSize: 12; visible: text !== ""; elide: Text.ElideRight }
                                }
                                Rectangle {
                                    id: roleTag
                                    visible: mem.role !== ""
                                    anchors.right: parent.right; anchors.rightMargin: 20
                                    anchors.verticalCenter: parent.verticalCenter
                                    width: visible ? roleText.implicitWidth + 14 : 0; height: 20; radius: 10
                                    color: t.accentSoft
                                    Text { id: roleText; anchors.centerIn: parent; text: mem.role; color: t.accent; font.pixelSize: 11; font.weight: Font.DemiBold }
                                }
                                MouseArea {
                                    id: memMa
                                    anchors.fill: parent
                                    hoverEnabled: true
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.call("open", mem.jid)
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    // ================================================================ media viewer
    Popup {
        id: viewer
        parent: Overlay.overlay
        x: 0; y: 0
        width: root.width; height: root.height
        modal: true
        dim: false
        focus: true
        padding: 0
        closePolicy: Popup.CloseOnEscape
        property string msgId: ""
        property string kind: ""     // image | video
        property string thumb: ""
        property string src: ""
        property string name: ""
        property bool failed: false
        property real zoom: 1
        readonly property bool loading: src === "" && !failed
        readonly property int pos: indexOf(msgId)

        function show(id, k, th, n) {
            msgId = id; kind = k; thumb = th; name = n
            src = ""; failed = false; resetZoom()
            if (!opened) open()
            root.call("view", id)
        }
        // opened from the info panel: arrows walk the media history
        property bool gallery: false
        function galleryData(i, role) { return mediaModel.data(mediaModel.index(i, 0), role) } // 257 id, 258 media, 259 kind
        function galleryIndex() {
            for (var i = 0; i < mediaModel.rowCount(); i++)
                if (galleryData(i, 257) === msgId) return i
            return -1
        }
        function showGallery(id, k, th) { gallery = true; show(id, k, th, "") }
        function ready(s) {
            var i = s.indexOf("\n")
            if (s.substring(0, i) !== msgId) return
            src = s.substring(i + 1)
            failed = src === ""
        }
        function resetZoom() { zoom = 1; vimg.x = 0; vimg.y = 0 }
        function zoomBy(f) {
            zoom = Math.max(1, Math.min(8, zoom * f))
            if (zoom === 1) resetZoom()
        }
        // neighbours among the loaded messages (row 0 is the newest)
        function indexOf(id) {
            for (var i = 0; i < messageModel.rowCount(); i++)
                if (root.msgData(i, "msgId") === id) return i
            return -1
        }
        function step(dir) { // +1 = older, -1 = newer
            if (gallery) {
                var g = galleryIndex() + dir
                if (g < 0 || g >= mediaModel.rowCount() || galleryIndex() < 0) return
                var gid = galleryData(g, 257), gk = galleryData(g, 259), gm = galleryData(g, 258)
                if (dir > 0 && g >= mediaModel.rowCount() - 4 && mediaModel.rowCount() < root.infoMediaTotal)
                    root.call("infoMore", "media")
                show(gid, gk === "video" ? "video" : "image", gm, "")
                return
            }
            if (pos < 0) return
            for (var i = pos + dir; i >= 0 && i < messageModel.rowCount(); i += dir) {
                var vk = root.msgData(i, "viewKind")
                if (vk !== "" && !root.msgData(i, "deleted")) {
                    var doc = root.msgData(i, "kind") === "document"
                    show(root.msgData(i, "msgId"), vk, root.msgData(i, doc ? "preview" : "media"),
                         doc ? root.msgData(i, "plain").split("\n")[0].replace(/^📄 /, "") : "")
                    return
                }
            }
        }
        function hasStep(dir) {
            if (gallery) {
                var c = galleryIndex()
                return c >= 0 && c + dir >= 0 && c + dir < mediaModel.rowCount()
            }
            if (pos < 0) return false
            for (var i = pos + dir; i >= 0 && i < messageModel.rowCount(); i += dir)
                if (root.msgData(i, "viewKind") !== "" && !root.msgData(i, "deleted")) return true
            return false
        }
        onClosed: { vplayer.stop(); src = ""; msgId = ""; gallery = false; composer.forceActiveFocus() }

        background: Rectangle { color: Qt.rgba(0.03, 0.04, 0.05, 0.96) }

        contentItem: Item {
            focus: true
            Keys.onLeftPressed: viewer.step(1)
            Keys.onRightPressed: viewer.step(-1)
            Keys.onSpacePressed: if (viewer.kind === "video") vplayer.toggle()
            Keys.onPressed: (e) => {
                if (e.key === Qt.Key_Plus || e.key === Qt.Key_Equal) { viewer.zoomBy(1.25); e.accepted = true }
                else if (e.key === Qt.Key_Minus) { viewer.zoomBy(0.8); e.accepted = true }
                else if (e.key === Qt.Key_0) { viewer.resetZoom(); e.accepted = true }
                else if (e.key === Qt.Key_C && (e.modifiers & Qt.ControlModifier) && viewer.kind === "image") { root.call("copyImage", viewer.msgId); e.accepted = true }
            }

            // ---- picture
            Item {
                id: stage
                anchors.fill: parent
                anchors.topMargin: 60
                anchors.bottomMargin: viewer.kind === "video" ? 76 : 24
                anchors.leftMargin: 64
                anchors.rightMargin: 64
                clip: true
                Image {
                    id: vimg
                    width: stage.width; height: stage.height
                    visible: viewer.kind === "image" || vplayer.playbackState === MediaPlayer.StoppedState && viewer.src === ""
                    source: viewer.kind === "image" && viewer.src !== "" ? viewer.src : viewer.thumb
                    fillMode: Image.PreserveAspectFit
                    asynchronous: true
                    autoTransform: true
                    smooth: true
                    mipmap: true
                    scale: viewer.zoom
                    Behavior on scale { NumberAnimation { duration: 90 } }
                }
                DragHandler { target: vimg; enabled: viewer.kind === "image" && viewer.zoom > 1; cursorShape: Qt.ClosedHandCursor }
                WheelHandler {
                    enabled: viewer.kind === "image"
                    onWheel: (ev) => viewer.zoomBy(ev.angleDelta.y > 0 ? 1.15 : 1 / 1.15)
                }
                TapHandler {
                    onDoubleTapped: if (viewer.kind === "image") { if (viewer.zoom > 1) viewer.resetZoom(); else viewer.zoom = 2.5 }
                    onTapped: if (viewer.kind === "video") vplayer.toggle()
                }
                // ---- video
                VideoOutput {
                    id: vout
                    anchors.fill: parent
                    visible: viewer.kind === "video" && viewer.src !== ""
                }
                MediaPlayer {
                    id: vplayer
                    videoOutput: vout
                    audioOutput: AudioOutput { id: vaudio }
                    source: viewer.kind === "video" ? viewer.src : ""
                    onSourceChanged: if (source.toString() !== "") play()
                    function toggle() { if (playbackState === MediaPlayer.PlayingState) pause(); else play() }
                }
                // nothing to show yet: big spinner; a thumbnail is up: small one in the bar
                BusyIndicator { anchors.centerIn: parent; running: viewer.loading && vimg.status !== Image.Ready; visible: running }
                Text {
                    anchors.centerIn: parent
                    visible: viewer.failed
                    text: "Couldn't load this file"
                    color: "white"; font.pixelSize: 15
                }
            }

            // ---- top bar
            Rectangle {
                anchors.left: parent.left; anchors.right: parent.right
                height: 56
                color: Qt.rgba(0, 0, 0, 0.35)
                RowLayout {
                    anchors.fill: parent
                    anchors.leftMargin: 18; anchors.rightMargin: 10
                    spacing: 4
                    Column {
                        Layout.fillWidth: true
                        spacing: 1
                        Text {
                            width: parent.width
                            text: viewer.name !== "" ? viewer.name : (viewer.kind === "video" ? "Video" : "Photo")
                            color: "white"; font.pixelSize: 15; font.weight: Font.DemiBold
                            elide: Text.ElideMiddle
                        }
                        Text {
                            text: root.currentName + (viewer.pos >= 0 ? " · " + root.msgData(viewer.pos, "time") : "")
                            color: "#b8c0c8"; font.pixelSize: 12
                        }
                    }
                    BusyIndicator {
                        running: viewer.loading && vimg.status === Image.Ready
                        visible: running
                        Layout.preferredWidth: 28; Layout.preferredHeight: 28
                        ToolTip.visible: hovered; ToolTip.text: "Loading full resolution…"
                    }
                    Text {
                        visible: viewer.kind === "image" && viewer.zoom > 1
                        text: Math.round(viewer.zoom * 100) + "%"
                        color: "#b8c0c8"; font.pixelSize: 12
                        Layout.rightMargin: 6
                    }
                    IconButton { iconName: "zoomout"; tip: "Zoom out (−)"; tint: "white"; visible: viewer.kind === "image"; enabled: viewer.zoom > 1; opacity: enabled ? 1 : 0.4; onClicked: viewer.zoomBy(0.8) }
                    IconButton { iconName: "zoomin"; tip: "Zoom in (+)"; tint: "white"; visible: viewer.kind === "image"; onClicked: viewer.zoomBy(1.25) }
                    IconButton { iconName: "copy"; tip: "Copy image (Ctrl+C)"; tint: "white"; visible: viewer.kind === "image"; onClicked: root.call("copyImage", viewer.msgId) }
                    IconButton { iconName: "appwindow"; tip: "Open with…"; tint: "white"; onClicked: root.call("openWith", viewer.msgId) }
                    IconButton { iconName: "download"; tip: root.portalOK ? "Save as…" : "Save to Downloads"; tint: "white"; onClicked: root.call("save", viewer.msgId) }
                    IconButton { iconName: "x"; tip: "Close (Esc)"; tint: "white"; onClicked: viewer.close() }
                }
            }

            // ---- previous / next
            component NavButton: AbstractButton {
                id: nb
                property string iconName
                width: 44; height: 44
                anchors.verticalCenter: parent.verticalCenter
                hoverEnabled: true
                background: Rectangle { radius: 22; color: nb.hovered ? Qt.rgba(1, 1, 1, 0.22) : Qt.rgba(1, 1, 1, 0.1) }
                contentItem: Item { Icon { anchors.centerIn: parent; name: nb.iconName; size: 24; tint: "white" } }
            }
            NavButton { x: 10; iconName: "left"; visible: viewer.hasStep(1); onClicked: viewer.step(1) }
            NavButton { x: parent.width - width - 10; iconName: "right"; visible: viewer.hasStep(-1); onClicked: viewer.step(-1) }

            // ---- video controls
            Rectangle {
                visible: viewer.kind === "video"
                anchors.left: parent.left; anchors.right: parent.right; anchors.bottom: parent.bottom
                height: 64
                color: Qt.rgba(0, 0, 0, 0.35)
                RowLayout {
                    anchors.fill: parent
                    anchors.leftMargin: 14; anchors.rightMargin: 14
                    spacing: 10
                    IconButton {
                        iconName: vplayer.playbackState === MediaPlayer.PlayingState ? "pause" : "play"
                        tint: "white"; enabled: viewer.src !== ""
                        onClicked: vplayer.toggle()
                    }
                    Text { text: root.clock(vplayer.position); color: "white"; font.pixelSize: 12; font.features: { "tnum": 1 } }
                    Slider {
                        id: seek
                        Layout.fillWidth: true
                        from: 0; to: Math.max(1, vplayer.duration)
                        value: pressed ? value : vplayer.position
                        enabled: vplayer.seekable
                        onMoved: vplayer.position = value
                        background: Rectangle {
                            x: seek.leftPadding; y: seek.topPadding + seek.availableHeight / 2 - 2
                            width: seek.availableWidth; height: 4; radius: 2
                            color: Qt.rgba(1, 1, 1, 0.25)
                            Rectangle { width: seek.visualPosition * parent.width; height: 4; radius: 2; color: t.accent }
                        }
                        handle: Rectangle {
                            x: seek.leftPadding + seek.visualPosition * (seek.availableWidth - width)
                            y: seek.topPadding + seek.availableHeight / 2 - height / 2
                            width: 14; height: 14; radius: 7
                            color: "white"
                            visible: seek.hovered || seek.pressed
                        }
                        hoverEnabled: true
                    }
                    Text { text: root.clock(vplayer.duration); color: "#b8c0c8"; font.pixelSize: 12; font.features: { "tnum": 1 } }
                    IconButton {
                        iconName: vaudio.muted ? "volumeoff" : "volume"; tint: "white"
                        tip: vaudio.muted ? "Unmute" : "Mute"
                        onClicked: vaudio.muted = !vaudio.muted
                    }
                }
            }
        }
    }

    // ================================================================ menus & dialogs
    FileDialog {
        id: fileDialog
        title: "Send files"
        fileMode: FileDialog.OpenFiles
        onAccepted: root.addAttachments(selectedFiles)
    }

    ThemedMenu {
        id: msgMenu
        property var m: null
        function openFor(m) { msgMenu.m = m; popup() }
        readonly property bool hasMedia: m !== null && m.kind !== "" && !m.deleted
        readonly property bool own: m !== null && m.fromMe && !m.deleted

        // quick reactions
        MenuItem {
            implicitHeight: 48
            hoverEnabled: false
            background: Item {}
            contentItem: Row {
                spacing: 2
                Repeater {
                    model: ["👍", "❤️", "😂", "😮", "😢", "🙏"]
                    delegate: AbstractButton {
                        required property string modelData
                        width: 32; height: 40
                        hoverEnabled: true
                        background: Rectangle { radius: 10; color: parent.hovered ? t.raisedHover : "transparent" }
                        contentItem: Text { text: modelData; font.pixelSize: 22; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                        scale: hovered ? 1.15 : 1
                        Behavior on scale { NumberAnimation { duration: 90 } }
                        onClicked: { root.call("react", msgMenu.m.msgId, modelData); msgMenu.close() }
                    }
                }
                AbstractButton {
                    width: 32; height: 40
                    hoverEnabled: true
                    background: Rectangle { radius: 10; color: parent.hovered ? t.raisedHover : "transparent" }
                    contentItem: Item { Icon { anchors.centerIn: parent; name: "smile"; size: 19; tint: t.text2 } }
                    onClicked: { var id = msgMenu.m.msgId; msgMenu.close(); emojiPicker.toggle(true, id) }
                }
            }
        }
        MenuSeparator {
            contentItem: Rectangle { implicitHeight: 1; color: t.line }
        }
        MenuEntry {
            text: "Reply"; iconName: "reply"
            enabled: msgMenu.m !== null && !msgMenu.m.deleted
            onTriggered: root.replyTo(msgMenu.m)
        }
        MenuEntry {
            text: "Forward"; iconName: "forward"
            enabled: msgMenu.m !== null && !msgMenu.m.deleted
            onTriggered: forwardPicker.openFor(msgMenu.m.msgId)
        }
        MenuEntry {
            text: "Copy image"; iconName: "image"
            visible: msgMenu.m !== null && msgMenu.m.viewKind === "image" && !msgMenu.m.deleted
            onTriggered: root.call("copyImage", msgMenu.m.msgId)
        }
        MenuEntry {
            text: "Copy text"; iconName: "copy"
            visible: msgMenu.m !== null && msgMenu.m.plain !== ""
            onTriggered: root.call("copy", msgMenu.m.plain)
        }
        MenuEntry {
            text: "Edit"; iconName: "edit"
            visible: msgMenu.own && msgMenu.m.kind === ""
            onTriggered: root.startEdit(msgMenu.m.msgId, msgMenu.m.plain)
        }
        MenuEntry {
            text: "Open"; iconName: "open"
            visible: msgMenu.hasMedia
            onTriggered: msgMenu.m.activate()
        }
        MenuEntry {
            text: "Open with…"; iconName: "appwindow"
            visible: msgMenu.hasMedia
            onTriggered: root.call("openWith", msgMenu.m.msgId)
        }
        MenuEntry {
            text: root.portalOK ? "Save as…" : "Save to Downloads"; iconName: "download"
            visible: msgMenu.hasMedia
            onTriggered: root.call("save", msgMenu.m.msgId)
        }
        MenuEntry {
            text: "Delete for everyone"; iconName: "trash"; destructive: true
            visible: msgMenu.own
            onTriggered: { deleteDialog.msgId = msgMenu.m.msgId; deleteDialog.open() }
        }
    }

    Dialog {
        id: deleteDialog
        property string msgId: ""
        anchors.centerIn: Overlay.overlay
        modal: true
        padding: 22
        background: Rectangle { radius: 14; color: t.raised; border.width: 1; border.color: t.line }
        header: Text { text: "Delete message?"; color: t.text; font.pixelSize: 17; font.weight: Font.DemiBold; padding: 22; bottomPadding: 0 }
        contentItem: Text { text: "It will be deleted for everyone in this chat."; color: t.text2; font.pixelSize: 14 }
        footer: Row {
            layoutDirection: Qt.RightToLeft
            spacing: 8
            padding: 16
            AbstractButton {
                id: delOk
                implicitWidth: 100; implicitHeight: 36
                hoverEnabled: true
                background: Rectangle { radius: 18; color: delOk.hovered ? Qt.lighter(t.danger, 1.1) : t.danger }
                contentItem: Text { text: "Delete"; color: "white"; font.weight: Font.DemiBold; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                onClicked: { root.call("delete", deleteDialog.msgId); deleteDialog.close() }
            }
            AbstractButton {
                id: delCancel
                implicitWidth: 100; implicitHeight: 36
                hoverEnabled: true
                background: Rectangle { radius: 18; color: delCancel.hovered ? t.hover : t.field }
                contentItem: Text { text: "Cancel"; color: t.text; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                onClicked: deleteDialog.close()
            }
        }
    }

    ThemedMenu {
        id: chatMenu
        property string jid: ""
        property bool pinned: false
        property bool muted: false
        property bool archived: false
        property int unread: 0
        function openFor(jid, pinned, muted, archived, unread) {
            chatMenu.jid = jid; chatMenu.pinned = pinned; chatMenu.muted = muted
            chatMenu.archived = archived; chatMenu.unread = unread
            popup()
        }
        MenuEntry {
            text: "Mark as read"; iconName: "checks"
            visible: chatMenu.unread > 0
            onTriggered: root.call("markRead", chatMenu.jid)
        }
        MenuEntry {
            text: chatMenu.pinned ? "Unpin chat" : "Pin chat"; iconName: "pin"
            onTriggered: root.call("pin", chatMenu.jid, chatMenu.pinned ? "0" : "1")
        }
        MenuEntry {
            text: "Unmute"; iconName: "mute"
            visible: chatMenu.muted
            onTriggered: root.call("mute", chatMenu.jid, "0")
        }
        MenuEntry { text: "Mute for 8 hours"; iconName: "mute"; visible: !chatMenu.muted; onTriggered: root.call("mute", chatMenu.jid, "8") }
        MenuEntry { text: "Mute for 1 week"; iconName: "mute"; visible: !chatMenu.muted; onTriggered: root.call("mute", chatMenu.jid, "168") }
        MenuEntry { text: "Mute always"; iconName: "mute"; visible: !chatMenu.muted; onTriggered: root.call("mute", chatMenu.jid, "-1") }
        MenuEntry {
            text: chatMenu.archived ? "Unarchive chat" : "Archive chat"; iconName: "archive"
            onTriggered: root.call("archive", chatMenu.jid, chatMenu.archived ? "0" : "1")
        }
        MenuEntry {
            text: "Delete chat"; iconName: "trash"; destructive: true
            onTriggered: { deleteChatDialog.jid = chatMenu.jid; deleteChatDialog.open() }
        }
    }

    Dialog {
        id: deleteChatDialog
        property string jid: ""
        anchors.centerIn: Overlay.overlay
        width: Math.min(420, root.width - 32)
        modal: true
        padding: 22
        background: Rectangle { radius: 14; color: t.raised; border.width: 1; border.color: t.line }
        header: Text { text: "Delete chat?"; color: t.text; font.pixelSize: 17; font.weight: Font.DemiBold; padding: 22; bottomPadding: 0 }
        contentItem: Text {
            wrapMode: Text.WordWrap
            text: deleteChatDialog.jid.endsWith("@g.us")
                  ? "Its messages will be removed from this app and your phone. You stay in the group, so new messages bring it back."
                  : "Its messages will be removed from this app and your phone. The other person keeps their copy."
            color: t.text2
            font.pixelSize: 14
        }
        footer: Row {
            layoutDirection: Qt.RightToLeft
            spacing: 8
            padding: 16
            AbstractButton {
                id: delChatOk
                implicitWidth: 100; implicitHeight: 36
                hoverEnabled: true
                background: Rectangle { radius: 18; color: delChatOk.hovered ? Qt.lighter(t.danger, 1.1) : t.danger }
                contentItem: Text { text: "Delete"; color: "white"; font.weight: Font.DemiBold; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                onClicked: { root.call("deleteChat", deleteChatDialog.jid); deleteChatDialog.close() }
            }
            AbstractButton {
                id: delChatCancel
                implicitWidth: 100; implicitHeight: 36
                hoverEnabled: true
                background: Rectangle { radius: 18; color: delChatCancel.hovered ? t.hover : t.field }
                contentItem: Text { text: "Cancel"; color: t.text; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                onClicked: deleteChatDialog.close()
            }
        }
    }

    // ---- forward target picker
    Popup {
        id: forwardPicker
        property string msgId: ""
        property var picked: ({})
        property int pickedCount: 0
        function openFor(id) {
            msgId = id; picked = ({}); pickedCount = 0
            pickSearch.text = ""
            root.call("pickSearch", "")
            open()
            pickSearch.forceActiveFocus()
        }
        function toggle(jid) {
            var p = picked
            if (p[jid]) delete p[jid]; else p[jid] = true
            picked = p
            pickedCount = Object.keys(p).length
        }
        anchors.centerIn: Overlay.overlay
        width: 420
        height: Math.min(root.height - 80, 600)
        modal: true
        padding: 0
        background: Rectangle { radius: 16; color: t.raised; border.width: 1; border.color: t.line }
        Overlay.modal: Rectangle { color: Qt.rgba(0, 0, 0, 0.5) }
        onClosed: composer.forceActiveFocus()

        ColumnLayout {
            anchors.fill: parent
            spacing: 0
            RowLayout {
                Layout.fillWidth: true
                Layout.preferredHeight: 60
                Layout.leftMargin: 10
                Layout.rightMargin: 16
                IconButton { iconName: "x"; onClicked: forwardPicker.close() }
                Text { Layout.fillWidth: true; text: "Forward message to"; color: t.text; font.pixelSize: 16; font.weight: Font.DemiBold }
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.leftMargin: 14
                Layout.rightMargin: 14
                Layout.preferredHeight: 38
                radius: 19
                color: t.dark ? t.bg : t.field
                Icon { id: psIcon; x: 12; anchors.verticalCenter: parent.verticalCenter; name: "search"; size: 16; tint: t.text3 }
                TextField {
                    id: pickSearch
                    anchors.left: psIcon.right
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    leftPadding: 8
                    placeholderText: "Search chats and contacts"
                    placeholderTextColor: t.text3
                    color: t.text
                    background: null
                    onTextChanged: root.call("pickSearch", text)
                    Keys.onEscapePressed: forwardPicker.close()
                    Keys.onReturnPressed: if (pickList.count > 0) forwardPicker.toggle(pickModel.data(pickModel.index(0, 0), 257))
                }
            }
            ListView {
                id: pickList
                WheelScroll { flick: pickList }
                Layout.fillWidth: true
                Layout.fillHeight: true
                Layout.topMargin: 8
                clip: true
                model: pickModel
                boundsBehavior: Flickable.StopAtBounds
                ScrollBar.vertical: ScrollBar {}
                delegate: Item {
                    id: pk
                    required property string jid
                    required property string name
                    required property string sub
                    required property string avatar
                    required property string initials
                    required property string tint
                    readonly property bool on: forwardPicker.picked[jid] === true
                    width: ListView.view.width
                    height: 58
                    Rectangle { anchors.fill: parent; anchors.leftMargin: 8; anchors.rightMargin: 8; radius: 10; color: pk.on ? t.accentSoft : (pkMa.containsMouse ? t.raisedHover : "transparent") }
                    Rectangle {
                        id: box
                        x: 22; anchors.verticalCenter: parent.verticalCenter
                        width: 20; height: 20; radius: 6
                        color: pk.on ? t.accent : "transparent"
                        border.width: pk.on ? 0 : 2
                        border.color: t.text3
                        Icon { anchors.centerIn: parent; visible: pk.on; name: "check"; size: 14; tint: t.accentFg }
                    }
                    Avatar {
                        id: pkAv
                        anchors.left: box.right; anchors.leftMargin: 14
                        anchors.verticalCenter: parent.verticalCenter
                        width: 40; height: 40
                        source: pk.avatar; initials: pk.initials; tint: pk.tint
                    }
                    Column {
                        anchors.left: pkAv.right; anchors.leftMargin: 12
                        anchors.right: parent.right; anchors.rightMargin: 20
                        anchors.verticalCenter: parent.verticalCenter
                        Text { width: parent.width; text: pk.name; color: t.text; font.pixelSize: 14; elide: Text.ElideRight }
                        Text { text: pk.sub; color: t.text3; font.pixelSize: 12 }
                    }
                    MouseArea { id: pkMa; anchors.fill: parent; hoverEnabled: true; onClicked: forwardPicker.toggle(pk.jid) }
                }
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 64
                color: "transparent"
                Rectangle { width: parent.width; height: 1; color: t.line }
                Text {
                    anchors.left: parent.left; anchors.leftMargin: 20
                    anchors.verticalCenter: parent.verticalCenter
                    text: forwardPicker.pickedCount === 0 ? "Select chats" : forwardPicker.pickedCount + " selected"
                    color: t.text2; font.pixelSize: 13
                }
                AbstractButton {
                    id: fwdSend
                    anchors.right: parent.right; anchors.rightMargin: 16
                    anchors.verticalCenter: parent.verticalCenter
                    enabled: forwardPicker.pickedCount > 0
                    implicitWidth: fwdRow.implicitWidth + 32; implicitHeight: 40
                    hoverEnabled: true
                    background: Rectangle { radius: 20; color: !fwdSend.enabled ? t.field : (fwdSend.hovered ? Qt.lighter(t.accent, 1.1) : t.accent) }
                    contentItem: Item {
                        Row {
                            id: fwdRow
                            anchors.centerIn: parent
                            spacing: 8
                            Icon { name: "send"; size: 16; tint: fwdSend.enabled ? t.accentFg : t.text3; anchors.verticalCenter: parent.verticalCenter }
                            Text { text: "Forward"; color: fwdSend.enabled ? t.accentFg : t.text3; font.weight: Font.DemiBold; anchors.verticalCenter: parent.verticalCenter }
                        }
                    }
                    onClicked: {
                        root.call("forward", forwardPicker.msgId, Object.keys(forwardPicker.picked).join(","))
                        forwardPicker.close()
                    }
                }
            }
        }
    }

    // ---- emoji picker
    Popup {
        id: emojiPicker
        property bool reactMode: false
        property string reactTo: ""
        function toggle(react, id) {
            if (visible && !react) { close(); return }
            reactMode = react; reactTo = id
            emojiSearch.text = ""
            root.call("emojiGroup", "Recent")
            tabs.current = "Recent"
            open()
            emojiSearch.forceActiveFocus()
        }
        x: 376
        y: root.height - height - 80
        width: 400
        height: 440
        padding: 10
        background: Rectangle { radius: 16; color: t.raised; border.width: 1; border.color: t.line }
        onClosed: composer.forceActiveFocus()

        readonly property var groupIcons: ({
            "Recent": "🕘", "Smileys & Emotion": "😀", "People & Body": "👋", "Animals & Nature": "🐻",
            "Food & Drink": "🍔", "Travel & Places": "✈️", "Activities": "⚽", "Objects": "💡",
            "Symbols": "❤️", "Flags": "🏁"
        })

        ColumnLayout {
            anchors.fill: parent
            spacing: 8

            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 38
                radius: 19
                color: t.dark ? t.bg : t.field
                Icon { id: esIcon; x: 12; anchors.verticalCenter: parent.verticalCenter; name: "search"; size: 16; tint: t.text3 }
                TextField {
                    id: emojiSearch
                    anchors.left: esIcon.right
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    leftPadding: 8
                    placeholderText: emojiPicker.reactMode ? "React with…" : "Search emoji"
                    placeholderTextColor: t.text3
                    color: t.text
                    background: null
                    onTextChanged: root.call("emojiSearch", text)
                    Keys.onReturnPressed: if (grid.count > 0) grid.pick(emojiModel.data(emojiModel.index(0, 0), 257))
                    Keys.onEscapePressed: emojiPicker.close()
                }
            }
            Row {
                id: tabs
                property string current: "Recent"
                visible: emojiSearch.text === ""
                Layout.alignment: Qt.AlignHCenter
                spacing: 2
                Repeater {
                    model: root.emojiGroups
                    delegate: AbstractButton {
                        required property string modelData
                        width: 36; height: 34
                        hoverEnabled: true
                        ToolTip.visible: hovered
                        ToolTip.text: modelData
                        ToolTip.delay: 500
                        background: Rectangle {
                            radius: 8
                            color: tabs.current === modelData ? t.accentSoft : (parent.hovered ? t.raisedHover : "transparent")
                            Rectangle {
                                visible: tabs.current === modelData
                                anchors.bottom: parent.bottom; anchors.horizontalCenter: parent.horizontalCenter
                                width: 16; height: 2; radius: 1; color: t.accent
                            }
                        }
                        contentItem: Text { text: emojiPicker.groupIcons[modelData] || "•"; font.pixelSize: 18; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                        onClicked: { tabs.current = modelData; root.call("emojiGroup", modelData); grid.positionViewAtBeginning() }
                    }
                }
            }
            GridView {
                id: grid
                WheelScroll { flick: grid }
                Layout.fillWidth: true
                Layout.fillHeight: true
                clip: true
                model: emojiModel
                cellWidth: Math.floor(width / 9)
                cellHeight: cellWidth
                boundsBehavior: Flickable.StopAtBounds
                ScrollBar.vertical: ScrollBar {}
                property string hoverName: ""
                function pick(e) {
                    if (emojiPicker.reactMode) {
                        root.call("react", emojiPicker.reactTo, e)
                        emojiPicker.close()
                    } else {
                        root.insertEmoji(e)
                    }
                }
                delegate: AbstractButton {
                    required property string e
                    required property string name
                    width: grid.cellWidth
                    height: grid.cellHeight
                    hoverEnabled: true
                    onHoveredChanged: if (hovered) grid.hoverName = name
                    background: Rectangle { radius: 8; color: parent.hovered ? t.raisedHover : "transparent" }
                    contentItem: Text { text: e; font.pixelSize: 25; horizontalAlignment: Text.AlignHCenter; verticalAlignment: Text.AlignVCenter }
                    onClicked: grid.pick(e)
                }
            }
            Text {
                Layout.fillWidth: true
                text: grid.hoverName !== "" ? grid.hoverName : " "
                color: t.text2
                font.pixelSize: 12
                elide: Text.ElideRight
                horizontalAlignment: Text.AlignHCenter
            }
        }
    }

    // ---- QR pairing
    Popup {
        visible: root.qrSource !== ""
        modal: true
        closePolicy: Popup.NoAutoClose
        anchors.centerIn: Overlay.overlay
        padding: 32
        background: Rectangle { radius: 20; color: t.raised; border.width: 1; border.color: t.line }
        Overlay.modal: Rectangle { color: Qt.rgba(0, 0, 0, 0.55) }
        Row {
            spacing: 36
            Column {
                spacing: 18
                width: 300
                anchors.verticalCenter: parent.verticalCenter
                Rectangle {
                    width: 56; height: 56; radius: 28; color: t.accentSoft
                    Icon { anchors.centerIn: parent; name: "chat"; size: 28; tint: t.accent }
                }
                Text { text: "Link WhatsUp to your phone"; color: t.text; font.pixelSize: 22; font.weight: Font.Bold; wrapMode: Text.Wrap; width: parent.width }
                Repeater {
                    model: ["Open WhatsApp on your phone", "Go to Settings → Linked devices", "Tap “Link a device” and scan the code"]
                    delegate: Row {
                        required property string modelData
                        required property int index
                        spacing: 12
                        Rectangle {
                            width: 24; height: 24; radius: 12; color: t.field
                            Text { anchors.centerIn: parent; text: index + 1; color: t.accent; font.pixelSize: 12; font.weight: Font.Bold }
                        }
                        Text { text: modelData; color: t.text2; font.pixelSize: 14; anchors.verticalCenter: parent.verticalCenter; width: 264; wrapMode: Text.Wrap }
                    }
                }
            }
            Rectangle {
                width: 300; height: 300; radius: 14; color: "white"
                Image { anchors.fill: parent; anchors.margins: 12; source: root.qrSource; cache: false; smooth: false }
            }
        }
    }
}
