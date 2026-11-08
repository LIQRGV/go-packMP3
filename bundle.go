package packmp3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// Bundle format (.pmpb, Go-only, NOT readable by the C++ packMP3):
//
//	"PMPB" | version (1)
//	uvarint group count, per group: kind byte, uvarint payload size
//	uvarint file count, per file: uvarint name length, name,
//	                              uvarint size, uvarint group index
//	group payloads...
//
// Files of the same MP3 format are concatenated (in input order) into one
// stream and packed as a single standard PMP stream, so the statistical
// models learn from all of them instead of starting over for each file.
// Restoring unpacks each group and cuts it at the stored file sizes.

var bundleMagic = []byte("PMPB")

const bundleVersion = 1

const (
	groupPMP = 0 // standard PMP stream of the concatenated files
	groupRaw = 1 // stored as is (input packMP3 can't handle)
)

// BundleFile is one file in a bundle.
type BundleFile struct {
	Name string
	Data []byte
}

// IsBundle reports whether data is a .pmpb bundle.
func IsBundle(data []byte) bool { return bytes.HasPrefix(data, bundleMagic) }

type bundleGroup struct {
	kind    byte
	members []int // indexes into files
	payload []byte
}

// PackBundle packs several MP3 files into one bundle. Every file is
// restored bit for bit; files packMP3 can't compress are stored as is.
// Options.Workers limits parallelism; Verify checks the result.
func PackBundle(files []BundleFile, opt Options) (out []byte, err error) {
	defer recoverCorrupt(&err)
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.Name] {
			return nil, fmt.Errorf("packmp3: duplicate name %q in bundle", f.Name)
		}
		seen[f.Name] = true
	}

	// group files by stream format; files that can't join get their own group
	var groups []*bundleGroup
	byKey := map[string]*bundleGroup{}
	for i, f := range files {
		key, ok := joinKey(f.Data)
		if !ok {
			groups = append(groups, &bundleGroup{members: []int{i}})
			continue
		}
		g := byKey[key]
		if g == nil {
			g = &bundleGroup{}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.members = append(g.members, i)
	}

	// pack groups in parallel; split a group if it doesn't pack cleanly
	var mu sync.Mutex
	var extra []*bundleGroup
	err = parallel(len(groups), opt.Workers, func(gi int) error {
		g := groups[gi]
		if packGroup(g, files) || len(g.members) == 1 {
			return nil
		}
		// fall back to one group per file
		solo := make([]*bundleGroup, len(g.members))
		for k, m := range g.members {
			solo[k] = &bundleGroup{members: []int{m}}
			packGroup(solo[k], files)
		}
		*g = *solo[0]
		mu.Lock()
		extra = append(extra, solo[1:]...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	groups = append(groups, extra...)

	group := make([]int, len(files))
	for gi, g := range groups {
		for _, m := range g.members {
			group[m] = gi
		}
	}
	out = append([]byte(nil), bundleMagic...)
	out = append(out, bundleVersion)
	out = binary.AppendUvarint(out, uint64(len(groups)))
	for _, g := range groups {
		out = append(out, g.kind)
		out = binary.AppendUvarint(out, uint64(len(g.payload)))
	}
	out = binary.AppendUvarint(out, uint64(len(files)))
	for i, f := range files {
		out = binary.AppendUvarint(out, uint64(len(f.Name)))
		out = append(out, f.Name...)
		out = binary.AppendUvarint(out, uint64(len(f.Data)))
		out = binary.AppendUvarint(out, uint64(group[i]))
	}
	for _, g := range groups {
		out = append(out, g.payload...)
	}
	if opt.Verify {
		back, err := UnpackBundle(out, Options{Workers: opt.Workers})
		if err != nil {
			return nil, fmt.Errorf("packmp3: verification failed: %w", err)
		}
		for i := range files {
			if back[i].Name != files[i].Name || !bytes.Equal(back[i].Data, files[i].Data) {
				return nil, fmt.Errorf("packmp3: verification failed for %s", files[i].Name)
			}
		}
	}
	return out, nil
}

// joinKey returns a key for files whose frames can be joined into one
// stream: a clean MP3 (no tags or garbage around the frames, no broken
// first frames) of a given sample rate, channel mode and CRC use.
func joinKey(data []byte) (string, bool) {
	c := newCodec()
	if c.readMP3(data) != nil || c.warning != nil {
		return "", false
	}
	if c.dataBeforeSize > 0 || c.dataAfterSize > 0 || c.nBadFirst > 0 {
		return "", false
	}
	f := c.firstframe
	return fmt.Sprintf("%d/%d/%d", f.samples, f.channels, f.protection), true
}

// packGroup packs the concatenated members of g. It reports false if the
// concatenation doesn't form one clean stream; a single member that can't
// be packed is stored raw instead.
func packGroup(g *bundleGroup, files []BundleFile) bool {
	var joined []byte
	for _, m := range g.members {
		joined = append(joined, files[m].Data...)
	}
	if len(g.members) > 1 {
		// all frames must be read as one stream, nothing left as "tail"
		c := newCodec()
		if c.readMP3(joined) != nil || c.dataAfterSize > 0 || c.nBadFirst > 0 {
			return false
		}
	}
	pmp, err := Pack(joined)
	if err != nil {
		if len(g.members) > 1 {
			return false
		}
		g.kind, g.payload = groupRaw, joined
		return true
	}
	g.kind, g.payload = groupPMP, pmp
	return true
}

// UnpackBundle restores all files of a bundle, in their original order.
func UnpackBundle(data []byte, opt Options) (files []BundleFile, err error) {
	defer recoverCorrupt(&err)
	bad := errors.New("packmp3: bad bundle header")
	if !IsBundle(data) {
		return nil, errors.New("packmp3: not a bundle")
	}
	r := bytes.NewReader(data[len(bundleMagic):])
	if v, _ := r.ReadByte(); v != bundleVersion {
		return nil, fmt.Errorf("packmp3: unsupported bundle version %d", v)
	}
	limit := uint64(len(data))
	uv := func() (int, error) {
		v, err := binary.ReadUvarint(r)
		if err != nil || v > limit {
			return 0, bad
		}
		return int(v), nil
	}
	ng, err := uv()
	if err != nil {
		return nil, err
	}
	kinds := make([]byte, ng)
	sizes := make([]int, ng)
	for i := range kinds {
		if kinds[i], err = r.ReadByte(); err != nil || kinds[i] > groupRaw {
			return nil, bad
		}
		if sizes[i], err = uv(); err != nil {
			return nil, err
		}
	}
	nf, err := uv()
	if err != nil {
		return nil, err
	}
	files = make([]BundleFile, nf)
	fsize := make([]int, nf)
	fgroup := make([]int, nf)
	for i := range files {
		n, err := uv()
		if err != nil || n > r.Len() {
			return nil, bad
		}
		name := make([]byte, n)
		r.Read(name)
		files[i].Name = string(name)
		if fsize[i], err = uv(); err != nil {
			return nil, err
		}
		if fgroup[i], err = uv(); err != nil || fgroup[i] >= ng {
			return nil, bad
		}
	}
	body := data[len(data)-r.Len():]
	payloads := make([][]byte, ng)
	for i, off := 0, 0; i < ng; i++ {
		if off+sizes[i] > len(body) {
			return nil, bad
		}
		payloads[i] = body[off : off+sizes[i]]
		off += sizes[i]
	}

	streams := make([][]byte, ng)
	err = parallel(ng, opt.Workers, func(i int) error {
		if kinds[i] == groupRaw {
			streams[i] = payloads[i]
			return nil
		}
		var e error
		streams[i], e = Unpack(payloads[i])
		return e
	})
	if err != nil {
		return nil, err
	}
	pos := make([]int, ng)
	for i := range files {
		g := fgroup[i]
		if pos[g]+fsize[i] > len(streams[g]) {
			return nil, errors.New("packmp3: bundle data doesn't match its file table")
		}
		files[i].Data = streams[g][pos[g] : pos[g]+fsize[i]]
		pos[g] += fsize[i]
	}
	for g := range streams {
		if pos[g] != len(streams[g]) {
			return nil, errors.New("packmp3: bundle data doesn't match its file table")
		}
	}
	return files, nil
}
