// Command packmp3 losslessly compresses MP3 files to PMP and back.
//
//	packmp3 [-p] [-ver] [-o] [-j N] [-c N] file...
//	packmp3 -b bundle.pmpb [-ver] [-o] file...
//
// .mp3 files are compressed to .pmp, .pmp files are decompressed to .mp3
// (the type is detected from the content, not the extension). With -b, all
// files go into one .pmpb bundle; giving a .pmpb extracts its files.
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
	bundle := flag.String("b", "", "pack all files into this one bundle (.pmpb) instead")
	dir := flag.String("d", ".", "directory to extract bundles into")
	chunk := flag.Int("c", 0, "chunked format: compress runs of N frames in parallel\n(faster, ~1% larger at N=1000, Go-only format); 0 = standard packMP3 format")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: packmp3 [-p] [-ver] [-o] [-j N] [-c N] file...\n")
		fmt.Fprintf(os.Stderr, "       packmp3 -b bundle.pmpb [-ver] [-o] [-j N] file...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	opt := packmp3.Options{ProceedOnWarnings: *proceed, Verify: *verify, ChunkFrames: *chunk, Workers: *jobs}
	files := flag.Args()
	if *bundle != "" {
		if err := createBundle(*bundle, files, opt, *overwrite); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", *bundle, err)
			os.Exit(1)
		}
		return
	}
	extractDir = *dir

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
	if packmp3.IsBundle(data) {
		return extractBundle(in, data, opt, overwrite)
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

var extractDir = "."

// bundleName is the name stored in a bundle: the path as given, or just the
// file name if the path is absolute or leaves the current directory.
func bundleName(path string) string {
	if filepath.IsLocal(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.Base(path)
}

func createBundle(out string, paths []string, opt packmp3.Options, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(out); err == nil {
			return fmt.Errorf("already exists (use -o to overwrite)")
		}
	}
	files := make([]packmp3.BundleFile, len(paths))
	total := 0
	for i, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[i] = packmp3.BundleFile{Name: bundleName(p), Data: data}
		total += len(data)
	}
	data, err := packmp3.PackBundle(files, opt)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%d files -> %s (%.2f%%)\n", len(files), out, 100*float64(len(data))/float64(max(total, 1)))
	return nil
}

func extractBundle(in string, data []byte, opt packmp3.Options, overwrite bool) (string, error) {
	files, err := packmp3.UnpackBundle(data, opt)
	if err != nil {
		return "", err
	}
	total := 0
	for _, f := range files {
		if !filepath.IsLocal(filepath.FromSlash(f.Name)) {
			return "", fmt.Errorf("unsafe file name %q in bundle", f.Name)
		}
		name := filepath.Join(extractDir, filepath.FromSlash(f.Name))
		if !overwrite {
			if _, err := os.Stat(name); err == nil {
				return "", fmt.Errorf("%s already exists (use -o to overwrite)", name)
			}
		}
		total += len(f.Data)
	}
	for _, f := range files {
		name := filepath.Join(extractDir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(name, f.Data, 0o644); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s -> %d files in %s (%.2f%%)", in, len(files), extractDir, 100*float64(total)/float64(len(data))), nil
}
