package packmp3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
)

// Chunked format (Go-only, NOT readable by the C++ packMP3):
//
//	"GPMP" | version (1) | uvarint chunk count | uvarint size per chunk | chunks...
//
// Each chunk is a standard PMP stream of a run of whole frames, so chunks
// can be packed and unpacked in parallel. A chunk that starts mid-stream
// has frames whose main data lies in the previous chunk (bit reservoir);
// packMP3's "bad first frames" handling stores those losslessly.

var chunkedMagic = []byte("GPMP")

const chunkedVersion = 1

// IsChunked reports whether data is in the chunked (parallel) format.
func IsChunked(data []byte) bool { return bytes.HasPrefix(data, chunkedMagic) }

// packChunked compresses an MP3 in chunks of opt.ChunkFrames frames on up
// to opt.Workers goroutines. If any chunk can't be packed (for example a
// header-bit tolerance that only applies at the file start), the whole file
// is stored as a single chunk instead.
func packChunked(mp3 []byte, opt Options) ([]byte, error) {
	if opt.ChunkFrames < 16 {
		return nil, errors.New("packmp3: ChunkFrames must be at least 16")
	}
	single := Options{ProceedOnWarnings: opt.ProceedOnWarnings}
	bounds, err := frameChunkBounds(mp3, opt.ChunkFrames)
	if err != nil {
		return nil, err
	}
	chunks := make([][]byte, len(bounds)-1)
	err = parallel(len(chunks), opt.Workers, func(i int) error {
		var e error
		chunks[i], e = PackWithOptions(mp3[bounds[i]:bounds[i+1]], single)
		return e
	})
	if err != nil {
		whole, err := PackWithOptions(mp3, single)
		if err != nil {
			return nil, err
		}
		chunks = [][]byte{whole}
	}
	out := append([]byte(nil), chunkedMagic...)
	out = append(out, chunkedVersion)
	out = binary.AppendUvarint(out, uint64(len(chunks)))
	for _, c := range chunks {
		out = binary.AppendUvarint(out, uint64(len(c)))
	}
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out, nil
}

// unpackChunked restores an MP3 from the chunked format.
func unpackChunked(data []byte, workers int) ([]byte, error) {
	r := bytes.NewReader(data[len(chunkedMagic):])
	if v, _ := r.ReadByte(); v != chunkedVersion {
		return nil, fmt.Errorf("packmp3: unsupported chunked format version %d", v)
	}
	n, err := binary.ReadUvarint(r)
	if err != nil || n == 0 || n > uint64(len(data)) {
		return nil, errors.New("packmp3: bad chunked header")
	}
	sizes := make([]int, n)
	total := 0
	for i := range sizes {
		s, err := binary.ReadUvarint(r)
		if err != nil || s > uint64(len(data)) {
			return nil, errors.New("packmp3: bad chunked header")
		}
		sizes[i] = int(s)
		total += int(s)
	}
	body := data[len(data)-r.Len():]
	if total != len(body) {
		return nil, errors.New("packmp3: bad chunked header")
	}
	parts := make([][]byte, n)
	for i, off := 0, 0; i < len(sizes); i++ {
		parts[i] = body[off : off+sizes[i]]
		off += sizes[i]
	}
	outs := make([][]byte, n)
	err = parallel(len(parts), workers, func(i int) error {
		var e error
		outs[i], e = Unpack(parts[i])
		if e != nil {
			return fmt.Errorf("chunk %d: %w", i, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return bytes.Join(outs, nil), nil
}

// frameChunkBounds returns byte offsets that split mp3 at frame boundaries
// into runs of chunkFrames frames. The first chunk keeps any leading tag,
// the last one any trailing data. A short last chunk is merged.
func frameChunkBounds(mp3 []byte, chunkFrames int) ([]int, error) {
	c := newCodec()
	if err := c.readMP3(mp3); err != nil {
		return nil, fmt.Errorf("packmp3: %w", err)
	}
	bounds := []int{0}
	pos, n := c.dataBeforeSize, 0
	for f := c.firstframe; f != nil; f = f.next {
		if n > 0 && n%chunkFrames == 0 && c.lastframe.n-f.n >= chunkFrames/2 {
			bounds = append(bounds, pos)
		}
		pos += f.frameSize
		n++
	}
	return append(bounds, len(mp3)), nil
}

func parallel(n, workers int, fn func(i int) error) error {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	errs := make([]error, n)
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				errs[i] = fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
	return errors.Join(errs...)
}
