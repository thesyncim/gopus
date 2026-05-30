//go:build gopus_custom

package custom_test

import (
	"errors"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt/custom"
)

// generateSine generates a sine wave at freqHz, numSamples long, at sampleRate.
func generateSine(freqHz, sampleRate float64, numSamples int) []float32 {
	out := make([]float32, numSamples)
	for i := range out {
		t := float64(i) / sampleRate
		out[i] = float32(0.5 * math.Sin(2*math.Pi*freqHz*t))
	}
	return out
}

// generateSineStereo generates interleaved stereo sine wave.
func generateSineStereo(freqL, freqR, sampleRate float64, samplesPerCh int) []float32 {
	out := make([]float32, samplesPerCh*2)
	for i := 0; i < samplesPerCh; i++ {
		t := float64(i) / sampleRate
		out[2*i] = float32(0.5 * math.Sin(2*math.Pi*freqL*t))
		out[2*i+1] = float32(0.5 * math.Sin(2*math.Pi*freqR*t))
	}
	return out
}

// hasEnergy returns true when the rms of samples exceeds threshold.
func hasEnergy(samples []float32, threshold float64) bool {
	if len(samples) == 0 {
		return false
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum/float64(len(samples))) > threshold
}

// TestRoundTripStandardMono verifies encode→decode for the standard 48 kHz 20ms
// mono mode, which must be byte-identical to libopus.
func TestRoundTripStandardMono(t *testing.T) {
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	if !mode.IsStandard() {
		t.Fatal("48000/960 mode must be standard")
	}

	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	pcm := generateSine(440.0, 48000, 960)
	data, err := enc.EncodeFloat(pcm, 200)
	if err != nil {
		t.Fatalf("EncodeFloat: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("encoded data is empty")
	}
	t.Logf("encoded %d samples → %d bytes", 960, len(data))

	decoded, err := dec.DecodeFloat(data, 960)
	if err != nil {
		t.Fatalf("DecodeFloat: %v", err)
	}
	if len(decoded) != 960 {
		t.Fatalf("decoded length %d, want 960", len(decoded))
	}
	if !hasEnergy(decoded, 1e-4) {
		t.Error("decoded audio has no energy")
	}
}

// TestRoundTripStandardStereo verifies stereo encode→decode for 48 kHz 20ms.
func TestRoundTripStandardStereo(t *testing.T) {
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}

	enc, err := custom.NewEncoder(mode, 2)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 2)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	pcm := generateSineStereo(440, 660, 48000, 960)
	data, err := enc.EncodeFloat(pcm, 250)
	if err != nil {
		t.Fatalf("EncodeFloat: %v", err)
	}
	t.Logf("stereo encoded %d samples/ch → %d bytes", 960, len(data))

	decoded, err := dec.DecodeFloat(data, 960)
	if err != nil {
		t.Fatalf("DecodeFloat: %v", err)
	}
	if len(decoded) != 960*2 {
		t.Fatalf("decoded length %d, want %d", len(decoded), 960*2)
	}
	if !hasEnergy(decoded, 1e-4) {
		t.Error("decoded stereo audio has no energy")
	}
}

// TestRoundTripStandardAllFrameSizes exercises all four standard frame sizes.
func TestRoundTripStandardAllFrameSizes(t *testing.T) {
	for _, sz := range []int{120, 240, 480, 960} {
		sz := sz
		t.Run(sizeLabel(sz), func(t *testing.T) {
			mode, err := custom.NewMode(48000, sz)
			if err != nil {
				t.Fatalf("NewMode: %v", err)
			}
			if !mode.IsStandard() {
				t.Fatalf("48000/%d must be standard", sz)
			}
			enc, err := custom.NewEncoder(mode, 1)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			dec, err := custom.NewDecoder(mode, 1)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcm := generateSine(440, 48000, sz)
			data, err := enc.EncodeFloat(pcm, 200)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			decoded, err := dec.DecodeFloat(data, sz)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if len(decoded) != sz {
				t.Fatalf("decoded length %d, want %d", len(decoded), sz)
			}
			if !hasEnergy(decoded, 1e-4) {
				t.Error("decoded audio has no energy")
			}
		})
	}
}

// TestRoundTripNonStandardMono verifies that a non-standard mode (16 kHz, 160
// samples = 10ms) builds a valid CustomMode but declines encode/decode with
// ErrNonStandard, rather than silently producing a non-conformant bitstream.
func TestRoundTripNonStandardMono(t *testing.T) {
	// 16 kHz, 160 samples = 10ms frame.
	mode, err := custom.NewMode(16000, 160)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	if mode.IsStandard() {
		t.Fatal("16000/160 must not be standard")
	}
	t.Logf("mode: Fs=%d frameSize=%d maxLM=%d nbEBands=%d overlap=%d",
		mode.Fs, mode.FrameSize, mode.MaxLM, mode.NbEBands, mode.Overlap)

	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	pcm := generateSine(300, 16000, 160)
	if _, err := enc.EncodeFloat(pcm, 80); !errors.Is(err, custom.ErrNonStandard) {
		t.Fatalf("EncodeFloat: err = %v, want ErrNonStandard", err)
	}
	if _, err := dec.DecodeFloat(make([]byte, 8), 160); !errors.Is(err, custom.ErrNonStandard) {
		t.Fatalf("DecodeFloat: err = %v, want ErrNonStandard", err)
	}
}

