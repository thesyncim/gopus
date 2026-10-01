//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type customSignallingSequenceStep struct {
	channels int
	lost     bool
	pcm      []float32
}

type customSignallingSequenceCase struct {
	frameSize, outputChannels int
	steps                     []customSignallingSequenceStep
}

type customSignallingSequenceResult struct {
	packet             []byte
	pcm                []float32
	encRange, decRange uint32
	sampleCount        int
}

var customSignallingSequenceHelper libopustest.HelperCache

func customSignallingSequenceHelperPath() (string, error) {
	return customSignallingSequenceHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "custom signalling channel transitions",
			OutputBase:  "gopus_libopus_custom_signalling_sequence",
			SourceFile:  "libopus_custom_signalling_sequence.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
}

func customSignallingSequenceInput(frameSize, channels, frame int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for channel := 0; channel < channels; channel++ {
			frequency := float64(347 + frame*71 + channel*193)
			value := .19*math.Sin(2*math.Pi*frequency*float64(i)/48000) +
				.11*math.Sin(2*math.Pi*(frequency+211)*float64(i)/48000)
			pcm[i*channels+channel] = float32(value)
		}
	}
	return pcm
}

func runCustomSignallingSequenceOracle(t *testing.T, cases []customSignallingSequenceCase) [][]customSignallingSequenceResult {
	t.Helper()
	path, err := customSignallingSequenceHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "custom signalling channel transitions", err)
		return nil
	}
	var request bytes.Buffer
	request.WriteString("GCSS")
	writeU32(&request, uint32(len(cases)))
	for _, tc := range cases {
		for _, value := range []int{48000, tc.frameSize, tc.outputChannels, len(tc.steps)} {
			writeU32(&request, uint32(value))
		}
		for _, step := range tc.steps {
			op := uint32(0)
			channels := step.channels
			if step.lost {
				op = 1
				channels = 0
			}
			writeU32(&request, op)
			writeU32(&request, uint32(channels))
			writeU32(&request, 200)
			for _, sample := range step.pcm {
				writef32(&request, sample)
			}
		}
	}
	output, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom signalling sequence oracle: %v", err)
	}
	r := bytes.NewReader(output)
	magic := make([]byte, 4)
	if _, err := r.Read(magic); err != nil || string(magic) != "GCSS" {
		t.Fatalf("custom signalling sequence oracle magic=%q err=%v", magic, err)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || int(count) != len(cases) {
		t.Fatalf("custom signalling sequence oracle count=%d want=%d err=%v", count, len(cases), err)
	}
	results := make([][]customSignallingSequenceResult, len(cases))
	for caseIndex, tc := range cases {
		var recordCount uint32
		if err := binary.Read(r, binary.LittleEndian, &recordCount); err != nil || int(recordCount) != len(tc.steps) {
			t.Fatalf("case %d sequence count=%d want=%d err=%v", caseIndex, recordCount, len(tc.steps), err)
		}
		results[caseIndex] = make([]customSignallingSequenceResult, len(tc.steps))
		for stepIndex := range tc.steps {
			result := &results[caseIndex][stepIndex]
			var packetSize, sampleCount int32
			if err := binary.Read(r, binary.LittleEndian, &packetSize); err != nil {
				t.Fatalf("case %d step %d packet size: %v", caseIndex, stepIndex, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &result.encRange); err != nil {
				t.Fatalf("case %d step %d encoder range: %v", caseIndex, stepIndex, err)
			}
			if packetSize < 0 || int(packetSize) > 200 {
				t.Fatalf("case %d step %d C packet size=%d", caseIndex, stepIndex, packetSize)
			}
			result.packet = make([]byte, int(packetSize))
			if _, err := r.Read(result.packet); err != nil && len(result.packet) != 0 {
				t.Fatalf("case %d step %d packet: %v", caseIndex, stepIndex, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &sampleCount); err != nil {
				t.Fatalf("case %d step %d sample count: %v", caseIndex, stepIndex, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &result.decRange); err != nil {
				t.Fatalf("case %d step %d decoder range: %v", caseIndex, stepIndex, err)
			}
			if int(sampleCount) != tc.frameSize {
				t.Fatalf("case %d step %d samples=%d want=%d", caseIndex, stepIndex, sampleCount, tc.frameSize)
			}
			result.sampleCount = int(sampleCount)
			result.pcm = make([]float32, result.sampleCount*tc.outputChannels)
			for sample := range result.pcm {
				var bits uint32
				if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
					t.Fatalf("case %d step %d pcm[%d]: %v", caseIndex, stepIndex, sample, err)
				}
				result.pcm[sample] = math.Float32frombits(bits)
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom signalling sequence oracle has %d trailing bytes", r.Len())
	}
	return results
}

func TestCustomSignallingChannelTransitionsMatchLibopus(t *testing.T) {
	var cases []customSignallingSequenceCase
	for _, frameSize := range []int{960, 640} {
		for _, outputChannels := range []int{1, 2} {
			firstChannels, secondChannels := 2, 1
			if outputChannels == 2 {
				firstChannels, secondChannels = 1, 2
			}
			steps := []customSignallingSequenceStep{
				{channels: firstChannels, pcm: customSignallingSequenceInput(frameSize, firstChannels, 0)},
				{lost: true},
				{channels: secondChannels, pcm: customSignallingSequenceInput(frameSize, secondChannels, 1)},
				{lost: true},
				{channels: firstChannels, pcm: customSignallingSequenceInput(frameSize, firstChannels, 2)},
			}
			cases = append(cases, customSignallingSequenceCase{
				frameSize: frameSize, outputChannels: outputChannels, steps: steps,
			})
		}
	}
	oracle := runCustomSignallingSequenceOracle(t, cases)
	for caseIndex, tc := range cases {
		t.Run(fmt.Sprintf("frame%d_outputch%d", tc.frameSize, tc.outputChannels), func(t *testing.T) {
			mode, err := custom.NewMode(48000, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			encoders := [2]*custom.CustomEncoder{}
			for channels := 1; channels <= 2; channels++ {
				encoders[channels-1], err = custom.NewEncoder(mode, channels)
				if err != nil {
					t.Fatal(err)
				}
			}
			dec, err := custom.NewDecoder(mode, tc.outputChannels)
			if err != nil {
				t.Fatal(err)
			}
			for stepIndex, step := range tc.steps {
				want := oracle[caseIndex][stepIndex]
				var packet []byte
				if !step.lost {
					enc := encoders[step.channels-1]
					packet, err = enc.EncodeFloat(step.pcm, 200)
					if err != nil {
						t.Fatalf("step %d encode: %v", stepIndex, err)
					}
					if !bytes.Equal(packet, want.packet) || enc.FinalRange() != want.encRange {
						t.Fatalf("step %d packet/range=%x/%08x want=%x/%08x", stepIndex, packet, enc.FinalRange(), want.packet, want.encRange)
					}
				}
				var got []float32
				if step.lost {
					got, err = dec.DecodeFloat(nil, tc.frameSize)
				} else {
					got, err = dec.DecodeFloat(want.packet, tc.frameSize)
				}
				if err != nil {
					t.Fatalf("step %d decode: %v", stepIndex, err)
				}
				if len(got) != len(want.pcm) || dec.FinalRange() != want.decRange {
					t.Fatalf("step %d samples/range=%d/%08x want=%d/%08x", stepIndex, len(got), dec.FinalRange(), len(want.pcm), want.decRange)
				}
				assertCustomDecodeExact(t, fmt.Sprintf("step %d", stepIndex), got, want.pcm)
			}
		})
	}
}
