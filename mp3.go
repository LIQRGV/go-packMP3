// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

import "bytes"

const (
	mpeg1LayerIII = 0xD

	mp3V1_0  = 3
	layerI   = 3
	layerIII = 1

	mp3Mono         = 3
	mp3Stereo       = 0
	mp3JointStereo  = 1
	longBlock       = 0
	startBlock      = 1
	shortBlock      = 2
	stopBlock       = 3
	paddingMaxRun   = 255
	switchingMaxRun = 31
	ggContextSize   = 16
	auxDataStep     = 255
	stuffingStep    = 15
	maxBadFirst     = 255

	garbageTolerance = 64 * 1024
	firstFrameArea   = 1 * 1024 * 1024
)

// scalar pointer tables from pmp3tbl.h (not converted automatically)
var (
	mp3SamplerateTable = samplerateTable[mp3V1_0]
	mp3BitrateTable    = bitrateTable[mp3V1_0][layerIII]
)

func bitlen8192p(v int) int { return int(pbitlenP8192[v]) }

type granuleInfo struct {
	n                  int
	share              int
	mainDataBit        int
	bigValPairs        int
	globalGain         int
	slength            int
	windowSwitching    int
	preemphasis        int
	coarseScalefactors int
	selectHtabB        int
	regionTable        [3]int
	region0Size        int
	region1Size        int
	regionBound        [3]int
	svBound            int
	blockType          int
	mixedFlag          int
	sbGain             [3]int
	next, prev         *granuleInfo
}

type mp3Frame struct {
	n            int
	mpeg         int
	layer        int
	protection   int
	bits         int
	samples      int
	padding      int
	privbit      int
	channels     int
	stereoMS     int
	stereoInt    int
	copyright    int
	original     int
	emphasis     int
	bitReservoir int
	padbits      int
	granules     [][2]*granuleInfo
	frameSize    int
	fixedSize    int
	mainSize     int
	auxSize      int
	mainIndex    int
	mainBits     int
	nchannels    int
	next, prev   *mp3Frame
}

func newGranules(nch int) [][2]*granuleInfo {
	g := make([][2]*granuleInfo, nch)
	for ch := range g {
		g[ch][0] = &granuleInfo{}
		g[ch][1] = &granuleInfo{}
	}
	return g
}

