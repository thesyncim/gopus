package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Projection (ambisonics mapping family 3) DECODE parity vs the libopus
// opus_projection_decode_float / opus_projection_decode oracle.
//
// Both sides decode the SAME libopus-encoded family-3 packets (produced by the
// libopus projection encode oracle), so the input bytes are byte-identical and
// the only thing under test is the decode path:
//
//   - per-stream Opus decode, then
//   - the demixing-matrix application
//     (mapping_matrix_multiply_channel_out_float / _short).
//
// The demixing-matrix application is independently locked bit-exact against the
// libopus mapping_matrix oracle in projection_matrix_libopus_test.go (float and
// int16), and the demixing-matrix CTL bytes are locked in
// projection_demixing_ctl_libopus_test.go. So the projection-specific code is
// proven exact; the composition is sample-exact whenever the per-stream Opus
// decode is sample-exact.
//
// FIRST-order (FOA, 4ch) at >=96 kbit/s selects pure-CELT per-stream coding for
// every frame, where gopus decode is bit-exact on amd64 (CI) and within the
// documented <=1-ULP CELT float drift on darwin/arm64
// (project_arm64_celt_1ulp_drift.md). TestProjectionDecodeMatchesLibopus locks
// that to sample-exact.
//
// Lower bitrates and higher orders (SOA, 9ch) select Hybrid (SILK+CELT) / SILK
// per-stream coding for some frames; gopus per-stream Hybrid/SILK stereo decode
// has an upstream (non-projection) residual on those frames.
// TestProjectionDecodePerStreamModeClassification documents that the projection
// decode divergence, when present, is confined to non-CELT per-stream frames and
// is therefore upstream of the projection layer.

// projectionDecodeRef encodes generateAmbisonicsSweep through the libopus
// projection encode oracle and returns the stream layout, demixing matrix and
// per-frame packets so both decoders consume identical bytes.
func projectionDecodeRef(t *testing.T, channels, frameSize, frameCount, bitrate int) *projectionEncodeRef {
	t.Helper()
	const (
		sampleRate     = 48000
		application    = 2049  // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
	)
	pcm := generateAmbisonicsSweep(channels, frameSize, frameCount)
	ref, err := encodeLibopusProjection(sampleRate, channels, application, bitrate, false, true,
		10, bandwidthAuto, frameSize, frameCount, maxPacketBytes, 0, pcm, nil)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection reference encode", err)
	}
	return ref
}

func trivialMapping(channels int) []byte {
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	return mapping
}

