package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
)

func TestDecoder_BandwidthAndLastPacketDuration(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() before decode=%d want=0", got)
	}
	n := decodeMinimalHybrid20ms(t, dec)
	if n != 960 {
		t.Fatalf("Decode returned %d samples, want 960", n)
	}

	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want=%v", got, BandwidthFullband)
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration()=%d want=960", got)
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() after Reset=%d want=0", got)
	}
}

func TestDecoder_InDTX(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if dec.InDTX() {
		t.Fatal("InDTX()=true before any packet")
	}

	dec.lastDataLen = 2
	if !dec.InDTX() {
		t.Fatal("InDTX()=false for 2-byte packet length")
	}

	dec.lastDataLen = 3
	if dec.InDTX() {
		t.Fatal("InDTX()=true for 3-byte packet length")
	}
}

func TestDecoder_SetGainBounds(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if err := dec.SetGain(-32769); err != ErrInvalidGain {
		t.Fatalf("SetGain(-32769) error=%v want=%v", err, ErrInvalidGain)
	}
	if err := dec.SetGain(32768); err != ErrInvalidGain {
		t.Fatalf("SetGain(32768) error=%v want=%v", err, ErrInvalidGain)
	}

	for _, gain := range []int{-32768, 0, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Fatalf("Gain()=%d want=%d", got, gain)
		}
	}
}

func TestDecoder_ComplexityControl(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.Complexity(); got != 0 {
		t.Fatalf("Complexity() default=%d want 0", got)
	}

	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Complexity()=%d want 7", got)
	}
	if got := dec.celtDecoder.Complexity(); got != 7 {
		t.Fatalf("CELT Complexity()=%d want 7", got)
	}
	if got := dec.hybridDecoder.Complexity(); got != 7 {
		t.Fatalf("Hybrid Complexity()=%d want 7", got)
	}

	if err := dec.SetComplexity(11); err != ErrInvalidComplexity {
		t.Fatalf("SetComplexity(11) error=%v want %v", err, ErrInvalidComplexity)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("invalid SetComplexity changed setting to %d", got)
	}

	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Reset changed Complexity() to %d", got)
	}
}

func TestDecoder_PhaseInversionControl(t *testing.T) {
	mono := newMonoTestDecoder(t)
	if !mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=false want true")
	}
	mono.SetPhaseInversionDisabled(false)
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=true after Set(false)")
	}
	mono.Reset()
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono Reset changed phase inversion control")
	}

	stereo, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder stereo error: %v", err)
	}
	if stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=true want false")
	}
	stereo.SetPhaseInversionDisabled(true)
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=false after Set(true)")
	}
	stereo.Reset()
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo Reset changed phase inversion control")
	}
}

func TestDecoder_IgnoreExtensions(t *testing.T) {
	assertIgnoreExtensionsControls(t, newMonoTestDecoder(t))
}

func TestDecoder_OptionalExtensionControls(t *testing.T) {
	dec := newMonoTestDecoder(t)

	assertOptionalDecoderControls(t, dec)
	if osce, ok := any(dec).(extraOSCEBWEControl); ok {
		if extsupport.OSCEBWERuntime {
			assertWorkingOSCEBWEControl(t, osce)
		} else {
			t.Fatal("non-OSCE-runtime build unexpectedly exposes OSCE BWE control")
		}
	} else if extsupport.OSCEBWERuntime {
		t.Fatal("OSCE runtime build does not expose OSCE BWE control")
	}
}

func TestDecoder_GainAppliedToDecodeOutput(t *testing.T) {
	packet := minimalHybridTestPacket20ms()

	base, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder base error: %v", err)
	}
	withGain, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder withGain error: %v", err)
	}
	if err := withGain.SetGain(256); err != nil {
		t.Fatalf("SetGain(+1dB) error: %v", err)
	}

	pcmBase := make([]float32, 960)
	pcmGain := make([]float32, 960)

	nBase, err := base.Decode(packet, pcmBase)
	if err != nil {
		t.Fatalf("base Decode error: %v", err)
	}
	nGain, err := withGain.Decode(packet, pcmGain)
	if err != nil {
		t.Fatalf("withGain Decode error: %v", err)
	}
	if nBase != nGain {
		t.Fatalf("decode sample mismatch: base=%d gain=%d", nBase, nGain)
	}

	rms := func(x []float32) float64 {
		if len(x) == 0 {
			return 0
		}
		var sum float64
		for _, v := range x {
			f := float64(v)
			sum += f * f
		}
		return math.Sqrt(sum / float64(len(x)))
	}

	baseRMS := rms(pcmBase[:nBase])
	gainRMS := rms(pcmGain[:nGain])
	if baseRMS == 0 || gainRMS == 0 {
		t.Fatalf("unexpected zero RMS: base=%.8f gain=%.8f", baseRMS, gainRMS)
	}

	gotRatio := gainRMS / baseRMS
	wantRatio := float64(decodeGainLinear(256))
	if math.Abs(gotRatio-wantRatio) > 0.02 {
		t.Fatalf("gain RMS ratio=%.6f want≈%.6f (tol=0.02)", gotRatio, wantRatio)
	}
}

func TestDecoder_PitchGetter(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	if got := dec.Pitch(); got != 0 {
		t.Fatalf("Pitch()=%d want=0 before decode", got)
	}

	celtEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder CELT error: %v", err)
	}
	if err := celtEnc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	packet := make([]byte, 4000)
	n, err := celtEnc.Encode(generateSineWave(48000, 440, 960), packet)
	if err != nil {
		t.Fatalf("CELT Encode error: %v", err)
	}
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("CELT Decode error: %v", err)
	}

	got := dec.Pitch()
	want := dec.celtDecoder.PostfilterPeriod()
	if got != want {
		t.Fatalf("CELT Pitch()=%d want=%d", got, want)
	}
	if got < 0 {
		t.Fatalf("Pitch() should not be negative: %d", got)
	}

	dec.Reset()
	silkEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder SILK error: %v", err)
	}
	if err := silkEnc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode(SILK) error: %v", err)
	}
	if err := silkEnc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth(WB) error: %v", err)
	}
	if err := silkEnc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal(Voice) error: %v", err)
	}
	n, err = silkEnc.Encode(generateSineWave(48000, 200, 960), packet)
	if err != nil {
		t.Fatalf("SILK Encode error: %v", err)
	}
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("SILK Decode error: %v", err)
	}
	want = 0
	if dec.silkDecoder.GetLastSignalType() == 2 {
		want = dec.silkDecoder.GetLagPrev() * 3
		if want <= 0 {
			t.Fatalf("SILK lagPrev=%d produced invalid pitch", dec.silkDecoder.GetLagPrev())
		}
	}
	if got := dec.Pitch(); got != want {
		t.Fatalf("SILK Pitch()=%d want=%d", got, want)
	}

	for _, tc := range []struct {
		bandwidth Bandwidth
		want      int
	}{
		{BandwidthNarrowband, 6},
		{BandwidthMediumband, 4},
		{BandwidthWideband, 3},
		{BandwidthSuperwideband, 3},
		{BandwidthFullband, 3},
	} {
		if got := silkPitchScale(tc.bandwidth); got != tc.want {
			t.Fatalf("silkPitchScale(%v)=%d want=%d", tc.bandwidth, got, tc.want)
		}
	}
}
