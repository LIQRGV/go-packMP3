// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

// Main data (scalefactors and coefficients) coding. This is a close port
// of pmp_encode_main_data / pmp_decode_main_data, including the quirks of
// the original: arrays are reused between granules and some stale values
// take part in the context modelling, so the order of every read and write
// matters for bit-exact output.
//
// The coefficient and context arrays have one guard element in front, as
// in the C++ code (abs = abs_c + 1). Index them with [i+1].

const cfLen = 578 + 1 + 1

type mainModels struct {
	scf [2][4][10]*modelS
	abv [2][8][32]*modelS
	asv [2][8][2]*modelB
	sgn [2][8]*modelB
	len [2][14]*modelS
	res *modelB
	svb *modelS
	bvf *modelS
	nst *modelS
	bst *modelB
	pad *modelB
	pap *modelB
	aux *modelS
	btr *modelS
}

func newMainModels(nch int, jCoding bool) *mainModels {
	m := &mainModels{
		svb: newModelS((576/4)+1+1, (576/4)+1+1, 1),
		bvf: newModelS((576/2)+1, 0, 0),
		nst: newModelS(stuffingStep+1, stuffingStep+1, 1),
		bst: newModelB(16, 1),
		pad: newModelB(256, 1),
		pap: newModelB(0, 0),
		aux: newModelS(auxDataStep+1, auxDataStep+1, 1),
		btr: newModelS(16, 16, 1),
		res: newModelB(13+1, 2),
	}
	for ch := 0; ch < nch; ch++ {
		for g := 0; g < 10; g++ {
			m.scf[ch][0][g] = newModelS(2, 16, 2)
			m.scf[ch][1][g] = newModelS(4, 16, 2)
			m.scf[ch][2][g] = newModelS(8, 16, 2)
			m.scf[ch][3][g] = newModelS(16, 16, 2)
		}
		nflags := 0x2
		if jCoding && ch != 0 {
			nflags = 0x8
		}
		for flags := 0; flags < nflags; flags++ {
			a := &m.abv[ch][flags]
			for t, maxv := range map[int]int{1: 1, 2: 2, 3: 2, 5: 3, 6: 3, 7: 5, 8: 5, 9: 5,
				10: 7, 11: 7, 12: 7, 13: 15, 15: 15} {
				a[t] = newModelS(maxv+1, 16, 2)
			}
			a[16] = newModelS(15+1, 16, 2)
			for t := 17; t <= 23; t++ {
				a[t] = a[16]
			}
			a[24] = newModelS(15+1, 16, 2)
			for t := 25; t <= 31; t++ {
				a[t] = a[24]
			}
			m.asv[ch][flags][0] = newModelB(16, 2)
			m.asv[ch][flags][1] = newModelB(16, 2)
			m.sgn[ch][flags] = newModelB(16, 2)
		}
		for i := 1; i <= 13; i++ {
			m.len[ch][i] = newModelS(i+1, 13+1, 1)
		}
	}
	return m
}

type mainBuffers struct {
	scfC, scfLLong, scfLShort [2][]byte
	absC, sgnC, lenC          [2][]byte
	lbtC                      [2][]uint16
	abslCtx, abssCtx          [2][]byte
	sgnlCtx, sgnsCtx          [2][]byte
	lenlCtx, lensCtx          [2][]byte
}

func newMainBuffers(nch int) *mainBuffers {
	b := &mainBuffers{}
	for ch := 0; ch < nch; ch++ {
		b.scfC[ch] = make([]byte, 21)
		b.scfLLong[ch] = make([]byte, 21)
		b.scfLShort[ch] = make([]byte, 21)
		b.absC[ch] = make([]byte, cfLen)
		b.sgnC[ch] = make([]byte, cfLen)
		b.lenC[ch] = make([]byte, cfLen)
		b.lbtC[ch] = make([]uint16, 576)
		b.abslCtx[ch] = make([]byte, cfLen)
		b.abssCtx[ch] = make([]byte, cfLen)
		b.sgnlCtx[ch] = make([]byte, cfLen)
		b.sgnsCtx[ch] = make([]byte, cfLen)
		b.lenlCtx[ch] = make([]byte, cfLen)
		b.lensCtx[ch] = make([]byte, cfLen)
	}
	return b
}

