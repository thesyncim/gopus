//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// Smoke test for the multistream decoder OSCE BWE / LACE fanout wiring.
//
// The single-stream decoder ships with full OSCE postfilter wiring (see
// `decoder_osce_bwe_apply.go` / `decoder_osce_lace_apply.go`). This test
// verifies that the multistream decoder propagates the matching control
// surface to every child stream decoder so the OSCE postfilter runs on
// SILK-WB streams inside a multistream packet:
//
//   - `MultistreamDecoder.SetOSCEBWE(true)` + `SetOSCELACE(true)` +
//     `SetDNNBlob(merged core + LACE + BWE blob)` succeeds.
//   - A SILK WB stereo packet produced by the matching multistream encoder
//     decodes without panic / NaN / Inf.
//   - The decoded PCM differs from a baseline decode with OSCE disabled,
//     confirming that the postfilter actually ran on at least one of the
//     child streams.

import (
	"math"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestMultistreamDecoderOSCEBWELACERuntimeIntegration(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(laceBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)
	merged = append(merged, bweBlob...)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960 // 20 ms @ 48 kHz
	)

	// Encode a SILK WB stereo packet via the multistream encoder. For
	// channels == 2 the default Vorbis mapping produces a single coupled
	// stream; the multistream packet payload is therefore the standard
	// stereo Opus packet without self-delimiting framing.
	packet := makeMultistreamStereoSILKWBPacket(t, sampleRate, channels, frameSize)
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
		t.Fatalf("unexpected TOC: mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)
	}
	if !toc.Stereo {
		t.Fatalf("expected stereo TOC but Stereo=false")
	}

	// Reference decode: OSCE disabled. The standard silk_resampler output
	// is the baseline against which the OSCE-enabled run is compared.
	decRef := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	if err := decRef.SetComplexity(7); err != nil {
		t.Fatalf("decRef.SetComplexity(7): %v", err)
	}
	if err := decRef.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(decRef): %v", err)
	}
	if err := decRef.SetOSCEBWE(false); err != nil {
		t.Fatalf("decRef.SetOSCEBWE(false): %v", err)
	}
	if err := decRef.SetOSCELACE(false); err != nil {
		t.Fatalf("decRef.SetOSCELACE(false): %v", err)
	}
	pcmRef := make([]float32, frameSize*channels)
	if n, err := decRef.Decode(packet, pcmRef); err != nil {
		t.Fatalf("decRef.Decode: %v", err)
	} else if n != frameSize {
		t.Fatalf("decRef.Decode returned %d samples/ch, want %d", n, frameSize)
	}

	// Active decode: enable OSCE BWE + LACE, bind the merged model blob.
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7): %v", err)
	}
	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	if got, err := dec.OSCEBWE(); err != nil || !got {
		t.Fatalf("OSCEBWE() = %v, %v after SetOSCEBWE(true)", got, err)
	}
	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true): %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || !got {
		t.Fatalf("OSCELACE() = %v, %v after SetOSCELACE(true)", got, err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged): %v", err)
	}

	pcm := make([]float32, frameSize*channels)
	if n, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("dec.Decode: %v", err)
	} else if n != frameSize {
		t.Fatalf("dec.Decode returned %d samples/ch, want %d", n, frameSize)
	}

	// Validate the OSCE-on output: non-zero energy, no NaN/Inf.
	var energy float64
	for i, v := range pcm {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			t.Fatalf("multistream OSCE-on PCM contains NaN/Inf at %d: %v", i, v)
		}
		energy += f * f
	}
	if energy == 0 {
		t.Fatalf("multistream OSCE-on PCM is silent -- postfilter clobbered the standard silk_resampler output to zero")
	}

	// The postfilter must actually run: compare the OSCE-on output against
	// the OSCE-off baseline. If they are bit-identical, the fanout did not
	// land or the per-stream apply helper short-circuited unexpectedly.
	var diffCount int
	var maxAbsDiff float64
	for i := range pcm {
		d := float64(pcm[i]) - float64(pcmRef[i])
		if d < 0 {
			d = -d
		}
		if d > maxAbsDiff {
			maxAbsDiff = d
		}
		if d > 0 {
			diffCount++
		}
	}
	if diffCount == 0 {
		t.Fatalf("multistream OSCE-on PCM is bit-identical to OSCE-off baseline: postfilter did not run on any child stream")
	}
	t.Logf("multistream OSCE postfilter altered %d/%d samples; max abs diff %g",
		diffCount, len(pcm), maxAbsDiff)
}

