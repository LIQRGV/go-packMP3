// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

import "bytes"

// --- ID3 tags / other tags / garbage (generic 8 bit data) ---

func (c *codec) pmpEncodeID3(enc *aricoder) {
	m := newModelS(256+1, 256, 0)
	for _, d := range [][]byte{c.dataBefore[:c.dataBeforeSize], c.dataAfter[:c.dataAfterSize]} {
		if len(d) > 0 {
			for _, b := range d {
				enc.encodeS(m, int(b))
			}
			enc.encodeS(m, 256) // end-of-data symbol
		}
	}
}

func (c *codec) pmpDecodeID3(dec *aricoder) {
	m := newModelS(256+1, 256, 0)
	read := func() []byte {
		var buf []byte
		for {
			s := dec.decodeS(m)
			if s == 256 {
				return buf
			}
			buf = append(buf, byte(s))
		}
	}
	if c.dataBeforeSize > 0 {
		c.dataBefore = read()
		c.dataBeforeSize = len(c.dataBefore)
	}
	if c.dataAfterSize > 0 {
		c.dataAfter = read()
		c.dataAfterSize = len(c.dataAfter)
	}
}

// --- run length helpers (padding bit, window switching) ---

func encodeRun(enc *aricoder, m *modelS, run, maxRun int, shiftLast bool) {
	for run >= maxRun+1 {
		enc.encodeS(m, maxRun)
		m.shiftContext(maxRun)
		enc.encodeS(m, 0)
		m.shiftContext(0)
		run -= maxRun
	}
	enc.encodeS(m, run)
	if shiftLast {
		m.shiftContext(run)
	}
}

// --- padding bit ---

func (c *codec) pmpEncodePadding(enc *aricoder) {
	m := newModelS(paddingMaxRun+1, paddingMaxRun+1, 1)
	run, bit := 0, 0
	for f := c.firstframe; f != nil; f = f.next {
		if f.padding != bit {
			bit ^= 1
			encodeRun(enc, m, run, paddingMaxRun, true)
			run = 0
		}
		run++
	}
	if run > 0 {
		encodeRun(enc, m, run, paddingMaxRun, false)
	}
}

func (c *codec) pmpDecodePadding(dec *aricoder) {
	m := newModelS(paddingMaxRun+1, paddingMaxRun+1, 1)
	bit := 0
	run := dec.decodeS(m)
	m.shiftContext(run)
	for f := c.firstframe; f != nil; f = f.next {
		for run == 0 {
			bit ^= 1
			run = dec.decodeS(m)
			m.shiftContext(run)
		}
		run--
		f.padding = bit
	}
}

// --- block types ---

func blockTypeContext(c int, g *granuleInfo) int {
	if c == stopBlock {
		return startBlock
	}
	if g.next != nil {
		if g.next.windowSwitching != 0 {
			return shortBlock
		}
		return stopBlock
	}
	return stopBlock
}

func (c *codec) pmpEncodeBlockTypes(enc *aricoder) {
	modSw := newModelS(switchingMaxRun+1, switchingMaxRun+1, 1)
	modBt := newModelS(4, 4, 1)
	for ch := 0; ch < c.gNchannels; ch++ {
		if c.iSbDiff == 0 && ch == 1 {
			break
		}
		modSw.shiftContext(0)
		run, bit := 0, 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.windowSwitching != bit {
				bit ^= 1
				encodeRun(enc, modSw, run, switchingMaxRun, true)
				run = 0
			}
			run++
		}
		if run > 0 {
			encodeRun(enc, modSw, run, switchingMaxRun, true)
		}
		bt := stopBlock
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.windowSwitching != 0 {
				modBt.shiftContext(blockTypeContext(bt, g))
				bt = g.blockType
				enc.encodeS(modBt, bt)
			}
		}
	}
}