// ctxArrays returns the (abs, sgn, len) history contexts for a channel.
func (b *mainBuffers) ctxArrays(ch int, short bool) ([]byte, []byte, []byte) {
	if !short {
		return b.abslCtx[ch], b.sgnlCtx[ch], b.lenlCtx[ch]
	}
	return b.abssCtx[ch], b.sgnsCtx[ch], b.lensCtx[ch]
}

// updateContext updates the neighbourhood contexts after a granule
// (identical in encoder and decoder). p is the end of coded coefficients.
func (b *mainBuffers) updateContext(ch, p int, sbl, jCoding bool) {
	abs, sgn, ln := b.absC[ch], b.sgnC[ch], b.lenC[ch]
	if p < 578 {
		clear(abs[p+1 : 578+1])
	}
	if jCoding && ch != 0 {
		return
	}
	hAbs, hSgn, hLen := b.ctxArrays(ch, sbl)
	for i := 578 - 1; i >= 0; i-- {
		k := i + 1
		hAbs[k] = byte((3*int(abs[k]) + int(abs[k-1]) + int(abs[k+1]) + 2*int(hAbs[k]) + 4) / 7)
		if abs[k] > 0 {
			hSgn[k] = ((hSgn[k] << 1) | (sgn[k] & 0x1)) & 0xF
			if abs[k] == 15 {
				hLen[k] = byte((2*int(ln[k]) + int(hLen[k]) + 2) / 3)
			} else {
				hLen[k] >>= 1
			}
		} else {
			hSgn[k] = (hSgn[k] << 1) & 0xF
			hLen[k] >>= 1
		}
	}
	if !jCoding {
		return
	}
	// channel 1 context is predicted from channel 0
	hAbs, hSgn, hLen = b.ctxArrays(1, sbl)
	if p < 578 {
		for k := p + 1; k < 578+1; k++ {
			hAbs[k], hSgn[k], hLen[k] = 0, 2, 0
		}
	}
	for i := p - 1; i >= 0; i-- {
		k := i + 1
		hAbs[k] = abs[k]
		if abs[k] > 0 {
			hSgn[k] = sgn[k]
			if abs[k] == 15 {
				hLen[k] = ln[k]
			} else {
				hLen[k] = 0
			}
		} else {
			hSgn[k] = 2
			hLen[k] = 0
		}
	}
}

func compressionFlags(prev, sbl, stereoMS, ch int, jCoding bool) int {
	if !jCoding || ch == 0 {
		return sbl
	}
	return sbl | (stereoMS << 1) | ((prev ^ sbl) << 2)
}

