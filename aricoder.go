// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

const (
	coderUseBits  = 31
	coderLimit100 = uint32(1) << coderUseBits
	coderLimit025 = (coderLimit100 / 4) * 1
	coderLimit050 = (coderLimit100 / 4) * 2
	coderLimit075 = (coderLimit100 / 4) * 3
	escapeSymbol  = int(coderLimit025)
	modelMaxCount = 511 // c_lim used by INIT_MODEL_S / INIT_MODEL_B
)

type symbol struct {
	lowCount  uint32
	highCount uint32
	scale     uint32
}

// ---------------------------------------------------------------------------
// arithmetic coder

type aricoder struct {
	// output (encoding) or input (decoding)
	out *[]byte
	in  *byteReader

	encoding bool
	overrun  int // bytes read past the end of the input
	bbyte    byte
	cbit     uint
	ccode    uint32
	clow     uint32
	chigh    uint32
	cstep    uint32
	nrbits   uint32
}

func newEncoder(out *[]byte) *aricoder {
	return &aricoder{out: out, encoding: true, chigh: coderLimit100 - 1}
}

func newDecoder(in *byteReader) *aricoder {
	a := &aricoder{in: in, chigh: coderLimit100 - 1}
	for i := 0; i < coderUseBits; i++ {
		a.ccode = (a.ccode << 1) | a.readBit()
	}
	return a
}

// finish flushes the encoder (C++ destructor).
func (a *aricoder) finish() {
	if !a.encoding {
		return
	}
	if a.clow < coderLimit025 {
		a.writeBit(0)
		a.writeBit(1)
		for ; a.nrbits > 0; a.nrbits-- {
			a.writeBit(1)
		}
	} else {
		a.writeBit(1)
	}
	for a.cbit > 0 {
		a.writeBit(0)
	}
}

func (a *aricoder) encode(s *symbol) {
	a.cstep = ((a.chigh - a.clow) + 1) / s.scale
	a.chigh = a.clow + (a.cstep * s.highCount) - 1
	a.clow = a.clow + (a.cstep * s.lowCount)

	for a.clow >= coderLimit050 || a.chigh < coderLimit050 {
		if a.chigh < coderLimit050 {
			a.writeBit(0)
			for ; a.nrbits > 0; a.nrbits-- {
				a.writeBit(1)
			}
		} else {
			a.writeBit(1)
			a.clow &= coderLimit050 - 1
			a.chigh &= coderLimit050 - 1
			for ; a.nrbits > 0; a.nrbits-- {
				a.writeBit(0)
			}
		}
		a.clow <<= 1
		a.chigh <<= 1
		a.chigh++
	}
	for a.clow >= coderLimit025 && a.chigh < coderLimit075 {
		a.nrbits++
		a.clow &= coderLimit025 - 1
		a.chigh ^= coderLimit025 + coderLimit050
		a.clow <<= 1
		a.chigh <<= 1
		a.chigh++
	}
}

func (a *aricoder) decodeCount(s *symbol) uint32 {
	a.cstep = ((a.chigh - a.clow) + 1) / s.scale
	return (a.ccode - a.clow) / a.cstep
}

func (a *aricoder) decode(s *symbol) {
	a.chigh = a.clow + (a.cstep * s.highCount) - 1
	a.clow = a.clow + (a.cstep * s.lowCount)

	for a.clow >= coderLimit050 || a.chigh < coderLimit050 {
		if a.clow >= coderLimit050 {
			a.clow &= coderLimit050 - 1
			a.chigh &= coderLimit050 - 1
			a.ccode &= coderLimit050 - 1
		}
		a.clow <<= 1
		a.chigh <<= 1
		a.chigh++
		a.ccode <<= 1
		a.ccode |= a.readBit()
		a.nrbits = 0
	}
	for a.clow >= coderLimit025 && a.chigh < coderLimit075 {
		a.nrbits++
		a.clow &= coderLimit025 - 1
		a.chigh ^= coderLimit025 + coderLimit050
		a.ccode -= coderLimit025
		a.clow <<= 1
		a.chigh <<= 1
		a.chigh++
		a.ccode <<= 1
		a.ccode |= a.readBit()
	}
}

