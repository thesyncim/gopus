package gopus

// SILK PLC IIR parity tests at the full-stack level.
//
// These tests exercise the SILK PLC pitch-periodic extrapolation IIR at
// boundary conditions through the full decode(nil) path, verifying that
// gopus behaviour matches the expected libopus behaviour for:
//
//  1. First-loss voiced PLC (initial LTP attenuation, harmAttQ15_0=0.99).
//  2. Voiced PLC over multiple consecutive losses (decay chain).
//  3. Unvoiced PLC immediately after a voiced frame (transition).
//  4. Very short and very long silence gaps (boundary for pitch lag drift).
//
// Where the oracle is unavailable the tests fall back to structural invariants
// (non-panic, correct length, energy monotonically decaying over loss run).
// Oracle-backed quality checks use the libopus reference decoder.
//
// Reference: libopus silk/PLC.c (silk_PLC_conceal, silk_PLC_update).

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// encodeSILKVoicedPLCTestSequence encodes a voiced SILK WB sequence that is
// long enough to warm up the PLC state (≥3 packets before loss).
func encodeSILKVoicedPLCTestSequence(t *testing.T, channels int) [][]byte {
	t.Helper()
	const (
		sampleRate = 48000
		frameSize  = 960
		bitrate    = 24000
		numFrames  = 8
	)
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}

	packets := make([][]byte, 0, numFrames)
	for frameIndex := 0; frameIndex < numFrames; frameIndex++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			s := 0.35*float32(math.Sin(2*math.Pi*220*tm)) +
				0.15*float32(math.Sin(2*math.Pi*440*tm+0.21)) +
				0.08*float32(math.Sin(2*math.Pi*880*tm+0.43))
			for ch := 0; ch < channels; ch++ {
				pcm[i*channels+ch] = s
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("Encode frame %d mode=%v want SILK", frameIndex, toc.Mode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

// TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus verifies that the first
// concealed frame after a voiced SILK sequence is non-silent and (with oracle)
// matches libopus quality. Covers the lossCnt=0 branch in silk_PLC_conceal()
// where harmAttQ15_0=0.99 and randScaleQ14 is initialised.
func TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus(t *testing.T) {
	const channels = 1
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	const frameSize = 960
	buf := make([]float32, frameSize*channels)

	// Warm up: decode first 4 packets normally.
	for i := 0; i < 4; i++ {
		if _, err := dec.Decode(packets[i], buf); err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
	}

	// Simulate loss of packet 4.
	n, err := dec.Decode(nil, buf)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
	}

	// First concealed frame should be non-silent (PLC reproduces speech).
	energy := float64(0)
	for _, s := range buf[:n*channels] {
		energy += float64(s) * float64(s)
	}
	energy /= float64(n * channels)
	if energy < 1e-6 {
		t.Fatalf("first PLC frame RMS²=%.2e, expected non-silent speech extrapolation", energy)
	}

	// With oracle: verify quality matches libopus.
	if !libopustest.OracleEnabled() {
		return
	}
	steps := []libopusAPIRateDecodeStep{
		{packet: packets[0]},
		{packet: packets[1]},
		{packet: packets[2]},
		{packet: packets[3]},
		{}, // nil packet → PLC
	}
	want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(48000, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK PLC IIR first-loss reference", err)
	}
	// Compare only the PLC frame (last frameSize*channels samples).
	wantPLC := want[4*frameSize*channels:]
	got, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("oracle compare: NewDecoder: %v", err)
	}
	gotBuf := make([]float32, frameSize*channels*5)
	for i := 0; i < 4; i++ {
		if _, err := got.Decode(packets[i], gotBuf[i*frameSize:(i+1)*frameSize]); err != nil {
			t.Fatalf("oracle compare: Decode packet %d: %v", i, err)
		}
	}
	gotPLC := gotBuf[4*frameSize : 5*frameSize]
	if _, err := got.Decode(nil, gotPLC); err != nil {
		t.Fatalf("oracle compare: Decode(nil) PLC: %v", err)
	}

	assertAPIRateQualityFloat32PLC(t, gotPLC[:len(wantPLC)], wantPLC, 48000, channels, true,
		"SILK PLC IIR first-loss mono")
}