func (c *codec) pmpEncodeMainData(enc *aricoder) error {
	jCoding := c.iChannels == mp3Stereo || c.iChannels == mp3JointStereo
	huff := newHuffmanReader(c.mainData)
	c.anc.reset()
	m := newMainModels(c.gNchannels, jCoding)
	b := newMainBuffers(c.gNchannels)
	bitratePred := mp3BitratePred[c.iSamplerate]
	var padAndAux [ancBufSize]byte

	var shared [2][4]int
	var ctxSvb [2]int
	ctxNst, ctxBst, ctxAux := 0, 0, 0
	flags := 0

	for frame := c.firstframe; frame != nil; frame = frame.next {
		bitp := 0
		for gr := 0; gr < 2; gr++ {
			for ch := 0; ch < c.gNchannels; ch++ {
				g := frame.granules[ch][gr]
				sbl := b2i(g.blockType == shortBlock)
				lmaxp := g.mainDataBit
				slen := slengthTable[g.slength]
				abs, sgn, ln, lbt := b.absC[ch], b.sgnC[ch], b.lenC[ch], b.lbtC[ch]
				flags = compressionFlags(flags, sbl, frame.stereoMS, ch, jCoding)
				if gr == 0 {
					for k := 0; k < 4; k++ {
						shared[ch][k] = (g.share >> uint(3-k)) & 0x1
					}
				}
				huff.resetCounter()
				bitc := 0
				badCoding := false

				// --- scalefactors ---
				if sbl == 0 {
					scf, prev := b.scfC[ch], b.scfLLong[ch]
					ctxScf := 0
					for grp, p := 0, 0; grp < 4; grp++ {
						sl := slen[b2i(grp >= 2)]
						if gr&shared[ch][grp] != 0 {
							copy(scf[p:p+scfWidth[grp]], prev[p:])
							p = scfBounds[grp]
							ctxScf = int(scf[p-1])
						} else if sl == 0 {
							clear(scf[p : p+scfWidth[grp]])
							p = scfBounds[grp]
							ctxScf = 0
						} else {
							idx := grp
							if shared[ch][grp] != 0 {
								idx |= 4
							}
							mod := m.scf[ch][sl-1][idx]
							for ; p < scfBounds[grp]; p++ {
								scf[p] = byte(huff.readBits(sl))
								mod.shift2(ctxScf, int(prev[p]))
								enc.encodeS(mod, int(scf[p]))
								ctxScf = int(scf[p])
							}
						}
					}
					b.scfC[ch], b.scfLLong[ch] = b.scfLLong[ch], b.scfC[ch]
				} else {
					for i := 0; i < 3; i++ {
						scf, prev := b.scfC[ch], b.scfLShort[ch]
						ctxScf := 0
						for grp, p := 0, 0; grp < 2; grp++ {
							sl := slen[grp]
							if sl == 0 {
								clear(scf[p : p+scfLhWidthShort[grp]])
								p = scfLhBoundsShort[grp]
							} else {
								mod := m.scf[ch][sl-1][grp|0x8]
								for ; p < scfLhBoundsShort[grp]; p++ {
									scf[p] = byte(huff.readBits(sl))
									mod.shift2(ctxScf, int(prev[p]))
									enc.encodeS(mod, int(scf[p]))
									ctxScf = int(scf[p])
								}
							}
						}
						b.scfC[ch], b.scfLShort[ch] = b.scfLShort[ch], b.scfC[ch]
					}
				}
				if huff.getCount() > lmaxp {
					return c.fatal("huffman decoding error (in frame #%d)", frame.n)
				}

				// --- decode big values ---
				p := 0
				for r := 0; r < 3 && !badCoding; r++ {
					if g.regionTable[r] == 0 {
						p = g.regionBound[r]
						continue
					}
					bv := &bvDecTable[g.regionTable[r]]
					if bv.h == nil {
						return c.fatal("bad huffman table (%d) used (in frame #%d)", g.regionTable[r], frame.n)
					}
					for ; p < g.regionBound[r]; bitc = huff.getCount() {
						huff.decodePair(bv.h, abs[p+1:])
						for i := 0; i < 2; i, p = i+1, p+1 {
							if abs[p+1] > 0 {
								if bv.linbits > 0 && abs[p+1] == 15 {
									lbt[p] = uint16(huff.readBits(bv.linbits))
									ln[p+1] = byte(bitlen8192p(int(lbt[p])))
								}
								sgn[p+1] = byte(huff.readBit())
							}
						}
						if huff.getCount() > lmaxp { // bad coding rollback
							badCoding = true
							for k := 0; k < 2; k++ {
								p--
								abs[p+1], sgn[p+1], ln[p+1] = 0, 0, 0
							}
							huff.rewindBits(huff.getCount() - bitc)
							break
						}
					}
				}
				// --- decode small values ---
				if !badCoding {
					conv := htabADec
					if g.selectHtabB != 0 {
						conv = htabBDec
					}
					for bitc = huff.getCount(); p < 576; bitc = huff.getCount() {
						if bitc == lmaxp {
							break
						}
						huff.decodeQuadruple(conv, abs[p+1:])
						for i := 0; i < 4; i, p = i+1, p+1 {
							if abs[p+1] > 0 {
								sgn[p+1] = byte(huff.readBit())
							}
						}
						if huff.getCount() > lmaxp {
							badCoding = true
							for k := 0; k < 4; k++ {
								p--
								abs[p+1], sgn[p+1] = 0, 0
							}
							huff.rewindBits(huff.getCount() - bitc)
							break
						}
					}
				}

				// --- sv bound ---
				g.svBound = p
				if sbl == 0 {
					m.svb.shiftContext(ctxSvb[ch])
				} else {
					m.svb.shiftContext(144 + 1)
				}
				if p >= g.regionBound[2] {
					v := g.svBound >> 2
					enc.encodeS(m.svb, v)
					if sbl == 0 {
						ctxSvb[ch] = v
					}
				} else { // broken file: encode diff with r2 bound
					enc.encodeS(m.svb, 144+1)
					enc.encodeS(m.bvf, (g.regionBound[2]-g.svBound)>>1)
					g.regionBound[2] = p
					for i := 1; i >= 0; i-- {
						if p < g.regionBound[i] {
							g.regionBound[i] = p
						} else {
							break
						}
					}
				}

				// --- encode coefficients ---
				hAbs, hSgn, hLen := b.ctxArrays(ch, sbl != 0)
				ctxAbs, ctxPat := 0, 0
				rlb := g.regionBound[2]
				modSgc := m.sgn[ch][flags]
				modAsc := m.asv[ch][flags][g.selectHtabB]
				for p = g.svBound - 1; p >= rlb; p-- {
					modAsc.shift2(ctxPat, int(hAbs[p+1]))
					enc.encodeB(modAsc, int(abs[p+1]))
					ctxPat = ((ctxPat << 1) | int(abs[p+1])) & 0xF
					ctxAbs = (2*int(abs[p+1]) + ctxAbs + 2) / 3
					if abs[p+1] == 1 {
						modSgc.shift2(int(hAbs[p+1]), int(hSgn[p+1]))
						enc.encodeB(modSgc, int(sgn[p+1]))
					}
				}
				for r := 2; r >= 0; r-- {
					rlb = 0
					if r > 0 {
						rlb = g.regionBound[r-1]
					}
					if p < rlb {
						continue
					}
					if g.regionTable[r] == 0 {
						clear(abs[rlb+1 : p+1])
						p = rlb
						ctxAbs = 0
						continue
					}
					linbits := bvDecTable[g.regionTable[r]].linbits
					modAbc := m.abv[ch][flags][g.regionTable[r]]
					modLnc := m.len[ch][linbits]
					for ; p >= rlb; p-- {
						a := int(abs[p+1])
						modAbc.shift2(ctxAbs, int(hAbs[p+1]))
						enc.encodeS(modAbc, a)
						ctxAbs = (2*a + ctxAbs + 2) / 3
						if a > 0 {
							modSgc.shift2(int(hAbs[p+1]), int(hSgn[p+1]))
							enc.encodeB(modSgc, int(sgn[p+1]))
							if linbits > 0 && a == 15 {
								l := int(ln[p+1])
								modLnc.shiftContext(int(hLen[p+1]))
								enc.encodeS(modLnc, l)
								for i := l - 2; i >= 0; i-- {
									m.res.shift2(l, i)
									enc.encodeB(m.res, int(lbt[p]>>uint(i))&1)
								}
							}
						}
					}
				}
				b.updateContext(ch, g.svBound, sbl != 0, jCoding)

				// --- stuffing bits ---
				n := lmaxp - bitc
				if n == 0 {
					m.nst.shiftContext(ctxNst)
					enc.encodeS(m.nst, 0)
					ctxNst = 0
				} else {
					for ; n >= stuffingStep; n -= stuffingStep {
						m.nst.shiftContext(ctxNst)
						enc.encodeS(m.nst, stuffingStep)
						ctxNst = stuffingStep
					}
					m.nst.shiftContext(ctxNst)
					enc.encodeS(m.nst, n)
					ctxNst = n
					for ; bitc < lmaxp; bitc++ {
						m.bst.shiftContext(ctxBst)
						v := int(huff.readBit())
						enc.encodeB(m.bst, v)
						ctxBst = ((ctxBst << 1) | v) & 0xF
					}
				}
				bitp += lmaxp
			}
		}

		// --- bitrate ---
		if c.iBitrate == -1 {
			m.btr.shiftContext(bitratePred[frame.mainSize])
			enc.encodeS(m.btr, frame.bits)
		}
		// --- aux data size ---
		if frame != c.lastframe && c.iBitRes != 0 {
			var n int
			if frame.auxSize+frame.next.bitReservoir < 511 {
				n = frame.auxSize
			} else {
				n = 511 - frame.next.bitReservoir
			}
			for ; n >= auxDataStep; n -= auxDataStep {
				m.aux.shiftContext(ctxAux)
				enc.encodeS(m.aux, auxDataStep)
				ctxAux = auxDataStep
			}
			m.aux.shiftContext(ctxAux)
			enc.encodeS(m.aux, n)
			ctxAux = n
		}
		// --- padding and aux data bits ---
		n := (frame.mainSize+frame.auxSize)*8 - bitp
		if n < 0 || n/8 >= ancBufSize {
			return c.fatal("bad ancillary data size (in frame #%d)", frame.n)
		}
		k, i := 0, n
		for ; i >= 8; i -= 8 {
			padAndAux[k] = byte(huff.readBits(8))
			k++
		}
		padAndAux[k] = byte(huff.readBits(i))
		if c.anc.predict(n, padAndAux[:]) {
			enc.encodeB(m.pap, 1)
		} else {
			enc.encodeB(m.pap, 0)
			huff.rewindBits(n)
			for ctxPad := 0xFF; n > 0; n-- {
				m.pad.shiftContext(ctxPad)
				v := int(huff.readBit())
				enc.encodeB(m.pad, v)
				ctxPad = ((ctxPad << 1) | v) & 0xFF
			}
		}
	}
	return nil
}