func (a *aricoder) writeBit(bit byte) {
	a.bbyte = (a.bbyte << 1) | bit
	a.cbit++
	if a.cbit == 8 {
		*a.out = append(*a.out, a.bbyte)
		a.cbit = 0
	}
}

func (a *aricoder) readBit() uint32 {
	if a.cbit == 0 {
		b, ok := a.in.readByte()
		if !ok {
			// a valid stream reads at most 4 bytes past its end; stop
			// endless decode loops on damaged input
			if a.overrun++; a.overrun > 1024 {
				panic(errTruncated)
			}
			b = 0
		}
		a.bbyte = b
		a.cbit = 8
	}
	a.cbit--
	return uint32(a.bbyte>>a.cbit) & 1
}

// ---------------------------------------------------------------------------
// universal statistical model (model_s)

type tableS struct {
	counts    []uint16
	links     []*tableS
	lesser    *tableS
	maxCount  uint16
	maxSymbol uint16
}

type modelS struct {
	totals       []uint32
	scoreboard   []byte
	sb0Count     int
	storage      []*tableS // storage[i+1] == contexts[i], i >= -1
	maxSymbol    int
	maxContext   int
	currentOrder int
	maxOrder     int
	maxCount     int
}

func newModelS(maxS, maxC, maxO int) *modelS {
	m := &modelS{
		maxSymbol:  maxS,
		maxContext: maxC,
		maxOrder:   maxO,
		maxCount:   modelMaxCount,
	}
	m.totals = make([]uint32, maxS+2)
	m.scoreboard = make([]byte, maxS)
	m.sb0Count = maxS
	m.currentOrder = maxO

	null := &tableS{counts: make([]uint16, maxS), maxCount: 1, maxSymbol: uint16(maxS)}
	for i := range null.counts {
		null.counts[i] = 1
	}
	start := &tableS{links: make([]*tableS, maxC), lesser: null}
	null.links = make([]*tableS, maxC)
	for i := range null.links {
		null.links[i] = start
	}
	m.storage = make([]*tableS, maxO+3)
	m.storage[0] = null
	m.storage[1] = start
	for i := 1; i <= maxO; i++ {
		t := &tableS{lesser: m.ctx(i - 1)}
		if i < maxO {
			t.links = make([]*tableS, maxC)
		}
		m.storage[i+1] = t
		m.ctx(i - 1).links[0] = t
	}
	return m
}

func (m *modelS) ctx(i int) *tableS { return m.storage[i+1] }

func (m *modelS) updateModel(sym int) {
	if sym >= 0 {
		lo := m.currentOrder
		if lo < 0 {
			lo = 0
		}
		for order := lo; order <= m.maxOrder; order++ {
			t := m.ctx(order)
			t.counts[sym]++
			c := t.counts[sym]
			if c > t.maxCount {
				t.maxCount = c
			}
			if sym >= int(t.maxSymbol) {
				t.maxSymbol = uint16(sym + 1)
			}
			if int(c) >= m.maxCount {
				m.rescaleTable(t, 1)
			}
		}
	}
	m.currentOrder = m.maxOrder
	for i := range m.scoreboard {
		m.scoreboard[i] = 0
	}
	m.sb0Count = m.maxSymbol
}

func (m *modelS) shiftContext(c int) {
	if m.maxOrder < 1 || c < 0 {
		return
	}
	for i := m.maxOrder; i > 0; i-- {
		t := m.ctx(i - 1).links[c]
		if t == nil {
			t = &tableS{lesser: m.ctx(i - 2).links[c]}
			if i != m.maxOrder {
				t.links = make([]*tableS, m.maxContext)
				m.ctx(i + 1).lesser = t
			}
			m.ctx(i - 1).links[c] = t
		}
		m.storage[i+1] = t
	}
}

func (m *modelS) flushModel(scaleFactor int) {
	m.recursiveFlush(m.ctx(0), scaleFactor)
}

