//go:build !gopus_fixed_point

package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	dcRejectOracleInputMagic  = "GDRI"
	dcRejectOracleOutputMagic = "GDRO"
	dcRejectOracleFrames      = 4
)

var dcRejectOracleHelper libopustest.HelperCache

type dcRejectOracleCase struct {
	name      string
	rate      int
	channels  int
	frameSize int
	initial   [4]float32
	frames    [][]float32
}

type dcRejectOracleFrame struct {
	pcm []float32
	mem [4]float32
}

func dcRejectOracleHelperPath() (string, error) {
	return dcRejectOracleHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "libopus float dc_reject",
		OutputBase:  "gopus_libopus_dc_reject",
		SourceFile:  "libopus_dc_reject_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusFloatDCReject(cases []dcRejectOracleCase) ([][]dcRejectOracleFrame, error) {
	bin, err := dcRejectOracleHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(dcRejectOracleInputMagic, 1, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.rate))
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(len(tc.frames)))
		payload.Float32s(tc.initial[:]...)
		for _, frame := range tc.frames {
			payload.Float32s(frame...)
		}
	}
	data, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run float dc_reject helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderVersion("float dc_reject", dcRejectOracleOutputMagic, data)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("helper version=%d want 1", version)
	}
	count := reader.Count(len(cases))
	out := make([][]dcRejectOracleFrame, count)
	for i, tc := range cases {
		frames := reader.Count(len(tc.frames))
		out[i] = make([]dcRejectOracleFrame, frames)
		for frame := range frames {
			output := &out[i][frame]
			samples := reader.Count(tc.frameSize * tc.channels)
			output.pcm = make([]float32, samples)
			for sample := range output.pcm {
				output.pcm[sample] = reader.Float32()
			}
			for j := range output.mem {
				output.mem[j] = reader.Float32()
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestDCRejectMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	var cases []dcRejectOracleCase
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			frameSize := rate / 50
			tc := dcRejectOracleCase{
				name:      fmt.Sprintf("fs%d/ch%d", rate, channels),
				rate:      rate,
				channels:  channels,
				frameSize: frameSize,
				initial:   [4]float32{0.125, -0.03125, -0.375, 0.0625},
				frames:    make([][]float32, dcRejectOracleFrames),
			}
			for frame := range tc.frames {
				tc.frames[frame] = make([]float32, frameSize*channels)
				for i := range tc.frames[frame] {
					// Distinct, bounded, finite samples exercise both the transient
					// and settled recurrence without relying on platform trig.
					n := (frame*37 + i*19 + rate/100) % 251
					tc.frames[frame][i] = float32(n-125)/512 + float32(frame+1)/128
				}
			}
			cases = append(cases, tc)
		}
	}

	want, err := probeLibopusFloatDCReject(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "float dc_reject", err)
		return
	}
	for caseIndex, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// opus_encoder.c:2008 routes non-VoIP restricted-SILK frames through
			// the literal 3 Hz dc_reject branch.
			enc := &Encoder{
				sampleRate:        int32(tc.rate),
				channels:          int32(tc.channels),
				lsbDepth:          24,
				mode:              ModeSILK,
				restrictedSilkApp: true,
				voipApp:           false,
				floatInputExact:   true,
				hpMem:             tc.initial,
				scratchDCPCM:      make([]opusRes, tc.frameSize*tc.channels),
			}
			for frame, input := range tc.frames {
				enc.floatInputFrame = input
				got := enc.preprocessInputHPFrame(input, tc.frameSize, ModeSILK, 0)
				if len(got) != len(want[caseIndex][frame].pcm) {
					t.Fatalf("frame%d output len=%d C=%d", frame, len(got), len(want[caseIndex][frame].pcm))
				}
				for i, sample := range got {
					if math.Float32bits(float32(sample)) != math.Float32bits(want[caseIndex][frame].pcm[i]) {
						t.Fatalf("frame%d output%d Go=%08x C=%08x", frame, i,
							math.Float32bits(float32(sample)), math.Float32bits(want[caseIndex][frame].pcm[i]))
					}
				}
				for i, state := range enc.hpMem {
					if math.Float32bits(state) != math.Float32bits(want[caseIndex][frame].mem[i]) {
						t.Fatalf("frame%d hpMem[%d] Go=%08x C=%08x", frame, i,
							math.Float32bits(state), math.Float32bits(want[caseIndex][frame].mem[i]))
					}
				}
			}

			allocEnc := &Encoder{
				sampleRate:        int32(tc.rate),
				channels:          int32(tc.channels),
				lsbDepth:          24,
				mode:              ModeSILK,
				restrictedSilkApp: true,
				voipApp:           false,
				floatInputExact:   true,
				floatInputFrame:   tc.frames[0],
				hpMem:             tc.initial,
				scratchDCPCM:      make([]opusRes, tc.frameSize*tc.channels),
			}
			_ = allocEnc.preprocessInputHPFrame(tc.frames[0], tc.frameSize, ModeSILK, 0)
			if allocs := testing.AllocsPerRun(100, func() {
				_ = allocEnc.preprocessInputHPFrame(tc.frames[0], tc.frameSize, ModeSILK, 0)
			}); allocs != 0 {
				t.Fatalf("steady-state allocations=%v want 0", allocs)
			}
		})
	}
}