func (c *codec) pmpDecodeMainData(dec *aricoder) error {
	jCoding := c.iChannels == mp3Stereo || c.iChannels == mp3JointStereo
	huff := newHuffmanWriter()
	c.anc.reset()
	m := newMainModels(c.gNchannels, jCoding)
	b := newMainBuffers(c.gNchannels)
	bitratePred := mp3BitratePred[c.iSamplerate]
	frameSizes := mp3FrameSizeTable[c.iSamplerate]
	var padAndAux [ancBufSize]byte

	var shared [2][4]int
	var ctxSvb [2]int
	ctxNst, ctxBst, ctxAux := 0, 0, 0
	flags := 0
	bitres := 0

	for frame := c.firstframe; frame != nil; frame = frame.next {
		bitp := 0
		frame.mainIndex = huff.getpos()
		for gr := 0; gr < 2; gr++ {
			for ch := 0; ch < c.gNchannels; ch++ {
				g := frame.granules[ch][gr]
				sbl := b2i(g.blockType == shortBlock)
				slen := slengthTable[g.slength]
				abs, sgn, ln, lbt := b.absC[ch], b.sgnC[ch], b.lenC[ch], b.lbtC[ch]
				flags = compressionFlags(flags, sbl, frame.stereoMS, ch, jCoding)
				if gr == 0 {
					for k := 0; k < 4; k++ {
						shared[ch][k] = (g.share >> uint(3-k)) & 0x1
					}
				}
				huff.resetCounter()
				frame.bitReservoir = bitres

				// --- scalefactors ---
				if sbl == 0 {
					scf, prev := b.scfC[ch], b.scfLLong[ch]
					ctxScf := 0
					for grp, p := 0, 0; grp < 4; grp++ {
						sl := slen[b2i(grp >= 2)]
						if gr&shared[ch][grp] != 0 {
							copy(scf[p:p+scfWidth[grp]], prev[p:])
							p = scfBounds[grp]
							ctxScf = int(scf[p-1])
						} else if sl == 0 {
							clear(scf[p : p+scfWidth[grp]])
							p = scfBounds[grp]
							ctxScf = 0
						} else {
							idx := grp
							if shared[ch][grp] != 0 {
								idx |= 4
							}
							mod := m.scf[ch][sl-1][idx]
							for ; p < scfBounds[grp]; p++ {
								mod.shift2(ctxScf, int(prev[p]))
								scf[p] = byte(dec.decodeS(mod))
								huff.writeBits(uint32(scf[p]), sl)
								ctxScf = int(scf[p])
							}
						}
					}
					b.scfC[ch], b.scfLLong[ch] = b.scfLLong[ch], b.scfC[ch]
				} else {
					for i := 0; i < 3; i++ {
						scf, prev := b.scfC[ch], b.scfLShort[ch]
						ctxScf := 0
						for grp, p := 0, 0; grp < 2; grp++ {
							sl := slen[grp]
							if sl == 0 {
								clear(scf[p : p+scfLhWidthShort[grp]])
								p = scfLhBoundsShort[grp]
							} else {
								mod := m.scf[ch][sl-1][grp|0x8]
								for ; p < scfLhBoundsShort[grp]; p++ {
									mod.shift2(ctxScf, int(prev[p]))
									scf[p] = byte(dec.decodeS(mod))
									huff.writeBits(uint32(scf[p]), sl)
									ctxScf = int(scf[p])
								}
							}
						}
						b.scfC[ch], b.scfLShort[ch] = b.scfLShort[ch], b.scfC[ch]
					}
				}

				// --- sv bound ---
				if sbl == 0 {
					m.svb.shiftContext(ctxSvb[ch])
				} else {
					m.svb.shiftContext(144 + 1)
				}
				v := dec.decodeS(m.svb)
				if v <= 144 {
					g.svBound = v << 2
					if g.regionBound[2]%4 == 2 {
						g.svBound += 2
					}
					if sbl == 0 {
						ctxSvb[ch] = v
					}
				} else {
					v = dec.decodeS(m.bvf)
					g.svBound = g.regionBound[2] - (v << 1)
					g.regionBound[2] = g.svBound
					for i := 1; i >= 0; i-- {
						if g.svBound < g.regionBound[i] {
							g.regionBound[i] = g.svBound
						} else {
							break
						}
					}
				}
				// the last small value quadruple may end at 578
				if g.svBound < 0 || g.svBound > 578 {
					return c.fatal("corrupted data: sv bound %d (in frame #%d)", g.svBound, frame.n)
				}

				// --- decode coefficients ---
				hAbs, hSgn, hLen := b.ctxArrays(ch, g.blockType == shortBlock)
				ctxAbs, ctxPat := 0, 0
				rlb := g.regionBound[2]
				modSgc := m.sgn[ch][flags]
				modAsc := m.asv[ch][flags][g.selectHtabB]
				p := g.svBound - 1
				for ; p >= rlb; p-- {
					modAsc.shift2(ctxPat, int(hAbs[p+1]))
					abs[p+1] = byte(dec.decodeB(modAsc))
					ctxPat = ((ctxPat << 1) | int(abs[p+1])) & 0xF
					ctxAbs = (2*int(abs[p+1]) + ctxAbs + 2) / 3
					if abs[p+1] == 1 {
						modSgc.shift2(int(hAbs[p+1]), int(hSgn[p+1]))
						sgn[p+1] = byte(dec.decodeB(modSgc))
					}
				}
				for r := 2; r >= 0; r-- {
					rlb = 0
					if r > 0 {
						rlb = g.regionBound[r-1]
					}
					if p < rlb {
						continue
					}
					if g.regionTable[r] == 0 {
						clear(abs[rlb+1 : p+1])
						p = rlb
						ctxAbs = 0
						continue
					}
					linbits := bvEncTable[g.regionTable[r]].linbits
					modAbc := m.abv[ch][flags][g.regionTable[r]]
					if modAbc == nil {
						return c.fatal("bad huffman table (%d) used (in frame #%d)", g.regionTable[r], frame.n)
					}
					modLnc := m.len[ch][linbits]
					for ; p >= rlb; p-- {
						modAbc.shift2(ctxAbs, int(hAbs[p+1]))
						a := dec.decodeS(modAbc)
						abs[p+1] = byte(a)
						ctxAbs = (2*a + ctxAbs + 2) / 3
						if a > 0 {
							modSgc.shift2(int(hAbs[p+1]), int(hSgn[p+1]))
							sgn[p+1] = byte(dec.decodeB(modSgc))
							if linbits > 0 && a == 15 {
								modLnc.shiftContext(int(hLen[p+1]))
								l := dec.decodeS(modLnc)
								ln[p+1] = byte(l)
								if l > 0 {
									lbt[p] = 1
									for i := l - 2; i >= 0; i-- {
										m.res.shift2(l, i)
										lbt[p] = (lbt[p] << 1) | uint16(dec.decodeB(m.res))
									}
								} else {
									lbt[p] = 0
								}
							}
						}
					}
				}

				// --- huffman encode coefficients ---
				p = 0
				for r := 0; r < 3; r++ {
					if g.regionTable[r] == 0 {
						p = g.regionBound[r]
						continue
					}
					bv := &bvEncTable[g.regionTable[r]]
					for p < g.regionBound[r] {
						huff.encodePair(bv.h, abs[p+1:])
						for i := 0; i < 2; i, p = i+1, p+1 {
							if abs[p+1] > 0 {
								if bv.linbits > 0 && abs[p+1] == 15 {
									huff.writeBits(uint32(lbt[p]), bv.linbits)
								}
								huff.writeBit(uint32(sgn[p+1]))
							}
						}
					}
				}
				hcode := htabAEnc
				if g.selectHtabB != 0 {
					hcode = htabBEnc
				}
				for p < g.svBound {
					huff.encodeQuadruple(hcode, abs[p+1:])
					for i := 0; i < 4; i, p = i+1, p+1 {
						if abs[p+1] != 0 {
							huff.writeBit(uint32(sgn[p+1]))
						}
					}
				}
				b.updateContext(ch, p, sbl != 0, jCoding)

				// --- stuffing bits ---
				n := 0
				for v := stuffingStep; v == stuffingStep; n += v {
					m.nst.shiftContext(ctxNst)
					v = dec.decodeS(m.nst)
					ctxNst = v
				}
				for ; n > 0; n-- {
					m.bst.shiftContext(ctxBst)
					v := dec.decodeB(m.bst)
					huff.writeBit(uint32(v))
					ctxBst = ((ctxBst << 1) | v) & 0xF
				}
				g.mainDataBit = huff.getCount()
				bitp += g.mainDataBit
			}
		}

		frame.mainBits = bitp
		frame.mainSize = (bitp + 7) / 8
		if c.iBitrate == -1 {
			if frame.mainSize >= len(bitratePred) {
				return c.fatal("corrupted data: main size (in frame #%d)", frame.n)
			}
			m.btr.shiftContext(bitratePred[frame.mainSize])
			frame.bits = dec.decodeS(m.btr)
		}
		frame.frameSize = frameSizes[frame.bits]
		if frame.padding != 0 {
			frame.frameSize++
		}
		if c.iBitRes != 0 {
			bitres = frame.frameSize - frame.fixedSize + frame.bitReservoir - frame.mainSize
			if frame != c.lastframe {
				n := 0
				if bitres >= 511 {
					n = bitres - 511
				}
				for v := auxDataStep; v == auxDataStep; n += v {
					m.aux.shiftContext(ctxAux)
					v = dec.decodeS(m.aux)
					ctxAux = v
				}
				frame.auxSize = n
			} else {
				frame.auxSize = bitres
			}
			bitres -= frame.auxSize
		} else {
			bitres = 0
			frame.auxSize = frame.frameSize - frame.fixedSize + frame.bitReservoir - frame.mainSize
		}

		n := (frame.mainSize+frame.auxSize)*8 - bitp
		if n < 0 || n/8 >= ancBufSize {
			return c.fatal("corrupted data: anc bits %d (in frame #%d)", n, frame.n)
		}
		if dec.decodeB(m.pap) == 1 {
			c.anc.predict(n, nil)
			k := 0
			for ; n >= 8; n -= 8 {
				huff.writeBits(uint32(c.anc.pred[k]), 8)
				k++
			}
			if n > 0 {
				huff.writeBits(uint32(c.anc.pred[k]>>uint(8-n)), n)
			}
		} else {
			k := 0
			padAndAux[0] = 0
			ctxPad := 0xFF
			for i := 1; i <= n; i++ {
				m.pad.shiftContext(ctxPad)
				v := dec.decodeB(m.pad)
				huff.writeBit(uint32(v))
				ctxPad = ((ctxPad << 1) | v) & 0xFF
				padAndAux[k] = (padAndAux[k] << 1) | byte(v)
				if i%8 == 0 {
					k++
					padAndAux[k] = 0
				}
			}
			if c.anc.predict(n, padAndAux[:]) {
				return c.fatal("ancilary prediction error, please report")
			}
		}
	}
	c.mainData = huff.getptr()
	return nil
}
