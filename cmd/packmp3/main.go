// Command packmp3 losslessly compresses MP3 files to PMP and back.
//
//	packmp3 [-p] [-ver] [-o] file...
//
// .mp3 files are compressed to .pmp, .pmp files are decompressed to .mp3
// (the type is detected from the content, not the extension).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	packmp3 "github.com/LIQRGV/go-packMP3"
)

func main() {
	proceed := flag.Bool("p", false, "proceed on warnings (output may not be bit-exact)")
	verify := flag.Bool("ver", false, "verify output after processing")
	overwrite := flag.Bool("o", false, "overwrite existing files")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: packmp3 [-p] [-ver] [-o] file...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	opt := packmp3.Options{ProceedOnWarnings: *proceed, Verify: *verify}
	failed := 0
	for _, in := range flag.Args() {
		if err := process(in, opt, *overwrite); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", in, err)
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func process(in string, opt packmp3.Options, overwrite bool) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
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
		return err
	}
	name := strings.TrimSuffix(in, filepath.Ext(in)) + ext
	if !overwrite {
		if _, err := os.Stat(name); err == nil {
			return fmt.Errorf("%s already exists (use -o to overwrite)", name)
		}
	}
	if err := os.WriteFile(name, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s -> %s (%.2f%%)\n", in, name, 100*float64(len(out))/float64(len(data)))
	return nil
}
