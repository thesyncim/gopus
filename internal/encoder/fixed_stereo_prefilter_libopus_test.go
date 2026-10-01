//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestFixedStereoPrefilterThresholdMatchesLibopus keeps the six-frame low-rate
// stereo history that reaches run_prefilter's opus_val16 energy thresholds.
// The third frame activates its pitch filter only when both threshold sums
// narrow to signed 16-bit, as in celt_encoder.c.
func TestFixedStereoPrefilterThresholdMatchesLibopus(t *testing.T) {
	assertFixedCapturedSequenceMatchesLibopus(t, "fs48000_ch2_dur200_wb_br6000_cx5_0")
}

// TestFixedCBRRawTailMatchesLibopus locks the fixed-storage CELT packet
// layout: raw end bytes remain at the end, with a zero gap after range bytes.
func TestFixedCBRRawTailMatchesLibopus(t *testing.T) {
	assertFixedCapturedSequenceMatchesLibopus(t, "fs48000_ch1_dur50_nb_br128000_cx10_2")
}

func assertFixedCapturedSequenceMatchesLibopus(t *testing.T, target string) {
	t.Helper()
	libopustest.RequireOracle(t)
	var spec encFixSpec
	found := false
	for _, candidate := range buildEncFixSweep() {
		if candidate.name == target {
			spec, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatalf("missing fixed CELT configuration %s", target)
	}
	const count = 6
	enc := configureFixCELT(spec)
	gotPackets := make([][]byte, count)
	gotRanges := make([]uint32, count)
	innerFrames := make([]libopustest.CELTFixedQ8Frame, count)
	topFrames := make([]libopustest.OpusEncodeFixedMixedFrame, count)
	innerBitrate, lsbDepth := 0, 0
	for f := range gotPackets {
		pcm := genFixFrame(spec, f, count)
		packet, err := enc.Encode(pcm, spec.frameSize)
		if err != nil {
			t.Fatalf("frame %d encode: %v", f, err)
		}
		if len(packet) < 1 || !enc.fixedCELTUsed {
			t.Fatalf("frame %d did not use integer CELT", f)
		}
		gotPackets[f] = append([]byte(nil), packet...)
		gotRanges[f] = enc.FinalRange()
		innerFrames[f] = fixedQ8OracleFrame(enc)
		if len(innerFrames[f].PCM) != spec.frameSize*spec.channels {
			t.Fatalf("frame %d Q8 input length=%d", f, len(innerFrames[f].PCM))
		}
		topFrames[f] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: append([]float32(nil), pcm...),
		}
		rate, _, depth := enc.LastFixedCELTControls()
		if f == 0 {
			innerBitrate, lsbDepth = rate, depth
		} else if rate != innerBitrate || depth != lsbDepth {
			t.Fatalf("frame %d inner controls changed", f)
		}
	}
	vbr := spec.mode != ModeCBR
	cvbr := spec.mode == ModeCVBR
	top, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: spec.rate, Channels: spec.channels,
		Application:    libopustest.OpusApplicationRestrictedLowDelay,
		MaxPacketBytes: 1276, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: spec.oracleBW,
		Bitrate:   spec.bitrate, Complexity: spec.complexity, VBR: vbr,
		VBRConstraint: cvbr,
		ForceChannels: spec.channels, FrameSize: spec.frameSize, FrameCount: count,
	}, topFrames)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed outer float encode", err)
		return
	}
	inner, err := probePublicFixedCELTQ8(libopustest.CELTFixedQ8Params{
		SampleRate: spec.rate, Channels: spec.channels, FrameSize: spec.frameSize,
		Start: 0, End: celtFixedEndBand(spec.bandwidth),
		Bitrate: innerBitrate, Complexity: spec.complexity, LSBDepth: lsbDepth,
		VBR: vbr, ConstrainedVBR: cvbr, Frames: innerFrames,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed CELT raw Q8", err)
		return
	}
	if len(top) != count || len(inner) != count {
		t.Fatalf("oracle records outer=%d inner=%d want %d", len(top), len(inner), count)
	}
	for f := range gotPackets {
		if top[f].Status < 0 || !bytes.Equal(gotPackets[f], top[f].Packet) ||
			gotRanges[f] != top[f].FinalRange {
			t.Fatalf("frame %d public packet/range differs: status=%d len=%d/%d range=%08x/%08x",
				f, top[f].Status, len(gotPackets[f]), len(top[f].Packet),
				gotRanges[f], top[f].FinalRange)
		}
		if !bytes.Equal(gotPackets[f][1:], inner[f].Packet) ||
			gotRanges[f] != inner[f].FinalRange {
			t.Fatalf("frame %d inner packet/range differs: len=%d/%d range=%08x/%08x",
				f, len(gotPackets[f])-1, len(inner[f].Packet),
				gotRanges[f], inner[f].FinalRange)
		}
	}
}