func (m *modelS) convertIntToSymbol(c int, s *symbol) bool {
	t := m.ctx(m.currentOrder)
	m.totalizeTable(t)
	s.scale = m.totals[0]
	if c >= 0 && t.counts[c] > 0 {
		s.lowCount = m.totals[c+2]
		s.highCount = m.totals[c+1]
		return false
	}
	s.lowCount = m.totals[1]
	s.highCount = m.totals[0]
	m.currentOrder--
	return true
}

func (m *modelS) getSymbolScale(s *symbol) {
	m.totalizeTable(m.ctx(m.currentOrder))
	s.scale = m.totals[0]
}

func (m *modelS) convertSymbolToInt(count uint32, s *symbol) int {
	c := 1
	for int32(count) < int32(m.totals[c]) {
		c++
	}
	s.lowCount = m.totals[c]
	s.highCount = m.totals[c-1]
	if c == 1 {
		m.currentOrder--
		return escapeSymbol
	}
	return c - 2
}

func (m *modelS) totalizeTable(t *tableS) {
	if t.counts == nil {
		t.counts = make([]uint16, m.maxSymbol)
		m.totals[0] = 1
		m.totals[1] = 0
		return
	}
	localSymb := m.sb0Count
	i := int(t.maxSymbol) - 1
	m.totals[i+2] = 0
	var total uint32
	for ; i >= 0; i-- {
		if m.scoreboard[i] == 0 {
			cnt := uint32(t.counts[i])
			if cnt > 0 {
				total += cnt
				m.scoreboard[i] = 1
				m.sb0Count--
			}
		}
		m.totals[i+1] = total
	}
	var esc uint32
	switch {
	case localSymb == m.sb0Count:
		esc = 1
	case m.sb0Count == 0:
		esc = 0
	default:
		esc = uint32(int32(m.sb0Count * (localSymb - m.sb0Count)))
		esc /= uint32(int32(localSymb * int(t.maxCount)))
		esc++
	}
	m.totals[0] = m.totals[1] + esc
}

func (m *modelS) rescaleTable(t *tableS, scaleFactor int) {
	if t.counts == nil {
		return
	}
	last := int(t.maxSymbol)
	for i := 0; i < last; i++ {
		if t.counts[i] > 0 {
			t.counts[i] >>= uint(scaleFactor)
		}
	}
	t.maxCount >>= uint(scaleFactor)
	i := last - 1
	for ; i >= 0; i-- {
		if t.counts[i] > 0 {
			break
		}
	}
	t.maxSymbol = uint16(i + 1)
}

func (m *modelS) recursiveFlush(t *tableS, scaleFactor int) {
	for _, l := range t.links {
		if l != nil {
			m.recursiveFlush(l, scaleFactor)
		}
	}
	m.rescaleTable(t, scaleFactor)
}

// ---------------------------------------------------------------------------
// binary statistical model (model_b)

type tableB struct {
	counts []uint16
	links  []*tableB
	lesser *tableB
	scale  uint32
}

type modelB struct {
	storage    []*tableB
	maxContext int
	maxOrder   int
	maxCount   int
}

func newModelB(maxC, maxO int) *modelB {
	m := &modelB{maxContext: maxC, maxOrder: maxO, maxCount: modelMaxCount}
	null := &tableB{counts: []uint16{1, 1}, scale: 2}
	start := &tableB{links: make([]*tableB, maxC), lesser: null}
	null.links = make([]*tableB, maxC)
	for i := range null.links {
		null.links[i] = start
	}
	m.storage = make([]*tableB, maxO+3)
	m.storage[0] = null
	m.storage[1] = start
	for i := 1; i <= maxO; i++ {
		t := &tableB{lesser: m.ctx(i - 1)}
		if i < maxO {
			t.links = make([]*tableB, maxC)
		}
		m.storage[i+1] = t
		m.ctx(i - 1).links[0] = t
	}
	return m
}

func (m *modelB) ctx(i int) *tableB { return m.storage[i+1] }

func (m *modelB) updateModel(sym int) {
	t := m.ctx(m.maxOrder)
	if sym >= 0 && m.maxOrder >= 0 {
		t.counts[sym]++
		t.scale++
		if int(t.counts[sym]) >= m.maxCount {
			m.rescaleTable(t, 1)
		}
	}
}

