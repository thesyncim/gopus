//go:build gopus_osce

package lace

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var osceFeatureOracleCache libopustest.HelperCache

type osceFeatureOracleFrame struct {
	pcm     [FrameSize]int16
	control FeatureControl
	bits    int32
}

func osceFeatureOracleFrames(order int32) [16]osceFeatureOracleFrame {
	var frames [16]osceFeatureOracleFrame
	for f := range frames {
		frame := &frames[f]
		frame.bits = int32(96 + (f*53)%760)
		frame.control.LPCOrder = order
		frame.control.SignalType = typeVoiced
		if f%5 == 4 {
			frame.control.SignalType = 1 // SILK TYPE_UNVOICED
		} else if f%7 == 6 {
			frame.control.SignalType = typeUnvoiced // SILK TYPE_NO_VOICE_ACTIVITY
		}
		for set := range frame.control.PredCoefQ12 {
			frame.control.PredCoefQ12[set][0] = int16(-2300 + f*17 + set*29)
			frame.control.PredCoefQ12[set][1] = int16(900 - f*11 - set*19)
			frame.control.PredCoefQ12[set][2] = int16(-320 + f*7)
			frame.control.PredCoefQ12[set][3] = int16(110 + set*5)
		}
		for sf := range SubframesPerFrame {
			frame.control.GainsQ16[sf] = int32(32768 + ((f*11+sf*7)%13)*8192)
			if frame.control.SignalType == typeVoiced {
				frame.control.PitchL[sf] = int32(48 + (f*7+sf*3)%93)
				for tap := range ltpLen {
					frame.control.LTPCoefQ14[sf*ltpLen+tap] = int16((tap-2)*330 + sf*27 - f*9)
				}
			}
		}
		for n := range frame.pcm {
			// Two changing tones and a bounded deterministic noise component.
			phase := float64(f*FrameSize + n)
			wave := 9000*math.Sin(2*math.Pi*(210+float64(f*13))*phase/16000) +
				4500*math.Sin(2*math.Pi*730*phase/16000)
			noise := float64(((n*67+f*151)%577)-288) * 9
			frame.pcm[n] = int16(math.Round(wave + noise))
		}
	}
	return frames
}

func osceFeatureCRecords(t *testing.T, frames *[16]osceFeatureOracleFrame) [16]struct {
	features [SubframesPerFrame * FeatureDim]uint32
	numbits  [2]uint32
	periods  [SubframesPerFrame]int32
} {
	t.Helper()
	var records [16]struct {
		features [SubframesPerFrame * FeatureDim]uint32
		numbits  [2]uint32
		periods  [SubframesPerFrame]int32
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	helper, err := osceFeatureOracleCache.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper(repoRoot, "libopus_osce_features_info.c", "libopus-osce-features-info", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE feature", err)
	}
	payload := libopustest.NewOraclePayload("GLFI", uint32(len(frames)))
	for f := range frames {
		frame := &frames[f]
		payload.I32(frame.control.LPCOrder)
		payload.I32(frame.control.SignalType)
		payload.I32(frame.bits)
		for set := range frame.control.PredCoefQ12 {
			for _, value := range frame.control.PredCoefQ12[set] {
				payload.I16(value)
			}
		}
		for _, value := range frame.control.LTPCoefQ14 {
			payload.I16(value)
		}
		for _, value := range frame.control.GainsQ16 {
			payload.I32(value)
		}
		for _, value := range frame.control.PitchL {
			payload.I32(value)
		}
		for _, value := range frame.pcm {
			payload.I16(value)
		}
	}
	output, err := libopustest.RunHelper(helper, payload.Bytes())
	if err != nil {
		t.Fatalf("run selected-C OSCE feature helper: %v", err)
	}
	reader, err := libopustest.NewOracleReader("OSCE features", "GLFO", output)
	if err != nil {
		t.Fatal(err)
	}
	reader.Count(len(frames))
	for f := range records {
		for i := range records[f].features {
			records[f].features[i] = reader.U32()
		}
		for i := range records[f].numbits {
			records[f].numbits[i] = reader.U32()
		}
		for i := range records[f].periods {
			records[f].periods[i] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestLACEAndNoLACEFeatureStateMatchesLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, order := range []int32{10, 16} {
		t.Run(fmt.Sprintf("lpc%d", order), func(t *testing.T) {
			frames := osceFeatureOracleFrames(order)
			want := osceFeatureCRecords(t, &frames)
			var state FeatureState
			var features [SubframesPerFrame * FeatureDim]float32
			var numbits [2]float32
			var periods [SubframesPerFrame]int
			for f := range frames {
				frame := &frames[f]
				if !state.CalculateFeatures(features[:], numbits[:], periods[:], frame.pcm[:], &frame.control, frame.bits) {
					t.Fatalf("frame %d: CalculateFeatures rejected matched input", f)
				}
				for i, value := range features {
					if got := math.Float32bits(value); got != want[f].features[i] {
						t.Fatalf("frame %d feature[%d] raw=%08x C=%08x", f, i, got, want[f].features[i])
					}
				}
				for i, value := range numbits {
					if got := math.Float32bits(value); got != want[f].numbits[i] {
						t.Fatalf("frame %d numbits[%d] raw=%08x C=%08x", f, i, got, want[f].numbits[i])
					}
				}
				for i, value := range periods {
					if int32(value) != want[f].periods[i] {
						t.Fatalf("frame %d period[%d]=%d C=%d", f, i, value, want[f].periods[i])
					}
				}
			}
			last := &frames[len(frames)-1]
			if allocations := testing.AllocsPerRun(100, func() {
				if !state.CalculateFeatures(features[:], numbits[:], periods[:], last.pcm[:], &last.control, last.bits) {
					panic("valid OSCE feature input was rejected")
				}
			}); allocations != 0 {
				t.Fatalf("warm CalculateFeatures allocations=%g want 0", allocations)
			}
		})
	}
}