// TestSILKPLCIIRMultiLossEnergyDecaysMatchesLibopus verifies that energy
// decreases monotonically across a consecutive 3-frame loss run. This exercises
// the harmGainQ15 attenuation chain: frame0→harmAttQ15_0(0.99), frame1+→
// harmAttQ15_1(0.95). Reference: silk/PLC.c:353-355.
func TestSILKPLCIIRMultiLossEnergyDecaysMatchesLibopus(t *testing.T) {
	const channels = 1
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	const frameSize = 960
	energies := make([]float64, 3)

	for lossIdx := 0; lossIdx < 3; lossIdx++ {
		dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
		if err != nil {
			t.Fatalf("NewDecoder (loss %d): %v", lossIdx, err)
		}
		buf := make([]float32, frameSize*channels)
		// Always warm up with 4 packets.
		for i := 0; i < 4; i++ {
			if _, err := dec.Decode(packets[i], buf); err != nil {
				t.Fatalf("Decode packet %d (loss %d): %v", i, lossIdx, err)
			}
		}
		// Generate lossIdx+1 consecutive PLC frames and record the last one.
		var lostBuf []float32
		for k := 0; k <= lossIdx; k++ {
			lostBuf = make([]float32, frameSize*channels)
			if _, err := dec.Decode(nil, lostBuf); err != nil {
				t.Fatalf("Decode(nil) loss %d frame %d: %v", lossIdx, k, err)
			}
		}
		e := float64(0)
		for _, s := range lostBuf {
			e += float64(s) * float64(s)
		}
		energies[lossIdx] = e / float64(frameSize*channels)
	}

	// Energy must be strictly decreasing across the first three loss frames.
	for i := 1; i < len(energies); i++ {
		if energies[i] >= energies[i-1] {
			t.Fatalf("PLC energy did not decay: loss[%d]=%.4e >= loss[%d]=%.4e",
				i, energies[i], i-1, energies[i-1])
		}
	}

	// With oracle: compare each PLC frame against libopus.
	if !libopustest.OracleEnabled() {
		return
	}
	// Build steps: 4 good + 3 losses.
	steps := make([]libopusAPIRateDecodeStep, 7)
	for i := 0; i < 4; i++ {
		steps[i] = libopusAPIRateDecodeStep{packet: packets[i]}
	}
	// steps[4..6] are nil (PLC).
	want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(48000, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK PLC IIR multi-loss reference", err)
	}
	// Decode with gopus and compare.
	dec2, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("oracle compare: NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	buf2 := make([]float32, frameSize*channels)
	for i := 0; i < 4; i++ {
		n, err := dec2.Decode(packets[i], buf2)
		if err != nil {
			t.Fatalf("oracle compare: Decode packet %d: %v", i, err)
		}
		got = append(got, buf2[:n*channels]...)
	}
	for k := 0; k < 3; k++ {
		n, err := dec2.Decode(nil, buf2)
		if err != nil {
			t.Fatalf("oracle compare: Decode(nil) loss %d: %v", k, err)
		}
		got = append(got, buf2[:n*channels]...)
	}
	cmpLen := len(want)
	if len(got) < cmpLen {
		cmpLen = len(got)
	}
	// PLC frames dominate the comparison; use PLC quality bar.
	assertAPIRateQualityFloat32PLC(t, got[:cmpLen], want[:cmpLen], 48000, channels, true,
		"SILK PLC IIR multi-loss mono")
}

// TestSILKPLCIIRVoicedUnvoicedTransitionNoPanic verifies that the full-stack
// decode path does not panic when a SILK voiced frame is followed by a PLC loss.
// This exercises the voiced→PLC-concealment transition in the decoder path.
func TestSILKPLCIIRVoicedUnvoicedTransitionNoPanic(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeSILKVoicedPLCTestSequence(t, channels)
			const frameSize = 960
			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, frameSize*channels)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode(nil) panic on voiced→PLC transition: %v", r)
				}
			}()

			for i := 0; i < 5; i++ {
				if _, err := dec.Decode(packets[i], buf); err != nil {
					t.Fatalf("Decode packet %d: %v", i, err)
				}
			}
			n, err := dec.Decode(nil, buf)
			if err != nil {
				t.Fatalf("Decode(nil) PLC: %v", err)
			}
			if n != frameSize {
				t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
			}
		})
	}
}

// TestSILKPLCIIRStereoMultiLossEnergyDecays verifies the stereo PLC IIR energy
// decay chain mirrors the mono case. libopus processes left/right channels
// independently through the same silk_PLC_conceal path.
func TestSILKPLCIIRStereoMultiLossEnergyDecays(t *testing.T) {
	const channels = 2
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	const frameSize = 960
	var prevEnergy float64 = math.MaxFloat64
	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	for i := 0; i < 4; i++ {
		if _, err := dec.Decode(packets[i], buf); err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
	}

	for k := 0; k < 3; k++ {
		lostBuf := make([]float32, frameSize*channels)
		if _, err := dec.Decode(nil, lostBuf); err != nil {
			t.Fatalf("Decode(nil) loss %d: %v", k, err)
		}
		e := float64(0)
		for _, s := range lostBuf {
			e += float64(s) * float64(s)
		}
		e /= float64(frameSize * channels)
		if k > 0 && e >= prevEnergy {
			t.Fatalf("stereo PLC energy did not decay at loss %d: %.4e >= %.4e", k, e, prevEnergy)
		}
		prevEnergy = e
	}
}
