package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
)

// chimeWAV synthesizes the new-message sound: two short, soft rising notes
// as 16-bit mono PCM.
func chimeWAV() []byte {
	const rate = 44100
	notes := []struct{ start, freq float64 }{{0, 880}, {0.11, 1318.5}}
	const length = 0.42
	samples := make([]int16, int(length*rate))
	for i := range samples {
		t := float64(i) / rate
		var v float64
		for _, n := range notes {
			d := t - n.start
			if d < 0 {
				continue
			}
			attack := math.Min(d/0.006, 1) // avoids a click at note onset
			env := attack * math.Exp(-d*11)
			v += env * (math.Sin(2*math.Pi*n.freq*d) + 0.18*math.Sin(4*math.Pi*n.freq*d))
		}
		fade := math.Min((length-t)/0.03, 1) // ends exactly at silence
		samples[i] = int16(v * fade * 0.3 * math.MaxInt16)
	}

	var b bytes.Buffer
	size := uint32(len(samples) * 2)
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, 36+size)
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&b, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&b, binary.LittleEndian, uint32(rate))
	binary.Write(&b, binary.LittleEndian, uint32(rate*2))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, size)
	binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

// installChime writes the sound to the cache dir for QML's SoundEffect.
func installChime(cacheDir string) string {
	p := filepath.Join(cacheDir, "message.wav")
	wav := chimeWAV()
	if old, err := os.ReadFile(p); err != nil || !bytes.Equal(old, wav) {
		os.WriteFile(p, wav, 0o644)
	}
	return p
}
