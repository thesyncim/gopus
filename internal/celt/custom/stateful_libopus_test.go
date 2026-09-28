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

const (
	customEncodeFrame uint32 = iota
	customLostFrame
	customResetFrame
)

type customSequenceRecord struct {
	op  uint32
	pcm []float32
}

type customSequenceCase struct {
	name                              string
	fs, frameSize, channels, maxBytes int
	records                           []customSequenceRecord
}

type customSequenceResult struct {
	packet                       []byte
	encRange, decRange, intRange uint32
	pcm                          []float32
	pcm16                        []int16
}

var customSequenceHelper libopustest.HelperCache

func runCustomSequenceOracle(t *testing.T, cases []customSequenceCase) [][]customSequenceResult {
	t.Helper()
	libopustest.RequireOracle(t)
	helper, err := customSequenceHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "custom stateful encode/decode",
			OutputBase:  "gopus_custom_stateful",
			SourceFile:  "libopus_custom_stateful.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom stateful", err)
		return nil
	}
	var req bytes.Buffer
	req.WriteString("GCWS")
	writeU32(&req, uint32(len(cases)))
	for _, tc := range cases {
		for _, v := range []int{tc.fs, tc.frameSize, tc.channels, len(tc.records)} {
			writeU32(&req, uint32(v))
		}
		for _, rec := range tc.records {
			writeU32(&req, rec.op)
			writeU32(&req, uint32(tc.maxBytes))
			for _, v := range rec.pcm {
				writef32(&req, v)
			}
		}
	}
	output, err := libopustest.RunHelper(helper, req.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(output)
	read := func(value any) {
		t.Helper()
		if err := binary.Read(r, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	var magic [4]byte
	read(&magic)
	if string(magic[:]) != "GCWS" {
		t.Fatalf("oracle magic=%q", magic)
	}
	var count uint32
	read(&count)
	if int(count) != len(cases) {
		t.Fatalf("oracle cases=%d want %d", count, len(cases))
	}
	results := make([][]customSequenceResult, len(cases))
	for i, tc := range cases {
		read(&count)
		if int(count) != len(tc.records) {
			t.Fatalf("%s oracle records=%d want %d", tc.name, count, len(tc.records))
		}
		results[i] = make([]customSequenceResult, int(count))
		for j := range results[i] {
			ref := &results[i][j]
			var size int32
			read(&size)
			read(&ref.encRange)
			if size < 0 || int(size) > tc.maxBytes {
				t.Fatalf("%s frame %d C packet size=%d", tc.name, j, size)
			}
			ref.packet = make([]byte, int(size))
			read(ref.packet)
			read(&size)
			read(&ref.decRange)
			if int(size) != tc.frameSize {
				t.Fatalf("%s frame %d C float decode count=%d", tc.name, j, size)
			}
			ref.pcm = make([]float32, int(size)*tc.channels)
			read(ref.pcm)
			read(&size)
			read(&ref.intRange)
			if int(size) != tc.frameSize {
				t.Fatalf("%s frame %d C int16 decode count=%d", tc.name, j, size)
			}
			ref.pcm16 = make([]int16, int(size)*tc.channels)
			read(ref.pcm16)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("oracle has %d trailing bytes", r.Len())
	}
	return results
}

func customSequenceInput(frameSize, channels, frame int) []float32 {
	pcm := make([]float32, frameSize*channels)
	seed := uint32(82732 + frame*97)
	for i := range pcm {
		seed = 1664525*seed + 1013904223
		noise := float32(int32(seed)) * 0x1p-31
		switch frame {
		case 0, 5:
			pcm[i] = float32(.3*math.Sin(float64(i)*.17) + .17*math.Sin(float64(i)*1.217))
		case 1, 4, 6, 44:
			pcm[i] = .4 * noise
		case 2:
			if i > len(pcm)/2 {
				pcm[i] = .7 * noise
			}
		}
	}
	return pcm
}

// The histories cover normal coding, reset replay, periodic concealment,
// the transition into noise PLC, and recovery from both concealment types.
func TestOracleWideBandStatefulParity(t *testing.T) {
	var cases []customSequenceCase
	for _, mode := range wideBandCases() {
		for _, channels := range []int{1, 2} {
			for _, history := range []struct {
				name    string
				frames  int
				budgets []int
			}{
				{"reset", 7, []int{40, 200, 600}},
				{"periodic", 9, []int{40, 200, 600}},
				{"noise", 45, []int{200}},
			} {
				for _, budget := range history.budgets {
					tc := customSequenceCase{
						name: fmt.Sprintf("fs%d_n%d_ch%d_%s_budget%d", mode.fs, mode.frame, channels, history.name, budget),
						fs:   mode.fs, frameSize: mode.frame, channels: channels, maxBytes: budget,
					}
					for frame := range history.frames {
						op := customEncodeFrame
						if history.name == "reset" && frame == 5 || history.name == "periodic" && frame == 7 {
							op = customResetFrame
						}
						if history.name == "periodic" && frame >= 3 && frame <= 5 || history.name == "noise" && frame >= 3 && frame <= 43 {
							op = customLostFrame
						}
						tc.records = append(tc.records, customSequenceRecord{op: op, pcm: customSequenceInput(mode.frame, channels, frame)})
					}
					cases = append(cases, tc)
				}
			}
		}
	}
	refs := runCustomSequenceOracle(t, cases)
	for caseIndex, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCustomSequenceParity(t, tc, refs[caseIndex])
		})
	}
}

