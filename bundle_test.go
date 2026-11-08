package packmp3

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestBundle packs every file in PACKMP3_TESTDATA (MP3s of any kind,
// including ones packMP3 rejects) into one bundle and checks that all of
// them come back unchanged.
func TestBundle(t *testing.T) {
	dir := os.Getenv("PACKMP3_TESTDATA")
	if dir == "" {
		t.Skip("PACKMP3_TESTDATA not set")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.mp3"))
	var files []BundleFile
	total := 0
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, BundleFile{Name: filepath.Base(p), Data: data})
		total += len(data)
	}
	b, err := PackBundle(files, Options{Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnpackBundle(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(files) {
		t.Fatalf("got %d files, want %d", len(back), len(files))
	}
	for i := range files {
		if back[i].Name != files[i].Name || !bytes.Equal(back[i].Data, files[i].Data) {
			t.Errorf("%s differs after bundle round trip", files[i].Name)
		}
	}
	t.Logf("%d files, %d -> %d bytes (%.2f%%)", len(files), total, len(b), 100*float64(len(b))/float64(total))

	// damaged bundles must fail cleanly
	if len(b) > 300_000 {
		return
	}
	rnd := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		d := append([]byte(nil), b...)
		if i%2 == 0 {
			d = d[:rnd.Intn(len(d))]
		} else {
			for k := 0; k < 1+rnd.Intn(6); k++ {
				d[rnd.Intn(len(d))] ^= byte(1 + rnd.Intn(255))
			}
		}
		_, _ = UnpackBundle(d, Options{})
	}
}

func TestBundleDuplicateNames(t *testing.T) {
	_, err := PackBundle([]BundleFile{{"a", []byte("x")}, {"a", []byte("y")}}, Options{})
	if err == nil {
		t.Fatal("duplicate names accepted")
	}
}

func TestBundleNonMP3(t *testing.T) {
	files := []BundleFile{{"empty", nil}, {"text", []byte("hello, not an mp3")}}
	b, err := PackBundle(files, Options{Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnpackBundle(b, Options{})
	if err != nil || len(back) != 2 || string(back[1].Data) != "hello, not an mp3" || len(back[0].Data) != 0 {
		t.Fatalf("non-mp3 round trip failed: %v", err)
	}
}
