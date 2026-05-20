package libopustest

import (
	"encoding/binary"
	"fmt"
	"math"
)

type OraclePayload struct {
	data []byte
}

func NewOraclePayload(inputMagic string, header ...uint32) *OraclePayload {
	p := &OraclePayload{}
	p.Magic(inputMagic)
	p.U32(1)
	p.U32s(header...)
	return p
}

func (p *OraclePayload) Magic(magic string) {
	if len(magic) != 4 {
		panic(fmt.Sprintf("oracle magic %q must be four bytes", magic))
	}
	p.data = append(p.data, magic...)
}

func (p *OraclePayload) U32(v uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	p.data = append(p.data, buf[:]...)
}

func (p *OraclePayload) I32(v int32) {
	p.U32(uint32(v))
}

func (p *OraclePayload) Float32(v float32) {
	p.U32(math.Float32bits(v))
}

func (p *OraclePayload) U32s(values ...uint32) {
	for _, v := range values {
		p.U32(v)
	}
}

func (p *OraclePayload) I32s(values ...int32) {
	for _, v := range values {
		p.I32(v)
	}
}

func (p *OraclePayload) Raw(data []byte) {
	p.data = append(p.data, data...)
}

func (p *OraclePayload) Bytes() []byte {
	return p.data
}

type OracleReader struct {
	label  string
	data   []byte
	offset int
	err    error
}

func NewOracleReader(label, outputMagic string, data []byte) (*OracleReader, error) {
	if len(outputMagic) != 4 {
		return nil, fmt.Errorf("oracle magic %q must be four bytes", outputMagic)
	}
	if len(data) < 8 || string(data[:4]) != outputMagic {
		return nil, fmt.Errorf("unexpected %s helper output", label)
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 1 {
		return nil, fmt.Errorf("%s helper version=%d want 1", label, version)
	}
	return &OracleReader{label: label, data: data, offset: 8}, nil
}

func (r *OracleReader) Count(want int) int {
	count := int(r.U32())
	if r.err != nil {
		return 0
	}
	if r.err == nil && want >= 0 && count != want {
		r.err = fmt.Errorf("%s helper count=%d want %d", r.label, count, want)
		return 0
	}
	return count
}

func (r *OracleReader) U32() uint32 {
	if r.err != nil {
		return 0
	}
	if r.offset+4 > len(r.data) {
		r.err = fmt.Errorf("truncated %s helper output", r.label)
		return 0
	}
	v := binary.LittleEndian.Uint32(r.data[r.offset:])
	r.offset += 4
	return v
}

func (r *OracleReader) I32() int32 {
	return int32(r.U32())
}

func (r *OracleReader) U16() uint16 {
	if r.err != nil {
		return 0
	}
	if r.offset+2 > len(r.data) {
		r.err = fmt.Errorf("truncated %s helper output", r.label)
		return 0
	}
	v := binary.LittleEndian.Uint16(r.data[r.offset:])
	r.offset += 2
	return v
}

func (r *OracleReader) I16() int16 {
	return int16(r.U16())
}

func (r *OracleReader) Float32() float32 {
	return math.Float32frombits(r.U32())
}

func (r *OracleReader) Bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 {
		r.err = fmt.Errorf("%s helper negative byte count %d", r.label, n)
		return nil
	}
	if r.offset+n > len(r.data) {
		r.err = fmt.Errorf("truncated %s helper output", r.label)
		return nil
	}
	out := r.data[r.offset : r.offset+n]
	r.offset += n
	return out
}

func (r *OracleReader) ExpectRemaining(n int) {
	if r.err != nil {
		return
	}
	if n < 0 {
		r.err = fmt.Errorf("%s helper negative remaining byte count %d", r.label, n)
		return
	}
	if remaining := len(r.data) - r.offset; remaining != n {
		r.err = fmt.Errorf("%s helper output length=%d want %d", r.label, len(r.data), r.offset+n)
	}
}

func (r *OracleReader) Err() error {
	return r.err
}

func (r *OracleReader) ExpectConsumed() error {
	if r.err != nil {
		return r.err
	}
	if r.offset != len(r.data) {
		return fmt.Errorf("%s helper output has %d trailing bytes", r.label, len(r.data)-r.offset)
	}
	return nil
}