func assertCustomSequenceParity(t *testing.T, tc customSequenceCase, refs []customSequenceResult) {
	t.Helper()
	mode, err := custom.NewMode(tc.fs, tc.frameSize)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := custom.NewEncoder(mode, tc.channels)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := custom.NewDecoder(mode, tc.channels)
	if err != nil {
		t.Fatal(err)
	}
	dec16, err := custom.NewDecoder(mode, tc.channels)
	if err != nil {
		t.Fatal(err)
	}
	for frame, rec := range tc.records {
		ref := refs[frame]
		if rec.op == customResetFrame {
			enc.Reset()
			dec.Reset()
			dec16.Reset()
		}
		if rec.op != customLostFrame {
			packet, err := enc.EncodeFloat(rec.pcm, tc.maxBytes)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(packet, ref.packet) || enc.FinalRange() != ref.encRange {
				t.Fatalf("frame %d packets/ranges differ: Go=%x/%08x C=%x/%08x", frame, packet, enc.FinalRange(), ref.packet, ref.encRange)
			}
		}
		// C-produced packets isolate decoder behavior from the Go encoder.
		pcm, err := dec.DecodeFloat(ref.packet, tc.frameSize)
		if err != nil {
			t.Fatal(err)
		}
		if len(pcm) != len(ref.pcm) || dec.FinalRange() != ref.decRange {
			t.Fatalf("frame %d float count/range=%d/%08x want %d/%08x", frame, len(pcm), dec.FinalRange(), len(ref.pcm), ref.decRange)
		}
		assertCustomDecodeExact(t, fmt.Sprintf("frame %d", frame), pcm, ref.pcm)
		pcm16, err := dec16.Decode(ref.packet, tc.frameSize)
		if err != nil {
			t.Fatal(err)
		}
		if len(pcm16) != len(ref.pcm16) || dec16.FinalRange() != ref.intRange {
			t.Fatalf("frame %d int16 count/range=%d/%08x want %d/%08x", frame, len(pcm16), dec16.FinalRange(), len(ref.pcm16), ref.intRange)
		}
		for i, got := range pcm16 {
			if got != ref.pcm16[i] {
				t.Fatalf("frame %d int16[%d]=%d want %d", frame, i, got, ref.pcm16[i])
			}
		}
	}
}

var customWideAllocationSink float32

func TestWideBandFloatEncodeDecodeZeroAllocs(t *testing.T) {
	for _, spec := range wideBandCases() {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.frame, channels), func(t *testing.T) {
				mode, err := custom.NewMode(spec.fs, spec.frame)
				if err != nil {
					t.Fatal(err)
				}
				enc, err := custom.NewEncoder(mode, channels)
				if err != nil {
					t.Fatal(err)
				}
				dec, err := custom.NewDecoder(mode, channels)
				if err != nil {
					t.Fatal(err)
				}
				pcm := customSequenceInput(spec.frame, channels, 1)
				consume := func(pcm []float32) {
					var sum float32
					for _, v := range pcm {
						if v < 0 {
							v = -v
						}
						sum += v
					}
					customWideAllocationSink = sum
				}
				run := func() {
					packet, err := enc.EncodeFloat(pcm, 200)
					if err != nil {
						panic(err)
					}
					out, err := dec.DecodeFloat(packet, spec.frame)
					if err != nil {
						panic(err)
					}
					consume(out)
				}
				for range 5 {
					run()
				}
				if got := testing.AllocsPerRun(30, run); got != 0 {
					t.Fatalf("warm float encode/decode allocations=%g", got)
				}
				// Exercise the transition from periodic to noise concealment, then
				// recovery, with the same warmed caller-visible float API.
				runLossRecovery := func() {
					run()
					for range 44 {
						out, err := dec.DecodeFloat(nil, spec.frame)
						if err != nil {
							panic(err)
						}
						consume(out)
					}
					run()
				}
				runLossRecovery()
				if got := testing.AllocsPerRun(3, runLossRecovery); got != 0 {
					t.Fatalf("warm concealment/recovery allocations=%g", got)
				}
				if !(customWideAllocationSink > 0) || math.IsInf(float64(customWideAllocationSink), 0) {
					t.Fatalf("inactive or nonfinite decoded output: %g", customWideAllocationSink)
				}
			})
		}
	}
}
