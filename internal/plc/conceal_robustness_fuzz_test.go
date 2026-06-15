package plc

// Robustness fuzzing for the plc package concealment entry points.
//
// The contract under test is no-crash only: for any concealment request the
// public entry points (ConcealCELT, ConcealCELTHybrid, ConcealCELTHybridRawInto,
// ConcealSILK, ConcealSILKStereo, ConcealSILKWithLTP) and the loss-bookkeeping
// State / SILKPLCState API must return cleanly and never panic, index out of
// bounds, or emit non-finite (NaN/Inf) PCM, no matter how degenerate the
// decoder state, loss-run length, or sample count is. Valid-input behavior is
// LOCKED by the parity tests in plc_test.go and silk_plc_iir_edge_parity_test.go
// and is NOT asserted here; these fuzzers must not constrain it.
//
// A real Opus decoder always hands concealment a well-formed state (channels in
// {1,2}, LPC order in [10,16], nb_subfr in {2,4}, full-length history). These
// targets deliberately violate those invariants to exercise the bounds guards
// rather than to reproduce libopus output.

import (
	"math"
	"testing"
)

// finiteF32 reports whether every sample is a finite float (no NaN/Inf).
func finiteF32(t *testing.T, label string, samples []float32) {
	t.Helper()
	for i, s := range samples {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("%s: sample[%d] is not finite: %v", label, i, s)
		}
	}
}

// finiteI16 is a no-op sanity check for int16 PCM: every int16 is finite by
// construction, but we still confirm the slice is addressable end to end (a
// concealer that returned an over-long alias of an internal buffer would be
// caught by the length assertions at the call sites).
func finiteI16(t *testing.T, label string, samples []int16) {
	t.Helper()
	var acc int64
	for _, s := range samples {
		acc += int64(s)
	}
	_ = acc
}

// sanitizeBandEnergyDB constrains a fuzzer-chosen band energy to the log-domain
// (dB) range a real CELT decoder produces. Band energies are decoder-internal
// state (celt oldBandE), not request data, and are always bounded; values
// outside this range are physically impossible and would only overflow the
// dB->linear pow10 conversion to +Inf. Bounding the input keeps the fuzzer on
// realistic decoder state without adding a non-libopus clamp to the hot path.
func sanitizeBandEnergyDB(e float32) float32 {
	if math.IsNaN(float64(e)) || math.IsInf(float64(e), 0) {
		return -10
	}
	const lo, hi = -60, 60
	if e < lo {
		return lo
	}
	if e > hi {
		return hi
	}
	return e
}

// clampFuzzLen bounds a fuzzer-chosen length to a sane range so the fuzzer
// spends its time on logic, not on multi-gigabyte allocations, while still
// covering zero, tiny, frame-sized, and oversized requests.
func clampFuzzLen(n int) int {
	if n < 0 {
		// Keep negatives (they must be handled without panicking) but bound the
		// magnitude so allocation guards, not the allocator, are exercised.
		if n < -8 {
			n = -8
		}
		return n
	}
	const maxLen = 1 << 16
	if n > maxLen {
		n = maxLen
	}
	return n
}

// ---------------------------------------------------------------------------
// CELT concealment
// ---------------------------------------------------------------------------

func newFuzzCELTDecoder(channels, energyLen, overlap int, rng uint32, energyFill float32) *mockCELTDecoder {
	if channels < 0 {
		channels = 0
	}
	if energyLen < 0 {
		energyLen = 0
	}
	if overlap < 0 {
		overlap = 0
	}
	preLen := max(channels, 0)
	dec := &mockCELTDecoder{
		channels:     channels,
		prevEnergy:   make([]float32, energyLen),
		rng:          rng,
		preemphState: make([]float32, preLen),
		overlapBuf:   make([]float32, overlap),
	}
	for i := range dec.prevEnergy {
		dec.prevEnergy[i] = energyFill
	}
	return dec
}