// TestRoundTripNonStandard44100 tests a 44100 Hz mode, a common custom request.
func TestRoundTripNonStandard44100(t *testing.T) {
	// 44100 Hz, 441 samples = 10ms. 441 is odd, invalid.
	_, err := custom.NewMode(44100, 441)
	if err == nil {
		t.Fatal("expected error for odd frame size 441")
	}
	// 44100 Hz, 440 samples = ~9.98ms.  Must satisfy frame_size*1000 >= Fs:
	// 440*1000 = 440000 >= 44100 ✓.
	mode, err := custom.NewMode(44100, 440)
	if err != nil {
		t.Fatalf("NewMode(44100, 440): %v", err)
	}
	t.Logf("44100/440 mode: maxLM=%d nbEBands=%d overlap=%d isStandard=%v",
		mode.MaxLM, mode.NbEBands, mode.Overlap, mode.IsStandard())

	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	pcm := generateSine(440, 44100, 440)
	if _, err := enc.EncodeFloat(pcm, 100); !errors.Is(err, custom.ErrNonStandard) {
		t.Fatalf("EncodeFloat: err = %v, want ErrNonStandard", err)
	}
	if _, err := dec.DecodeFloat(make([]byte, 8), 440); !errors.Is(err, custom.ErrNonStandard) {
		t.Fatalf("DecodeFloat: err = %v, want ErrNonStandard", err)
	}
}

// TestRoundTripInt16 verifies the Encode/Decode (int16) path.
func TestRoundTripInt16(t *testing.T) {
	mode, err := custom.NewMode(48000, 480)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Generate int16 sine wave at 1 kHz.
	pcm := make([]int16, 480)
	for i := range pcm {
		pcm[i] = int16(16000 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}

	data, err := enc.Encode(pcm, 150)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Encode produced empty packet")
	}

	decoded, err := dec.Decode(data, 480)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded) != 480 {
		t.Fatalf("Decode length %d, want 480", len(decoded))
	}
}

// TestRoundTripMultiFrame verifies that state is preserved across frames (no reset
// between calls) and that energy is consistently non-zero.
func TestRoundTripMultiFrame(t *testing.T) {
	const frames = 10
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	for i := 0; i < frames; i++ {
		// Shift phase so each frame has a different starting phase.
		pcm := make([]float32, 960)
		for j := range pcm {
			t := float64(i*960+j) / 48000
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*t))
		}
		data, err := enc.EncodeFloat(pcm, 200)
		if err != nil {
			t.Fatalf("frame %d EncodeFloat: %v", i, err)
		}
		decoded, err := dec.DecodeFloat(data, 960)
		if err != nil {
			t.Fatalf("frame %d DecodeFloat: %v", i, err)
		}
		if len(decoded) != 960 {
			t.Fatalf("frame %d decoded length %d, want 960", i, len(decoded))
		}
		// Skip energy check for first couple frames (may be silence/transient).
		if i >= 2 && !hasEnergy(decoded, 1e-5) {
			t.Errorf("frame %d has no decoded energy", i)
		}
	}
}

// TestModeValidation exercises the NewMode validation rules matching libopus
// modes.c opus_custom_mode_create():
//   - Fs must be in [8000, 96000]
//   - frame_size must be in [40, 1024], even
//   - frame_size * 1000 >= Fs (at least 1ms)
//   - short block must be <= 3.3ms
func TestModeValidation(t *testing.T) {
	cases := []struct {
		fs, sz  int
		wantErr bool
		label   string
	}{
		{48000, 960, false, "standard 48k/960"},
		{48000, 480, false, "standard 48k/480"},
		{16000, 160, false, "16k 10ms"},
		{8000, 80, false, "8k 10ms"},
		{96000, 960, false, "96k 10ms"},
		// Errors:
		{7999, 960, true, "Fs too low"},
		{96001, 960, true, "Fs too high"},
		{48000, 38, true, "frame_size below 40"},
		{48000, 1026, true, "frame_size above 1024"},
		{48000, 961, true, "frame_size odd"},
		{48000, 40, true, "frame_size*1000 < Fs (40*1000=40000 < 48000)"},
		{8000, 40, false, "8k/40 samples = 5ms, valid"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			m, err := custom.NewMode(tc.fs, tc.sz)
			if tc.wantErr {
				if err == nil {
					t.Errorf("NewMode(%d, %d): expected error, got mode=%v", tc.fs, tc.sz, m)
				}
			} else {
				if err != nil {
					t.Errorf("NewMode(%d, %d): unexpected error: %v", tc.fs, tc.sz, err)
				}
			}
		})
	}
}

