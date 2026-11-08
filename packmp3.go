// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

// Package packmp3 losslessly compresses MP3 (MPEG-1 Layer III) files.
//
// It is a pure Go port of packMP3 v1.0g by Matthias Stirner. Compressed
// files use the original .pmp format, so files made by this package can be
// decompressed by the C++ packMP3 and vice versa. Unpack(Pack(x)) returns
// x bit for bit.
package packmp3

import (
	"bytes"
	"errors"
	"fmt"
)

// Options control Pack and Unpack.
type Options struct {
	// ProceedOnWarnings continues when the input has recoverable problems
	// (CRC mismatch, inconsistent private/copyright/original bits,
	// non-zero padbits). Like packMP3's -p switch, the result may then NOT
	// be bit-identical to the input. Leave it off for lossless operation.
	ProceedOnWarnings bool

	// Verify decompresses the result again and checks that it reproduces
	// the input exactly (packMP3's -ver switch).
	Verify bool

	// ChunkFrames > 0 selects the chunked format: the MP3 is split into runs
	// of this many frames (at least 16) that are compressed in parallel.
	// Much faster on multi-core machines, but slightly larger, and only
	// this Go package can read it (not the C++ packMP3). 1000 is a good
	// value: about 1.3% larger than the standard format. 0 = standard.
	ChunkFrames int

	// Workers limits the goroutines used for the chunked format
	// (<= 0: number of CPUs).
	Workers int
}

var errTruncated = errors.New("unexpected end of data")

// ErrNotPMP is returned by Unpack for data without the PMP signature.
var ErrNotPMP = errors.New("packmp3: not a PMP file")

// IsPMP reports whether data starts with the PMP signature (standard or
// chunked format).
func IsPMP(data []byte) bool {
	return IsChunked(data) || len(data) >= 2 && data[0] == pmpMagic[0] && data[1] == pmpMagic[1]
}

// Pack compresses an MP3 file. It fails on any input it cannot reproduce
// bit for bit.
func Pack(mp3 []byte) ([]byte, error) {
	return PackWithOptions(mp3, Options{})
}

// Unpack decompresses a PMP file (standard or chunked) back to the
// original MP3.
func Unpack(pmp []byte) ([]byte, error) {
	return UnpackWithOptions(pmp, Options{})
}

// PackWithOptions is Pack with options.
func PackWithOptions(mp3 []byte, opt Options) (out []byte, err error) {
	defer recoverCorrupt(&err)
	if opt.ChunkFrames > 0 {
		out, err = packChunked(mp3, opt)
		if err == nil && opt.Verify {
			err = verifyEqual(out, mp3)
		}
		return out, err
	}
	c := newCodec()
	check := func() error {
		if c.warning != nil && !opt.ProceedOnWarnings {
			return fmt.Errorf("packmp3: %w (lossless compression not possible)", c.warning)
		}
		return nil
	}
	if err := c.readMP3(mp3); err != nil {
		return nil, fmt.Errorf("packmp3: %w", err)
	}
	if err := check(); err != nil {
		return nil, err
	}
	c.analyzeFrames()
	out, err = c.compressMP3()
	if err != nil {
		return nil, fmt.Errorf("packmp3: %w", err)
	}
	if err := check(); err != nil {
		return nil, err
	}
	if opt.Verify {
		if err := verifyEqual(out, mp3); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func verifyEqual(pmp, mp3 []byte) error {
	back, err := Unpack(pmp)
	if err != nil {
		return fmt.Errorf("packmp3: verification failed: %w", err)
	}
	if !bytes.Equal(back, mp3) {
		return errors.New("packmp3: verification failed: output differs from input")
	}
	return nil
}

// UnpackWithOptions is Unpack with options.
func UnpackWithOptions(pmp []byte, opt Options) (out []byte, err error) {
	defer recoverCorrupt(&err)
	if !IsPMP(pmp) {
		return nil, ErrNotPMP
	}
	if IsChunked(pmp) {
		return unpackChunked(pmp, opt.Workers)
	}
	c := newCodec()
	if err := c.uncompressPMP(pmp); err != nil {
		return nil, fmt.Errorf("packmp3: %w", err)
	}
	out, err = c.writeMP3()
	if err != nil {
		return nil, fmt.Errorf("packmp3: %w", err)
	}
	if opt.Verify {
		again, err := PackWithOptions(out, Options{})
		if err != nil {
			return nil, fmt.Errorf("packmp3: verification failed: %w", err)
		}
		if !bytes.Equal(again, pmp) {
			return nil, errors.New("packmp3: verification failed: recompressed data differs")
		}
	}
	return out, nil
}

// recoverCorrupt turns index-out-of-range panics from malformed input into
// errors. The C++ original has no such checks and would crash instead.
func recoverCorrupt(err *error) {
	if r := recover(); r != nil {
		if r == errTruncated {
			*err = fmt.Errorf("packmp3: %w", errTruncated)
			return
		}
		if _, ok := r.(interface{ RuntimeError() }); ok {
			*err = fmt.Errorf("packmp3: corrupted or unsupported data (%v)", r)
			return
		}
		panic(r)
	}
}
