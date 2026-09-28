//go:build gopus_dred && gopus_osce

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDREDHistoryOSCEComplexityAndMethodOverrideMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		t.Fatalf("emit DRED carrier: %v", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)

	for _, complexity := range []int{0, 5, 6, 7} {
		t.Run(fmt.Sprintf("complexity_%d", complexity), func(t *testing.T) {
			options := libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: complexity}
			want := probeDREDHistoryCarrier(t, seedPacket, packetInfo, options)
			dec := newDREDHistoryDecoder(t, complexity, true)
			decodeDREDHistorySeedAndCarrier(t, dec, seedPacket, packetInfo.packet)
			assertDREDHistoryInt24MatchesC(t, dec, parseCarrierDREDForExplicitDecode(t, 48000, packetInfo), packetInfo, want, "automatic OSCE method")
		})
	}

	// SetOSCELACE is an explicit enable/disable override. An explicit false
	// suppresses the complexity-selected OSCE method; explicit true still uses
	// the method selected by complexity, which is NONE below complexity 6.
	for _, tc := range []struct {
		name             string
		complexity       int
		methodEnabled    bool
		oracleComplexity int
	}{
		{name: "disable_automatic_nolace", complexity: 7, methodEnabled: false, oracleComplexity: 5},
		{name: "enable_below_lace_threshold", complexity: 5, methodEnabled: true, oracleComplexity: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: tc.oracleComplexity}
			want := probeDREDHistoryCarrier(t, seedPacket, packetInfo, options)
			dec := newDREDHistoryDecoder(t, tc.complexity, true)
			if err := dec.SetOSCELACE(tc.methodEnabled); err != nil {
				t.Fatalf("SetOSCELACE(%v): %v", tc.methodEnabled, err)
			}
			decodeDREDHistorySeedAndCarrier(t, dec, seedPacket, packetInfo.packet)
			assertDREDHistoryInt24MatchesC(t, dec, parseCarrierDREDForExplicitDecode(t, 48000, packetInfo), packetInfo, want, "explicit OSCE method override")
		})
	}
}

func decodeDREDHistorySeedAndCarrier(t *testing.T, dec *Decoder, seedPacket, carrierPacket []byte) {
	t.Helper()
	pcm := make([]float32, dec.maxPacketSamples)
	if got, err := dec.Decode(seedPacket, pcm); err != nil || got != 960 {
		t.Fatalf("Decode(seed)=(%d,%v) want (960,nil)", got, err)
	}
	if got, err := dec.Decode(carrierPacket, pcm); err != nil || got != 960 {
		t.Fatalf("Decode(carrier)=(%d,%v) want (960,nil)", got, err)
	}
}
