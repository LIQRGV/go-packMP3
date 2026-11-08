package packmp3

import (
	"os"
	"testing"
)

// BenchmarkPack needs PACKMP3_BENCHFILE set to an mp3 file.
func BenchmarkPack(b *testing.B) {
	f := os.Getenv("PACKMP3_BENCHFILE")
	if f == "" {
		b.Skip("PACKMP3_BENCHFILE not set")
	}
	mp3, err := os.ReadFile(f)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(mp3)))
	for i := 0; i < b.N; i++ {
		if _, err := Pack(mp3); err != nil {
			b.Fatal(err)
		}
	}
}