// mp3ReadFrame parses one physical frame. It returns nil for "no frame
// here"; a non-nil error means a fatal problem.
func (c *codec) mp3ReadFrame(data []byte) (*mp3Frame, error) {
	if len(data) < 21 {
		return nil, nil
	}
	header := data
	if header[0] != 0xFF || header[1]&0xFA != 0xFA {
		return nil, nil
	}
	f := &mp3Frame{}
	f.mpeg = int(header[1]>>3) & 0x3
	f.layer = int(header[1]>>1) & 0x3
	f.protection = (int(header[1]) & 0x1) ^ 1
	f.bits = int(header[2]>>4) & 0xF
	f.samples = int(header[2]>>2) & 0x3
	f.padding = int(header[2]>>1) & 0x1
	f.privbit = int(header[2]) & 0x1
	f.channels = int(header[3]>>6) & 0x3
	f.stereoMS = int(header[3]>>5) & 0x1
	f.stereoInt = int(header[3]>>4) & 0x1
	f.copyright = int(header[3]>>3) & 0x1
	f.original = int(header[3]>>2) & 0x1
	f.emphasis = int(header[3]) & 0x3
	if f.bits == 0x0 || f.bits == 0xF || f.samples == 0x3 {
		return nil, nil
	}
	if f.channels == mp3Mono {
		f.nchannels = 1
	} else {
		f.nchannels = 2
	}
	nch := f.nchannels
	f.granules = newGranules(nch)
	f.frameSize = mp3FrameSizeTable[f.samples][f.bits]
	if f.padding != 0 {
		f.frameSize++
	}
	nsb := 32
	if nch == 1 {
		nsb = 17
	}
	f.fixedSize = 4 + nsb
	if f.protection != 0 {
		f.fixedSize += 2
	}
	if f.fixedSize > len(data) {
		return nil, nil
	}
	var sideinfo []byte
	if f.protection == 1 {
		sideinfo = data[6:]
		crc := uint16(data[4])<<8 + uint16(data[5])
		if crc != mp3CalcLayer3CRC(header, sideinfo, nsb) {
			c.warn("crc checksum mismatch")
		}
	} else {
		sideinfo = data[4:]
	}
	sr := newBitReader(sideinfo[:nsb])
	f.bitReservoir = int(int16(sr.read(9)))
	if nch == 1 {
		f.padbits = int(int8(sr.read(5)))
	} else {
		f.padbits = int(int8(sr.read(3)))
	}
	for ch := 0; ch < nch; ch++ {
		f.granules[ch][0].share = int(sr.read(4))
		f.granules[ch][1].share = 0
	}
	bounds := mp3BandwidthBounds[f.samples]
	for gr := 0; gr < 2; gr++ {
		for ch := 0; ch < nch; ch++ {
			g := f.granules[ch][gr]
			g.mainDataBit = int(sr.read(12))
			g.bigValPairs = int(sr.read(9))
			g.globalGain = int(sr.read(8))
			g.slength = int(sr.read(4))
			g.windowSwitching = int(sr.read(1))
			if g.windowSwitching == 0 {
				g.regionTable[0] = int(sr.read(5))
				g.regionTable[1] = int(sr.read(5))
				g.regionTable[2] = int(sr.read(5))
				g.region0Size = int(sr.read(4))
				g.region1Size = int(sr.read(3))
				if g.region0Size+g.region1Size > 20 {
					return nil, c.fatal("region size out of bounds")
				}
				g.regionBound[0] = bounds[g.region0Size+1]
				g.regionBound[1] = bounds[g.region0Size+g.region1Size+2]
				g.regionBound[2] = g.bigValPairs << 1
				if g.regionBound[0] > g.regionBound[2] {
					g.regionBound[0] = g.regionBound[2]
					g.regionBound[1] = g.regionBound[2]
				} else if g.regionBound[1] > g.regionBound[2] {
					g.regionBound[1] = g.regionBound[2]
				}
				g.blockType = longBlock
				g.mixedFlag = 0
				g.sbGain = [3]int{}
			} else {
				g.blockType = int(sr.read(2))
				g.mixedFlag = int(sr.read(1))
				g.regionTable[0] = int(sr.read(5))
				g.regionTable[1] = int(sr.read(5))
				g.sbGain[0] = int(sr.read(3))
				g.sbGain[1] = int(sr.read(3))
				g.sbGain[2] = int(sr.read(3))
				if g.blockType != shortBlock {
					g.region0Size = 8
					g.region1Size = 0
					g.regionBound[0] = bounds[8]
				} else {
					g.region0Size = 9
					g.region1Size = 0
					g.regionBound[0] = mp3BandwidthBoundsShort[f.samples][9/3] * 3
				}
				g.regionBound[1] = g.bigValPairs << 1
				if g.regionBound[0] > g.regionBound[1] {
					g.regionBound[0] = g.regionBound[1]
				}
				g.regionBound[2] = g.regionBound[1]
				g.regionTable[2] = 0
			}
			g.preemphasis = int(sr.read(1))
			g.coarseScalefactors = int(sr.read(1))
			g.selectHtabB = int(sr.read(1))
			g.svBound = 0
			if g.mainDataBit == 0 && (g.bigValPairs != 0 || g.slength != 0) {
				f.n = -1 // frame number misused as trouble indicator
			}
		}
	}
	f.mainBits = 0
	for gr := 0; gr < 2; gr++ {
		for ch := 0; ch < nch; ch++ {
			f.mainBits += f.granules[ch][gr].mainDataBit
		}
	}
	f.mainSize = (f.mainBits + 7) >> 3
	f.auxSize = f.frameSize - f.fixedSize + f.bitReservoir - f.mainSize
	return f, nil
}

