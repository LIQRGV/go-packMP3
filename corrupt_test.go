package packmp3

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestCorruptInput feeds randomly damaged MP3 and PMP data to Pack and
// Unpack. They must return (an error or output) instead of panicking or
// hanging. Uses the files in PACKMP3_TESTDATA.
func TestCorruptInput(t *testing.T) {
	dir := os.Getenv("PACKMP3_TESTDATA")
	if dir == "" {
		t.Skip("PACKMP3_TESTDATA not set")
	}
	mp3s, _ := filepath.Glob(filepath.Join(dir, "*.mp3"))
	rnd := rand.New(rand.NewSource(1))
	n := 0
	for _, f := range mp3s {
		mp3, err := os.ReadFile(f)
		if err != nil || len(mp3) > 200_000 {
			continue
		}
		pmp, err := Pack(mp3)
		if err != nil {
			continue
		}
		cpmp, err := PackWithOptions(mp3, Options{ChunkFrames: 16})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 40; i++ {
			for _, src := range [][]byte{mp3, pmp, cpmp} {
				d := append([]byte(nil), src...)
				switch rnd.Intn(3) {
				case 0: // flip a few bytes
					for k := 0; k < 1+rnd.Intn(8); k++ {
						d[rnd.Intn(len(d))] ^= byte(1 + rnd.Intn(255))
					}
				case 1: // truncate
					d = d[:rnd.Intn(len(d))]
				case 2: // flip bytes near the start (headers)
					for k := 0; k < 1+rnd.Intn(3); k++ {
						d[rnd.Intn(min(len(d), 64))] ^= byte(1 + rnd.Intn(255))
					}
				}
				if IsPMP(d) {
					_, _ = Unpack(d)
				} else {
					_, _ = Pack(d)
				}
				n++
			}
		}
	}
	t.Logf("%d damaged inputs handled", n)
}
