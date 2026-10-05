package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestEncoderResetPreservesFECDecisionLibopus compares the FEC decision carried
// across OPUS_RESET_STATE with libopus. silk_mode.LBRR_coded precedes
// OPUS_ENCODER_RESET_START in libopus's OpusEncoder, so reset preserves it for
// the next decide_fec call.
func TestEncoderResetPreservesFECDecisionLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name       string
		sampleRate int
		frameSize  int
	}{
		{name: "16kHz_60ms", sampleRate: 16000, frameSize: 960},
		{name: "24kHz_40ms", sampleRate: 24000, frameSize: 960},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := []libopustest.CTLOp{
				{Op: libopustest.CTLOpSet, Request: reqSetBitrate, Arg: cOpusAuto},
				{Op: libopustest.CTLOpSet, Request: reqSetInbandFEC, Arg: 1},
				{Op: libopustest.CTLOpSet, Request: reqSetPacketLossPerc, Arg: 20},
				{Op: libopustest.CTLOpProcess},
				{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
				{Op: libopustest.CTLOpReset},
				{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
				{Op: libopustest.CTLOpProcess},
				{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
			}
			oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
				SampleRate:     tc.sampleRate,
				Channels:       1,
				Application:    cAppAudio,
				FrameSize:      tc.frameSize,
				CapturePackets: true,
				Ops:            ops,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "encoder FEC reset oracle", err)
				return
			}

			enc := mustNewTestEncoder(t, tc.sampleRate, 1, ApplicationAudio)
			if err := enc.SetFrameSize(tc.frameSize); err != nil {
				t.Fatalf("SetFrameSize(%d): %v", tc.frameSize, err)
			}
			pcm := generateSineWaveFloat32(tc.sampleRate, 440, tc.frameSize, 1)
			got := make([]libopustest.CTLResult, len(ops))
			for i, op := range ops {
				if op.Op == libopustest.CTLOpProcess {
					packet, err := enc.EncodeFloat32(pcm)
					if err != nil {
						t.Fatalf("EncodeFloat32 at op %d: %v", i, err)
					}
					got[i] = libopustest.CTLResult{Ret: int32(len(packet)), Packet: packet}
					continue
				}
				ret, value, haveValue := applyEncoderOp(t, enc, op)
				got[i] = libopustest.CTLResult{Ret: ret, Value: value, HaveValue: haveValue}
			}
			compareCTLResults(t, tc.name, ops, got, oracle)
			for i, op := range ops {
				if op.Op == libopustest.CTLOpProcess && !bytes.Equal(got[i].Packet, oracle[i].Packet) {
					t.Fatalf("packet at op %d differs after reset: Go len=%d C len=%d Go=%x C=%x",
						i, len(got[i].Packet), len(oracle[i].Packet), got[i].Packet, oracle[i].Packet)
				}
			}

			out := make([]byte, 4000)
			var n int
			var encodeErr error
			allocs := testing.AllocsPerRun(5, func() {
				enc.Reset()
				n, encodeErr = enc.Encode(pcm, out)
			})
			if encodeErr != nil || n == 0 {
				t.Fatalf("warm reset→encode: n=%d err=%v", n, encodeErr)
			}
			if allocs != 0 {
				t.Fatalf("warm reset→encode allocations=%v want 0", allocs)
			}
		})
	}
}
