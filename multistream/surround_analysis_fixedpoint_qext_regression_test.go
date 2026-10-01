//go:build gopus_fixed_point && gopus_qext

package multistream

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFixedQEXTMonoSurroundCELTResetMatchesLibopus keeps a small raw-CELT
// witness for the multistream surround failure: two persistent mono frames,
// followed by a state reset and two more frames. The channel mask is supplied
// as Q24 so this test isolates the elementary encoder from outer analysis. The
// selected archive has ENABLE_QEXT compiled in, while runtime QEXT stays off.
func TestFixedQEXTMonoSurroundCELTResetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		frameSize = 480
		maxBytes  = 64
	)
	mask := make([]int32, surroundBands)
	for i := range mask {
		mask[i] = -int32(i%6) << 20
	}
	frames := make([]libopustest.CELTFixedQ8Frame, 4)
	for frame := range frames {
		frequency := 400
		if frame != 0 {
			frequency = 500
		}
		pcm := make([]int32, frameSize)
		for i := range pcm {
			phase := 2 * math.Pi * float64(frequency*(frame*frameSize+i)) / 48000
			sample := int16(9000 * math.Sin(phase))
			pcm[i] = int32(sample) << 8
		}
		frames[frame] = libopustest.CELTFixedQ8Frame{
			PCM: append([]int32(nil), pcm...), MaxBytes: maxBytes,
			EnergyMask: append([]int32(nil), mask...), ResetBefore: frame == 2,
			SetPrediction: true, Prediction: 2,
		}
	}
	params := libopustest.CELTFixedQ8Params{
		SampleRate: 48000, Channels: 1, StreamChannels: 1, FrameSize: frameSize,
		Start: 0, End: 21, Bitrate: 40000, Complexity: 10, LSBDepth: 16,
		VBR: true, ConstrainedVBR: true, Frames: frames,
	}
	want, err := libopustest.ProbeCELTFixedQEXTQ8State(params)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed-QEXT mono raw CELT state trace", err)
		return
	}
	if len(want) != len(frames) {
		t.Fatalf("selected C frames=%d, want %d", len(want), len(frames))
	}
	if want[0].State.PrefilterGain == 0 && want[1].State.PrefilterGain == 0 {
		t.Fatal("raw mono fixture does not exercise a nonzero prefilter gain before its reset")
	}

	enc := fixedpoint.NewCELTEncoderRate(1, 48000)
	enc.SetStreamChannels(1)
	enc.SetBandRange(0, 21)
	enc.SetBitrate(params.Bitrate)
	enc.SetComplexity(params.Complexity)
	enc.SetLSBDepth(params.LSBDepth)
	enc.SetVBR(params.VBR)
	enc.SetConstrainedVBR(params.ConstrainedVBR)
	packet := make([]byte, maxBytes)
	rng := &rangecoding.Encoder{}
	for frame := range frames {
		input := frames[frame]
		if input.ResetBefore {
			enc.Reset()
		}
		enc.SetBandRange(0, 21)
		enc.SetStreamChannels(1)
		enc.SetBitrate(params.Bitrate)
		enc.SetComplexity(params.Complexity)
		enc.SetLSBDepth(params.LSBDepth)
		enc.SetPrediction(2)
		enc.SetSilkInfo(input.SilkSignalType, input.SilkOffset)
		enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
		enc.SetEnergyMask(input.EnergyMask)
		clear(packet)
		rng.Init(packet)
		n := enc.EncodeWithECRes(input.PCM, frameSize, rng, input.MaxBytes)
		got := rng.Buffer()[:n]
		if n != len(want[frame].Packet) || rng.Range() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
			first := firstByteMismatch(got, want[frame].Packet)
			t.Fatalf("frame %d raw mono CELT mismatch: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x", frame,
				first, len(got), len(want[frame].Packet), rng.Range(), want[frame].FinalRange)
		}
		if diff := fixedSurroundCELTStateMismatch(fixedSurroundCELTEncoderState(enc), want[frame].State); diff != "" {
			t.Fatalf("frame %d raw mono CELT state mismatch: %s", frame, diff)
		}
	}
	t.Logf("matched %d raw mono Q8 frames (combined ENABLE_QEXT archive, runtime-off; reset at frame 2)", len(frames))
}
