package encoder

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
	"math"
	"testing"
)

func TestHPCutoffMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frames = 4
	type frameOutput struct {
		pcm []float32
		mem [4]float32
	}
	type testCase struct {
		name   string
		e      *Encoder
		input  []float32
		output [frames]frameOutput
	}
	var cases []testCase
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, direct := range []bool{false, true} {
				for _, seeded := range []bool{false, true} {
					e := &Encoder{sampleRate: int32(rate), channels: int32(channels), lsbDepth: 24, mode: ModeCELT, floatInputExact: direct}
					if seeded {
						e.hpMem = [4]float32{.1, -.125, .03, -.015}
						e.variableHPSmth2Inited = true
						e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15() + 8192
					}
					cases = append(cases, testCase{name: fmt.Sprintf("fs%d/ch%d/direct%t/seeded%t", rate, channels, direct, seeded), e: e})
				}
			}
		}
	}
	payload := libopustest.NewOraclePayload("GHPI", uint32(len(cases)))
	for i := range cases {
		tc := &cases[i]
		e := tc.e
		frameSize := int(e.sampleRate) / 50
		payload.U32(uint32(e.sampleRate))
		payload.U32(uint32(e.channels))
		payload.U32(uint32(frameSize))
		payload.U32(frames)
		payload.Float32s(e.hpMem[:]...)
		for f := range frames {
			pcm := make([]float32, frameSize*int(e.channels))
			for j := range pcm {
				if f != frames-1 {
					pcm[j] = float32(.1 + .2*math.Sin(.013*float64(f*len(pcm)+j)))
					pcm[j] += float32((j*31)%53-26) / 1024
				}
			}
			e.floatInputFrame = pcm
			out := e.hpCutoff(pcm, frameSize)
			payload.U32(uint32(silk.VariableHPCutoffHz(e.variableHPSmth2Q15)))
			payload.Float32s(pcm...)
			tc.output[f] = frameOutput{pcm: append([]float32(nil), out...), mem: e.hpMem}
			if f == 0 {
				tc.input = pcm
			}
		}
	}
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label: "libopus VoIP high-pass filter", OutputBase: "gopus_libopus_hp_cutoff", SourceFile: "libopus_hp_cutoff_info.c",
		CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt", "silk", "src"},
		Libs: []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}, DeadStrip: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hp_cutoff", err)
		return
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "hp_cutoff", "GHPO")
	if err != nil {
		libopustest.HelperUnavailable(t, "hp_cutoff", err)
		return
	}
	if n := reader.Count(len(cases)); n != len(cases) {
		t.Fatalf("C cases=%d want=%d", n, len(cases))
	}
	for _, tc := range cases {
		if n := reader.U32(); n != frames {
			t.Fatalf("C frames=%d want=%d", n, frames)
		}
		var want [frames]frameOutput
		for f := range frames {
			n := reader.Count(len(tc.output[f].pcm))
			if n != len(tc.output[f].pcm) {
				t.Fatalf("C output length=%d want=%d", n, len(tc.output[f].pcm))
			}
			want[f].pcm = make([]float32, n)
			for i := range want[f].pcm {
				want[f].pcm[i] = reader.Float32()
			}
			for i := range want[f].mem {
				want[f].mem[i] = reader.Float32()
			}
		}
		if err := reader.Err(); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name, func(t *testing.T) {
			for f, got := range tc.output {
				for i, v := range got.pcm {
					if math.Float32bits(v) != math.Float32bits(want[f].pcm[i]) {
						t.Fatalf("frame%d output%d bits: Go=%08x C=%08x", f, i, math.Float32bits(v), math.Float32bits(want[f].pcm[i]))
					}
				}
				for i, v := range got.mem {
					if math.Float32bits(v) != math.Float32bits(want[f].mem[i]) {
						t.Fatalf("frame%d state%d bits: Go=%08x C=%08x", f, i, math.Float32bits(v), math.Float32bits(want[f].mem[i]))
					}
				}
			}
			n := int(tc.e.sampleRate) / 50
			tc.e.floatInputFrame = tc.input
			tc.e.hpCutoff(tc.input, n)
			if allocs := testing.AllocsPerRun(100, func() { tc.e.hpCutoff(tc.input, n) }); allocs != 0 {
				t.Fatalf("allocations=%v want0", allocs)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestVoIPHPCutoffResetMatchesFreshEncoder(t *testing.T) {
	const frameSize = 480
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(.55*float64((i%37)-18)/18 + .22*float64((i%11)-5)/5)
	}
	newEncoder := func() *Encoder {
		e := NewEncoder(48000, 1)
		e.SetVoIPApplication(true)
		e.mode = ModeCELT
		e.bandwidth = types.BandwidthFullband
		e.bitrateMode = ModeCBR
		e.useVBR = false
		e.vbrConstraint = false
		e.bitrate = 64000
		e.complexity = 0
		e.forceChannels = 1
		return e
	}
	encode := func(e *Encoder) ([]byte, error) {
		return e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, 400)
	}

	e := newEncoder()
	e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15() + 8192
	e.variableHPSmth2Inited = true
	_, err := encode(e)
	if err != nil {
		t.Fatalf("seed encode: %v", err)
	}
	if e.hpMem == ([4]float32{}) || !e.variableHPSmth2Inited ||
		e.variableHPSmth2Q15 <= silk.InitVariableHPSmth2Q15() {
		t.Fatalf("seed frame did not retain elevated VoIP HP state: mem=%v smoother=%d initialized=%t",
			e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}

	e.Reset()
	if e.hpMem != ([4]float32{}) || e.variableHPSmth2Q15 != 0 || e.variableHPSmth2Inited {
		t.Fatalf("Reset did not clear VoIP HP state: mem=%v smoother=%d initialized=%t",
			e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}
	resetPacket, err := encode(e)
	if err != nil {
		t.Fatalf("encode after Reset: %v", err)
	}
	fresh := newEncoder()
	freshPacket, err := encode(fresh)
	if err != nil {
		t.Fatalf("encode fresh state: %v", err)
	}
	if !bytes.Equal(resetPacket, freshPacket) || e.FinalRange() != fresh.FinalRange() || e.hpMem != fresh.hpMem {
		t.Fatalf("Reset replay differs from fresh VoIP state: packet=%x/%x range=%08x/%08x mem=%v/%v",
			resetPacket, freshPacket, e.FinalRange(), fresh.FinalRange(), e.hpMem, fresh.hpMem)
	}
}