func (c *codec) pmpDecodeBlockTypes(dec *aricoder) {
	modSw := newModelS(switchingMaxRun+1, switchingMaxRun+1, 1)
	modBt := newModelS(4, 4, 1)
	for ch := 0; ch < c.gNchannels; ch++ {
		if c.iSbDiff == 0 && ch == 1 {
			break
		}
		modSw.shiftContext(0)
		run := dec.decodeS(modSw)
		modSw.shiftContext(run)
		bit := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			for run == 0 {
				bit ^= 1
				run = dec.decodeS(modSw)
				modSw.shiftContext(run)
			}
			run--
			g.windowSwitching = bit
		}
		bt := stopBlock
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.windowSwitching != 0 {
				modBt.shiftContext(blockTypeContext(bt, g))
				bt = dec.decodeS(modBt)
				g.blockType = bt
			} else {
				g.blockType = longBlock
			}
		}
	}
	if c.iSbDiff == 0 && c.gNchannels == 2 {
		g1 := c.firstframe.granules[1][0]
		for g0 := c.firstframe.granules[0][0]; g0 != nil; g0 = g0.next {
			g1.windowSwitching = g0.windowSwitching
			g1.blockType = g0.blockType
			g1 = g1.next
		}
	}
}

// --- global gain ---

func (c *codec) pmpEncodeGlobalGain(enc *aricoder) {
	m := newModelS(256, 0, 0)
	last := 0
	for g := c.firstframe.granules[0][0]; g != nil; g = g.next {
		enc.encodeS(m, (g.globalGain-last)&0xFF)
		last = g.globalGain
	}
	if c.gNchannels == 2 {
		g0 := c.firstframe.granules[0][0]
		for g1 := c.firstframe.granules[1][0]; g1 != nil; g1 = g1.next {
			enc.encodeS(m, (g1.globalGain-g0.globalGain)&0xFF)
			g0 = g0.next
		}
	}
}

func (c *codec) pmpDecodeGlobalGain(dec *aricoder) {
	m := newModelS(256, 0, 0)
	last := 0
	for g := c.firstframe.granules[0][0]; g != nil; g = g.next {
		last = (dec.decodeS(m) + last) & 0xFF
		g.globalGain = last
	}
	if c.gNchannels == 2 {
		g0 := c.firstframe.granules[0][0]
		for g1 := c.firstframe.granules[1][0]; g1 != nil; g1 = g1.next {
			g1.globalGain = (dec.decodeS(m) + g0.globalGain) & 0xFF
			g0 = g0.next
		}
	}
}

// --- slength ---

func (c *codec) pmpEncodeSlength(enc *aricoder) {
	m := newModelS(16, max(ggContextSize, 16), 2)
	for ch := 0; ch < c.gNchannels; ch++ {
		m.flushModel(1)
		ctx := c.ggContext[ch]
		v := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			m.shift2(int(ctx[0]), v)
			ctx = ctx[1:]
			v = g.slength
			enc.encodeS(m, v)
		}
	}
}

func (c *codec) pmpDecodeSlength(dec *aricoder) {
	m := newModelS(16, max(ggContextSize, 16), 2)
	for ch := 0; ch < c.gNchannels; ch++ {
		m.flushModel(1)
		ctx := c.ggContext[ch]
		v := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			m.shift2(int(ctx[0]), v)
			ctx = ctx[1:]
			v = dec.decodeS(m)
			g.slength = v
		}
	}
}

// --- ms stereo ---

func (c *codec) pmpEncodeStereoMS(enc *aricoder) {
	m := newModelB(16, 1)
	ctx := 0
	for f := c.firstframe; f != nil; f = f.next {
		m.shiftContext(ctx)
		enc.encodeB(m, f.stereoMS)
		ctx = ((ctx << 1) | f.stereoMS) & 0xF
	}
}

func (c *codec) pmpDecodeStereoMS(dec *aricoder) {
	m := newModelB(16, 1)
	ctx := 0
	for f := c.firstframe; f != nil; f = f.next {
		m.shiftContext(ctx)
		f.stereoMS = dec.decodeB(m)
		ctx = ((ctx << 1) | f.stereoMS) & 0xF
	}
}

// --- region bounds and huffman table selection ---

type regionModels struct {
	t0, t1, t2, s0, s1, bv *modelS
	ts                     *modelB
}

func newRegionModels() *regionModels {
	return &regionModels{
		t0: newModelS(32, max(ggContextSize, 32), 2),
		t1: newModelS(32, 32, 2),
		t2: newModelS(32, 32, 2),
		ts: newModelB(16, 1),
		s0: newModelS(16, 22, 2),
		s1: newModelS(8, 22, 2),
		bv: newModelS((576/2)+1, 1+1, 1),
	}
}