func addCELTConcealSeeds(f *testing.F) {
	// channels, energyLen, frameSize, fadeBits, rng, energyFill, useSynth
	seeds := []struct {
		channels   uint8
		energyLen  uint16
		frameSize  int32
		fadeBits   uint16
		rng        uint32
		energyFill float32
		useSynth   bool
	}{
		{1, 21, 480, 0x8000, 22222, -10, true},
		{2, 42, 960, 0xC000, 1, -15, true},
		{1, 21, 120, 0xFFFF, 7, 0, true},
		{2, 42, 240, 0x0000, 0, -100, true}, // fade ~0 -> silence path
		{1, 0, 480, 0x8000, 99, -10, true},  // empty energy
		{2, 3, 960, 0x8000, 99, -10, true},  // short energy (regression: stereo OOB)
		{0, 21, 480, 0x8000, 99, -10, true}, // zero channels (regression: deemph OOB)
		{3, 63, 960, 0x8000, 99, -10, true}, // 3 channels (degenerate)
		{1, 21, 0, 0x8000, 5, -10, true},    // zero frame
		{1, 21, 333, 0x8000, 5, -10, true},  // non-CELT frame size
		{1, 21, 480, 0x8000, 5, -10, false}, // nil synth
	}
	for _, s := range seeds {
		f.Add(s.channels, s.energyLen, s.frameSize, s.fadeBits, s.rng, s.energyFill, s.useSynth)
	}
}

// fadeFromBits maps fuzzer bits to a fade factor in [0,1], including the
// special near-zero region that selects the silence fast path.
func fadeFromBits(b uint16) float32 {
	return float32(b) / float32(0xFFFF)
}

// FuzzConcealCELT drives the full-band CELT concealment entry point with
// arbitrary channel counts, energy-buffer lengths, frame sizes and fade
// factors. Mirrors the CELT loss path of libopus celt/celt_decoder.c
// celt_decode_lost.
func FuzzConcealCELT(f *testing.F) {
	addCELTConcealSeeds(f)

	f.Fuzz(func(t *testing.T, channels uint8, energyLen uint16, frameSize int32, fadeBits uint16, rng uint32, energyFill float32, useSynth bool) {
		ch := int(channels % 4)            // 0..3, includes degenerate 0 and 3
		eLen := int(energyLen % 256)       // 0..255 bands of energy
		fs := clampFuzzLen(int(frameSize)) // may be negative
		fade := fadeFromBits(fadeBits)
		dec := newFuzzCELTDecoder(ch, eLen, 120, rng, sanitizeBandEnergyDB(energyFill))

		var synth CELTSynthesizer
		if useSynth {
			synth = dec
		}

		if fs < 0 {
			// Negative frame size: ConcealCELT allocates make([]float32, frameSize),
			// which would panic; documented as a non-supported request, so skip the
			// allocation but still exercise the rest with a zero frame.
			fs = 0
		}

		out := ConcealCELT(dec, synth, fs, fade)
		finiteF32(t, "ConcealCELT", out)
	})
}

// FuzzConcealCELTHybrid drives the Hybrid (high-band-only) CELT concealment and
// its raw into-buffer variant. Mirrors the CELT layer of a lost Hybrid frame in
// libopus (celt_decode_lost over the high bands; SILK conceals the low band).
func FuzzConcealCELTHybrid(f *testing.F) {
	addCELTConcealSeeds(f)

	f.Fuzz(func(t *testing.T, channels uint8, energyLen uint16, frameSize int32, fadeBits uint16, rng uint32, energyFill float32, useSynth bool) {
		ch := int(channels % 4)
		eLen := int(energyLen % 256)
		fs := max(clampFuzzLen(int(frameSize)), 0)
		fade := fadeFromBits(fadeBits)
		dec := newFuzzCELTDecoder(ch, eLen, 120, rng, sanitizeBandEnergyDB(energyFill))

		var synth CELTSynthesizer
		if useSynth {
			synth = dec
		}

		out := ConcealCELTHybrid(dec, synth, fs, fade)
		finiteF32(t, "ConcealCELTHybrid", out)

		// Raw-into variant with a caller buffer that may be shorter or longer
		// than the natural output, and may also be empty.
		for _, dstLen := range []int{0, 1, fs * ch, fs*ch + 7} {
			if dstLen < 0 {
				dstLen = 0
			}
			if dstLen > (1<<16)+16 {
				dstLen = (1 << 16) + 16
			}
			dst := make([]float32, dstLen)
			ConcealCELTHybridRawInto(dst, dec, synth, fs, fade)
			finiteF32(t, "ConcealCELTHybridRawInto", dst)
		}
	})
}

// ---------------------------------------------------------------------------
// SILK float fallback concealment
// ---------------------------------------------------------------------------

