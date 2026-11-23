package main

import (
	"encoding/binary"
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
