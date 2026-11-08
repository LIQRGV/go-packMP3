// Command packmp3 losslessly compresses MP3 files to PMP and back.
//
//	packmp3 [-p] [-ver] [-o] [-j N] [-c N] file...
//
// .mp3 files are compressed to .pmp, .pmp files are decompressed to .mp3
// (the type is detected from the content, not the extension).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	packmp3 "github.com/LIQRGV/go-packMP3"
)

func main() {
	proceed := flag.Bool("p", false, "proceed on warnings (output may not be bit-exact)")
	verify := flag.Bool("ver", false, "verify output after processing")
	overwrite := flag.Bool("o", false, "overwrite existing files")
	jobs := flag.Int("j", runtime.NumCPU(), "number of files to process in parallel")
	chunk := flag.Int("c", 0, "chunked format: compress runs of N frames in parallel\n(faster, ~1% larger at N=1000, Go-only format); 0 = standard packMP3 format")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: packmp3 [-p] [-ver] [-o] [-j N] [-c N] file...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	opt := packmp3.Options{ProceedOnWarnings: *proceed, Verify: *verify, ChunkFrames: *chunk}
	files := flag.Args()

	// Each file is independent (the codec itself is sequential), so files
	// are spread over a worker pool. Results are printed in input order.
	type result struct {
		msg string
		err error
	}
	results := make([]chan result, len(files))
	for i := range results {
		results[i] = make(chan result, 1)
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < max(1, min(*jobs, len(files))); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				msg, err := process(files[i], opt, *overwrite)
				results[i] <- result{msg, err}
			}
		}()
	}
	go func() {
		for i := range files {
			next <- i
		}
		close(next)
	}()

	failed := 0
	for i, in := range files {
		r := <-results[i]
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", in, r.err)
			failed++
		} else {
			fmt.Println(r.msg)
		}
	}
	wg.Wait()
	if failed > 0 {
		os.Exit(1)
	}
}

func process(in string, opt packmp3.Options, overwrite bool) (string, error) {
	data, err := os.ReadFile(in)
	if err != nil {
		return "", err
	}
	var out []byte
	var ext string
	if packmp3.IsPMP(data) {
		out, err = packmp3.UnpackWithOptions(data, opt)
		ext = ".mp3"
	} else {
		out, err = packmp3.PackWithOptions(data, opt)
		ext = ".pmp"
	}
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(in, filepath.Ext(in)) + ext
	if !overwrite {
		if _, err := os.Stat(name); err == nil {
			return "", fmt.Errorf("%s already exists (use -o to overwrite)", name)
		}
	}
	if err := os.WriteFile(name, out, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s -> %s (%.2f%%)", in, name, 100*float64(len(out))/float64(len(data))), nil
}
