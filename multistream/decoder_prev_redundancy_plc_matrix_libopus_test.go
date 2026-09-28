package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type previousRedundancyPLCMatrixCase struct {
	name       string
	sampleRate int
	frameSize  int
	channels   int
	gainQ8     int
	format     uint32
}

const (
	previousRedundancyPCMFloat32 uint32 = iota
	previousRedundancyPCMInt16
	previousRedundancyPCMInt24
)

func TestPLCModeAfterCELTRedundancyAcrossFormatsMatchesLibopus(t *testing.T) {
	cases := []previousRedundancyPLCMatrixCase{
		{name: "48k_mono_10ms_float_zero_gain", sampleRate: 48000, frameSize: 480, channels: 1, format: previousRedundancyPCMFloat32},
		{name: "48k_mono_20ms_int24_plus8db", sampleRate: 48000, frameSize: 960, channels: 1, gainQ8: 2048, format: previousRedundancyPCMInt24},
		{name: "48k_stereo_10ms_int16_minus8db", sampleRate: 48000, frameSize: 480, channels: 2, gainQ8: -2048, format: previousRedundancyPCMInt16},
		{name: "48k_stereo_20ms_int24_saturated_gain", sampleRate: 48000, frameSize: 960, channels: 2, gainQ8: 8192, format: previousRedundancyPCMInt24},
		{name: "24k_mono_10ms_float_minus8db", sampleRate: 24000, frameSize: 240, channels: 1, gainQ8: -2048, format: previousRedundancyPCMFloat32},
		{name: "24k_stereo_20ms_int16_zero_gain", sampleRate: 24000, frameSize: 480, channels: 2, format: previousRedundancyPCMInt16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packets := encodePreviousRedundancyPLCMatrixPackets(t, tc)
			sequence := [][]byte{packets[0], packets[1], packets[2], nil, nil, packets[3]}
			mapping := []byte{0}
			coupled := 0
			if tc.channels == 2 {
				mapping = []byte{0, 1}
				coupled = 1
			}
			const streams = 1
			wantFloat, wantInt16, wantInt24 := referencePLCMatrixOutput(t, tc, streams, coupled, mapping, sequence)

			dec, err := NewDecoder(tc.sampleRate, tc.channels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(tc.gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", tc.gainQ8, err)
			}
			gotFloat := make([]float32, 0, len(wantFloat))
			gotInt16 := make([]int16, 0, len(wantInt16))
			gotInt24 := make([]int32, 0, len(wantInt24))
			for i, packet := range sequence {
				switch tc.format {
				case previousRedundancyPCMFloat32:
					pcm, err := dec.DecodeToFloat32(packet, tc.frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToFloat32: %v", i, err)
					}
					gotFloat = append(gotFloat, pcm...)
				case previousRedundancyPCMInt16:
					pcm, err := dec.DecodeToInt16(packet, tc.frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToInt16: %v", i, err)
					}
					gotInt16 = append(gotInt16, pcm...)
				case previousRedundancyPCMInt24:
					pcm, err := dec.DecodeToInt24(packet, tc.frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToInt24: %v", i, err)
					}
					gotInt24 = append(gotInt24, pcm...)
				}
				if i == 2 {
					assertPreviousRedundancySelectsCELT(t, dec)
				}
				if i == 3 {
					assertPLCConsumedPreviousRedundancy(t, dec)
				}
			}
			switch tc.format {
			case previousRedundancyPCMFloat32:
				assertPLCMatrixFloat32(t, gotFloat, wantFloat)
			case previousRedundancyPCMInt16:
				assertPLCMatrixInt16(t, gotInt16, wantInt16)
			case previousRedundancyPCMInt24:
				assertPLCMatrixInt24(t, gotInt24, wantInt24)
			}
		})
	}
}