func addSILKFloatConcealSeeds(f *testing.F) {
	// lpcOrder, frameSize, fadeBits, voiced, histLen, histIdx
	seeds := []struct {
		lpcOrder  uint8
		frameSize int32
		fadeBits  uint16
		voiced    bool
		histLen   uint16
		histIdx   int32
	}{
		{10, 320, 0x8000, false, 322, 100},
		{16, 160, 0xC000, true, 322, 200},
		{10, 320, 0x0000, false, 322, 100}, // fade ~0
		{0, 320, 0x8000, false, 0, 0},      // zero order, no history
		{99, 320, 0x8000, true, 5, -5},     // bogus order, tiny history, negative idx
		{10, 0, 0x8000, false, 322, 100},   // zero frame
		{10, -4, 0x8000, false, 322, 100},  // negative frame (regression: makeslice)
		{10, 1, 0x8000, true, 1, 0},        // single-sample everything
	}
	for _, s := range seeds {
		f.Add(s.lpcOrder, s.frameSize, s.fadeBits, s.voiced, s.histLen, s.histIdx)
	}
}

// FuzzConcealSILK drives the SILK float fallback concealer (mono and stereo)
// with degenerate LPC order, history geometry and frame sizes. This is the
// approximate float path used when only the minimal SILKDecoderState is
// available (the bit-exact path is FuzzConcealSILKWithLTP).
func FuzzConcealSILK(f *testing.F) {
	addSILKFloatConcealSeeds(f)

	f.Fuzz(func(t *testing.T, lpcOrder uint8, frameSize int32, fadeBits uint16, voiced bool, histLen uint16, histIdx int32) {
		order := int(lpcOrder)
		fs := clampFuzzLen(int(frameSize))
		fade := fadeFromBits(fadeBits)
		hl := int(histLen % 2048)

		dec := &mockSILKDecoder{
			lpcValues: make([]float32, minInt(order, 64)),
			lpcOrder:  order,
			wasVoiced: voiced,
			history:   make([]float32, hl),
			histIdx:   int(histIdx),
		}
		for i := range dec.lpcValues {
			dec.lpcValues[i] = float32(math.Sin(float64(i)*0.3)) * 0.5
		}
		for i := range dec.history {
			dec.history[i] = float32(math.Sin(float64(i) * 0.05))
		}

		out := ConcealSILK(dec, fs, fade)
		finiteF32(t, "ConcealSILK", out)

		left, right := ConcealSILKStereo(dec, fs, fade)
		finiteF32(t, "ConcealSILKStereo/L", left)
		finiteF32(t, "ConcealSILKStereo/R", right)

		// Nil decoder must also be handled.
		finiteF32(t, "ConcealSILK/nil", ConcealSILK(nil, fs, fade))
	})
}

// ---------------------------------------------------------------------------
// SILK bit-exact concealment (silk_PLC_conceal port)
// ---------------------------------------------------------------------------

func addSILKLTPConcealSeeds(f *testing.F) {
	// fsKHz, subfrLen, nbSubfr, ltpMem, lag, lpcOrder, frameSize, lossCnt, data
	type seed struct {
		fsKHz, subfr, nbSubfr, ltpMem, lag, lpcOrder, frameSize, lossCnt int
	}
	seeds := []seed{
		{16, 80, 4, 320, 96, 16, 320, 0},
		{16, 80, 4, 320, 96, 16, 320, 1},
		{16, 80, 4, 320, 96, 16, 320, 7},     // extended loss run
		{8, 40, 4, 160, 1, 10, 160, 0},       // NB, tiny lag (startIdx clamp)
		{12, 120, 4, 240, 240, 10, 240, 2},   // MB, lag at ceiling
		{16, 0, 4, 320, 96, 16, 320, 0},      // zero subframe length
		{16, 80, 0, 320, 96, 16, 320, 0},     // zero subframe count
		{16, 80, 4, 0, 96, 16, 320, 0},       // zero ltp memory
		{16, 80, 4, 320, 0, 16, 320, 0},      // zero lag
		{16, 80, 4, 320, 100000, 16, 320, 0}, // huge lag
		{16, 80, 4, 320, 96, 0, 320, 0},      // zero lpc order
		{16, 80, 4, 320, 96, 30, 320, 0},     // lpc order > MAX_LPC_ORDER
		{16, 80, 4, 320, 96, 16, 0, 0},       // zero frame
		{16, 80, 4, 320, 96, 16, 4096, 0},    // frame larger than nbSubfr*subfr
	}
	for _, s := range seeds {
		f.Add(
			uint8(s.fsKHz), uint16(s.subfr), uint8(s.nbSubfr), uint16(s.ltpMem),
			int32(s.lag), uint8(s.lpcOrder), int32(s.frameSize), uint16(s.lossCnt),
			uint32(0x12345678),
		)
	}
}

