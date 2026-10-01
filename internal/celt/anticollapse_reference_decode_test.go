package celt_test

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func decodeAntiCollapseReferencePCM(t *testing.T, bin string, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	// GQDI v4 carries the API rate and keeps one decoder across the sequence.
	payload := libopustest.NewOraclePayloadVersion("GQDI", 4,
		0, // opus_decode_float
		1, // mono
		uint32(frameSize),
		uint32(len(packets)),
		0, // output gain
		48000,
	)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "selected anti-collapse decode", "GQDO", 4)
	if err != nil {
		t.Fatalf("run selected anti-collapse C decoder: %v", err)
	}
	total := reader.Count(len(packets) * frameSize)
	if total < preSkip+len(fixture) {
		t.Fatalf("selected anti-collapse C decoder returned %d samples; need %d after pre-skip", total, preSkip+len(fixture))
	}
	pcm := make([]float32, total)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	reader.Count(len(packets))
	for range packets {
		reader.U32() // final range for each packet
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return pcm[preSkip : preSkip+len(fixture)], true
}

var antiCollapseDNNSelectedRefHelper libopustest.HelperCache

func buildAntiCollapseDNNReferenceHelper() (string, error) {
	return antiCollapseDNNSelectedRefHelper.Path(func() (string, error) {
		return libopustest.BuildDNNCHelper("", libopustest.CHelperConfig{
			Label:      "DNN anti-collapse decode",
			OutputBase: "gopus_anticollapse_dnn_decode",
			SourceFile: "libopus_qext_decode96k_info.c",
			CFlags:     []string{"-O3", "-DNDEBUG", "-ffp-contract=off"},
			DeadStrip:  true,
		})
	})
}
