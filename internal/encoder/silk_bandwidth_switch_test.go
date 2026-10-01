package encoder

import (
	"math"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func generateSinePCM(frameSize, channels int, frequency float64) []float64 {
	pcm := make([]float64, frameSize*channels)
	const fs = 48000.0
	for i := range frameSize {
		sample := 0.25 * math.Sin(2*math.Pi*frequency*float64(i)/fs)
		if channels == 2 {
			pcm[2*i] = sample
			pcm[2*i+1] = sample
		} else {
			pcm[i] = sample
		}
	}
	return pcm
}

// silkTOCBandwidth returns the bandwidth a SILK-only TOC byte signals.
func silkTOCBandwidth(t *testing.T, packet []byte) types.Bandwidth {
	t.Helper()
	if len(packet) == 0 {
		t.Fatal("empty packet")
	}
	config := packet[0] >> 3
	if config >= 12 {
		t.Fatalf("TOC 0x%02x is not SILK-only", packet[0])
	}
	return types.BandwidthNarrowband + types.Bandwidth(config/4)
}

// TestSILKInternalRateWaitsForBandwidthSwitch checks that a lower requested
// bandwidth does not rebuild the SILK encoder: while speech keeps SILK from
// allowing a bandwidth switch (silk_control_audio_bandwidth), the internal
// rate and the TOC stay wideband.
func TestSILKInternalRateWaitsForBandwidthSwitch(t *testing.T) {
	for _, channels := range []int{1, 2} {
		enc := NewEncoder(48000, channels)
		enc.SetMode(ModeSILK)
		enc.SetBitrate(32000 * channels)
		enc.SetBandwidth(types.BandwidthWideband)
		const frameSize = 960
		pcm := generateSinePCM(frameSize, channels, 440.0)
		for range 3 {
			if _, err := encodeTest(enc, pcm, frameSize); err != nil {
				t.Fatalf("channels=%d: WB encode failed: %v", channels, err)
			}
		}
		enc.SetBandwidth(types.BandwidthNarrowband)
		packet, err := encodeTest(enc, pcm, frameSize)
		if err != nil {
			t.Fatalf("channels=%d: NB request encode failed: %v", channels, err)
		}
		if got := enc.silkMode.InternalSampleRate; got != 16000 {
			t.Fatalf("channels=%d: internal rate %d right after the NB request, want 16000", channels, got)
		}
		if got := silkTOCBandwidth(t, packet); got != types.BandwidthWideband {
			t.Fatalf("channels=%d: TOC bandwidth %v, want the SILK internal wideband", channels, got)
		}
	}
}

// TestSILKInternalRateSwitchesStepByStep follows a wideband to narrowband
// request through the libopus switching sequence on quiet input: the variable
// LP filter fades the band out, SILK reports switchReady, the Opus layer lets
// it switch after a redundant CELT frame, and the rate moves one step at a
// time. The TOC of every SILK frame signals the SILK internal bandwidth.
func TestSILKInternalRateSwitchesStepByStep(t *testing.T) {
	for _, channels := range []int{1, 2} {
		enc := NewEncoder(48000, channels)
		enc.SetMode(ModeSILK)
		enc.SetBitrate(32000 * channels)
		enc.SetBandwidth(types.BandwidthWideband)
		const frameSize = 960
		quiet := make([]float64, frameSize*channels)
		if _, err := encodeTest(enc, quiet, frameSize); err != nil {
			t.Fatalf("channels=%d: WB encode failed: %v", channels, err)
		}
		if got := enc.silkMode.InternalSampleRate; got != 16000 {
			t.Fatalf("channels=%d: first internal rate %d, want 16000", channels, got)
		}

		enc.SetBandwidth(types.BandwidthNarrowband)
		rates := []int32{16000}
		switchReady := 0
		for frame := 0; frame < 400; frame++ {
			packet, err := encodeTest(enc, quiet, frameSize)
			if err != nil {
				t.Fatalf("channels=%d frame %d: %v", channels, frame, err)
			}
			rate := enc.silkMode.InternalSampleRate
			if got, want := silkTOCBandwidth(t, packet), silkInternalBandwidth(rate); got != want {
				t.Fatalf("channels=%d frame %d: TOC bandwidth %v, SILK codes %v", channels, frame, got, want)
			}
			if enc.silkMode.OpusCanSwitch {
				switchReady++
			}
			if rate != rates[len(rates)-1] {
				rates = append(rates, rate)
			}
		}
		if want := []int32{16000, 12000, 8000}; !slices.Equal(rates, want) {
			t.Fatalf("channels=%d: internal rates %v, want %v", channels, rates, want)
		}
		if switchReady != 2 {
			t.Fatalf("channels=%d: %d signalled switches, want 2", channels, switchReady)
		}
	}
}

// TestSILKInternalRateSwitchZeroAlloc locks the SILK internal bandwidth switch
// at zero allocations once warm: each run requests the other bandwidth and
// codes quiet frames until the SILK internal rate has taken one step, through
// the redundant CELT frames around the switch and the prefill that starts the
// first frame at the new rate.
func TestSILKInternalRateSwitchZeroAlloc(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(32000)
	enc.SetBandwidth(types.BandwidthWideband)
	const frameSize = 960
	quiet := make([]float32, frameSize)
	encode := func() {
		if _, err := enc.EncodeFloat32WithAnalysisMaxBytes(quiet, frameSize, quiet, 4000); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	encode()
	target := types.BandwidthNarrowband
	step := func() {
		enc.SetBandwidth(target)
		rate := enc.silkMode.InternalSampleRate
		for range 400 {
			encode()
			if enc.silkMode.InternalSampleRate != rate {
				break
			}
		}
		if enc.silkMode.InternalSampleRate == rate {
			t.Fatalf("internal rate stayed at %d after the %v request", rate, target)
		}
		if target == types.BandwidthNarrowband {
			target = types.BandwidthWideband
		} else {
			target = types.BandwidthNarrowband
		}
	}
	for range 4 {
		step()
	}
	if allocs := testing.AllocsPerRun(4, step); allocs != 0 {
		t.Fatalf("bandwidth switch allocs/op = %v, want 0", allocs)
	}
}

// TestSILKOnlyByteBudgetCapsInternalRate checks the SILK-only caps of
// opus_encode_frame_native: a 10 ms CBR frame of 10 bytes has an effective
// maximum rate of 8000*2/3 bits/s, below 7 kb/s, so SILK codes narrowband
// from the first frame whatever the requested bandwidth.
func TestSILKOnlyByteBudgetCapsInternalRate(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrateMode(ModeCBR)
	enc.SetBitrate(8000)
	enc.SetBandwidth(types.BandwidthMediumband)
	const frameSize = 480
	pcm := generateSinePCM(frameSize, 1, 440.0)
	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if got := enc.silkMode.MaxInternalSampleRate; got != 8000 {
		t.Fatalf("maxInternalSampleRate %d, want 8000", got)
	}
	if got := enc.silkMode.InternalSampleRate; got != 8000 {
		t.Fatalf("internal rate %d, want 8000", got)
	}
	if got := silkTOCBandwidth(t, packet); got != types.BandwidthNarrowband {
		t.Fatalf("TOC bandwidth %v, want narrowband", got)
	}
}

func TestSILKEncoderForcedBandwidthOverridesMaxBandwidthMono(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(32000)
	enc.SetBandwidth(types.BandwidthFullband)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 1, 440.0)

	tests := []struct {
		name       string
		maxBW      types.Bandwidth
		wantRateHz int
	}{
		{name: "narrowband", maxBW: types.BandwidthNarrowband, wantRateHz: 16000},
		{name: "mediumband", maxBW: types.BandwidthMediumband, wantRateHz: 16000},
		{name: "wideband", maxBW: types.BandwidthWideband, wantRateHz: 16000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc.SetMaxBandwidth(tc.maxBW)
			packet, err := encodeTest(enc, pcm, frameSize)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			if packet == nil {
				t.Fatal("encode returned nil packet")
			}
			checkSILKBandwidth(t, enc, tc.wantRateHz)
		})
	}
}

func TestSILKStereoSideEncoderForcedBandwidthOverridesMaxBandwidth(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(48000)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthNarrowband)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 2, 330.0)

	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("encode returned nil packet")
	}
	checkSILKBandwidth(t, enc, 16000)
	if got := enc.silkMode.NChannelsInternal; got != 2 {
		t.Fatalf("SILK coded %d channels, want 2", got)
	}
}

// checkSILKBandwidth checks the internal sampling rate silk_Encode reported
// for the last packet.
func checkSILKBandwidth(t *testing.T, enc *Encoder, wantRateHz int) {
	t.Helper()
	if enc.silk == nil {
		t.Fatal("silk encoder is nil")
	}
	if got := int(enc.silkMode.InternalSampleRate); got != wantRateHz {
		t.Fatalf("silk internal sample rate=%d want %d", got, wantRateHz)
	}
}
