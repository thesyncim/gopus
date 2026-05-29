package gopus

import (
	"math"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPCMSoftClipMatchesLibopus verifies that the public PCMSoftClip function
// produces bit-exact output compared to libopus opus_pcm_soft_clip().
// Mirrors: src/opus.c opus_pcm_soft_clip(), celt/celt.c opus_pcm_soft_clip_impl()
func TestPCMSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		channels int
		mem      []float32
		samples  []float32
	}{
		{
			name:     "mono_clipped",
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{1.25, 1.5, 1.8, 1.4, 0.8, -0.2, -1.2, -1.6},
		},
		{
			name:     "stereo_clipped",
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.2, -1.8, 1.4, -1.2, 1.7, -0.4, 0.6, 0.3, -0.5, 1.1, -1.3, 1.9},
		},
		{
			name:     "carryover_mem",
			channels: 1,
			mem:      []float32{-0.2},
			samples:  []float32{0.8, 0.4, -0.2, 1.4, 1.1, 0.3},
		},
		{
			name:     "within_range_zero_mem",
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.5, -0.5, 0.3, -0.3, 0.1, -0.1, 0.7, -0.7},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := len(tc.samples) / tc.channels
			want, wantMem, err := libopustest.ProbeSoftClip(n, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}

			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			PCMSoftClip(got, tc.channels, gotMem)

			if len(got) != len(want) {
				t.Fatalf("pcm len=%d want %d", len(got), len(want))
			}
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("pcm[%d]=%g (%08x) want %g (%08x)", i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
				}
			}
			for c := range gotMem {
				if math.Float32bits(gotMem[c]) != math.Float32bits(wantMem[c]) {
					t.Fatalf("mem[%d]=%g (%08x) want %g (%08x)", c, gotMem[c], math.Float32bits(gotMem[c]), wantMem[c], math.Float32bits(wantMem[c]))
				}
			}
		})
	}
}

// TestPCMSoftClipNilAndEdgeCases tests no-op and edge-case inputs.
func TestPCMSoftClipNilAndEdgeCases(t *testing.T) {
	t.Run("zero_channels", func(t *testing.T) {
		pcm := []float32{1.5}
		mem := []float32{}
		PCMSoftClip(pcm, 0, mem)
	})
	t.Run("nil_mem", func(t *testing.T) {
		pcm := []float32{1.5}
		PCMSoftClip(pcm, 1, nil)
	})
	t.Run("empty_pcm", func(t *testing.T) {
		mem := []float32{0}
		PCMSoftClip(nil, 1, mem)
	})
	t.Run("mem_shorter_than_channels", func(t *testing.T) {
		pcm := []float32{1.5, 1.5}
		mem := []float32{0}
		PCMSoftClip(pcm, 2, mem)
	})
}

// TestErrorStringMatchesLibopus verifies that ErrorString returns the same
// strings as opus_strerror() from celt/celt.c in libopus 1.6.1.
func TestErrorStringMatchesLibopus(t *testing.T) {
	// Mirrors celt/celt.c opus_strerror():
	//   static const char * const error_strings[8] = {
	//     "success", "invalid argument", "buffer too small", "internal error",
	//     "corrupted stream", "request not implemented", "invalid state",
	//     "memory allocation failed"
	//   }
	//   if (error > 0 || error < -7) return "unknown error";
	want := map[int]string{
		0:  "success",
		-1: "invalid argument",
		-2: "buffer too small",
		-3: "internal error",
		-4: "corrupted stream",
		-5: "request not implemented",
		-6: "invalid state",
		-7: "memory allocation failed",
	}
	unknownCodes := []int{1, 2, 100, -8, -100, -32768, 32767}

	for code, wantStr := range want {
		got := ErrorString(code)
		if got != wantStr {
			t.Errorf("ErrorString(%d)=%q want %q", code, got, wantStr)
		}
	}
	for _, code := range unknownCodes {
		got := ErrorString(code)
		if got != "unknown error" {
			t.Errorf("ErrorString(%d)=%q want %q", code, got, "unknown error")
		}
	}
}

// TestVersionStringIsGopus verifies VersionString returns a gopus version string.
// Unlike opus_get_version_string() (celt/celt.c), this returns a gopus identifier,
// not a libopus string.
func TestVersionStringIsGopus(t *testing.T) {
	v := VersionString()
	if v == "" {
		t.Fatal("VersionString() returned empty string")
	}
	if !strings.HasPrefix(v, "gopus") {
		t.Errorf("VersionString()=%q does not start with %q", v, "gopus")
	}
	// Must not claim to be libopus.
	if strings.Contains(v, "libopus") {
		t.Errorf("VersionString()=%q must not contain %q", v, "libopus")
	}
}

// TestDecodeGainTransitionMatchesLibopus verifies that SetGain changes take effect
// on the next call to Decode, matching libopus's abrupt (non-smoothed) gain application.
//
// libopus opus_decode_native() in src/opus_decoder.c applies decode_gain
// unconditionally after each frame with no inter-frame ramping:
//
//	if (st->decode_gain) {
//	    gain = celt_exp2(MULT16_16_P15(QCONST16(6.48814081e-4f, 25), st->decode_gain));
//	    for (i=0; i<frame_size*st->channels; i++) { ... pcm[i] = SATURATE(x, 32767); }
//	}
//
// The oracle drives two consecutive decodes with OPUS_SET_GAIN applied before
// the first packet, so both frames see the same gain. gopus must match exactly.
func TestDecodeGainTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		gainQ8     = 8192
	)
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)

			// Oracle: decode two frames (one real + one PLC) with constant gain.
			steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "gain transition reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}

			got := make([]float32, 0, len(want))
			frame := make([]float32, frameSize*channels)
			n, err := dec.Decode(packet, frame)
			if err != nil {
				t.Fatalf("Decode packet: %v", err)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.Decode(nil, frame)
			if err != nil {
				t.Fatalf("Decode(nil): %v", err)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "gain transition float32")
		})
	}
}

// TestDecodeGainChangeTransitionMatchesLibopus verifies that when SetGain is called
// between two Decode calls the new gain is applied abruptly (immediately) on the
// next frame, exactly as libopus does. libopus stores decode_gain in the decoder
// state and reads it on every opus_decode_native call without ramping.
func TestDecodeGainChangeTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
	)
	gains := []int{256, 512, -256}
	for _, channels := range []int{1, 2} {
		for _, gainQ8 := range gains {
			t.Run("ch_"+itoaSmall(channels)+"_gain_"+itoaSmall(gainQ8), func(t *testing.T) {
				packet := encodeAPIRateCELTPacket(t, channels)

				// Oracle applies gainQ8 before the first decode; both frames use it.
				steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
				want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "gain change reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				if err := dec.SetGain(gainQ8); err != nil {
					t.Fatalf("SetGain(%d): %v", gainQ8, err)
				}

				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				n, err := dec.Decode(packet, frame)
				if err != nil {
					t.Fatalf("Decode packet: %v", err)
				}
				got = append(got, frame[:n*channels]...)

				clear(frame)
				n, err = dec.Decode(nil, frame)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				got = append(got, frame[:n*channels]...)

				assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "gain change transition float32")
			})
		}
	}
}
