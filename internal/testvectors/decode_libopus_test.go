// Package testvectors provides analysis of libopus packet structure.
package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestDecodeLibopusFlags decodes the first few symbols from a libopus packet
// to understand what flags are encoded.
func TestDecodeLibopusFlags(t *testing.T) {
	// libopus encoded packet for 440Hz sine, 20ms, mono, 64kbps
	// First byte is TOC (0xf8), rest is range-coded data
	packet := []byte{
		0xf8, 0x3a, 0x50, 0x53, 0x92, 0x29, 0xda, 0xb1,
		0x30, 0x4a, 0x51, 0xbd, 0x7d, 0x71, 0xb1, 0x92,
	}

	// Skip TOC byte
	data := packet[1:]

	// Create range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(data)

	t.Logf("Decoding libopus packet flags...")
	t.Logf("Initial range: 0x%08x", rd.Range())
	t.Logf("Initial val:   0x%08x", rd.Val())

	// Try to decode what libopus encoded
	// According to libopus, the sequence should be:
	// 1. Silence flag (logp=15)
	// 2. Prefilter on/off (logp=1) - if not silence
	// 3. ... more flags depending on prefilter

	// Decode silence flag
	silenceVal := rd.Val()
	silenceRng := rd.Range()
	silenceR := silenceRng >> 15
	silenceThreshold := silenceRng - silenceR
	silence := 0
	if silenceVal >= silenceThreshold {
		silence = 1
	}
	t.Logf("Silence flag: %d (val=0x%08x, threshold=0x%08x, r=0x%08x)",
		silence, silenceVal, silenceThreshold, silenceR)

	// Manually update range state for silence=0
	if silence == 0 {
		rd.DecodeSymbol(0, silenceThreshold, silenceRng)
	} else {
		rd.DecodeSymbol(silenceThreshold, silenceRng, silenceRng)
	}

	t.Logf("After silence: range=0x%08x, val=0x%08x", rd.Range(), rd.Val())

	// Try to decode prefilter flag (logp=1)
	pfVal := rd.Val()
	pfRng := rd.Range()
	pfR := pfRng >> 1
	pfThreshold := pfRng - pfR
	prefilter := 0
	if pfVal >= pfThreshold {
		prefilter = 1
	}
	t.Logf("Prefilter flag: %d (val=0x%08x, threshold=0x%08x, r=0x%08x)",
		prefilter, pfVal, pfThreshold, pfR)

	// Update range state
	if prefilter == 0 {
		rd.DecodeSymbol(0, pfThreshold, pfRng)
	} else {
		rd.DecodeSymbol(pfThreshold, pfRng, pfRng)
	}

	t.Logf("After prefilter: range=0x%08x, val=0x%08x", rd.Range(), rd.Val())

	// Try to decode transient flag (logp=3)
	trVal := rd.Val()
	trRng := rd.Range()
	trR := trRng >> 3
	trThreshold := trRng - trR
	transient := 0
	if trVal >= trThreshold {
		transient = 1
	}
	t.Logf("Transient flag: %d (val=0x%08x, threshold=0x%08x, r=0x%08x)",
		transient, trVal, trThreshold, trR)

	// Update range state
	if transient == 0 {
		rd.DecodeSymbol(0, trThreshold, trRng)
	} else {
		rd.DecodeSymbol(trThreshold, trRng, trRng)
	}

	t.Logf("After transient: range=0x%08x, val=0x%08x", rd.Range(), rd.Val())

	// Try to decode spread (ICDF with 5-bit precision)
	// spreadICDF = []uint8{25, 23, 2, 0}
	// This decodes symbol 0-3
	t.Log("Trying spread decode with ICDF...")
	spreadVal := rd.Val()
	spreadRng := rd.Range()
	// ft = 1 << 5 = 32
	ft := uint32(32)
	fl := spreadRng / ft
	// Symbol is determined by which interval val falls into
	sym := spreadVal / fl
	if sym >= ft {
		sym = ft - 1
	}
	// Need to invert because ICDF is decreasing
	// icdf[s] gives the cumulative probability that symbol >= s
	icdf := []uint8{25, 23, 2, 0}
	spread := -1
	for s := 0; s < len(icdf); s++ {
		if uint32(icdf[s]) <= sym {
			spread = s
			break
		}
	}
	t.Logf("Spread decision: %d (val=0x%08x, rng=0x%08x, sym_index=%d)",
		spread, spreadVal, spreadRng, sym)
}

// TestCompareEncodingSteps compares encoding steps between gopus and libopus.
func TestCompareEncodingSteps(t *testing.T) {
	// Create a range encoder and encode our sequence
	buf := make([]byte, 256)
	enc := &rangecoding.Encoder{}
	enc.Init(buf)

	t.Log("Encoding our sequence...")
	t.Logf("Initial: rng=0x%08x, val=0x%08x", enc.Range(), enc.Val())

	// Encode silence = 0 (logp=15)
	enc.EncodeBit(0, 15)
	t.Logf("After silence=0: rng=0x%08x, val=0x%08x, tell=%d",
		enc.Range(), enc.Val(), enc.Tell())

	// Encode transient = 0 (logp=3)
	enc.EncodeBit(0, 3)
	t.Logf("After transient=0: rng=0x%08x, val=0x%08x, tell=%d",
		enc.Range(), enc.Val(), enc.Tell())

	// Encode intra = 1 (logp=3)
	enc.EncodeBit(1, 3)
	t.Logf("After intra=1: rng=0x%08x, val=0x%08x, tell=%d",
		enc.Range(), enc.Val(), enc.Tell())

	// Finalize
	result := enc.Done()
	t.Logf("Result: %x (len=%d)", result, len(result))

	// Now try with prefilter flag
	t.Log("\nEncoding with prefilter flag...")
	enc2 := &rangecoding.Encoder{}
	enc2.Init(buf)

	// Encode silence = 0 (logp=15)
	enc2.EncodeBit(0, 15)
	t.Logf("After silence=0: rng=0x%08x, val=0x%08x", enc2.Range(), enc2.Val())

	// Encode prefilter = 0 (logp=1)
	enc2.EncodeBit(0, 1)
	t.Logf("After prefilter=0: rng=0x%08x, val=0x%08x", enc2.Range(), enc2.Val())

	// Encode transient = 0 (logp=3)
	enc2.EncodeBit(0, 3)
	t.Logf("After transient=0: rng=0x%08x, val=0x%08x", enc2.Range(), enc2.Val())

	// Encode intra = 1 (logp=3)
	enc2.EncodeBit(1, 3)
	t.Logf("After intra=1: rng=0x%08x, val=0x%08x", enc2.Range(), enc2.Val())

	result2 := enc2.Done()
	t.Logf("Result with prefilter: %x (len=%d)", result2, len(result2))
}