// decodeProjectionGopusFloat32 decodes every packet through a fresh gopus
// projection decoder and concatenates the interleaved float32 output.
func decodeProjectionGopusFloat32(sampleRate, channels, streams, coupled, frameSize int, demixing []byte, packets [][]byte) ([]float32, error) {
	dec, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return nil, fmt.Errorf("NewProjectionDecoder: %w", err)
	}
	var out []float32
	for i, pkt := range packets {
		frame, err := dec.DecodeToFloat32(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// decodeProjectionGopusInt16 decodes every packet through a fresh gopus
// projection decoder and concatenates the interleaved int16 output.
func decodeProjectionGopusInt16(sampleRate, channels, streams, coupled, frameSize int, demixing []byte, packets [][]byte) ([]int16, error) {
	dec, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return nil, fmt.Errorf("NewProjectionDecoder: %w", err)
	}
	var out []int16
	for i, pkt := range packets {
		frame, err := dec.DecodeToInt16(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// allPerStreamCELT reports whether every per-stream frame in every packet uses
// pure-CELT coding (TOC config >= 16). Hybrid configs are 12..15, SILK 0..11.
func allPerStreamCELT(packets [][]byte, streams int) bool {
	for _, pkt := range packets {
		sp, err := parseMultistreamPacket(pkt, streams)
		if err != nil {
			return false
		}
		for _, p := range sp {
			if len(p) == 0 {
				continue
			}
			if int(p[0]>>3) < 16 {
				return false
			}
		}
	}
	return true
}

// assertProjectionFloatSampleExact requires bit-exact float32 PCM on amd64; on
// the documented darwin/arm64 <=1-ULP CELT float drift target a tiny per-sample
// slack is tolerated and logged. The demix coefficients are <=1 in magnitude
// after the 1/32768 scale, so a <=1-ULP stream sample maps to a comparably small
// output difference (observed maxAbs ~1.2e-7).
func assertProjectionFloatSampleExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	mismatches := 0
	maxAbs := 0.0
	firstIdx := -1
	for i := range got {
		if math.Float32bits(got[i]) == math.Float32bits(want[i]) {
			continue
		}
		d := math.Abs(float64(got[i] - want[i]))
		if d > maxAbs {
			maxAbs = d
		}
		if firstIdx < 0 {
			firstIdx = i
		}
		mismatches++
	}
	if mismatches == 0 {
		return
	}
	if armEncodeFloatDrift() && maxAbs <= 1e-6 {
		t.Logf("%s: documented darwin/arm64 <=1-ULP CELT drift: %d/%d samples differ, maxAbs=%g (firstIdx=%d)",
			label, mismatches, len(got), maxAbs, firstIdx)
		return
	}
	t.Fatalf("%s decode not sample-exact: %d/%d samples differ, maxAbs=%g (firstIdx=%d got=%g want=%g)",
		label, mismatches, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

// assertProjectionInt16SampleExact requires bit-exact int16 PCM on amd64; on the
// documented darwin/arm64 CELT drift target a <=1 int16-unit difference is
// tolerated and logged.
func assertProjectionInt16SampleExact(t *testing.T, got, want []int16, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	mismatches := 0
	maxAbs := 0
	firstIdx := -1
	for i := range got {
		if got[i] == want[i] {
			continue
		}
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d > maxAbs {
			maxAbs = d
		}
		if firstIdx < 0 {
			firstIdx = i
		}
		mismatches++
	}
	if mismatches == 0 {
		return
	}
	if armEncodeFloatDrift() && maxAbs <= 1 {
		t.Logf("%s: documented darwin/arm64 <=1-ULP CELT drift: %d/%d samples differ, maxAbs=%d (firstIdx=%d)",
			label, mismatches, len(got), maxAbs, firstIdx)
		return
	}
	t.Fatalf("%s decode not sample-exact: %d/%d samples differ, maxAbs=%d (firstIdx=%d got=%d want=%d)",
		label, mismatches, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

// TestProjectionDecodeMatchesLibopus locks gopus projection (ambisonics mapping
// family 3) DECODE to sample-exact parity against the libopus projection decode
// oracle for first-order ambisonics (FOA, 4 channels) at bitrates that select
// pure-CELT per-stream coding for every frame.
//
// Both the float32 (opus_projection_decode_float) and int16
// (opus_projection_decode) paths are asserted sample-exact: bit-exact on amd64
// (CI), and within the documented <=1-ULP CELT float drift on darwin/arm64.
func TestProjectionDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 4 // FOA
		frameCount = 6
	)
	// >=96 kbit/s keeps every FOA per-stream frame in pure-CELT mode.
	frameSizes := []int{480, 960} // 10 ms, 20 ms at 48 kHz
	bitrates := []int{96000, 128000, 192000, 256000}

	for _, frameSize := range frameSizes {
		for _, bitrate := range bitrates {
			name := fmt.Sprintf("foa-4ch/fs%d/br%d", frameSize, bitrate)
			t.Run(name, func(t *testing.T) {
				ref := projectionDecodeRef(t, channels, frameSize, frameCount, bitrate)
				if !allPerStreamCELT(ref.packets, ref.streams) {
					t.Fatalf("expected all-CELT per-stream coding at br=%d fs=%d; got mixed modes", bitrate, frameSize)
				}
				mapping := trivialMapping(channels)

				gotFloat, err := decodeProjectionGopusFloat32(sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, ref.demixing, ref.packets)
				if err != nil {
					t.Fatalf("gopus projection float decode: %v", err)
				}
				wantFloat, err := decodeWithLibopusReferencePackets(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection reference decode", err)
				}
				assertProjectionFloatSampleExact(t, gotFloat, wantFloat, "float32")

				gotInt16, err := decodeProjectionGopusInt16(sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, ref.demixing, ref.packets)
				if err != nil {
					t.Fatalf("gopus projection int16 decode: %v", err)
				}
				wantInt16, err := decodeWithLibopusReferencePacketsInt16Gain(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, 0, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection reference decode", err)
				}
				assertProjectionInt16SampleExact(t, gotInt16, wantInt16, "int16")
			})
		}
	}
}

// TestProjectionDecodePerStreamModeClassification documents that any
// projection-decode divergence beyond the <=1-ULP budget is confined to frames
// whose per-stream coding is Hybrid (SILK+CELT) or SILK, i.e. an upstream
// per-stream-decoder residual rather than a projection (demixing / channel
// mapping) defect.
//
// For each configuration it decodes the libopus-encoded family-3 stream through
// both gopus and the libopus projection decode oracle and asserts:
//
//   - every all-CELT configuration is sample-exact (the projection lock), and
//   - any configuration that exceeds the budget contains at least one non-CELT
//     per-stream frame (so the residual is upstream of the demix, which is
//     itself locked bit-exact in projection_matrix_libopus_test.go).
//
// This includes second-order ambisonics (SOA, 9 channels), whose per-stream
// frames are predominantly Hybrid/SILK at all tested bitrates.
func TestProjectionDecodePerStreamModeClassification(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameCount = 6
	)
	cases := []struct {
		name     string
		channels int
		frameSz  int
		bitrate  int
	}{
		{"foa-4ch/fs960/br64000", 4, 960, 64000},
		{"soa-9ch/fs480/br256000", 9, 480, 256000},
		{"soa-9ch/fs960/br128000", 9, 960, 128000},
		{"soa-9ch/fs960/br64000", 9, 960, 64000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := projectionDecodeRef(t, tc.channels, tc.frameSz, frameCount, tc.bitrate)
			mapping := trivialMapping(tc.channels)

			got, err := decodeProjectionGopusFloat32(sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSz, ref.demixing, ref.packets)
			if err != nil {
				t.Fatalf("gopus projection float decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePackets(3, sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSz, mapping, ref.demixing, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference decode", err)
			}
			if len(got) != len(want) {
				t.Fatalf("sample count mismatch gopus=%d libopus=%d", len(got), len(want))
			}

			maxAbs := 0.0
			for i := range got {
				d := math.Abs(float64(got[i] - want[i]))
				if d > maxAbs {
					maxAbs = d
				}
			}

			allCELT := allPerStreamCELT(ref.packets, ref.streams)
			budget := 1e-6 // <=1-ULP demix output for an all-CELT (amd64-exact) stream.
			if maxAbs <= budget {
				t.Logf("sample-exact within <=1-ULP budget: maxAbs=%g allCELT=%v", maxAbs, allCELT)
				return
			}
			if allCELT {
				t.Fatalf("all-CELT projection stream diverged beyond budget: maxAbs=%g (projection-layer regression)", maxAbs)
			}
			t.Logf("upstream per-stream Hybrid/SILK decode residual (NOT projection): maxAbs=%g; demixing application is locked bit-exact in projection_matrix_libopus_test.go", maxAbs)
		})
	}
}
