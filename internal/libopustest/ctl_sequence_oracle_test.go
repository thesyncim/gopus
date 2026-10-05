package libopustest

import (
	"reflect"
	"testing"
)

func TestProbeCTLSequenceBatchMatchesIndependentPrograms(t *testing.T) {
	RequireOracle(t)
	feeder, err := ProbeCTLSequence(CTLSequenceParams{
		SampleRate:     48000,
		Channels:       1,
		Application:    2049,
		FrameSize:      960,
		CapturePackets: true,
		Ops: []CTLOp{
			{Op: CTLOpSet, Request: 4002, Arg: 64000},
			{Op: CTLOpProcess},
			{Op: CTLOpReset},
			{Op: CTLOpProcess},
		},
	})
	if err != nil {
		HelperUnavailable(t, "ctl sequence", err)
		return
	}
	if len(feeder) != 4 || len(feeder[1].Packet) == 0 || len(feeder[3].Packet) == 0 {
		t.Fatalf("single capture results=%+v, want packets for both process operations", feeder)
	}
	feed := feeder[1].Packet

	programs := []CTLSequenceParams{
		{
			SampleRate:     48000,
			Channels:       1,
			Application:    2049,
			FrameSize:      960,
			CapturePackets: true,
			Ops: []CTLOp{
				{Op: CTLOpGet, Request: 4003},
				{Op: CTLOpSet, Request: 4002, Arg: 96000},
				{Op: CTLOpProcess},
				{Op: CTLOpGet, Request: 4003},
				{Op: CTLOpReset},
				{Op: CTLOpGet, Request: 4003},
			},
		},
		{
			SampleRate:  48000,
			Channels:    2,
			Application: 2048,
			FrameSize:   960,
			Ops: []CTLOp{
				{Op: CTLOpGet, Request: 4003},
				{Op: CTLOpSet, Request: 4002, Arg: -1000},
				{Op: CTLOpReset},
				{Op: CTLOpGet, Request: 4003},
			},
		},
		{
			IsDecoder:  true,
			SampleRate: 48000,
			Channels:   1,
			FrameSize:  960,
			FeedPacket: feed,
			Ops: []CTLOp{
				{Op: CTLOpProcess},
				{Op: CTLOpGet, Request: 99999},
				{Op: CTLOpReset},
				{Op: CTLOpProcess},
			},
		},
		{
			IsDecoder:      true,
			SampleRate:     48000,
			Channels:       1,
			FrameSize:      960,
			FeedPacket:     feed,
			CapturePackets: true,
			Ops: []CTLOp{
				{Op: CTLOpProcess},
				{Op: CTLOpReset},
				{Op: CTLOpProcess},
			},
		},
	}

	got, err := ProbeCTLSequenceBatch(programs)
	if err != nil {
		HelperUnavailable(t, "ctl sequence batch", err)
		return
	}
	if len(got) != len(programs) {
		t.Fatalf("batch programs=%d want %d", len(got), len(programs))
	}
	for i, program := range programs {
		want, err := ProbeCTLSequence(program)
		if err != nil {
			HelperUnavailable(t, "single ctl sequence", err)
			return
		}
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("program %d batch results differ from independent single-program oracle\n got: %#v\nwant: %#v", i, got[i], want)
		}
	}
}

func TestProbeCTLSequenceBatchRejectsInvalidRequests(t *testing.T) {
	if _, err := ProbeCTLSequenceBatch(make([]CTLSequenceParams, ctlSequenceMaxBatch+1)); err == nil {
		t.Fatal("ProbeCTLSequenceBatch accepted more than the bounded batch size")
	}
	if _, err := ProbeCTLSequenceBatch([]CTLSequenceParams{{Channels: 3}}); err == nil {
		t.Fatal("ProbeCTLSequenceBatch accepted an invalid channel count")
	}
}

func TestParseCTLSequenceBatchResultsRejectsTruncatedAndTrailingOutput(t *testing.T) {
	programs := []CTLSequenceParams{{
		Channels: 1,
		Ops:      []CTLOp{{Op: CTLOpGet, Request: 4003}},
	}}
	output := NewOraclePayloadVersion(ctlSequenceBatchMagic, ctlSequenceBatchVersion, 1)
	output.Magic(ctlSequenceOutputMagic)
	output.U32(1)
	output.U32(1)
	output.I32(0)
	output.I32(64000)
	output.U32(1)

	valid := append([]byte(nil), output.Bytes()...)
	if _, err := parseCTLSequenceBatchResults(valid, programs); err != nil {
		t.Fatalf("parse valid output: %v", err)
	}
	if _, err := parseCTLSequenceBatchResults(valid[:len(valid)-1], programs); err == nil {
		t.Fatal("batch result parser accepted truncated output")
	}
	if _, err := parseCTLSequenceBatchResults(append(valid, 0), programs); err == nil {
		t.Fatal("batch result parser accepted trailing output")
	}
	zeroPrograms := NewOraclePayloadVersion(ctlSequenceBatchMagic, ctlSequenceBatchVersion, 0)
	if _, err := parseCTLSequenceBatchResults(zeroPrograms.Bytes(), programs); err == nil {
		t.Fatal("batch result parser accepted a short program count")
	}

	captureProgram := []CTLSequenceParams{{
		Channels:       1,
		CapturePackets: true,
		Ops:            []CTLOp{{Op: CTLOpProcess}},
	}}
	captureOutput := NewOraclePayloadVersion(ctlSequenceBatchMagic, ctlSequenceBatchVersion, 1)
	captureOutput.Magic(ctlSequenceOutputMagic)
	captureOutput.U32(2)
	captureOutput.U32(1)
	captureOutput.I32(1)
	captureOutput.I32(0)
	captureOutput.U32(0)
	captureOutput.U32(3)
	captureOutput.Raw([]byte{0xaa, 0xbb, 0xcc, 0})
	validCapture := append([]byte(nil), captureOutput.Bytes()...)
	if _, err := parseCTLSequenceBatchResults(validCapture, captureProgram); err != nil {
		t.Fatalf("parse valid packet-capture output: %v", err)
	}
	if _, err := parseCTLSequenceBatchResults(validCapture[:len(validCapture)-1], captureProgram); err == nil {
		t.Fatal("batch result parser accepted truncated version-2 packet padding")
	}
}

func TestCTLSequenceBatchHelperRejectsTruncatedAndTrailingInput(t *testing.T) {
	RequireOracle(t)
	binPath, err := CTLSequenceHelperPath()
	if err != nil {
		HelperUnavailable(t, "ctl sequence batch", err)
		return
	}

	truncated := NewOraclePayloadVersion(ctlSequenceBatchMagic, ctlSequenceBatchVersion, 1)
	truncated.Raw([]byte(ctlSequenceInputMagic))
	if _, err := RunHelper(binPath, truncated.Bytes()); err == nil {
		t.Fatal("CTL sequence batch helper accepted a truncated program")
	}

	trailing := NewOraclePayloadVersion(ctlSequenceBatchMagic, ctlSequenceBatchVersion, 1)
	appendCTLSequenceProgram(trailing, CTLSequenceParams{
		SampleRate:  48000,
		Channels:    1,
		Application: 2049,
	})
	trailing.Raw([]byte{0})
	if _, err := RunHelper(binPath, trailing.Bytes()); err == nil {
		t.Fatal("CTL sequence batch helper accepted trailing input")
	}
}