func (c *codec) pmpEncodeRegionData(enc *aricoder) error {
	bwConv := mp3BandwidthConv[c.iSamplerate]
	m := newRegionModels()
	for ch := 0; ch < c.gNchannels; ch++ {
		m.s0.flushModel(1)
		m.s1.flushModel(1)
		m.bv.flushModel(1)
		ggCtx := c.ggContext[ch]
		ctxSv, tR0, sR0 := 0, 0, 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.bigValPairs > 576/2 {
				return c.fatal("big value pairs out of bounds (%d>%d)", g.bigValPairs, 576/2)
			}
			if g.windowSwitching != 0 {
				m.bv.shiftContext(b2i(g.blockType == shortBlock))
				enc.encodeS(m.bv, g.bigValPairs)
				sR0 = 0
			} else {
				sR2 := bwConv[g.bigValPairs<<1]
				m.bv.shiftContext(0)
				enc.encodeS(m.bv, g.bigValPairs)
				m.s0.shift2(sR0, sR2)
				sR0 = g.region0Size
				enc.encodeS(m.s0, sR0)
				m.s1.shift2(sR0, sR2)
				enc.encodeS(m.s1, g.region1Size)
				sR0++
			}
			m.t0.shift2(int(ggCtx[0]), tR0)
			tR0 = g.regionTable[0]
			enc.encodeS(m.t0, tR0)
			m.t1.shift2(tR0, sR0)
			tR1 := g.regionTable[1]
			enc.encodeS(m.t1, tR1)
			if g.windowSwitching == 0 {
				m.t2.shift2(tR0, tR1)
				enc.encodeS(m.t2, g.regionTable[2])
			}
			m.ts.shiftContext(ctxSv)
			tSv := g.selectHtabB
			enc.encodeB(m.ts, tSv)
			ctxSv = ((ctxSv << 1) | tSv) & 0xF
			ggCtx = ggCtx[1:]
		}
	}
	return nil
}

func (c *codec) pmpDecodeRegionData(dec *aricoder) {
	bwConv := mp3BandwidthConv[c.iSamplerate]
	bounds := mp3BandwidthBounds[c.iSamplerate]
	m := newRegionModels()
	for ch := 0; ch < c.gNchannels; ch++ {
		m.s0.flushModel(1)
		m.s1.flushModel(1)
		m.bv.flushModel(1)
		ggCtx := c.ggContext[ch]
		ctxSv, tR0, sR0 := 0, 0, 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.windowSwitching == 0 {
				m.bv.shiftContext(0)
				g.bigValPairs = dec.decodeS(m.bv)
				sR2 := bwConv[g.bigValPairs<<1]
				m.s0.shift2(sR0, sR2)
				sR0 = dec.decodeS(m.s0)
				g.region0Size = sR0
				m.s1.shift2(sR0, sR2)
				g.region1Size = dec.decodeS(m.s1)
				g.regionBound[0] = bounds[sR0+1]
				g.regionBound[1] = bounds[sR0+g.region1Size+2]
				g.regionBound[2] = g.bigValPairs << 1
				if g.regionBound[0] > g.regionBound[2] {
					g.regionBound[0] = g.regionBound[2]
					g.regionBound[1] = g.regionBound[2]
				} else if g.regionBound[1] > g.regionBound[2] {
					g.regionBound[1] = g.regionBound[2]
				}
				sR0++
			} else {
				if g.blockType != shortBlock {
					g.region0Size = 8
					g.regionBound[0] = bounds[8]
					m.bv.shiftContext(0)
				} else {
					g.region0Size = 9
					g.regionBound[0] = mp3BandwidthBoundsShort[c.iSamplerate][9/3] * 3
					m.bv.shiftContext(1)
				}
				g.region1Size = 0
				g.bigValPairs = dec.decodeS(m.bv)
				g.regionBound[1] = g.bigValPairs << 1
				if g.regionBound[0] > g.regionBound[1] {
					g.regionBound[0] = g.regionBound[1]
				}
				g.regionBound[2] = g.regionBound[1]
				sR0 = 0
			}
			m.t0.shift2(int(ggCtx[0]), tR0)
			tR0 = dec.decodeS(m.t0)
			g.regionTable[0] = tR0
			m.t1.shift2(tR0, sR0)
			tR1 := dec.decodeS(m.t1)
			g.regionTable[1] = tR1
			if g.windowSwitching == 0 {
				m.t2.shift2(tR0, tR1)
				g.regionTable[2] = dec.decodeS(m.t2)
			} else {
				g.regionTable[2] = 0
			}
			m.ts.shiftContext(ctxSv)
			tSv := dec.decodeB(m.ts)
			g.selectHtabB = tSv
			ctxSv = ((ctxSv << 1) | tSv) & 0xF
			ggCtx = ggCtx[1:]
		}
	}
}