// mp3BuildFrame builds an empty frame from the i_ variables.
func (c *codec) mp3BuildFrame() *mp3Frame {
	f := &mp3Frame{
		mpeg:       c.iMpeg,
		layer:      c.iLayer,
		protection: c.iProtection,
		bits:       c.iBitrate,
		samples:    c.iSamplerate,
		padding:    c.iPadding,
		privbit:    c.iPrivbit,
		channels:   c.iChannels,
		stereoMS:   c.iStereoMS,
		stereoInt:  c.iStereoInt,
		copyright:  c.iCopyright,
		original:   c.iOriginal,
		emphasis:   c.iEmphasis,
		padbits:    c.iPadbits,
	}
	if f.channels == mp3Mono {
		f.fixedSize = 4 + 17
		f.nchannels = 1
	} else {
		f.fixedSize = 4 + 32
		f.nchannels = 2
	}
	if f.protection != 0 {
		f.fixedSize += 2
	}
	f.granules = newGranules(f.nchannels)
	for ch := 0; ch < f.nchannels; ch++ {
		for gr := 0; gr < 2; gr++ {
			g := f.granules[ch][gr]
			if gr == 0 {
				g.share = c.iShare
			}
			g.windowSwitching = c.iSblocks
			g.mixedFlag = c.iMixed
			g.blockType = longBlock
			g.sbGain = [3]int{c.iSbgain, c.iSbgain, c.iSbgain}
			g.preemphasis = c.iPreemphasis
			g.coarseScalefactors = c.iCoarse
		}
	}
	return f
}

func (c *codec) mp3AppendFrame(f *mp3Frame) {
	if c.lastframe == nil {
		c.firstframe = f
		f.prev = nil
		c.lastgranule = [2]*granuleInfo{}
		c.appendN = 0
	} else {
		c.lastframe.next = f
		f.prev = c.lastframe
	}
	c.lastframe = f
	for ch := 0; ch < f.nchannels; ch++ {
		g0, g1 := f.granules[ch][0], f.granules[ch][1]
		g0.n = c.appendN << 1
		g1.n = g0.n | 1
		g0.next = g1
		g1.prev = g0
		g0.prev = c.lastgranule[ch]
		g1.next = nil
		if c.lastgranule[ch] != nil {
			c.lastgranule[ch].next = g0
		}
		c.lastgranule[ch] = g1
	}
	f.n = c.appendN
	c.appendN++
	f.next = nil
}

func (c *codec) mp3MuteFrame(f *mp3Frame) {
	nch := f.nchannels
	if len(c.unmuteData) == 0 {
		c.unmuteData = []byte{0}
	}
	c.unmuteData = append(c.unmuteData,
		byte((f.privbit&1)<<7|(f.original&1)<<6|(f.copyright&1)<<5|(f.bitReservoir>>8)&1),
		byte(f.bitReservoir&0xFF))
	for ch := 0; ch < nch; ch++ {
		for gr := 0; gr < 2; gr++ {
			g := f.granules[ch][gr]
			c.unmuteData = append(c.unmuteData,
				byte((g.mainDataBit>>4)&0xFF),
				byte(((g.mainDataBit<<4)&0xF0)|(g.slength&0x0F)),
				byte((g.bigValPairs>>1)&0xFF),
				byte((g.bigValPairs<<7)&0x80))
		}
	}
	c.unmuteData[0]++ // careful, this must not exceed 255

	if f.prev != nil && c.iBitRes != 0 {
		f.bitReservoir += f.prev.auxSize
		if f.bitReservoir >= 512 {
			f.prev.auxSize = f.bitReservoir - 511
			f.bitReservoir = 511
		} else {
			f.prev.auxSize = 0
		}
	} else {
		f.bitReservoir = 0
	}
	f.auxSize = f.frameSize - f.fixedSize + f.bitReservoir
	if f.next != nil {
		f.auxSize -= f.next.bitReservoir
	}
	f.mainSize = 0
	f.mainBits = 0
	for ch := 0; ch < nch; ch++ {
		for gr := 0; gr < 2; gr++ {
			g := f.granules[ch][gr]
			g.mainDataBit = 0
			g.bigValPairs = 0
			g.slength = 0
			g.regionBound = [3]int{}
		}
	}
}

