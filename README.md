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

packmp3 [-p] [-ver] [-o] [-j N] [-c N] file...
```

`.mp3` input becomes `.pmp` and vice versa (detected from the content).
Several files are processed in parallel (`-j`, default: number of CPUs);
results are printed in input order. `-c N` writes the chunked format
(see below); unpacking detects it automatically.

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

### Parallel batches (`-j`)

Coding one file is inherently sequential (a single arithmetic-coded stream
whose models learn from every previous symbol), but files are independent,
so the CLI spreads them over CPUs. Same machine, median of 5 runs:

| Batch | `-j 1` pack / unpack | `-j 20` pack / unpack | Speedup |
|---|---:|---:|---:|
| 18 short clips, 248 KB | 0.087 s / 0.099 s | 0.024 s / 0.021 s | 3.6× / 4.6× |
| 10 mixed files, 7.3 MB | 2.26 s / 2.23 s | 0.85 s / 0.90 s | 2.7× / 2.5× |

The mixed batch can't go below the time of its largest file (2.7 MB,
~0.85 s on its own). Output is identical with any `-j`.

### Chunked format: one large file on many cores (`-c`, `Options.ChunkFrames`)

The standard format can't be split, so a single large file uses one core.
The optional chunked format cuts the MP3 at frame boundaries into runs of
N frames and stores each run as an independent PMP stream, so chunks are
packed and unpacked in parallel. The round trip stays bit-exact: a chunk
that starts mid-stream has frames whose data lies in the previous chunk
(MP3 bit reservoir), and packMP3's "bad first frames" handling stores those
losslessly.

The price is compression: every chunk starts with untrained models, and
its first frames are stored less efficiently. Measured with 20 cores
(size compared to the standard `.pmp`, speed to single-threaded):

| Chunk size | 2.73 MB file | 2.86 MB file | 1.50 MB file |
|---:|---|---|---|
| 500 frames | +2.15%, pack 7.9× / unpack 7.1× | +1.99%, 7.8× / 7.3× | +2.21%, 4.9× / 4.7× |
| 1000 frames | +1.29%, 4.8× / 4.6× | +1.13%, 4.3× / 4.5× | +1.37%, 3.3× / 3.3× |
| 2000 frames | +0.73%, 3.2× / 2.9× | +0.68%, 3.4× / 3.5× | +0.57%, 1.6× / 1.6× |

For scale: packMP3 saves about 12–14% on these files, so +1.3% gives up
roughly a tenth of the saving. Files shorter than one chunk are not
split. (1000 frames ≈ 26 seconds of audio.)

Caveats: the chunked format (magic `GPMP`) can only be read by this Go
package, not by the C++ packMP3; keep the default for compatibility.

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