func encodePreviousRedundancyPLCMatrixPackets(t *testing.T, tc previousRedundancyPLCMatrixCase) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)
	const frameCount = 4
	perFrame := tc.frameSize * tc.channels
	pcm := make([]int16, perFrame*frameCount)
	for frame := 0; frame < frameCount; frame++ {
		for i := 0; i < tc.frameSize; i++ {
			for ch := 0; ch < tc.channels; ch++ {
				time := float64(frame*tc.frameSize+i) / float64(tc.sampleRate)
				frequency := 220 + ch*90
				value := 0.23*math.Sin(2*math.Pi*float64(frequency)*time) + 0.07*math.Sin(2*math.Pi*1700*time)
				pcm[frame*perFrame+i*tc.channels+ch] = int16(value * 32767)
			}
		}
	}
	maxBandwidth := libopustest.OpusBandwidthFullband
	if tc.sampleRate == 24000 {
		maxBandwidth = libopustest.OpusBandwidthSuperwideband
	}
	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{ShortPCM: pcm[:perFrame], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[perFrame : 2*perFrame], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[2*perFrame : 3*perFrame], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: maxBandwidth},
		{ShortPCM: pcm[3*perFrame:], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: maxBandwidth},
	}
	params := libopustest.OpusEncodeFixedParams{
		SampleRate:    tc.sampleRate,
		Channels:      tc.channels,
		Application:   libopustest.OpusApplicationAudio,
		Bitrate:       128000,
		Complexity:    10,
		ForceChannels: tc.channels,
		LSBDepth:      16,
		FrameSize:     tc.frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected-C CELT redundancy matrix sequence", err)
	}
	if len(records) != frameCount {
		t.Fatalf("selected-C encoder returned %d frames, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("selected-C frame %d status=%d packetBytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	if parseStreamTOC(packets[0][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[1][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[2][0]).mode != streamModeHybrid ||
		parseStreamTOC(packets[3][0]).mode != streamModeCELT {
		t.Fatalf("selected-C mode sequence sampleRate=%d frameSize=%d channels=%d is %v, want SILK/SILK/Hybrid/CELT",
			tc.sampleRate, tc.frameSize, tc.channels,
			[]int{parseStreamTOC(packets[0][0]).mode, parseStreamTOC(packets[1][0]).mode, parseStreamTOC(packets[2][0]).mode, parseStreamTOC(packets[3][0]).mode})
	}
	return packets
}

func referencePLCMatrixOutput(t *testing.T, tc previousRedundancyPLCMatrixCase, streams, coupled int, mapping []byte, packets [][]byte) ([]float32, []int16, []int32) {
	t.Helper()
	switch tc.format {
	case previousRedundancyPCMFloat32:
		want, err := decodeWithLibopusReferencePacketsGain(1, tc.sampleRate, tc.channels, streams, coupled, tc.frameSize, tc.gainQ8, mapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "selected-C float PLC matrix decode", err)
		}
		return want, nil, nil
	case previousRedundancyPCMInt16:
		want, err := decodeWithLibopusReferencePacketsInt16Gain(1, tc.sampleRate, tc.channels, streams, coupled, tc.frameSize, tc.gainQ8, mapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "selected-C int16 PLC matrix decode", err)
		}
		return nil, want, nil
	case previousRedundancyPCMInt24:
		want, err := decodeWithLibopusReferencePacketsInt24Gain(1, tc.sampleRate, tc.channels, streams, coupled, tc.frameSize, tc.gainQ8, mapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "selected-C int24 PLC matrix decode", err)
		}
		return nil, nil, want
	default:
		t.Fatalf("unsupported output format %d", tc.format)
		return nil, nil, nil
	}
}

func assertPLCMatrixFloat32(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("float32 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("float32 sample %d Go=%08x C=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func assertPLCMatrixInt16(t *testing.T, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("int16 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("int16 sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}
}

func assertPLCMatrixInt24(t *testing.T, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("int24 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("int24 sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}
}
