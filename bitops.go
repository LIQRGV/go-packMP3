// Go port of packMP3 (c) 2010-2016 Ratisbon University & Matthias Stirner.
// Licensed under the GNU LGPL v3, see LICENSE.

package packmp3

// bitReader reads an array bitwise, MSB first (port of abitreader).
type bitReader struct {
	data  []byte
	lbyte int
	cbyte int
	cbit  int
	eof   bool
	peof  int
}

func newBitReader(data []byte) *bitReader {
	return &bitReader{data: data, lbyte: len(data), cbit: 8}
}

// rbits returns the lowest n bits of c (RBITS).
func rbits(c byte, n int) uint32 { return uint32(c) & (0xFF >> uint(8-n)) }

func (r *bitReader) read(nbits int) uint32 {
	var retval uint32
	if r.eof {
		r.peof += nbits
		return 0
	}
	for nbits >= r.cbit {
		nbits -= r.cbit
		retval |= rbits(r.data[r.cbyte], r.cbit) << uint(nbits)
		r.cbit = 8
		r.cbyte++
		if r.cbyte >= r.lbyte {
			r.peof = nbits
			r.eof = true
			return retval
		}
	}
	if nbits > 0 {
		retval |= rbits(r.data[r.cbyte], r.cbit) >> uint(r.cbit-nbits)
		r.cbit -= nbits
	}
	return retval
}

func (r *bitReader) readBit() uint32 {
	if r.eof {
		r.peof++
		return 0
	}
	r.cbit--
	bit := uint32(r.data[r.cbyte]>>uint(r.cbit)) & 1
	if r.cbit == 0 {
		r.cbyte++
		if r.cbyte == r.lbyte {
			r.eof = true
		}
		r.cbit = 8
	}
	return bit
}

func (r *bitReader) getpos() int  { return r.cbyte }
func (r *bitReader) getbitp() int { return r.cbit }

func (r *bitReader) setpos(pbyte, pbit int) {
	if pbyte < r.lbyte {
		r.eof = false
		r.cbyte = pbyte
		r.cbit = pbit
	} else {
		r.eof = true
		r.cbyte = r.lbyte
		r.cbit = 8
		r.peof = ((pbyte - r.lbyte) * 8) + 8 - pbit
	}
}

func (r *bitReader) rewindBits(nbits int) {
	if r.eof {
		if nbits > r.peof {
			nbits -= r.peof
		} else {
			r.peof -= nbits
			return
		}
		r.eof = false
	}
	for r.cbit += nbits; r.cbit > 8; r.cbit -= 8 {
		r.cbyte--
	}
	if r.cbyte < 0 {
		r.cbyte = 0
		r.cbit = 8
	}
}

// bitWriter writes an array bitwise, MSB first (port of abitwriter).
type bitWriter struct {
	data    []byte
	cbyte   int
	cbit    int
	fillbit uint32
}

func newBitWriter(size int) *bitWriter {
	if size <= 0 {
		size = 65536
	}
	return &bitWriter{data: make([]byte, size), cbit: 8, fillbit: 1}
}

func (w *bitWriter) grow() {
	if w.cbyte > len(w.data)-5 {
		w.data = append(w.data, make([]byte, 65536)...)
	}
}

func (w *bitWriter) write(val uint32, nbits int) {
	w.grow()
	for nbits >= w.cbit {
		// MBITS32( val, nbits, nbits-cbit )
		w.data[w.cbyte] |= byte((val & (0xFFFFFFFF >> uint(32-nbits))) >> uint(nbits-w.cbit))
		nbits -= w.cbit
		w.cbyte++
		w.cbit = 8
	}
	if nbits > 0 {
		w.data[w.cbyte] |= byte((val & (0xFFFFFFFF >> uint(32-nbits))) << uint(w.cbit-nbits))
		w.cbit -= nbits
	}
}

func (w *bitWriter) writeBit(bit uint32) {
	w.cbit--
	if bit != 0 {
		w.data[w.cbyte] |= 1 << uint(w.cbit)
	}
	if w.cbit == 0 {
		w.cbyte++
		w.grow()
		w.cbit = 8
	}
}

func (w *bitWriter) pad(fillbit uint32) {
	for w.cbit < 8 {
		w.write(fillbit, 1)
	}
}

// getptr pads the last byte with fillbit and returns the written data.
func (w *bitWriter) getptr() []byte {
	w.pad(w.fillbit)
	return w.data[:w.cbyte]
}

func (w *bitWriter) getpos() int { return w.cbyte }

// byteReader is the memory-input part of the C++ iostream class.
type byteReader struct {
	data []byte
	pos  int
	eof  bool
}

func newByteReader(data []byte) *byteReader {
	return &byteReader{data: data, eof: len(data) == 0}
}

// read copies up to len(to) bytes and returns how many were read.
func (r *byteReader) read(to []byte) int {
	n := copy(to, r.data[r.pos:])
	r.pos += n
	if n < len(to) {
		r.eof = true
	}
	return n
}

func (r *byteReader) readByte() (byte, bool) {
	if r.pos >= len(r.data) {
		r.eof = true
		return 0, false
	}
	b := r.data[r.pos]
	r.pos++
	return b, true
}
