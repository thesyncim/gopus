package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestEncoderMaxBandwidthRaiseRestoresAutoCELTBandwidthLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	ops := []libopustest.CTLOp{
		{Op: libopustest.CTLOpSet, Request: reqSetBitrate, Arg: 64000},
		{Op: libopustest.CTLOpSet, Request: reqSetForceMode, Arg: libopustest.OpusForceModeCELTOnly},
		{Op: libopustest.CTLOpSet, Request: reqSetMaxBandwidth, Arg: cBandwidthNB},
		{Op: libopustest.CTLOpProcess},
		{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
		{Op: libopustest.CTLOpGet, Request: reqGetBandwidth},
		{Op: libopustest.CTLOpSet, Request: reqSetMaxBandwidth, Arg: cBandwidthWB},
		{Op: libopustest.CTLOpProcess},
		{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
		{Op: libopustest.CTLOpGet, Request: reqGetBandwidth},
	}
	const sampleRate, channels, frameSize = 16000, 1, 320
	oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
		SampleRate: sampleRate, Channels: channels, Application: cAppAudio,
		FrameSize: frameSize, CapturePackets: true, Ops: ops,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "encoder max-bandwidth transition", err)
		return
	}
	if len(oracle) != len(ops) {
		t.Fatalf("oracle returned %d results for %d operations", len(oracle), len(ops))
	}

	enc := mustNewTestEncoder(t, sampleRate, channels, ApplicationAudio)
	if enc.FrameSize() != frameSize {
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
		}
	}
	pcm := generateSineWaveFloat32(sampleRate, 440, frameSize, channels)
	packetBuf := make([]byte, 4000)
	packetIndex := 0
	bandwidths := []int32{cBandwidthNB, cBandwidthWB}
	for i, op := range ops {
		got := libopustest.CTLResult{}
		switch op.Op {
		case libopustest.CTLOpSet:
			got.Ret = applyEncoderSet(enc, op.Request, op.Arg)
		case libopustest.CTLOpProcess:
			n, err := enc.Encode(pcm, packetBuf)
			if err != nil {
				t.Fatalf("PROCESS %d: %v", packetIndex, err)
			}
			got.Ret = int32(n)
			got.Packet = append([]byte(nil), packetBuf[:n]...)
		case libopustest.CTLOpGet:
			got.Value, got.HaveValue = applyEncoderGet(enc, op.Request)
			got.Ret = cOpusOK
		default:
			t.Fatalf("unexpected operation %d at index %d", op.Op, i)
		}

		ref := oracle[i]
		if got.Ret != ref.Ret || got.Value != ref.Value || got.HaveValue != ref.HaveValue {
			t.Fatalf("operation %d (%+v): Go ret/value/presence=%d/%08x/%t, libopus=%d/%08x/%t",
				i, op, got.Ret, uint32(got.Value), got.HaveValue, ref.Ret, uint32(ref.Value), ref.HaveValue)
		}
		if op.Op == libopustest.CTLOpProcess {
			if !bytes.Equal(got.Packet, ref.Packet) {
				first := firstByteDifference(got.Packet, ref.Packet)
				t.Fatalf("packet %d differs: Go length=%d TOC=%s range=%08x, libopus length=%d TOC=%s range=%08x, first byte difference=%d",
					packetIndex, len(got.Packet), packetTOCLabel(got.Packet), enc.FinalRange(), len(ref.Packet), packetTOCLabel(ref.Packet), uint32(oracle[i+1].Value), first)
			}
			packetIndex++
		}
		if op.Op == libopustest.CTLOpGet && op.Request == reqGetBandwidth {
			want := bandwidths[packetIndex-1]
			if got.Value != want {
				t.Fatalf("GET_BANDWIDTH after packet %d = %d, want %d", packetIndex-1, got.Value, want)
			}
		}
	}
	if packetIndex != len(bandwidths) {
		t.Fatalf("encoded %d packets, want %d", packetIndex, len(bandwidths))
	}
}
