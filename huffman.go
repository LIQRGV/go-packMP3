// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

const bitBufferSize = 16

type huffmanCode struct {
	len  uint8
	code uint32
}

type huffmanEncTable struct {
	h       [][]huffmanCode
	linbits int
	max     int
}

type huffmanConv struct {
	len uint8
	v0  int
	v1  int
	ext int
}

type huffmanConvSet struct {
	h      []huffmanConv
	hwidth int
}

type huffmanDecTable struct {
	h       []huffmanConvSet
	linbits int
	max     int
}

// these two are plain structs in huffmp3tbl.h, not arrays, so the
// table converter leaves them out
var (
	htabADec = []huffmanConvSet{{htabADecRaw, 6}}
	htabBDec = []huffmanConvSet{{htabBDecRaw, 4}}
)

// small values quadruple conversion table (abs)
var svQconvAbs = [16][4]uint8{
	{0, 0, 0, 0}, {0, 0, 0, 1}, {0, 0, 1, 0}, {0, 0, 1, 1},
	{0, 1, 0, 0}, {0, 1, 0, 1}, {0, 1, 1, 0}, {0, 1, 1, 1},
	{1, 0, 0, 0}, {1, 0, 0, 1}, {1, 0, 1, 0}, {1, 0, 1, 1},
	{1, 1, 0, 0}, {1, 1, 0, 1}, {1, 1, 1, 0}, {1, 1, 1, 1},
}

// huffmanReader decodes huffman data / reads bits (port of huffman_reader).
type huffmanReader struct {
	br     *bitReader
	buffer uint32
	count  int
}

func newHuffmanReader(data []byte) *huffmanReader {
	h := &huffmanReader{br: newBitReader(data)}
	h.buffer = h.br.read(bitBufferSize)
	return h
}

func (h *huffmanReader) decodePair(table []huffmanConvSet, vals []uint8) {
	var conv *huffmanConv
	for ext := 0; ext != -1; ext = conv.ext {
		set := &table[ext]
		conv = &set.h[h.buffer>>uint(bitBufferSize-set.hwidth)]
		h.advance(int(conv.len))
	}
	vals[0] = uint8(conv.v0)
	vals[1] = uint8(conv.v1)
}

func (h *huffmanReader) decodeQuadruple(table []huffmanConvSet, vals []uint8) {
	set := &table[0]
	conv := &set.h[h.buffer>>uint(bitBufferSize-set.hwidth)]
	h.advance(int(conv.len))
	copy(vals[:4], svQconvAbs[conv.v0][:])
}

func (h *huffmanReader) readBits(n int) uint32 {
	var bits uint32
	if n <= bitBufferSize {
		bits = h.buffer >> uint(bitBufferSize-n)
	} else {
		bits = (h.buffer << uint(n-bitBufferSize)) | h.br.read(n-bitBufferSize)
	}
	h.advance(n)
	return bits
}

func (h *huffmanReader) readBit() uint32 {
	bit := h.buffer >> (bitBufferSize - 1)
	h.buffer = ((h.buffer << 1) | h.br.readBit()) & ((1 << bitBufferSize) - 1)
	h.count++
	return bit
}

func (h *huffmanReader) resetCounter() { h.count = 0 }
func (h *huffmanReader) getCount() int { return h.count }

func (h *huffmanReader) rewindBits(n int) {
	h.br.rewindBits(n + bitBufferSize)
	h.buffer = h.br.read(bitBufferSize)
	if h.count > n {
		h.count -= n
	} else {
		h.count = 0
	}
}

func (h *huffmanReader) advance(n int) {
	if n >= bitBufferSize {
		h.buffer = h.br.read(bitBufferSize)
	} else {
		h.buffer = ((h.buffer << uint(n)) | h.br.read(n)) & ((1 << bitBufferSize) - 1)
	}
	h.count += n
}

// huffmanWriter encodes huffman data / writes bits (port of huffman_writer).
type huffmanWriter struct {
	bw    *bitWriter
	count int
}

func newHuffmanWriter() *huffmanWriter {
	return &huffmanWriter{bw: newBitWriter(5 * 1024 * 1024)}
}

func (h *huffmanWriter) encodePair(hcodes [][]huffmanCode, vals []uint8) {
	c := &hcodes[vals[0]][vals[1]]
	h.writeBits(c.code, int(c.len))
}

func (h *huffmanWriter) encodeQuadruple(hcode []huffmanCode, vals []uint8) {
	bits := 0
	for i := 0; i < 4; i++ {
		bits <<= 1
		if vals[i] != 0 {
			bits |= 1
		}
	}
	c := &hcode[bits]
	h.writeBits(c.code, int(c.len))
}

func (h *huffmanWriter) writeBits(bits uint32, n int) {
	h.bw.write(bits, n)
	h.count += n
}

func (h *huffmanWriter) writeBit(bit uint32) {
	h.bw.writeBit(bit)
	h.count++
}

func (h *huffmanWriter) resetCounter()  { h.count = 0 }
func (h *huffmanWriter) getCount() int  { return h.count }
func (h *huffmanWriter) getptr() []byte { return h.bw.getptr() }
func (h *huffmanWriter) getpos() int    { return h.bw.getpos() }
