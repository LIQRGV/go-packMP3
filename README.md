# go-packMP3

A pure Go port of [packMP3](https://github.com/packjpg/packMP3) v1.0g by
Matthias Stirner: lossless compression for MP3 (MPEG-1 Layer III) files.

- **Lossless:** `Unpack(Pack(x))` returns `x` bit for bit.
- **Compatible:** it reads and writes the original `.pmp` format. Files
  packed by this port are byte-identical to the ones the C++ packMP3
  produces, and either version can unpack the other's files.
- **No cgo:** plain Go, no C/C++ toolchain needed.

Typical savings are 10–20% on top of the MP3 (packMP3 re-encodes the
Huffman-coded data with context modelling and arithmetic coding).

## Library

```go
import packmp3 "github.com/LIQRGV/go-packMP3"

pmp, err := packmp3.Pack(mp3Bytes)   // compress
mp3, err := packmp3.Unpack(pmp)      // restore the exact original

// with options (same meaning as packMP3's -p and -ver switches)
pmp, err = packmp3.PackWithOptions(mp3Bytes, packmp3.Options{Verify: true})
```

`Pack` refuses input it cannot reproduce exactly (CRC mismatches,
inconsistent header bits, non-zero padbits, MPEG-2/2.5, mixed blocks).
`Options.ProceedOnWarnings` mirrors `packMP3 -p`: it continues anyway, but the
result may then not be bit-identical.

## Command line

```
go install github.com/LIQRGV/go-packMP3/cmd/packmp3@latest

packmp3 [-p] [-ver] [-o] file...
```

`.mp3` input becomes `.pmp` and vice versa (detected from the content).

## Speed: Go vs C++

Wall time per command, median of 5 runs, one process per run (including
file I/O). Both versions are single-threaded.

| Input | Size | C++ pack | Go pack | C++ unpack | Go unpack |
|---|---:|---:|---:|---:|---:|
| Large mono file, 7109 frames | 2.73 MB | 1.06 s | 1.26 s (1.19×) | 1.32 s | 1.46 s (1.11×) |
| Medium mono file, 3584 frames | 1.50 MB | 0.67 s | 0.84 s (1.25×) | 0.56 s | 0.89 s (1.58×) |
| Small mono file, 159 frames | 99.6 KB | 0.045 s | 0.050 s (1.11×) | 0.046 s | 0.049 s (1.08×) |
| 18 short joint-stereo clips, one command | 248 KB total | 0.126 s | 0.122 s (0.97×) | 0.164 s | 0.133 s (0.81×) |

(× = Go time ÷ C++ time; below 1 means Go was faster.)

In short: on larger files the Go port is about **10–60% slower**, typically
around 20%. On small files the difference disappears; for batches of short
clips Go was even slightly faster, likely because the C++ CLI does more
per-file setup and console output.

Setup: Intel Core i7-13650HX, Linux (WSL2), Go 1.25.4, C++ packMP3 v1.0g
built with g++ 13.3 `-O2`. WSL timings are somewhat noisy; treat
differences under ~10% as noise. Most of the Go time is spent in the
arithmetic coder and the context models, as in the original.

To reproduce the Go side: `PACKMP3_BENCHFILE=file.mp3 go test -bench Pack`.

## Testing

The tests need MP3 files, which are not included in this repository:

```
# folder with *.mp3 and the *.pmp files the C++ packMP3 made from them
PACKMP3_TESTDATA=/path/to/files go test ./...
```

`TestCompat` checks that Go output equals the C++ output byte for byte,
that Go unpacks the C++ files to the exact original, and that the round
trip is exact. An `.mp3` without a matching `.pmp` is expected to be
rejected (as the C++ version did). `TestCorruptInput` feeds damaged data to
make sure bad input returns an error instead of crashing or hanging.

Verified so far against the C++ version: mono, joint stereo, plain stereo
and dual-channel files; CBR and VBR; short blocks; ID3v2, ID3v1 and APE
tags; garbage before/after the audio; files cut mid-stream (the
"bad first frames" muting path) and truncated last frames.

The tables in `tables_*.go` are generated from the C++ headers by
`tools/convtables.py`.

## Differences from the C++ version

- Library only works in memory (`[]byte` in, `[]byte` out); no global
  state, so it is safe to use from multiple goroutines.
- Malformed input returns an error where the original would crash, hang,
  or read out of bounds.
- The developer/analysis functions (`-dev` build) are not ported.

## License

GNU LGPL v3, like the original (see `LICENSE`; the LGPL builds on the GNU GPL
v3, whose text is in `COPYING`). This is a derivative work
of packMP3, Copyright 2010-2016 Ratisbon University & Matthias Stirner.
