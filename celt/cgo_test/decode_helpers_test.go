//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"sync"

	"github.com/thesyncim/gopus"
)

type decoderScratch struct {
	f32 []float32
	i16 []int16
}

var decoderScratchMap sync.Map

func scratchForDecoder(dec *gopus.Decoder) *decoderScratch {
	if dec == nil {
		return &decoderScratch{}
	}
	if scratch, ok := decoderScratchMap.Load(dec); ok {
		return scratch.(*decoderScratch)
	}
	cfg := gopus.DefaultDecoderConfig(dec.SampleRate(), dec.Channels())
	scratch := &decoderScratch{
		f32: make([]float32, cfg.MaxPacketSamples*cfg.Channels),
		i16: make([]int16, cfg.MaxPacketSamples*cfg.Channels),
	}
	decoderScratchMap.Store(dec, scratch)
	return scratch
}

func decodeFloat32(dec *gopus.Decoder, packet []byte) ([]float32, error) {
	scratch := scratchForDecoder(dec)
	n, err := dec.Decode(packet, scratch.f32)
	if err != nil {
		return nil, err
	}
	return scratch.f32[:n*dec.Channels()], nil
}

func decodeInt16(dec *gopus.Decoder, packet []byte) ([]int16, error) {
	scratch := scratchForDecoder(dec)
	n, err := dec.DecodeInt16(packet, scratch.i16)
	if err != nil {
		return nil, err
	}
	return scratch.i16[:n*dec.Channels()], nil
}