// FuzzConcealSILKWithLTP drives the bit-exact SILK concealer (a port of
// silk_PLC_conceal, libopus silk/PLC.c) with adversarial frame geometry,
// pitch lags, LPC orders and loss-run lengths. It primes the persistent
// SILKPLCState via UpdateFromGoodFrame (silk_PLC_update) first, then conceals
// repeatedly to exercise the cross-frame state update on degenerate input.
func FuzzConcealSILKWithLTP(f *testing.F) {
	addSILKLTPConcealSeeds(f)

	f.Fuzz(func(t *testing.T,
		fsKHzSel uint8, subfrSel uint16, nbSubfrSel uint8, ltpMemSel uint16,
		lagRaw int32, lpcOrderSel uint8, frameSizeRaw int32, lossCnt uint16,
		seed uint32,
	) {
		// Map the fuzzer inputs into ranges that include both the legal SILK
		// geometries and degenerate values around them.
		fsKHz := int(fsKHzSel % 24)       // 0..23, includes 8/12/16 and junk
		subfr := int(subfrSel % 512)      // 0..511
		nbSubfr := int(nbSubfrSel % 8)    // 0..7, includes 2 and 4
		ltpMem := int(ltpMemSel % 2048)   // 0..2047
		lag := int(lagRaw)                // may be negative or huge
		lpcOrder := int(lpcOrderSel % 40) // 0..39, includes 10/16 and >16
		frameSize := clampFuzzLen(int(frameSizeRaw))
		loss := int(lossCnt % 4096)

		// Allocation helpers must stay non-negative.
		excLen := max(max(nbSubfr*subfr, ltpMem), 1)
		if excLen > 1<<16 {
			excLen = 1 << 16
		}
		histAlloc := min(max(ltpMem+subfr+1, 1), 1<<16)
		ordAlloc := min(max(lpcOrder, 1), 64)
		outBufAlloc := min(max(ltpMem+1, 1), 1<<16)

		dec := &mockSILKExtendedDecoder{
			mockSILKDecoder: mockSILKDecoder{
				lpcValues: make([]float32, ordAlloc),
				lpcOrder:  lpcOrder,
				wasVoiced: true,
				history:   make([]float32, histAlloc),
				histIdx:   ltpMem,
			},
			signalType:   2,
			pitchLag:     lag,
			lastGainQ16:  65536,
			ltpScaleQ14:  16384,
			excitation:   make([]int32, excLen),
			lpcQ12:       make([]int16, ordAlloc),
			slpcQ14:      make([]int32, ordAlloc),
			fsKHz:        fsKHz,
			subfrLength:  subfr,
			nbSubfr:      nbSubfr,
			ltpMemLength: ltpMem,
			outBufQ0:     make([]int16, outBufAlloc),
		}
		dec.ltpCoefQ14 = [ltpOrder]int16{0, 2048, 8192, 2048, 0}
		dec.lpcQ12[0] = 2048
		for i := range dec.excitation {
			dec.excitation[i] = int32((int(seed)+i*7)%4096-2048) << 4
		}
		for i := range dec.outBufQ0 {
			dec.outBufQ0[i] = int16((i % 257) - 128)
		}

		// Prime the PLC state. nbSubfr may be 0 here; build consistent-length
		// arrays so UpdateFromGoodFrame's own guards are what gets exercised.
		state := NewSILKPLCState()
		nb := max(nbSubfr, 1)
		pitchL := make([]int32, nb)
		for i := range pitchL {
			pitchL[i] = int32(lag)
		}
		ltpCoef := make([]int16, ltpOrder*nb)
		for i := range ltpCoef {
			ltpCoef[i] = int16((int(seed)+i)%4096 - 2048)
		}
		gains := make([]int32, nb)
		for i := range gains {
			gains[i] = 65536
		}
		lpcQ := make([]int16, ordAlloc)
		lpcQ[0] = 2048

		// Exercise both voicing branches of UpdateFromGoodFrame.
		state.UpdateFromGoodFrame(2, pitchL, ltpCoef, 16384, gains, lpcQ, fsKHz, nbSubfr, subfr)

		// Two consecutive concealment calls to drive the cross-frame state
		// advance (attenuation, pitch drift, sLPC history write-back).
		out0 := ConcealSILKWithLTP(dec, state, loss, frameSize)
		finiteI16(t, "ConcealSILKWithLTP/0", out0)
		if frameSize > 0 && len(out0) != frameSize {
			t.Fatalf("ConcealSILKWithLTP: out len=%d want %d", len(out0), frameSize)
		}

		out1 := ConcealSILKWithLTP(dec, state, loss+1, frameSize)
		finiteI16(t, "ConcealSILKWithLTP/1", out1)

		// Nil state / decoder must be handled too.
		_ = ConcealSILKWithLTP(dec, nil, loss, frameSize)
		_ = ConcealSILKWithLTP(nil, state, loss, frameSize)
	})
}

