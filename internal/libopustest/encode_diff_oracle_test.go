package libopustest

import (
	"math"
	"reflect"
	"testing"
)

func TestProbeEncodeDiffBatchMatchesIndependentPrograms(t *testing.T) {
	RequireOracle(t)
	programs := []EncodeDiffParams{
		{
			SampleRate:  48000,
			Channels:    1,
			Application: EncodeDiffApplicationAudio,
			Bitrate:     24000,
			Complexity:  7,
			Signal:      EncodeDiffSignalVoice,
			VBR:         true,
			FrameSize:   120,
			FrameCount:  3,
			PCM:         makeEncodeDiffOraclePCM(120, 3, 1, 0.1),
		},
		{
			SampleRate:    48000,
			Channels:      2,
			Application:   EncodeDiffApplicationAudio,
			ForceMode:     EncodeDiffForceModeCELTOnly,
			Bandwidth:     EncodeDiffBandwidthFullband,
			MaxBandwidth:  EncodeDiffBandwidthFullband,
			Bitrate:       96000,
			Complexity:    10,
			Signal:        EncodeDiffSignalMusic,
			VBR:           false,
			VBRConstraint: false,
			ForceChannels: 2,
			FrameSize:     480,
			FrameCount:    2,
			PCM:           makeEncodeDiffOraclePCM(480, 2, 2, 0.7),
		},
		{
			SampleRate:   48000,
			Channels:     1,
			Application:  EncodeDiffApplicationAudio,
			ForceMode:    EncodeDiffForceModeSILKOnly,
			Bandwidth:    EncodeDiffBandwidthWideband,
			MaxBandwidth: EncodeDiffBandwidthWideband,
			Bitrate:      24000,
			Complexity:   8,
			Signal:       EncodeDiffSignalVoice,
			VBR:          true,
			InbandFEC:    1,
			PacketLoss:   20,
			FrameSize:    960,
			FrameCount:   2,
			PCM:          makeEncodeDiffOraclePCM(960, 2, 1, 1.2),
		},
	}

	got, err := ProbeEncodeDiffBatch(programs)
	if err != nil {
		HelperUnavailable(t, "encode diff batch", err)
		return
	}
	if len(got) != len(programs) {
		t.Fatalf("batch programs=%d want %d", len(got), len(programs))
	}
	for i, program := range programs {
		want, err := ProbeEncodeDiff(program)
		if err != nil {
			HelperUnavailable(t, "single encode diff", err)
			return
		}
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("program %d batch records differ from independent single-program oracle\n got: %#v\nwant: %#v", i, got[i], want)
		}
	}
}

func TestProbeEncodeDiffBatchRejectsInvalidRequests(t *testing.T) {
	if got, err := ProbeEncodeDiffBatch(nil); err != nil || got != nil {
		t.Fatalf("empty batch=(%v,%v), want (nil,nil)", got, err)
	}
	if _, err := ProbeEncodeDiffBatch(make([]EncodeDiffParams, encodeDiffMaxBatch+1)); err == nil {
		t.Fatal("ProbeEncodeDiffBatch accepted more than the bounded batch size")
	}
	if _, err := ProbeEncodeDiffBatch([]EncodeDiffParams{{Channels: 3}}); err == nil {
		t.Fatal("ProbeEncodeDiffBatch accepted an invalid channel count")
	}
}

func TestParseEncodeDiffBatchResultsRejectsTruncatedAndTrailingOutput(t *testing.T) {
	programs := []EncodeDiffParams{{FrameCount: 1}}
	output := NewOraclePayloadVersion(encodeDiffBatchMagic, encodeDiffBatchVersion, 1)
	output.Magic(encodeDiffOutputMagic)
	output.U32(1)
	output.U32(1)
	output.U32(3)
	output.U32(0x12345678)
	output.U32(3)
	output.Raw([]byte{0xaa, 0xbb, 0xcc, 0})
	valid := append([]byte(nil), output.Bytes()...)

	got, err := parseEncodeDiffBatchResults(valid, programs)
	if err != nil {
		t.Fatalf("parse valid output: %v", err)
	}
	if len(got) != 1 || len(got[0]) != 1 || got[0][0].Ret != 3 ||
		got[0][0].FinalRange != 0x12345678 || !reflect.DeepEqual(got[0][0].Packet, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("parsed records=%+v", got)
	}
	if _, err := parseEncodeDiffBatchResults(valid[:len(valid)-1], programs); err == nil {
		t.Fatal("batch result parser accepted truncated packet padding")
	}
	if _, err := parseEncodeDiffBatchResults(append(valid, 0), programs); err == nil {
		t.Fatal("batch result parser accepted trailing output")
	}
	zeroPrograms := NewOraclePayloadVersion(encodeDiffBatchMagic, encodeDiffBatchVersion, 0)
	if _, err := parseEncodeDiffBatchResults(zeroPrograms.Bytes(), programs); err == nil {
		t.Fatal("batch result parser accepted a short program count")
	}
}

func TestEncodeDiffBatchHelperRejectsTruncatedAndTrailingInput(t *testing.T) {
	RequireOracle(t)
	binPath, err := EncodeDiffHelperPath()
	if err != nil {
		HelperUnavailable(t, "encode diff batch", err)
		return
	}

	truncated := NewOraclePayloadVersion(encodeDiffBatchMagic, encodeDiffBatchVersion, 1)
	truncated.Raw([]byte(encodeDiffInputMagic))
	if _, err := RunHelper(binPath, truncated.Bytes()); err == nil {
		t.Fatal("encode diff batch helper accepted a truncated program")
	}

	trailing := NewOraclePayloadVersion(encodeDiffBatchMagic, encodeDiffBatchVersion, 1)
	appendEncodeDiffProgram(trailing, EncodeDiffParams{
		SampleRate:  48000,
		Channels:    1,
		Application: EncodeDiffApplicationAudio,
		Complexity:  10,
		FrameSize:   120,
		FrameCount:  1,
		PCM:         makeEncodeDiffOraclePCM(120, 1, 1, 0),
	})
	trailing.Raw([]byte{0})
	if _, err := RunHelper(binPath, trailing.Bytes()); err == nil {
		t.Fatal("encode diff batch helper accepted trailing input")
	}
}

func makeEncodeDiffOraclePCM(frameSize, frameCount, channels int, phase float64) []float32 {
	pcm := make([]float32, frameSize*frameCount*channels)
	for i := 0; i < frameSize*frameCount; i++ {
		sample := float32(0.35 * math.Sin(2*math.Pi*440*float64(i)/48000+phase))
		for channel := 0; channel < channels; channel++ {
			pcm[i*channels+channel] = sample
		}
	}
	return pcm
}
