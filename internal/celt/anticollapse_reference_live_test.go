//go:build !gopus_dred || gopus_qext || gopus_fixed_point

package celt_test

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var antiCollapseSelectedRefHelper libopustest.HelperCache

// selectedAntiCollapseReferencePCM decodes the pinned packets through the
// libopus feature and instruction lane used by the active Go decoder.
func selectedAntiCollapseReferencePCM(t *testing.T, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	bin, err := antiCollapseSelectedRefHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(antiCollapseSelectedRefConfig())
	})
	if err != nil {
		t.Fatalf("build selected anti-collapse C decoder: %v", err)
	}

	// Version 4 of libopus_qext_decode96k_info.c accepts an explicit 48 kHz
	// API rate and keeps one decoder across the complete packet sequence.
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