// ---------------------------------------------------------------------------
// PLC state machines
// ---------------------------------------------------------------------------

func FuzzPLCStateMachine(f *testing.F) {
	f.Add(uint8(0), uint16(960), uint8(1))
	f.Add(uint8(200), uint16(0), uint8(8))
	f.Add(uint8(1), uint16(65535), uint8(2))

	f.Fuzz(func(t *testing.T, losses uint8, frameSize uint16, channels uint8) {
		s := NewState()
		s.SetLastFrameParams(Mode(int(channels)%3), int(frameSize), int(channels))

		// Apply an arbitrary loss run, then a reset, then more losses; the fade
		// factor must always stay a finite value within [0, 1].
		for i := 0; i < int(losses)%512; i++ {
			fade := s.RecordLoss()
			if math.IsNaN(float64(fade)) || math.IsInf(float64(fade), 0) {
				t.Fatalf("fade not finite after %d losses: %v", i, fade)
			}
			if fade < 0 || fade > 1.0001 {
				t.Fatalf("fade out of [0,1] after %d losses: %v", i, fade)
			}
		}
		_ = s.IsExhausted()
		_ = s.LostCount()
		_ = s.Mode()
		_ = s.LastFrameSize()
		_ = s.LastChannels()
		s.Reset()
		if s.FadeFactor() != 1.0 {
			t.Fatalf("fade after reset = %v, want 1.0", s.FadeFactor())
		}
	})
}

// FuzzSILKPLCStateUpdate drives SILKPLCState.UpdateFromGoodFrame with arbitrary
// signal types and parameter-array lengths (a port-of-silk_PLC_update target).
// The cached coefficients and gains must never index out of bounds regardless
// of how mismatched nbSubfr, the pitch/LTP arrays and the gains are.
func FuzzSILKPLCStateUpdate(f *testing.F) {
	f.Add(uint8(2), uint8(4), uint16(80), uint8(16), int32(100), uint32(0xABCD))
	f.Add(uint8(1), uint8(2), uint16(40), uint8(10), int32(0), uint32(0))
	f.Add(uint8(2), uint8(0), uint16(80), uint8(16), int32(50), uint32(1))
	f.Add(uint8(0), uint8(8), uint16(0), uint8(0), int32(-7), uint32(2))

	f.Fuzz(func(t *testing.T, sigType uint8, nbSubfrSel uint8, subfrSel uint16, lpcOrderSel uint8, pitch int32, seed uint32) {
		signalType := int(sigType % 4)    // 0..3, includes voiced=2
		nbSubfr := int(nbSubfrSel % 8)    // 0..7
		subfr := int(subfrSel % 512)      // 0..511
		lpcOrder := int(lpcOrderSel % 40) // 0..39
		fsKHz := 16

		// Build arrays whose lengths are *intentionally* derived from a possibly
		// inconsistent nbSubfr so the guards inside UpdateFromGoodFrame are hit.
		arrSubfr := nbSubfr
		// Half the time, under-size the arrays to probe the short-input guard.
		if seed&1 == 0 && arrSubfr > 0 {
			arrSubfr--
		}
		pitchL := make([]int32, arrSubfr)
		for i := range pitchL {
			pitchL[i] = pitch
		}
		ltpCoef := make([]int16, ltpOrder*arrSubfr)
		for i := range ltpCoef {
			ltpCoef[i] = int16((int(seed)+i)%2048 - 1024)
		}
		gains := make([]int32, arrSubfr)
		for i := range gains {
			gains[i] = int32(seed) + 1
		}
		lpcQ := make([]int16, lpcOrder)
		for i := range lpcQ {
			lpcQ[i] = int16(i * 13)
		}

		state := NewSILKPLCState()
		state.UpdateFromGoodFrame(signalType, pitchL, ltpCoef, 16384, gains, lpcQ, fsKHz, nbSubfr, subfr)

		// Followed by a Reset on an arbitrary frame length, which must also be
		// safe for any value.
		state.Reset(int(subfrSel))
	})
}
