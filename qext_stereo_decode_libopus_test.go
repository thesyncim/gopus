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
	binPath, err := libopusQEXTDecodeSingleHelper.Path(buildLibopusQEXTDecodeSingleHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "qext stateful float decoder", err)
		return
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 8, 0, 48000, 0, 2, 240, uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0) // decode_fec
		payload.U32(240)
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "qext stateful float decoder", "GOSO", 3)
	if err != nil {
		t.Fatal(err)
	}
	const samplesPerPacket = 240 * 2
	wantPCM := make([]float32, 3*samplesPerPacket)
	if got := reader.Count(len(wantPCM)); got != len(wantPCM) {
		t.Fatalf("C PCM count %d, want %d", got, len(wantPCM))
	}
	for i := range wantPCM {
		wantPCM[i] = reader.Float32()
	}
	if got := reader.Count(len(packets)); got != len(packets) {
		t.Fatalf("C decode record count %d, want %d", got, len(packets))
	}
	var wantRange [3]uint32
	for i := range packets {
		status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
		if status != 0 || samples != 240 || offset != uint32(i*samplesPerPacket) {
			t.Fatalf("C frame %d record status=%d samples=%d offset=%d", i, status, samples, offset)
		}
		wantRange[i] = finalRange
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
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
		if got := dec.FinalRange(); got != wantRange[i] {
			t.Errorf("frame %d range Go=%08x C=%08x", i, got, wantRange[i])
		}
		for j, got := range gotPCM {
			want := wantPCM[i*samplesPerPacket+j]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame %d PCM[%d] Go=%08x C=%08x", i, j, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}
