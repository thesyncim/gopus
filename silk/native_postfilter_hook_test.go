//go:build gopus_extra_controls

package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestNativePostfilterHookFeedsMonoResampler(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	pcm := make([]float32, 4*config.SubframeSamples)
	for i := range pcm {
		pcm[i] = 0.3 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(config.SampleRate)))
	}
	encoded, err := Encode(pcm, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	rdRef := &rangecoding.Decoder{}
	rdRef.Init(encoded)
	ref := NewDecoder()
	refOut := make([]float32, 960)
	nRef, err := ref.DecodeWithDecoderInto(rdRef, BandwidthWideband, 960, true, refOut)
	if err != nil {
		t.Fatalf("DecodeWithDecoderInto(ref): %v", err)
	}

	rdHook := &rangecoding.Decoder{}
	rdHook.Init(encoded)
	withHook := NewDecoder()
	hookCalls := 0
	withHook.SetNativePostfilterHook(func(channel int, samples []int16, ctrl LatestDecoderControl) bool {
		hookCalls++
		if channel != 0 {
			t.Fatalf("channel=%d want 0", channel)
		}
		if ctrl.FsKHz != 16 || ctrl.NbSubfr != 4 || len(samples) != 320 {
			t.Fatalf("unexpected ctrl/samples: fs=%d nbSubfr=%d len=%d", ctrl.FsKHz, ctrl.NbSubfr, len(samples))
		}
		clear(samples)
		return true
	})
	hookOut := make([]float32, 960)
	nHook, err := withHook.DecodeWithDecoderInto(rdHook, BandwidthWideband, 960, true, hookOut)
	if err != nil {
		t.Fatalf("DecodeWithDecoderInto(hook): %v", err)
	}
	if nHook != nRef {
		t.Fatalf("hook samples=%d want %d", nHook, nRef)
	}
	if hookCalls != 1 {
		t.Fatalf("hookCalls=%d want 1", hookCalls)
	}
	refEnergy := float64(0)
	hookEnergy := float64(0)
	for i := 0; i < nRef; i++ {
		refEnergy += float64(refOut[i]) * float64(refOut[i])
		hookEnergy += float64(hookOut[i]) * float64(hookOut[i])
	}
	if refEnergy == 0 {
		t.Fatal("reference decode was silent")
	}
	if hookEnergy != 0 {
		t.Fatalf("hooked decode energy=%g want 0", hookEnergy)
	}
}
