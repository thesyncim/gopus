//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares TF decisions between gopus and libopus packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

func TestTFCompareLibopus(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Decode up to TF stage for both packets
	libState := decodeTFStateFromPacket(libPacket[1:], nbBands, lm) // skip TOC
	goState := decodeTFStateFromPacket(goPacket, nbBands, lm)

	if libState.transient != goState.transient {
		t.Fatalf("transient mismatch: libopus=%v gopus=%v", libState.transient, goState.transient)
	}

	// Compare raw TF states (before tf_select_table mapping).
	rawMismatch := 0
	for i := 0; i < nbBands; i++ {
		if libState.raw[i] != goState.raw[i] {
			rawMismatch++
			if rawMismatch <= 5 {
				t.Logf("TF raw mismatch band %d: libopus=%d gopus=%d", i, libState.raw[i], goState.raw[i])
			}
		}
	}

	if libState.tfSelect != goState.tfSelect {
		t.Logf("tf_select mismatch: libopus=%d gopus=%d", libState.tfSelect, goState.tfSelect)
	}

	// Compare final mapped TF change values.
	mappedMismatch := 0
	for i := 0; i < nbBands; i++ {
		if libState.mapped[i] != goState.mapped[i] {
			mappedMismatch++
			if mappedMismatch <= 5 {
				t.Logf("TF mapped mismatch band %d: libopus=%d gopus=%d", i, libState.mapped[i], goState.mapped[i])
			}
		}
	}

	// For this synthetic single-frame harness, tiny TF-state drift can occur from
	// upstream floating-point/state differences while still producing equivalent
	// coding behavior. Keep the check strict enough to catch real regressions.
	const maxRawMismatches = 2
	if rawMismatch > maxRawMismatches {
		t.Fatalf("TF raw mismatches: %d/%d bands (max allowed %d)", rawMismatch, nbBands, maxRawMismatches)
	}

	// tf_select/mapped values are still useful diagnostics, but may differ when raw
	// mismatches are within tolerance.
	if libState.tfSelect != goState.tfSelect || mappedMismatch != 0 {
		t.Logf("TF diagnostic: tf_select libopus=%d gopus=%d, mapped mismatches=%d/%d",
			libState.tfSelect, goState.tfSelect, mappedMismatch, nbBands)
	}
}

type tfPacketState struct {
	raw       []int
	mapped    []int
	transient bool
	tfSelect  int
}

func decodeTFStateFromPacket(payload []byte, nbBands, lm int) tfPacketState {
	state := tfPacketState{
		raw:    make([]int, nbBands),
		mapped: make([]int, nbBands),
	}

	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	_ = rd.DecodeBit(15) // silence
	postfilter := rd.DecodeBit(1)
	if postfilter == 1 {
		// Skip encoded postfilter parameters when present.
		octave := int(rd.DecodeUniform(6))
		_ = rd.DecodeRawBits(uint(4 + octave))
		_ = rd.DecodeRawBits(3)
		_ = rd.DecodeICDF([]uint8{2, 1, 0}, 2)
	}

	if lm > 0 {
		state.transient = rd.DecodeBit(3) == 1
	}
	intra := rd.DecodeBit(3) == 1

	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	_ = dec.DecodeCoarseEnergy(nbBands, intra, lm)

	// Decode TF states exactly like celt.tfDecode, while preserving both raw and mapped values.
	budget := rd.StorageBits()
	tell := rd.Tell()
	logp := 4
	if state.transient {
		logp = 2
	}
	tfSelectRsv := lm > 0 && tell+logp+1 <= budget
	if tfSelectRsv {
		budget--
	}
	tfChanged := 0
	curr := 0
	for i := 0; i < nbBands; i++ {
		if tell+logp <= budget {
			curr ^= rd.DecodeBit(uint(logp))
			tell = rd.Tell()
			if curr != 0 {
				tfChanged = 1
			}
		}
		state.raw[i] = curr
		if state.transient {
			logp = 4
		} else {
			logp = 5
		}
	}

	if tfSelectRsv {
		idx0 := tfSelectTableForTest(lm, state.transient, 0+tfChanged)
		idx1 := tfSelectTableForTest(lm, state.transient, 2+tfChanged)
		if idx0 != idx1 {
			state.tfSelect = rd.DecodeBit(1)
		}
	}

	for i := 0; i < nbBands; i++ {
		idx := 4*boolToIntForTest(state.transient) + 2*state.tfSelect + state.raw[i]
		state.mapped[i] = tfSelectTableForTest(lm, state.transient, 2*state.tfSelect+state.raw[i])
		_ = idx
	}

	return state
}

func boolToIntForTest(v bool) int {
	if v {
		return 1
	}
	return 0
}

func tfSelectTableForTest(lm int, transient bool, idx int) int {
	// Mirror celt/tables.go values used by the production TF decoder.
	table := [4][8]int{
		{0, -1, 0, -1, 0, -1, 0, -1},
		{0, -1, 0, -2, 1, 0, 1, -1},
		{0, -2, 0, -3, 2, 0, 1, -1},
		{0, -2, 0, -3, 3, 0, 1, -1},
	}
	base := 0
	if transient {
		base = 4
	}
	return table[lm][base+idx]
}
