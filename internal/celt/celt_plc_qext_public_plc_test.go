//go:build gopus_qext

package celt

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	qextPLCProjectionPacket   = "f47f502e2777afcc6995053b6425a92a246bdd246ad1e83c21070c3be1e29013007ad0d36569ed9a"
	qextPLCProjectionStream5  = "f47e514de2f9194f4026d0a4b5842deb193aac7ed9983382a85471106bf1dded268f39daeec89ea9"
	qextPLCStrictStereoPacket = "f47f0c05f9d878630b5cdad1f21379984583f0579186a73674bd1c5ec5f1f40996d9855652726afa"
)

func TestQEXTFirstLossPublicPLCMatchesSelectedReference(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name   string
		packet string
	}{
		{name: "projection_stream0", packet: qextPLCProjectionPacket},
		{name: "projection_stream5_transition", packet: qextPLCProjectionStream5},
		{name: "strict_stereo_transition", packet: qextPLCStrictStereoPacket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet, err := hex.DecodeString(tc.packet)
			if err != nil {
				t.Fatal(err)
			}
			dec := NewDecoder(2)
			if _, err := dec.DecodeFrame(packet[1:], 480); err != nil {
				t.Fatalf("decode preceding CELT packet: %v", err)
			}
			got, err := dec.DecodeFrame(nil, 240)
			if err != nil {
				t.Fatalf("decode first PLC frame: %v", err)
			}

			payload := libopustest.NewOraclePayload("GQPI", 48000, 2, 480, 240)
			payload.U32(uint32(len(packet)))
			payload.Raw(packet)
			bin, err := qextPLCPublicOracleHelper.Path(buildQEXTPLCPublicOracleHelper)
			if err != nil {
				libopustest.HelperUnavailable(t, "QEXT CELT public PLC", err)
			}
			reader, err := libopustest.RunOracle(bin, payload.Bytes(), "QEXT CELT public PLC", "GQPO")
			if err != nil {
				libopustest.HelperUnavailable(t, "QEXT CELT public PLC", err)
			}
			if gotGood := int32(reader.U32()); gotGood != 480 {
				t.Fatalf("selected C good decode returned %d samples, want 480", gotGood)
			}
			if gotLoss := int32(reader.U32()); gotLoss != 240 {
				t.Fatalf("selected C PLC decode returned %d samples, want 240", gotLoss)
			}
			reader.ExpectRemaining(240 * 2 * 4)
			for i, gotSample := range got {
				wantSample := reader.Float32()
				if math.Float32bits(gotSample) != math.Float32bits(wantSample) {
					t.Fatalf("public first-loss PLC sample[%d] Go=%08x %.10g C=%08x %.10g",
						i, math.Float32bits(gotSample), gotSample, math.Float32bits(wantSample), wantSample)
				}
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
