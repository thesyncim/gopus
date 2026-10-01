//go:build gopus_custom_modes && gopus_fixed_point && gopus_qext

package custom_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type qextBudgetRecord struct {
	frame, budget int
	reset         bool
	pcm           []int16
}

func TestFixedCustomQEXTEncoderBudgetAndLMParity(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "fixed custom QEXT encoder budgets",
		OutputBase:  "gopus_custom_qext_encoder_budget",
		SourceFile:  "libopus_custom_qext_encoder_budget.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{"-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed custom QEXT encoder budgets", err)
		return
	}
	for _, spec := range []struct {
		fs, modeFrame, channels int
		frames                  []int
	}{
		{48000, 640, 1, []int{80, 160, 320, 640}},
		{48000, 640, 2, []int{80, 160, 320, 640}},
		{96000, 1920, 1, []int{240, 480, 960, 1920}},
		{96000, 1920, 2, []int{240, 480, 960, 1920}},
	} {
		t.Run(fmt.Sprintf("fs%d_n%d_ch%d", spec.fs, spec.modeFrame, spec.channels), func(t *testing.T) {
			mode, err := custom.NewMode(spec.fs, spec.modeFrame)
			if err != nil {
				t.Fatal(err)
			}
			enc := fixedpoint.NewCELTEncoderCustomQEXT(spec.channels, fixedQEXTModeConfig(mode))
			if enc == nil {
				t.Fatal("QEXT custom CELT constructor rejected mode")
			}
			enc.SetComplexity(9)
			enc.SetLSBDepth(16)
			enc.SetVBR(false)
			enc.SetConstrainedVBR(false)
			var records []qextBudgetRecord
			for i, frame := range spec.frames {
				pcm := make([]int16, frame*spec.channels)
				for j := range pcm {
					pcm[j] = int16((j*97+i*811)%28000 - 14000)
				}
				records = append(records, qextBudgetRecord{
					frame: frame, budget: []int{5, 10, 20, 1275}[i], pcm: pcm,
				})
			}
			// Reset preserves custom mode and controls while changing the next
			// frame size and per-frame byte budget on the same encoder instance.
			records = append(records, qextBudgetRecord{
				frame: spec.frames[0], budget: 200, reset: true,
				pcm: records[0].pcm,
			})
			var request bytes.Buffer
			request.WriteString("GQBI")
			for _, v := range []int{spec.fs, spec.modeFrame, spec.channels, len(records)} {
				_ = binary.Write(&request, binary.LittleEndian, uint32(v))
			}
			for _, rec := range records {
				for _, v := range []int{rec.frame, rec.budget} {
					_ = binary.Write(&request, binary.LittleEndian, uint32(v))
				}
				if rec.reset {
					_ = binary.Write(&request, binary.LittleEndian, uint32(1))
				} else {
					_ = binary.Write(&request, binary.LittleEndian, uint32(0))
				}
				_ = binary.Write(&request, binary.LittleEndian, rec.pcm)
			}
			output, err := libopustest.RunHelper(helper, request.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			r := bytes.NewReader(output)
			var magic [4]byte
			if _, err := r.Read(magic[:]); err != nil || string(magic[:]) != "GQBO" {
				t.Fatalf("budget oracle magic=%q error=%v", magic, err)
			}
			read := func() uint32 {
				t.Helper()
				var value uint32
				if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			if count := read(); int(count) != len(records) {
				t.Fatalf("budget oracle records=%d want %d", count, len(records))
			}
			packet := make([]byte, 1275)
			var coder rangecoding.Encoder
			for i, rec := range records {
				n, cRange := int(int32(read())), read()
				if n <= 0 || n > rec.budget {
					t.Fatalf("record %d selected C rejected accepted-budget case: frame=%d budget=%d n=%d", i, rec.frame, rec.budget, n)
				}
				want := make([]byte, n)
				if _, err := r.Read(want); err != nil {
					t.Fatal(err)
				}
				if rec.reset {
					enc.Reset()
				}
				clear(packet)
				coder.Init(packet[:rec.budget])
				gotN := enc.EncodeWithEC(rec.pcm, rec.frame, &coder, rec.budget)
				if gotN < 0 || gotN > len(packet) {
					t.Fatalf("record %d Go encode returned %d", i, gotN)
				}
				got := coder.Buffer()[:gotN]
				if gotN != n || enc.FinalRange() != cRange || !bytes.Equal(got, want) {
					t.Fatalf("record %d frame=%d budget=%d packet/range differs: Go=%x/%08x C=%x/%08x",
						i, rec.frame, rec.budget, got, enc.FinalRange(), want, cRange)
				}
			}
			if r.Len() != 0 {
				t.Fatalf("budget oracle has %d trailing bytes", r.Len())
			}
		})
	}
}
