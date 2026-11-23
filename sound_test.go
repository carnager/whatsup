package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestChimeIsValidWAV(t *testing.T) {
	w := chimeWAV()
	if string(w[:4]) != "RIFF" || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" {
		t.Fatal("bad WAV header")
	}
	if got := binary.LittleEndian.Uint32(w[40:]); int(got) != len(w)-44 {
		t.Fatalf("data size %d, have %d bytes", got, len(w)-44)
	}
	var peak int
	for i := 44; i+1 < len(w); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(w[i:])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak < 3000 || peak > 30000 {
		t.Fatalf("peak %d is silent or clipping", peak)
	}
}

func TestMessageSoundPath(t *testing.T) {
	data, cache := t.TempDir(), t.TempDir()
	builtin := filepath.Join(cache, "message.wav")
	custom := filepath.Join(data, "message.wav")
	if got := messageSoundPath(data, cache); got != builtin {
		t.Fatalf("no custom file: got %s", got)
	}
	os.WriteFile(custom, []byte("ID3 this is not a wav file"), 0o644)
	if got := messageSoundPath(data, cache); got != builtin {
		t.Fatalf("invalid custom file: got %s", got)
	}
	os.WriteFile(custom, chimeWAV(), 0o644)
	if got := messageSoundPath(data, cache); got != custom {
		t.Fatalf("valid custom file: got %s", got)
	}
}

func TestSettingPersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	s, err := OpenMsgStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Setting("muteAll") != "" {
		t.Fatal("expected empty default")
	}
	s.SetSetting("muteAll", "1")
	s.SetSetting("muteAll", "0")
	s.SetSetting("muteAll", "1")
	s.db.Close()
	s, err = OpenMsgStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Setting("muteAll") != "1" {
		t.Fatal("setting lost on reopen")
	}
}