func (m *modelB) shiftContext(c int) {
	if m.maxOrder < 1 || c < 0 {
		return
	}
	for i := m.maxOrder; i > 0; i-- {
		t := m.ctx(i - 1).links[c]
		if t == nil {
			t = &tableB{lesser: m.ctx(i - 2).links[c]}
			if i != m.maxOrder {
				t.links = make([]*tableB, m.maxContext)
				m.ctx(i + 1).lesser = t
			}
			m.ctx(i - 1).links[c] = t
		}
		m.storage[i+1] = t
	}
}

func (m *modelB) flushModel(scaleFactor int) {
	m.recursiveFlush(m.ctx(0), scaleFactor)
}

func (m *modelB) convertIntToSymbol(c int, s *symbol) {
	t := m.ctx(m.maxOrder)
	m.checkCounts(t)
	s.scale = t.scale
	if c == 0 {
		s.lowCount = 0
		s.highCount = uint32(t.counts[0])
	} else {
		s.lowCount = uint32(t.counts[0])
		s.highCount = t.scale
	}
}

func (m *modelB) getSymbolScale(s *symbol) {
	t := m.ctx(m.maxOrder)
	m.checkCounts(t)
	s.scale = t.scale
}

func (m *modelB) convertSymbolToInt(count uint32, s *symbol) int {
	c0 := t0(m.ctx(m.maxOrder))
	if int(int32(count)) < int(c0) {
		s.lowCount = 0
		s.highCount = uint32(c0)
		return 0
	}
	s.lowCount = uint32(c0)
	s.highCount = s.scale
	return 1
}

func t0(t *tableB) uint16 { return t.counts[0] }

func (m *modelB) checkCounts(t *tableB) {
	if t.counts == nil {
		t.counts = []uint16{1, 1}
		t.scale = 2
	}
}

func (m *modelB) rescaleTable(t *tableB, scaleFactor int) {
	if t.counts == nil {
		return
	}
	t.counts[0] >>= uint(scaleFactor)
	t.counts[1] >>= uint(scaleFactor)
	if t.counts[0] == 0 {
		t.counts[0] = 1
	}
	if t.counts[1] == 0 {
		t.counts[1] = 1
	}
	t.scale = uint32(t.counts[0]) + uint32(t.counts[1])
}

func (m *modelB) recursiveFlush(t *tableB, scaleFactor int) {
	for _, l := range t.links {
		if l != nil {
			m.recursiveFlush(l, scaleFactor)
		}
	}
	m.rescaleTable(t, scaleFactor)
}

// ---------------------------------------------------------------------------
// generic encode / decode helpers

func (m *modelS) shift2(c1, c2 int)     { m.shiftContext(c1); m.shiftContext(c2) }
func (m *modelS) shift3(c1, c2, c3 int) { m.shiftContext(c1); m.shiftContext(c2); m.shiftContext(c3) }
func (m *modelB) shift2(c1, c2 int)     { m.shiftContext(c1); m.shiftContext(c2) }

func (a *aricoder) encodeS(m *modelS, c int) {
	var s symbol
	for {
		esc := m.convertIntToSymbol(c, &s)
		a.encode(&s)
		if !esc {
			break
		}
	}
	m.updateModel(c)
}

func (a *aricoder) decodeS(m *modelS) int {
	var s symbol
	var c int
	for {
		m.getSymbolScale(&s)
		count := a.decodeCount(&s)
		c = m.convertSymbolToInt(count, &s)
		a.decode(&s)
		if c != escapeSymbol {
			break
		}
	}
	m.updateModel(c)
	return c
}

func (a *aricoder) encodeB(m *modelB, c int) {
	var s symbol
	m.convertIntToSymbol(c, &s)
	a.encode(&s)
	m.updateModel(c)
}

func (a *aricoder) decodeB(m *modelB) int {
	var s symbol
	m.getSymbolScale(&s)
	count := a.decodeCount(&s)
	c := m.convertSymbolToInt(count, &s)
	a.decode(&s)
	m.updateModel(c)
	return c
}
