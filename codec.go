// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

import "fmt"

const appVersion = 10 // packMP3 v1.0g file format

var pmpMagic = [2]byte{'M', 'S'}

// codec holds everything the C++ version kept in global variables.
type codec struct {
	// data storage
	firstframe, lastframe *mp3Frame
	mainData              []byte
	dataBefore            []byte
	dataAfter             []byte
	dataBeforeSize        int // may be a 0/1 flag while decoding, as in the original
	dataAfterSize         int
	unmuteData            []byte
	nBadFirst             int
	ggContext             [2][]byte

	// mp3AppendFrame state (function statics in the original)
	lastgranule [2]*granuleInfo
	appendN     int

	// pmpPredictLameAnc state (function statics in the original)
	anc ancPredictor

	// audio file info
	gNframes    int
	gNchannels  int
	gSamplerate int
	gBitrate    int

	// frame analysis info
	iMpeg, iLayer, iSamplerate, iBitrate   int
	iProtection, iPadding, iPrivbit        int
	iChannels, iStereoMS, iStereoInt       int
	iCopyright, iOriginal, iEmphasis       int
	iPadbits, iBitRes, iShare, iSblocks    int
	iMixed, iPreemphasis, iCoarse, iSbgain int
	iAuxH, iSbDiff                         int

	// error handling
	warning error // first warning (errorlevel 1)
}

func newCodec() *codec {
	c := &codec{}
	c.resetAnalysis()
	return c
}

func (c *codec) resetAnalysis() {
	for _, p := range []*int{
		&c.iMpeg, &c.iLayer, &c.iSamplerate, &c.iBitrate, &c.iProtection,
		&c.iPadding, &c.iPrivbit, &c.iChannels, &c.iStereoMS, &c.iStereoInt,
		&c.iCopyright, &c.iOriginal, &c.iEmphasis, &c.iPadbits, &c.iBitRes,
		&c.iShare, &c.iSblocks, &c.iMixed, &c.iPreemphasis, &c.iCoarse,
		&c.iSbgain, &c.iAuxH, &c.iSbDiff,
	} {
		*p = -1
	}
}