// TestCTLs verifies that CTL setters are applied without error and are readable
// back via the matching getter.
func TestCTLs(t *testing.T) {
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	if err := enc.SetComplexity(5); err != nil {
		t.Errorf("SetComplexity: %v", err)
	}
	if enc.Complexity() != 5 {
		t.Errorf("Complexity = %d, want 5", enc.Complexity())
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Errorf("SetBitrate: %v", err)
	}
	if enc.Bitrate() != 128000 {
		t.Errorf("Bitrate = %d, want 128000", enc.Bitrate())
	}
	if err := enc.SetVBR(true); err != nil {
		t.Errorf("SetVBR: %v", err)
	}
	if !enc.VBR() {
		t.Error("VBR should be true")
	}
	if err := enc.SetConstrainedVBR(true); err != nil {
		t.Errorf("SetConstrainedVBR: %v", err)
	}
	if !enc.ConstrainedVBR() {
		t.Error("ConstrainedVBR should be true")
	}
	if err := enc.SetPrediction(0); err != nil {
		t.Errorf("SetPrediction: %v", err)
	}
	if enc.Prediction() != 0 {
		t.Errorf("Prediction = %d, want 0", enc.Prediction())
	}
	if err := enc.SetLSBDepth(16); err != nil {
		t.Errorf("SetLSBDepth: %v", err)
	}
	if enc.LSBDepth() != 16 {
		t.Errorf("LSBDepth = %d, want 16", enc.LSBDepth())
	}
	if err := enc.SetPacketLoss(10); err != nil {
		t.Errorf("SetPacketLoss: %v", err)
	}
	if enc.PacketLoss() != 10 {
		t.Errorf("PacketLoss = %d, want 10", enc.PacketLoss())
	}

	if err := dec.SetComplexity(3); err != nil {
		t.Errorf("decoder SetComplexity: %v", err)
	}
	if dec.Complexity() != 3 {
		t.Errorf("decoder Complexity = %d, want 3", dec.Complexity())
	}

	// Invalid CTL values.
	if err := enc.SetComplexity(-1); err == nil {
		t.Error("SetComplexity(-1) expected error")
	}
	if err := enc.SetComplexity(11); err == nil {
		t.Error("SetComplexity(11) expected error")
	}
	if err := enc.SetPrediction(3); err == nil {
		t.Error("SetPrediction(3) expected error")
	}
	if err := enc.SetLSBDepth(7); err == nil {
		t.Error("SetLSBDepth(7) expected error")
	}
	if err := enc.SetPacketLoss(101); err == nil {
		t.Error("SetPacketLoss(101) expected error")
	}
}

// TestResetState verifies that Reset() does not leave state that causes decoding
// to panic or produce invalid output after the reset.
func TestResetState(t *testing.T) {
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	pcm := generateSine(440, 48000, 960)
	// Encode and decode a few frames.
	for i := 0; i < 3; i++ {
		data, _ := enc.EncodeFloat(pcm, 200)
		_, _ = dec.DecodeFloat(data, 960)
	}

	// Reset both.
	enc.Reset()
	dec.Reset()

	// Encode/decode should still work after reset.
	data, err := enc.EncodeFloat(pcm, 200)
	if err != nil {
		t.Fatalf("EncodeFloat after Reset: %v", err)
	}
	decoded, err := dec.DecodeFloat(data, 960)
	if err != nil {
		t.Fatalf("DecodeFloat after Reset: %v", err)
	}
	if len(decoded) != 960 {
		t.Fatalf("decoded length %d after Reset, want 960", len(decoded))
	}
}

// TestModeSamplesAndRate checks that mode attributes are accessible.
func TestModeSamplesAndRate(t *testing.T) {
	cases := []struct {
		fs, sz int
	}{
		{48000, 960},
		{48000, 480},
		{16000, 160},
		{8000, 80},
	}
	for _, tc := range cases {
		m, err := custom.NewMode(tc.fs, tc.sz)
		if err != nil {
			t.Fatalf("NewMode(%d, %d): %v", tc.fs, tc.sz, err)
		}
		if m.SampleRate() != tc.fs {
			t.Errorf("SampleRate = %d, want %d", m.SampleRate(), tc.fs)
		}
		if m.Samples() != tc.sz {
			t.Errorf("Samples = %d, want %d", m.Samples(), tc.sz)
		}
		if m.PreemphCoef() == 0 {
			t.Errorf("PreemphCoef should be non-zero for Fs=%d", tc.fs)
		}
	}
}

func sizeLabel(sz int) string {
	switch sz {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	default:
		return "custom"
	}
}
