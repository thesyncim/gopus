package gopus_test

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// BenchmarkEncoderEncode_RestrictedCELT5msChirpCBRStreamAfterReset matches the
// signal, controls, and reset lifecycle of encoderbenchcmp's CELT-FB-5ms-mono-64k
// case. Each operation encodes one second of the shared chirp signal.
func BenchmarkEncoderEncode_RestrictedCELT5msChirpCBRStreamAfterReset(b *testing.B) {
	const sampleRate = 48000
	const frameSize = 240
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    1,
		Application: gopus.ApplicationRestrictedCelt,
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		b.Fatal(err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
		b.Fatal(err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		b.Fatal(err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		b.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		b.Fatal(err)
	}
	if err := enc.SetSignal(gopus.SignalMusic); err != nil {
		b.Fatal(err)
	}
	pcm, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantChirpSweepV1, sampleRate, sampleRate, 1)
	if err != nil {
		b.Fatal(err)
	}
	packet := make([]byte, 4000)
	encodeStream := func() {
		enc.Reset()
		for start := 0; start < len(pcm); start += frameSize {
			if _, err := enc.Encode(pcm[start:start+frameSize], packet); err != nil {
				b.Fatalf("Encode frame %d: %v", start/frameSize, err)
			}
		}
	}
	encodeStream()
	if allocs := testing.AllocsPerRun(2, encodeStream); allocs != 0 {
		b.Fatalf("warm allocations per stream=%g, want 0", allocs)
	}
	b.ReportAllocs()
	for b.Loop() {
		encodeStream()
	}
}