// --- scalefactor sharing ---

func (c *codec) pmpEncodeSharing(enc *aricoder) {
	m := newModelS(16, 16, 3)
	for ch := 0; ch < c.gNchannels; ch++ {
		m.flushModel(1)
		v := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next.next {
			m.shift3(v, g.slength, g.next.slength)
			v = g.share
			enc.encodeS(m, v)
		}
	}
}

func (c *codec) pmpDecodeSharing(dec *aricoder) {
	m := newModelS(16, 16, 3)
	for ch := 0; ch < c.gNchannels; ch++ {
		m.flushModel(1)
		v := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next.next {
			m.shift3(v, g.slength, g.next.slength)
			v = dec.decodeS(m)
			g.share = v
		}
	}
}

// --- preemphasis and coarse scalefactors (1 bit per granule) ---

func (c *codec) encodeGranuleBit(enc *aricoder, get func(*granuleInfo) int) {
	m := newModelB(16, 1)
	for ch := 0; ch < c.gNchannels; ch++ {
		m.flushModel(1)
		ctx := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			m.shiftContext(ctx)
			v := get(g)
			enc.encodeB(m, v)
			ctx = ((ctx << 1) | v) & 0xF
		}
	}
}

func (c *codec) decodeGranuleBit(dec *aricoder, set func(*granuleInfo, int)) {
	m := newModelB(16, 1)
	for ch := 0; ch < c.gNchannels; ch++ {
		m.flushModel(1)
		ctx := 0
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			m.shiftContext(ctx)
			v := dec.decodeB(m)
			set(g, v)
			ctx = ((ctx << 1) | v) & 0xF
		}
	}
}

func (c *codec) pmpEncodePreemphasis(enc *aricoder) {
	c.encodeGranuleBit(enc, func(g *granuleInfo) int { return g.preemphasis })
}

func (c *codec) pmpDecodePreemphasis(dec *aricoder) {
	c.decodeGranuleBit(dec, func(g *granuleInfo, v int) { g.preemphasis = v })
}

func (c *codec) pmpEncodeCoarseSF(enc *aricoder) {
	c.encodeGranuleBit(enc, func(g *granuleInfo) int { return g.coarseScalefactors })
}

func (c *codec) pmpDecodeCoarseSF(dec *aricoder) {
	c.decodeGranuleBit(dec, func(g *granuleInfo, v int) { g.coarseScalefactors = v })
}

// --- subblock gain ---

func (c *codec) pmpEncodeSubblockGain(enc *aricoder) {
	m := newModelS(8, 8, 1)
	m.shiftContext(0)
	for ch := 0; ch < c.gNchannels; ch++ {
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.windowSwitching != 0 {
				for sb := 0; sb < 3; sb++ {
					enc.encodeS(m, g.sbGain[sb])
					m.shiftContext(g.sbGain[sb])
				}
			}
		}
	}
}

func (c *codec) pmpDecodeSubblockGain(dec *aricoder) {
	m := newModelS(8, 8, 1)
	m.shiftContext(0)
	for ch := 0; ch < c.gNchannels; ch++ {
		for g := c.firstframe.granules[ch][0]; g != nil; g = g.next {
			if g.windowSwitching != 0 {
				for sb := 0; sb < 3; sb++ {
					g.sbGain[sb] = dec.decodeS(m)
					m.shiftContext(g.sbGain[sb])
				}
			} else {
				g.sbGain = [3]int{}
			}
		}
	}
}

// --- universal context from global gain ---