func TestMultistreamDecoderOSCEBWEMatchesSingleStreamDecoder(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)

	packet := makeMultistreamStereoSILKWBPacket(t, sampleRate, channels, frameSize)

	decSingle, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder(single): %v", err)
	}
	if err := decSingle.SetComplexity(4); err != nil {
		t.Fatalf("single.SetComplexity(4): %v", err)
	}
	if err := decSingle.SetOSCEBWE(true); err != nil {
		t.Fatalf("single.SetOSCEBWE(true): %v", err)
	}
	if err := decSingle.SetOSCELACE(false); err != nil {
		t.Fatalf("single.SetOSCELACE(false): %v", err)
	}
	if err := decSingle.SetDNNBlob(merged); err != nil {
		t.Fatalf("single.SetDNNBlob(merged): %v", err)
	}
	pcmSingle := make([]float32, frameSize*channels)
	if n, err := decSingle.Decode(packet, pcmSingle); err != nil {
		t.Fatalf("single.Decode: %v", err)
	} else if n != frameSize {
		t.Fatalf("single.Decode returned %d samples/ch, want %d", n, frameSize)
	}

	decMS := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	if err := decMS.SetComplexity(4); err != nil {
		t.Fatalf("multistream.SetComplexity(4): %v", err)
	}
	if err := decMS.SetOSCEBWE(true); err != nil {
		t.Fatalf("multistream.SetOSCEBWE(true): %v", err)
	}
	if err := decMS.SetOSCELACE(false); err != nil {
		t.Fatalf("multistream.SetOSCELACE(false): %v", err)
	}
	if err := decMS.SetDNNBlob(merged); err != nil {
		t.Fatalf("multistream.SetDNNBlob(merged): %v", err)
	}
	pcmMS := make([]float32, frameSize*channels)
	if n, err := decMS.Decode(packet, pcmMS); err != nil {
		t.Fatalf("multistream.Decode: %v", err)
	} else if n != frameSize {
		t.Fatalf("multistream.Decode returned %d samples/ch, want %d", n, frameSize)
	}

	for i := range pcmSingle {
		if got, want := math.Float32bits(pcmMS[i]), math.Float32bits(pcmSingle[i]); got != want {
			t.Fatalf("multistream OSCE BWE sample %d bits=0x%08x want single-stream 0x%08x (values %g vs %g)",
				i, got, want, pcmMS[i], pcmSingle[i])
		}
	}
}

// makeMultistreamStereoSILKWBPacket encodes a stereo SILK WB test packet via
// the public MultistreamEncoder. The default 2-channel mapping produces a
// single coupled stream, so the multistream packet payload is the same as a
// regular stereo Opus packet (no length prefix because streams == 1).
//
// `MultistreamEncoder` does not export a `SetMode` control surface today;
// the helper reaches through `e.enc` (an unexported `*multistream.Encoder`
// field accessible from this package) to pin the encoder to SILK at the
// requested bandwidth, matching the existing
// `makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest` shape.
func makeMultistreamStereoSILKWBPacket(t *testing.T, sampleRate, channels, frameSize int) []byte {
	t.Helper()

	if channels != 2 {
		t.Fatalf("makeMultistreamStereoSILKWBPacket requires channels==2, got %d", channels)
	}

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationVoIP)
	// Drive the underlying stream encoder to SILK / WB / stereo so the
	// produced packet exercises the OSCE BWE + LACE gates on the decoder.
	enc.enc.SetMode(internalenc.ModeSILK)
	enc.enc.SetBandwidth(types.BandwidthWideband)
	enc.enc.SetForceChannels(2)
	enc.enc.SetBitrate(48000)

	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / float64(sampleRate)
		l := 0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23)
		r := 0.27*math.Sin(2*math.Pi*263*tm+0.41) + 0.14*math.Sin(2*math.Pi*431*tm+0.07)
		pcm[2*i] = float32(l)
		pcm[2*i+1] = float32(r)
	}

	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("MultistreamEncoder.SetFrameSize(%d): %v", frameSize, err)
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("MultistreamEncoder.EncodeFloat32: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("MultistreamEncoder.EncodeFloat32 returned empty packet")
	}
	return packet
}
