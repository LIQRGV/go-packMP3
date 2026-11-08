package packmp3

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompat compares against reference files made by the C++ packMP3.
// Set PACKMP3_TESTDATA to a folder with *.mp3 files and the *.pmp files the
// C++ packMP3 made from them. An mp3 without a .pmp is expected to be rejected.
// With PACKMP3_PROCEED=1 the references are expected to come from "packMP3 -p",
// and only Pack output is compared (the round trip may legitimately differ).
func TestCompat(t *testing.T) {
	dir := os.Getenv("PACKMP3_TESTDATA")
	if dir == "" {
		t.Skip("PACKMP3_TESTDATA not set")
	}
	mp3s, _ := filepath.Glob(filepath.Join(dir, "*.mp3"))
	if len(mp3s) == 0 {
		t.Fatalf("no mp3 files in %s", dir)
	}
	for _, f := range mp3s {
		t.Run(filepath.Base(f), func(t *testing.T) {
			mp3, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			ref, refErr := os.ReadFile(strings.TrimSuffix(f, ".mp3") + ".pmp")
			opt := Options{ProceedOnWarnings: os.Getenv("PACKMP3_PROCEED") == "1"}
			pmp, err := PackWithOptions(mp3, opt)
			if err != nil {
				if refErr != nil { // the C++ version rejected it, too
					t.Skipf("rejected like the original: %v", err)
				}
				t.Fatalf("Pack: %v", err)
			}
			if refErr == nil {
				if !bytes.Equal(pmp, ref) {
					t.Errorf("Pack output differs from C++ packMP3 (%d vs %d bytes)", len(pmp), len(ref))
				}
				if opt.ProceedOnWarnings {
					return
				}
				back, err := Unpack(ref)
				if err != nil {
					t.Fatalf("Unpack(C++ pmp): %v", err)
				}
				if !bytes.Equal(back, mp3) {
					t.Errorf("Unpack of C++ pmp differs from original")
				}
			}
			back, err := Unpack(pmp)
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			if !bytes.Equal(back, mp3) {
				t.Fatalf("round trip differs (%d vs %d bytes)", len(back), len(mp3))
			}
			if opt.ProceedOnWarnings {
				return
			}
			// chunked format: small chunks to force splitting
			opt.ChunkFrames = 16
			cpmp, err := PackWithOptions(mp3, opt)
			if err != nil {
				t.Fatalf("chunked Pack: %v", err)
			}
			if back, err = Unpack(cpmp); err != nil || !bytes.Equal(back, mp3) {
				t.Fatalf("chunked round trip failed: %v", err)
			}
		})
	}
}