func (c *codec) pmpBuildContext() {
	ngr := c.gNframes * 2
	c.ggContext[0] = make([]byte, ngr)
	c.ggContext[1] = make([]byte, ngr)
	var count [2][256]int
	i := 0
	for g := c.firstframe.granules[0][0]; g != nil; g = g.next {
		c.ggContext[0][i] = byte(g.globalGain)
		count[0][c.ggContext[0][i]]++
		i++
	}
	for i := 1; i < 256; i++ {
		count[0][i] += count[0][i-1]
	}
	if c.gNchannels == 2 {
		i = 0
		for g := c.firstframe.granules[1][0]; g != nil; g = g.next {
			c.ggContext[1][i] = byte(g.globalGain)
			count[1][c.ggContext[1][i]]++
			i++
		}
		for i := 1; i < 256; i++ {
			count[1][i] += count[1][i-1]
		}
	}
	for ch := 0; ch < c.gNchannels; ch++ {
		lbound, inc0 := 0, 0
		for i := 0; i < 256-ggContextSize; i++ {
			inc1 := count[ch][i+ggContextSize] - count[ch][i]
			if inc1 >= inc0 {
				inc0 = inc1
				lbound = i
			}
		}
		gg := c.ggContext[ch]
		for i := range gg {
			if int(gg[i]) >= lbound {
				gg[i] -= byte(lbound)
				if gg[i] >= ggContextSize {
					gg[i] = ggContextSize - 1
				}
			} else {
				gg[i] = 0
			}
		}
	}
}

// --- LAME ancillary data predictor ---

const ancBufSize = 2048

type ancPredictor struct {
	pred       [ancBufSize]byte
	lameStr    [4 + 16]byte
	lameStrLen int
	lameBit    int
	altPred    bool
}

func (p *ancPredictor) reset() {
	p.lameBit = 0
	p.lameStrLen = 4
	copy(p.lameStr[:], "LAME")
	p.altPred = false
}

// predict builds the prediction for nbits of padding/ancillary data. With
// a reference it returns true when the prediction matched; otherwise it
// relearns from the reference. The prediction is in p.pred.
func (p *ancPredictor) predict(nbits int, ref []byte) (match bool) {
	const b01, b10, b00, b11 = 0x55, 0xAA, 0x00, 0xFF
	offset := nbits % 8
	nbytes := nbits / 8
	if !p.altPred {
		i := 0
		for ; nbits >= 8 && i < 4; nbits, i = nbits-8, i+1 {
			p.pred[i] = p.lameStr[i]
		}
		if nbits >= 32 {
			for ; nbits >= 8 && i < p.lameStrLen; nbits, i = nbits-8, i+1 {
				p.pred[i] = p.lameStr[i]
			}
		}
		for ; nbits > 0; nbits, i = nbits-8, i+1 {
			if p.lameBit == 0 {
				p.pred[i] = b01
			} else {
				p.pred[i] = b10
			}
		}
		p.lameBit ^= offset % 2
	}
	if ref == nil {
		return true
	}
	if bytes.Equal(p.pred[:nbytes], ref[:nbytes]) &&
		int(ref[nbytes]) == int(p.pred[nbytes])>>uint(8-offset) {
		return true
	}
	switch {
	case nbytes >= 8 && bytes.Equal(ref[:4], p.lameStr[:4]):
		i := 4
		for ; i < 4+16 && i < nbytes; i++ {
			if ref[i] == b01 {
				p.lameBit = 0
				break
			} else if ref[i] == b10 {
				p.lameBit = 1
				break
			}
			p.lameStr[i] = ref[i]
		}
		p.lameStrLen = i
	case nbytes == 0 && offset > 1:
		switch {
		case (b00^int(ref[0]))>>uint(8-offset) == 0:
			p.fill(b00)
		case (b11^int(ref[0]))>>uint(8-offset) == 0:
			p.fill(b11)
		default:
			p.altPred = false
		}
	case nbytes >= 1:
		switch ref[0] {
		case b00:
			p.fill(b00)
		case b11:
			p.fill(b11)
		default:
			p.altPred = false
		}
	}
	return false
}

func (p *ancPredictor) fill(b byte) {
	for i := range p.pred {
		p.pred[i] = b
	}
	p.altPred = true
}