// fatal returns an error for an unrecoverable problem (errorlevel 2).
func (c *codec) fatal(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// warn records a recoverable problem (errorlevel 1). Like the original,
// processing continues, but the result may not be bit-exact.
func (c *codec) warn(format string, args ...any) {
	if c.warning == nil {
		c.warning = fmt.Errorf(format, args...)
	}
}

// readMP3 parses the MP3 frame structure (read_mp3).
func (c *codec) readMP3(mp3data []byte) error {
	size := len(mp3data)
	if size <= 0 {
		return c.fatal("empty input")
	}
	area := mp3data
	if len(area) > firstFrameArea {
		area = area[:firstFrameArea]
	}
	pos := mp3SeekFirstframe(area)
	if pos == -1 {
		return c.fatal("no mpeg audio data recognized")
	}
	mainDataBegin := pos
	if len(area)-pos >= 2 {
		typ := int(area[pos+1]>>1) & 0xF
		if typ != mpeg1LayerIII {
			return c.fatal("file is %s, not supported", filetypeDescription[typ])
		}
	}

	mpeg, layer, samples, channels := -1, -1, -1, -1
	incompleteLastFrame := false
	var main []byte
	for pos < size {
		frame, err := c.mp3ReadFrame(mp3data[pos:])
		if err != nil {
			n := 0
			if c.lastframe != nil {
				n = c.lastframe.n + 1
			}
			return fmt.Errorf("%w (frame #%d at 0x%X)", err, n, pos)
		}
		if frame == nil {
			break
		} else if frame.frameSize > size-pos {
			incompleteLastFrame = true
			break
		} else if frame.mpeg != mpeg || frame.layer != layer ||
			frame.samples != samples || frame.channels != channels {
			if mpeg == -1 {
				mpeg, layer = frame.mpeg, frame.layer
				samples, channels = frame.samples, frame.channels
			} else {
				break
			}
		}
		if c.lastframe != nil {
			lf := c.lastframe
			if frame.auxSize < 0 { // main data out of bounds
				if lf.n > maxBadFirst-2 {
					break
				}
				c.nBadFirst = lf.n + 2
			} else if lf.auxSize < frame.bitReservoir { // overlapping main data
				if lf.n > maxBadFirst-1 {
					break
				}
				c.nBadFirst = lf.n + 1
			} else if frame.n == -1 { // obvious contradiction
				if lf.n > maxBadFirst-2 {
					break
				}
				c.nBadFirst = lf.n + 2
			}
			lf.auxSize -= frame.bitReservoir
		} else if frame.auxSize < 0 || frame.n == -1 {
			c.nBadFirst = 1
		}
		frame.mainIndex = len(main) - frame.bitReservoir
		main = append(main, mp3data[pos+frame.fixedSize:pos+frame.frameSize]...)
		c.mp3AppendFrame(frame)
		if frame.mainIndex < 0 {
			c.nBadFirst = frame.n + 1
		}
		pos += frame.frameSize
	}
	c.mainData = main

	if c.lastframe == nil || c.lastframe.n+1-c.nBadFirst < 5 {
		return c.fatal("corrupted file, compression not possible")
	}
	mainDataEnd := pos
	if !incompleteLastFrame && mainDataEnd < size {
		if mainDataEnd+mp3GetID3Size(mp3data[mainDataEnd:]) != size {
			if size-mainDataEnd > garbageTolerance {
				return c.fatal("synching failure (frame #%d at 0x%X)", c.lastframe.n+1, pos)
			}
		}
	}
	c.dataAfterSize = size - mainDataEnd
	c.dataBeforeSize = mainDataBegin
	if c.dataAfterSize > 0 {
		c.dataAfter = append([]byte(nil), mp3data[mainDataEnd:]...)
	}
	if c.dataBeforeSize > 0 {
		c.dataBefore = append([]byte(nil), mp3data[:mainDataBegin]...)
	}
	return nil
}

// writeMP3 rebuilds the MP3 file from the frame structure (write_mp3).
func (c *codec) writeMP3() ([]byte, error) {
	out := make([]byte, 0, len(c.dataBefore)+len(c.mainData)+len(c.dataAfter)+c.gNframes*38)
	out = append(out, c.dataBefore[:c.dataBeforeSize]...)
	data := c.mainData
	for f := c.firstframe; f != nil; f = f.next {
		out = append(out, mp3BuildFixed(f)...)
		n := f.frameSize - f.fixedSize
		if n < 0 || n > len(data) {
			return nil, c.fatal("main data size mismatch in frame #%d", f.n)
		}
		out = append(out, data[:n]...)
		data = data[n:]
	}
	out = append(out, c.dataAfter[:c.dataAfterSize]...)
	return out, nil
}

// analyzeFrames checks frames for redundancies and errors (analyze_frames).
func (c *codec) analyzeFrames() {
	ff := c.firstframe
	c.iMpeg = ff.mpeg
	c.iLayer = ff.layer
	c.iSamplerate = ff.samples
	c.iBitrate = ff.bits
	c.iProtection = ff.protection
	c.iPadding = ff.padding
	c.iPrivbit = ff.privbit
	c.iChannels = ff.channels
	c.iStereoMS = ff.stereoMS
	c.iStereoInt = ff.stereoInt
	c.iCopyright = ff.copyright
	c.iOriginal = ff.original
	c.iEmphasis = ff.emphasis
	c.iPadbits = ff.padbits
	c.iBitRes = b2i(ff.bitReservoir > 0)
	aux0, aux1 := false, false
	if ff.auxSize > 0 {
		c.iAuxH = 1
		aux0 = true
	} else {
		c.iAuxH = 0
	}
	g := ff.granules[0][0]
	c.iShare = b2i(g.share != 0)
	c.iSblocks = b2i(g.windowSwitching != 0)
	c.iMixed = b2i(g.mixedFlag != 0)
	c.iPreemphasis = g.preemphasis
	c.iCoarse = g.coarseScalefactors
	c.iSbDiff = 0
	c.iSbgain = 0

	// header bits that often get mixed up in frame #0
	tolerant := func(iv *int, v, n int) {
		if *iv != -1 && *iv != v {
			if n == 1 {
				if c.nBadFirst == 0 {
					c.nBadFirst = 1
				}
				*iv = v
			} else {
				*iv = -1
			}
		}
	}
	strict := func(iv *int, v int) {
		if *iv != -1 && *iv != v {
			*iv = -1
		}
	}
	for f := ff; f != nil; f = f.next {
		tolerant(&c.iPrivbit, f.privbit, f.n)
		tolerant(&c.iCopyright, f.copyright, f.n)
		tolerant(&c.iOriginal, f.original, f.n)
		strict(&c.iProtection, f.protection)
		strict(&c.iBitrate, f.bits)
		strict(&c.iPadding, f.padding)
		strict(&c.iStereoMS, f.stereoMS)
		strict(&c.iStereoInt, f.stereoInt)
		strict(&c.iEmphasis, f.emphasis)
		strict(&c.iPadbits, f.padbits)
		if c.iBitRes != 1 && f.bitReservoir > 0 {
			c.iBitRes = 1
		}
		if c.iAuxH != -1 {
			if f.auxSize > 0 {
				if !aux0 {
					aux1 = true
				}
			} else {
				if aux1 {
					c.iAuxH = -1
				}
				if aux0 {
					aux0 = false
				}
			}
		}
		for ch := 0; ch < f.nchannels; ch++ {
			for gr := 0; gr < 2; gr++ {
				g := f.granules[ch][gr]
				sbxGain := g.sbGain[0] != 0 || g.sbGain[1] != 0 || g.sbGain[2] != 0
				if c.iShare != 1 && g.share != 0 {
					c.iShare = 1
				}
				if c.iSblocks != 1 && g.windowSwitching != 0 {
					c.iSblocks = 1
				}
				if c.iMixed != 1 && g.mixedFlag != 0 {
					c.iMixed = 1
				}
				strict(&c.iPreemphasis, g.preemphasis)
				strict(&c.iCoarse, g.coarseScalefactors)
				if c.iSbgain != -1 && sbxGain {
					if g.blockType == shortBlock {
						c.iSbgain = 1
					} else {
						c.iSbgain = -1
					}
				}
				if c.iSbDiff != 1 && ch == 1 && g.blockType != f.granules[0][gr].blockType {
					c.iSbDiff = 1
				}
			}
		}
	}
	if c.iAuxH != -1 && aux1 {
		c.iAuxH = 1
	}
	c.gNframes = c.lastframe.n + 1
	c.gNchannels = 2
	if c.iChannels == mp3Mono {
		c.gNchannels = 1
	}
	c.gSamplerate = mp3SamplerateTable[c.iSamplerate]
	c.gBitrate = 0
	if c.iBitrate != -1 {
		c.gBitrate = mp3BitrateTable[c.iBitrate]
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compressMP3 writes the PMP file (compress_mp3).
func (c *codec) compressMP3() ([]byte, error) {
	if c.iProtection == -1 {
		return nil, c.fatal("inconsistent use of checksums, not supported")
	}
	if c.iStereoInt == -1 {
		return nil, c.fatal("inconsistent use of int stereo, not supported")
	}
	if c.iEmphasis == -1 {
		return nil, c.fatal("inconsistent use of emphasis, not supported")
	}
	if c.iMixed != 0 {
		return nil, c.fatal("mixed blocks used, not supported")
	}
	if c.iPrivbit == -1 {
		c.warn("inconsistent private bit")
		c.iPrivbit = 0
	}
	if c.iCopyright == -1 {
		c.warn("inconsistent copyright bit")
		c.iCopyright = 0
	}
	if c.iOriginal == -1 {
		c.warn("inconsistent original bit")
		c.iOriginal = 1
	}
	if c.iPadbits != 0 {
		c.warn("non-zero padbits found")
		c.iPadbits = 0
	}

	out := []byte{pmpMagic[0], pmpMagic[1], appVersion}
	out = append(out, c.pmpHeader()...)

	if c.nBadFirst > 0 {
		f := c.firstframe
		for ; c.nBadFirst > 0; c.nBadFirst-- {
			c.mp3MuteFrame(f)
			f = f.next
		}
		out = append(out, c.unmuteData...)
	}

	enc := newEncoder(&out)
	if c.dataBeforeSize > 0 || c.dataAfterSize > 0 {
		c.pmpEncodeID3(enc)
	}
	if c.iPadding != 0 {
		c.pmpEncodePadding(enc)
	}
	if c.iSblocks != 0 {
		c.pmpEncodeBlockTypes(enc)
	}
	c.pmpEncodeGlobalGain(enc)
	c.pmpBuildContext()
	c.pmpEncodeSlength(enc)
	if err := c.pmpEncodeRegionData(enc); err != nil {
		return nil, err
	}
	if c.iShare != 0 {
		c.pmpEncodeSharing(enc)
	}
	if c.iPreemphasis != 0 {
		c.pmpEncodePreemphasis(enc)
	}
	if c.iCoarse != 0 {
		c.pmpEncodeCoarseSF(enc)
	}
	if c.iSbgain != 0 {
		c.pmpEncodeSubblockGain(enc)
	}
	if c.iStereoMS != 0 {
		c.pmpEncodeStereoMS(enc)
	}
	if err := c.pmpEncodeMainData(enc); err != nil {
		return nil, err
	}
	enc.finish()
	return out, nil
}

// uncompressPMP reads the PMP file (uncompress_pmp).
func (c *codec) uncompressPMP(pmp []byte) error {
	in := newByteReader(pmp)
	var hdr [3]byte
	in.read(hdr[:])
	if hdr[2] != appVersion {
		return c.fatal("incompatible file, use packMP3 v%d.%d", hdr[2]/10, hdr[2]%10)
	}
	if err := c.pmpReadHeader(in); err != nil {
		return err
	}
	if c.nBadFirst > 0 {
		if err := c.pmpUnstoreUnmuteData(in); err != nil {
			return err
		}
		c.nBadFirst = 0
	}

	dec := newDecoder(in)
	if c.dataBeforeSize > 0 || c.dataAfterSize > 0 {
		c.pmpDecodeID3(dec)
	}
	if c.iPadding != 0 {
		c.pmpDecodePadding(dec)
	}
	if c.iSblocks != 0 {
		c.pmpDecodeBlockTypes(dec)
	}
	c.pmpDecodeGlobalGain(dec)
	c.pmpBuildContext()
	c.pmpDecodeSlength(dec)
	c.pmpDecodeRegionData(dec)
	if c.iShare != 0 {
		c.pmpDecodeSharing(dec)
	}
	if c.iPreemphasis != 0 {
		c.pmpDecodePreemphasis(dec)
	}
	if c.iCoarse != 0 {
		c.pmpDecodeCoarseSF(dec)
	}
	if c.iSbgain != 0 {
		c.pmpDecodeSubblockGain(dec)
	}
	if c.iStereoMS != 0 {
		c.pmpDecodeStereoMS(dec)
	}
	if err := c.pmpDecodeMainData(dec); err != nil {
		return err
	}
	if len(c.unmuteData) > 0 {
		f := c.firstframe
		for ; c.nBadFirst < int(c.unmuteData[0]); c.nBadFirst++ {
			c.mp3UnmuteFrame(f)
			f = f.next
		}
	}
	return nil
}

// pmpHeader builds the 8 byte PMP header (pmp_write_header).
func (c *codec) pmpHeader() []byte {
	var h [8]byte
	h[0] |= byte(c.iSamplerate << 6)
	h[0] |= byte(c.iChannels << 4)
	if c.iBitrate != -1 {
		h[0] |= byte(c.iBitrate)
	}
	h[1] |= byte(b2i(c.iPadding != 0) << 7)
	h[1] |= byte(b2i(c.iStereoMS != 0) << 6)
	h[1] |= byte(b2i(c.iStereoInt != 0) << 5)
	h[1] |= byte(b2i(c.iSblocks != 0) << 4)
	h[1] |= byte(b2i(c.iSbgain != 0) << 3)
	h[1] |= byte(b2i(c.iShare != 0) << 2)
	h[1] |= byte(b2i(c.iPreemphasis != 0) << 1)
	h[1] |= byte(b2i(c.iCoarse != 0))
	h[2] |= byte(c.iProtection << 7)
	h[2] |= byte(c.iOriginal << 6)
	h[2] |= byte(c.iCopyright << 5)
	h[2] |= byte(c.iPrivbit << 4)
	h[2] |= byte(c.iEmphasis << 2)
	h[2] |= byte(b2i(c.dataBeforeSize > 0) << 1)
	h[2] |= byte(b2i(c.dataAfterSize > 0))
	h[3] |= byte(b2i(c.iBitRes != 0) << 7)
	h[3] |= byte(b2i(c.iSbDiff != 0) << 6)
	h[3] |= byte(b2i(c.nBadFirst > 0) << 5)
	h[4] = byte(c.gNframes >> 24)
	h[5] = byte(c.gNframes >> 16)
	h[6] = byte(c.gNframes >> 8)
	h[7] = byte(c.gNframes)
	return h[:]
}

// pmpReadHeader parses the PMP header and builds the frame chain.
func (c *codec) pmpReadHeader(in *byteReader) error {
	var h [8]byte
	in.read(h[:])
	if in.eof {
		return c.fatal("unexpected end of data")
	}
	c.iSamplerate = int(h[0]>>6) & 0x3
	c.iChannels = int(h[0]>>4) & 0x3
	c.iBitrate = int(h[0]) & 0xF
	if c.iBitrate == 0 {
		c.iBitrate = -1
	}
	c.iPadding = -(int(h[1]>>7) & 1)
	c.iStereoMS = -(int(h[1]>>6) & 1)
	c.iStereoInt = int(h[1]>>5) & 1
	c.iSblocks = int(h[1]>>4) & 1
	c.iSbgain = -(int(h[1]>>3) & 1)
	c.iShare = -(int(h[1]>>2) & 1)
	c.iPreemphasis = -(int(h[1]>>1) & 1)
	c.iCoarse = -(int(h[1]) & 1)
	c.iProtection = int(h[2]>>7) & 1
	c.iOriginal = int(h[2]>>6) & 1
	c.iCopyright = int(h[2]>>5) & 1
	c.iPrivbit = int(h[2]>>4) & 1
	c.iEmphasis = int(h[2]>>2) & 3
	c.dataBeforeSize = int(h[2]>>1) & 1
	c.dataAfterSize = int(h[2]) & 1
	c.iBitRes = -(int(h[3]>>7) & 1)
	c.iSbDiff = int(h[3]>>6) & 1
	c.nBadFirst = int(h[3]>>5) & 1
	c.iMpeg = mp3V1_0
	c.iLayer = layerIII
	c.iPadbits = 0
	c.iMixed = 0
	c.iAuxH = -2
	if c.iBitrate == 0xF || c.iSamplerate == 0x3 {
		return c.fatal("not a proper PMP file")
	}
	c.gNframes = int(int32(uint32(h[4])<<24 | uint32(h[5])<<16 | uint32(h[6])<<8 | uint32(h[7])))
	// Even digital silence costs a fraction of a bit per frame (each frame
	// codes ~15 symbols, and model counts are capped at 511), so this bound
	// only rejects absurd frame counts before allocating them.
	if c.gNframes < 0 || c.gNframes > len(in.data)*256+4096 {
		return c.fatal("not a proper PMP file")
	}
	for n := 0; n < c.gNframes; n++ {
		c.mp3AppendFrame(c.mp3BuildFrame())
	}
	c.gNchannels = 2
	if c.iChannels == mp3Mono {
		c.gNchannels = 1
	}
	c.gSamplerate = mp3SamplerateTable[c.iSamplerate]
	c.gBitrate = 0
	if c.iBitrate != -1 {
		c.gBitrate = mp3BitrateTable[c.iBitrate]
	}
	return nil
}

func (c *codec) pmpUnstoreUnmuteData(in *byteReader) error {
	n, ok := in.readByte()
	if !ok {
		n = 0
	}
	size := 1 + int(n)*(2+c.gNchannels*2*4)
	c.unmuteData = make([]byte, size)
	c.unmuteData[0] = n
	in.read(c.unmuteData[1:])
	if in.eof {
		return c.fatal("unexpected end of data")
	}
	return nil
}
