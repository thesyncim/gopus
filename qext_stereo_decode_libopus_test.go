//go:build gopus_qext

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusQEXTDecodeSingleHelper libopustest.HelperCache

func buildLibopusQEXTDecodeSingleHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "qext stateful float decoder",
		OutputBase:  "gopus_libopus_qext_decode_single",
		SourceFile:  "libopus_refdecode_single.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTStateful5msStereoWithoutExtensionDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packets := testQEXTStatefulPacketsWithSizeMatchLibopus(t, 240, 3, 2, 128000, BitrateModeCVBR, "-cvbr", false)
	if len(packets) != 3 {
		t.Fatalf("reference packet count %d, want 3", len(packets))
	}
	const samplesPerPacket = 240 * 2
	wantPCM, wantRanges, err := selectedQEXTDecodeSequenceReference(2, 240, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected QEXT decoder", err)
		return
	}
	if len(wantPCM) != 3*samplesPerPacket || len(wantRanges) != len(packets) {
		t.Fatalf("selected reference geometry PCM=%d ranges=%d", len(wantPCM), len(wantRanges))
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	gotPCM := make([]float32, samplesPerPacket)
	for i, packet := range packets {
		n, err := dec.Decode(packet, gotPCM)
		if err != nil || n != 240 {
			t.Fatalf("Go frame %d decode: samples=%d err=%v", i, n, err)
		}
		if got := dec.FinalRange(); got != wantRanges[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRanges[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}
