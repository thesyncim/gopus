//go:build gopus_custom_modes && gopus_fixed_point

package custom_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var fixedCustomControlsHelper libopustest.HelperCache

func TestFixedCustomStandardControlsParity(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := fixedCustomControlsHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "fixed custom controls",
			OutputBase:  "gopus_custom_fixed_controls",
			SourceFile:  "libopus_custom_ctl_packet.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed custom controls", err)
		return
	}
	cases := []struct {
		name                 string
		complexity, lsbDepth int
		bitrate, prediction  int
		lossRate             int
		vbr, constrainedVBR  bool
	}{
		{name: "default", complexity: 9, lsbDepth: 16, bitrate: -1, prediction: 2},
		{name: "loss5", complexity: 9, lsbDepth: 16, bitrate: -1, prediction: 2, lossRate: 5},
		{name: "no_prediction", complexity: 9, lsbDepth: 16, bitrate: -1, prediction: 0},
		{name: "complexity3", complexity: 3, lsbDepth: 16, bitrate: -1, prediction: 2},
		{name: "depth24", complexity: 9, lsbDepth: 24, bitrate: -1, prediction: 2},
		{name: "vbr64k", complexity: 9, lsbDepth: 16, bitrate: 64000, prediction: 2, vbr: true, constrainedVBR: true},
	}
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	pcm := customSequenceInput(960, 1, 0)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := custom.NewEncoder(mode, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetSignalling(false); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetComplexity(tc.complexity); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetLSBDepth(tc.lsbDepth); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetVBR(tc.vbr); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetConstrainedVBR(tc.constrainedVBR); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetPrediction(tc.prediction); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetPacketLoss(tc.lossRate); err != nil {
				t.Fatal(err)
			}

			var req bytes.Buffer
			req.WriteString("GCCL")
			write := func(value any) {
				t.Helper()
				if err := binary.Write(&req, binary.LittleEndian, value); err != nil {
					t.Fatal(err)
				}
			}
			for _, value := range []uint32{
				48000, 960, 1, 200, uint32(tc.complexity), uint32(tc.lsbDepth),
				uint32(int32(tc.bitrate)), uint32(boolInt(tc.vbr)), uint32(boolInt(tc.constrainedVBR)),
				uint32(tc.prediction), uint32(tc.lossRate), uint32(len(pcm)),
			} {
				write(value)
			}
			for _, sample := range pcm {
				write(sample)
			}
			response, err := libopustest.RunHelper(helper, req.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if len(response) < 12 || string(response[:4]) != "GCCL" {
				t.Fatalf("invalid C response %x", response)
			}
			wantLen := int(binary.LittleEndian.Uint32(response[4:8]))
			wantRange := binary.LittleEndian.Uint32(response[8:12])
			if len(response) != 12+wantLen {
				t.Fatalf("C response length %d, packet %d", len(response), wantLen)
			}
			packet, err := enc.EncodeFloat(pcm, 200)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(packet, response[12:]) || enc.FinalRange() != wantRange {
				at := -1
				for i := range min(len(packet), wantLen) {
					if packet[i] != response[12+i] {
						at = i
						break
					}
				}
				t.Fatalf("packet/range mismatch: length %d/%d, first byte %d, range %08x/%08x", len(packet), wantLen, at, enc.FinalRange(), wantRange)
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