func (c *codec) mp3UnmuteFrame(f *mp3Frame) {
	nch := c.gNchannels
	ums := 2 + nch*2*4
	p := c.unmuteData[1+c.nBadFirst*ums:]
	f.privbit = int(p[0]>>7) & 1
	f.original = int(p[0]>>6) & 1
	f.copyright = int(p[0]>>5) & 1
	// note: the flag bits end up in bit_reservoir, too (as in the original)
	f.bitReservoir = int(int16(uint16(p[0])<<8 | uint16(p[1])))
	p = p[2:]
	for ch := 0; ch < nch; ch++ {
		for gr := 0; gr < 2; gr++ {
			g := f.granules[ch][gr]
			g.mainDataBit = int(p[0])<<4 | int(p[1])>>4
			g.slength = int(p[1]) & 0x0F
			g.bigValPairs = int(p[2])<<1 | int(p[3])>>7
			p = p[4:]
		}
	}
}

// mp3BuildFixed builds the fixed part (header, crc, side info) of a frame.
func mp3BuildFixed(f *mp3Frame) []byte {
	fixed := make([]byte, 64)
	nch := f.nchannels
	nsb := 32
	if nch == 1 {
		nsb = 17
	}
	header := fixed
	sideinfo := fixed[4:]
	if f.protection != 0 {
		sideinfo = fixed[6:]
	}
	header[0] = 0xFF
	header[1] = 0xFA
	header[1] |= byte(f.mpeg << 3)
	header[1] |= byte(f.layer << 1)
	header[1] |= byte(f.protection ^ 1)
	header[2] |= byte(f.bits << 4)
	header[2] |= byte(f.samples << 2)
	header[2] |= byte(f.padding << 1)
	header[2] |= byte(f.privbit)
	header[3] |= byte(f.channels << 6)
	header[3] |= byte(f.stereoMS << 5)
	header[3] |= byte(f.stereoInt << 4)
	header[3] |= byte(f.copyright << 3)
	header[3] |= byte(f.original << 2)
	header[3] |= byte(f.emphasis)

	sw := newBitWriter(64)
	w := func(v, n int) { sw.write(uint32(v), n) }
	w(f.bitReservoir, 9)
	if nch == 1 {
		w(f.padbits, 5)
	} else {
		w(f.padbits, 3)
	}
	for ch := 0; ch < nch; ch++ {
		w(f.granules[ch][0].share, 4)
	}
	for gr := 0; gr < 2; gr++ {
		for ch := 0; ch < nch; ch++ {
			g := f.granules[ch][gr]
			w(g.mainDataBit, 12)
			w(g.bigValPairs, 9)
			w(g.globalGain, 8)
			w(g.slength, 4)
			w(g.windowSwitching, 1)
			if g.windowSwitching == 0 {
				w(g.regionTable[0], 5)
				w(g.regionTable[1], 5)
				w(g.regionTable[2], 5)
				w(g.region0Size, 4)
				w(g.region1Size, 3)
			} else {
				w(g.blockType, 2)
				w(g.mixedFlag, 1)
				w(g.regionTable[0], 5)
				w(g.regionTable[1], 5)
				w(g.sbGain[0], 3)
				w(g.sbGain[1], 3)
				w(g.sbGain[2], 3)
			}
			w(g.preemphasis, 1)
			w(g.coarseScalefactors, 1)
			w(g.selectHtabB, 1)
		}
	}
	side := sw.getptr()
	copy(sideinfo[:nsb], side)
	if f.protection == 1 {
		crc := mp3CalcLayer3CRC(header, sideinfo, nsb)
		header[4] = byte(crc >> 8)
		header[5] = byte(crc)
	}
	return fixed[:f.fixedSize]
}

