package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// This file fuzzes the SILK decode path against malformed / truncated / hostile
// input. The contract under test is robustness only: on any byte slice and any
// (bandwidth, frame-size, vad) combination the public decode entry points must
// return cleanly (value + optional error) and never panic, index out of bounds,
// or emit non-finite PCM. Valid-input behaviour is locked by the parity tests
// and is NOT asserted here.
//
// The range decoder (rangecoding) is designed to keep returning symbols past
// the end of the buffer rather than failing, so a malformed SILK frame decodes
// to garbage indices that must still be bounded by the SILK decoder itself.

// silkDecodeFuzzBandwidths are the SILK-internal bandwidths plus two
// out-of-range values to exercise the bandwidth-validation guards.
var silkDecodeFuzzBandwidths = []Bandwidth{
	BandwidthNarrowband,
	BandwidthMediumband,
	BandwidthWideband,
	Bandwidth(3), // SWB: rejected by Decode/DecodeStereo as non-SILK.
	Bandwidth(255),
}

// silkDecodeFuzzFrameSizes covers every legal 48 kHz SILK frame size plus a
// few values that do not map to a SILK duration (0, tiny, huge, odd).
var silkDecodeFuzzFrameSizes = []int{
	0,
	1,
	480,  // 10ms @48k
	960,  // 20ms @48k
	1920, // 40ms @48k
	2880, // 60ms @48k
	123,  // unmapped
	100000,
}

func requireFiniteSILKPCM(t *testing.T, samples []float32) {
	t.Helper()
	for i, s := range samples {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("sample[%d] is not finite: %v", i, s)
		}
	}
}

func addSILKDecodeSeeds(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		{0x00},
		{0xFF},
		{0x00, 0x00, 0x00, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF},
		{0x80, 0x01, 0x02, 0x03, 0x04, 0x05},
		make([]byte, 64),  // all-zero medium
		make([]byte, 320), // all-zero large
	}
	// A pseudo-entropy-coded buffer like createMockRangeDecoder uses.
	mock := make([]byte, 128)
	for i := range mock {
		mock[i] = byte(i * 7)
	}
	seeds = append(seeds, mock)

	for _, s := range seeds {
		f.Add(s, uint8(2), uint8(3), true) // WB / 20ms-ish / vad
		f.Add(s, uint8(0), uint8(1), false)
	}

	// Regression: a 1-byte malformed packet decoded as WB primes the decoder
	// state, then the follow-up nil-data PLC at a 60 ms (2880-sample) frame size
	// requested an oversized single-call concealment that overflowed the
	// fixed-size CNG synthesis buffer (cng.go silk_CNG). bwSel 2 -> WB,
	// fsSel 5 -> 2880.
	f.Add([]byte{0x00}, uint8(2), uint8(5), true)
	// Regression: stereo PLC with frameSizeSamples 0 mapped to zero native
	// samples and reached silkStereoMSToLR with frameLength 0, reading past the
	// mid/side history. fsSel 0 -> frame size 0.
	f.Add([]byte{0x00}, uint8(2), uint8(0), true)
}

// FuzzSILKDecodeMonoNeverPanics drives Decoder.Decode (the primary public mono
// SILK entry point) with arbitrary bytes and parameters. It also issues a
// follow-up nil-data PLC decode to exercise the loss path on whatever state the
// malformed packet left behind.
//
// Mirrors the public SILK decode entry of libopus silk/dec_API.c silk_Decode.
func FuzzSILKDecodeMonoNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, vad bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		fs := silkDecodeFuzzFrameSizes[int(fsSel)%len(silkDecodeFuzzFrameSizes)]

		d := NewDecoder()
		out, err := d.Decode(data, bw, fs, vad)
		if err == nil {
			requireFiniteSILKPCM(t, out)
		}

		// Follow-up loss concealment on the same decoder state.
		plcOut, plcErr := d.Decode(nil, bw, fs, vad)
		if plcErr == nil {
			requireFiniteSILKPCM(t, plcOut)
		}
	})
}

// FuzzSILKDecodeStereoNeverPanics drives the stereo public entry points
// (DecodeStereo, DecodeStereoToMono, DecodeMonoToStereo) with malformed bytes.
//
// Mirrors the stereo branch of libopus silk/dec_API.c silk_Decode (mid/side
// channel decode + MS->LR unmix in silk/stereo_MS_to_LR.c).
func FuzzSILKDecodeStereoNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, vad bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		fs := silkDecodeFuzzFrameSizes[int(fsSel)%len(silkDecodeFuzzFrameSizes)]

		d := NewDecoder()
		if out, err := d.DecodeStereo(data, bw, fs, vad); err == nil {
			requireFiniteSILKPCM(t, out)
		}

		d2 := NewDecoder()
		if out, err := d2.DecodeStereoToMono(data, bw, fs, vad); err == nil {
			requireFiniteSILKPCM(t, out)
		}

		d3 := NewDecoder()
		if out, err := d3.DecodeMonoToStereo(data, bw, fs, vad, false); err == nil {
			requireFiniteSILKPCM(t, out)
		}
	})
}

