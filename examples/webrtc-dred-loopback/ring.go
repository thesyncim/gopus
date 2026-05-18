package main

import (
	"encoding/binary"
	"math"
	"sync"
)

type sampleRing struct {
	mu  sync.Mutex
	buf []float32
	r   int
	w   int
	n   int
}

func newSampleRing(capacity int) *sampleRing {
	if capacity < 1 {
		capacity = 1
	}
	return &sampleRing{buf: make([]float32, capacity)}
}

func (r *sampleRing) write(src []float32) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range src {
		r.writeOneLocked(s)
	}
	return len(src)
}

func (r *sampleRing) writeF32LE(src []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	samples := len(src) / 4
	for i := 0; i < samples; i++ {
		bits := binary.LittleEndian.Uint32(src[i*4:])
		r.writeOneLocked(math.Float32frombits(bits))
	}
	return samples
}

func (r *sampleRing) writeOneLocked(s float32) {
	if len(r.buf) == 0 {
		return
	}
	if r.n == len(r.buf) {
		r.r = (r.r + 1) % len(r.buf)
		r.n--
	}
	r.buf[r.w] = s
	r.w = (r.w + 1) % len(r.buf)
	r.n++
}

func (r *sampleRing) read(dst []float32) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(dst) > r.n {
		dst = dst[:r.n]
	}
	for i := range dst {
		dst[i] = r.buf[r.r]
		r.r = (r.r + 1) % len(r.buf)
		r.n--
	}
	return len(dst)
}

func (r *sampleRing) readF32LE(dst []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	samples := len(dst) / 4
	if samples > r.n {
		samples = r.n
	}
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint32(dst[i*4:], math.Float32bits(r.buf[r.r]))
		r.r = (r.r + 1) % len(r.buf)
		r.n--
	}
	return samples
}

func (r *sampleRing) clear() {
	r.mu.Lock()
	r.r, r.w, r.n = 0, 0, 0
	r.mu.Unlock()
}