// mp3SeekFirstframe finds the first of 5 consecutive consistent frames.
func mp3SeekFirstframe(data []byte) int {
	size := len(data)
	var mpeg, layer, samples, channels, protection int
	frameSize := 0
	pos0 := mp3GetID3Size(data)
	pos1 := pos0 + garbageTolerance
	if pos1 > size-4 {
		pos1 = size - 4
	}
	for {
		for ; pos0 < pos1; pos0++ {
			if data[pos0] == 0xFF && data[pos0+1]&0xE0 == 0xE0 {
				break
			}
		}
		if pos0 == pos1 {
			return -1
		}
		n := 0
		for pos := pos0; n < 5 && pos < size-4; n, pos = n+1, pos+frameSize {
			if data[pos] != 0xFF || data[pos+1]&0xE0 != 0xE0 {
				break
			}
			h1, h2, h3 := int(data[pos+1]), int(data[pos+2]), int(data[pos+3])
			if n == 0 {
				mpeg = (h1 >> 3) & 0x3
				layer = (h1 >> 1) & 0x3
				protection = h1 & 0x1
				samples = (h2 >> 2) & 0x3
				channels = (h3 >> 6) & 0x3
			} else if mpeg != (h1>>3)&0x3 || layer != (h1>>1)&0x3 ||
				protection != h1&0x1 || samples != (h2>>2)&0x3 ||
				channels != (h3>>6)&0x3 {
				break
			}
			bits := (h2 >> 4) & 0xF
			padding := (h2 >> 1) & 0x1
			if mpeg == 0x1 || layer == 0x0 || bits == 0x0 || bits == 0xF || samples == 0x3 {
				break
			}
			frameSize = frameSizeTable[mpeg][layer][samples][bits]
			if padding != 0 {
				if layer == layerI {
					frameSize += 4
				} else {
					frameSize++
				}
			}
		}
		if n >= 5 {
			break
		}
		pos0++
	}
	return pos0
}

// mp3GetID3Size returns the size of an ID3/Lyrics3/APE tag at the start
// of data, or 0 if there is none.
func mp3GetID3Size(tag []byte) int {
	maxSize := len(tag)
	if maxSize < 10 {
		return 0
	}
	var size int
	switch {
	case bytes.HasPrefix(tag, []byte("ID3")):
		size = 10
		unsync := (tag[5]>>7)&1 == 1
		if (tag[5]>>4)&1 == 1 {
			size += 10
		}
		size += int(tag[9]) + int(tag[8])<<7 + int(tag[7])<<14 + int(tag[6])<<21
		if size > maxSize {
			return 0
		}
		if unsync {
			for pos := 0; pos < size-1; pos++ {
				if tag[pos] == 0xFF && tag[pos+1] == 0x00 {
					pos++
					size++
					if size > maxSize {
						return 0
					}
				}
			}
		}
	case bytes.HasPrefix(tag, []byte("TAG")):
		size = 128
		if size > maxSize {
			return 0
		}
	case bytes.HasPrefix(tag, []byte("LYRICS")) && maxSize >= 148:
		if bytes.HasPrefix(tag, []byte("LYRICSBEGIN")) &&
			bytes.HasPrefix(tag[maxSize-128:], []byte("TAG")) &&
			bytes.HasPrefix(tag[maxSize-128-9:], []byte("LYRICS")) {
			size = maxSize
		} else {
			return 0
		}
	case bytes.HasPrefix(tag, []byte("APETAGEX")):
		size = maxSize
	default:
		return 0
	}
	return size
}

func mp3CalcLayer3CRC(header, sideinfo []byte, sidesize int) uint16 {
	crc := uint16(0xFFFF)
	crc = (crc << 8) ^ crcTable[(crc>>8)^uint16(header[2])]
	crc = (crc << 8) ^ crcTable[(crc>>8)^uint16(header[3])]
	for i := 0; i < sidesize; i++ {
		crc = (crc << 8) ^ crcTable[(crc>>8)^uint16(sideinfo[i])]
	}
	return crc
}