// FuzzSILKDecodeFECNeverPanics drives the LBRR/FEC recovery path
// (Decoder.DecodeFEC and HasLBRR) with malformed bytes. FEC decoding reads the
// LBRR flag header then either decodes a redundant frame or runs concealment,
// so out-of-range gains / pitch / NLSF indices in the LBRR payload must stay
// bounded.
//
// Mirrors libopus silk/dec_API.c silk_Decode with lostFlag=FLAG_DECODE_LBRR
// (LBRR frame decode in silk/decode_frame.c).
func FuzzSILKDecodeFECNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, stereo bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		fs := silkDecodeFuzzFrameSizes[int(fsSel)%len(silkDecodeFuzzFrameSizes)]
		channels := 1
		if stereo {
			channels = 2
		}

		d := NewDecoder()
		// HasLBRR parses the same header and must never panic.
		_ = d.HasLBRR(data, bw, fs)

		if out, err := d.DecodeFEC(data, bw, fs, stereo, channels); err == nil {
			requireFiniteSILKPCM(t, out)
		}
	})
}

// FuzzSILKDecodeStereoEncodedNeverPanics drives the package-level
// DecodeStereoEncoded helper, which is the convenience wrapper used to decode a
// standalone SILK stereo bitstream.
func FuzzSILKDecodeStereoEncodedNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte, bwSel, fsSel uint8, _ bool) {
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]

		left, right, err := DecodeStereoEncoded(data, bw)
		if err == nil {
			requireFiniteSILKPCM(t, left)
			requireFiniteSILKPCM(t, right)
		}
	})
}

// FuzzSILKDecodeFrameRawNeverPanics drives the lower-level frame entry points
// that operate directly on a caller-provided range decoder over the full
// duration matrix. These bypass the API-rate frame-size mapping, so they
// exercise the native-rate decode core (silk_decode_core, NLSF2A, LTP/LPC
// synthesis) against malformed range-coded bits more directly.
//
// Mirrors libopus silk/decode_frame.c silk_decode_frame for every supported
// (bandwidth, nb_subfr) combination.
func FuzzSILKDecodeFrameRawNeverPanics(f *testing.F) {
	addSILKDecodeSeeds(f)

	durations := []FrameDuration{Frame10ms, Frame20ms, Frame40ms, Frame60ms, FrameDuration(99)}

	f.Fuzz(func(t *testing.T, data []byte, bwSel, durSel uint8, vad bool) {
		if len(data) == 0 {
			data = []byte{0}
		}
		if len(data) > 8192 {
			data = data[:8192]
		}
		bw := silkDecodeFuzzBandwidths[int(bwSel)%len(silkDecodeFuzzBandwidths)]
		dur := durations[int(durSel)%len(durations)]

		// Mono frame, with-delay variant.
		func() {
			d := NewDecoder()
			var rd rangecoding.Decoder
			rd.Init(data)
			if out, err := d.DecodeFrame(&rd, bw, dur, vad); err == nil {
				requireFiniteSILKPCM(t, out)
			}
		}()

		// Mono frame, raw (no-delay) variant.
		func() {
			d := NewDecoder()
			var rd rangecoding.Decoder
			rd.Init(data)
			if out, err := d.DecodeFrameRaw(&rd, bw, dur, vad); err == nil {
				requireFiniteSILKPCM(t, out)
			}
		}()

		// Stereo frame.
		func() {
			d := NewDecoder()
			var rd rangecoding.Decoder
			rd.Init(data)
			if l, r, err := d.DecodeStereoFrame(&rd, bw, dur, vad); err == nil {
				requireFiniteSILKPCM(t, l)
				requireFiniteSILKPCM(t, r)
			}
		}()
	})
}

// FuzzSILKDecodeStatefulSequence drives a sequence of decode calls on a single
// persistent decoder, interleaving good-frame decode attempts, loss
// concealment, FEC recovery and bandwidth switches. Cross-frame state
// (prevNLSF, gains, pitch, stereo predictors, PLC history) is the area most
// likely to hide an out-of-bounds on malformed input, so this exercises it.
func FuzzSILKDecodeStatefulSequence(f *testing.F) {
	f.Add([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, uint8(0xb1))
	f.Add(make([]byte, 200), uint8(0x00))
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, uint8(0xff))

	f.Fuzz(func(t *testing.T, data []byte, ops uint8) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		d := NewDecoder()

		// Derive up to 8 operations from the bits of ops; each step picks a
		// bandwidth and an action so the fuzzer can build hostile sequences.
		chunk := len(data) / 8
		if chunk == 0 {
			chunk = len(data)
		}
		for step := 0; step < 8; step++ {
			bw := silkDecodeFuzzBandwidths[step%3] // NB/MB/WB only (valid set)
			var frame []byte
			if chunk > 0 {
				start := (step * chunk) % (len(data) + 1)
				end := start + chunk
				if end > len(data) {
					end = len(data)
				}
				if start <= end {
					frame = data[start:end]
				}
			}
			action := (ops >> uint(step)) & 1
			switch action {
			case 0:
				if out, err := d.Decode(frame, bw, 960, true); err == nil {
					requireFiniteSILKPCM(t, out)
				}
			default:
				if out, err := d.Decode(nil, bw, 960, true); err == nil {
					requireFiniteSILKPCM(t, out)
				}
			}
		}
	})
}
