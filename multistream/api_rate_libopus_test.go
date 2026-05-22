package multistream

import (
	"math"
	"strconv"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func TestLibopus_APIRateMultistreamDecodeMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encoderSampleRate = 48000
		sampleRate        = 16000
		channels          = 3
		encoderFrameSize  = encoderSampleRate / 50
		frameSize         = sampleRate / 50
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encoderSampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := make([]float64, encoderFrameSize*channels)
	for i := 0; i < encoderFrameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			freq := 330.0 + 170.0*float64(ch)
			pcm[i*channels+ch] = 0.25 * math.Sin(2*math.Pi*freq*float64(i)/encoderSampleRate)
		}
	}
	packet, err := enc.Encode(pcm, encoderFrameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, err := PacketDurationAtRate(packet, streams, sampleRate); err != nil || got != frameSize {
		t.Fatalf("PacketDurationAtRate()=(%d,%v) want (%d,nil)", got, err, frameSize)
	}
	if got, err := PacketDuration(packet, streams); err != nil || got != 960 {
		t.Fatalf("PacketDuration()=(%d,%v) want (960,nil)", got, err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got64, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got := make([]float32, len(got64))
	for i, v := range got64 {
		got[i] = float32(v)
	}

	want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	_, maxAbsDiff := computeDiffStatsF32(got, want)
	if maxAbsDiff > 5e-4 {
		t.Fatalf("api-rate multistream max abs diff=%g want <=5e-4", maxAbsDiff)
	}
}

func TestLibopus_APIRateMultistreamOutputGainMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate       = 48000
		channels         = 3
		frameSize        = sampleRate / 50
		gainQ8           = 8192
		encoderBitrate   = 256000
		encoderFrequency = 997
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(encoderBitrate)
	packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, encoderFrequency), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	want, err := decodeWithLibopusReferencePacketsGain(1, sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, nil, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode gain", err)
	}
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got64, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got := make([]float32, len(got64))
	for i, v := range got64 {
		got[i] = float32(v)
	}
	_, maxAbsDiff := computeDiffStatsF32(got, want)
	if maxAbsDiff != 0 {
		t.Fatalf("api-rate multistream gain max abs diff=%g want 0", maxAbsDiff)
	}
}

func TestLibopus_APIRateMultistreamDecodeToInt16MatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate       = 48000
		channels         = 3
		frameSize        = sampleRate / 50
		gainQ8           = 8192
		encoderBitrate   = 256000
		encoderFrequency = 997
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(encoderBitrate)
	packets := make([][]byte, 2)
	for i := range packets {
		packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, encoderFrequency+float64(i)*113), frameSize)
		if err != nil {
			t.Fatalf("Encode packet %d: %v", i, err)
		}
		packets[i] = packet
	}

	want, err := decodeWithLibopusReferencePacketsInt16Gain(1, sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, nil, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference int16 decode gain", err)
	}
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got := make([]int16, 0, len(want))
	for i, packet := range packets {
		frame, err := dec.DecodeToInt16(packet, frameSize)
		if err != nil {
			t.Fatalf("DecodeToInt16 packet %d: %v", i, err)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("DecodeToInt16 sample count=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DecodeToInt16 sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

func TestLibopus_APIRateMultistreamCELTDecodeAndPLCMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encoderSampleRate = 48000
		channels          = 3
		encoderFrameSize  = encoderSampleRate / 50
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encoderSampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	pcm := generateTestSignal(channels, encoderFrameSize, encoderSampleRate, 997)
	packet, err := enc.Encode(pcm, encoderFrameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	streamPackets, err := parseMultistreamPacket(packet, streams)
	if err != nil {
		t.Fatalf("parseMultistreamPacket: %v", err)
	}
	for i, streamPacket := range streamPackets {
		if got := parseStreamTOC(streamPacket[0]).mode; got != streamModeCELT {
			t.Fatalf("stream %d mode=%d want CELT", i, got)
		}
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000} {
		frameSize := encoderFrameSize * sampleRate / encoderSampleRate
		t.Run("fs_"+strconv.Itoa(sampleRate), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "reference decode", err)
			}

			dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got64 := make([]float64, 0, len(want))
			for i, pkt := range sequence {
				frame, err := dec.Decode(pkt, frameSize)
				if err != nil {
					t.Fatalf("Decode sequence[%d]: %v", i, err)
				}
				if len(frame) != frameSize*channels {
					t.Fatalf("Decode sequence[%d] samples=%d want %d", i, len(frame)/channels, frameSize)
				}
				got64 = append(got64, frame...)
			}
			got := make([]float32, len(got64))
			for i, v := range got64 {
				got[i] = float32(v)
			}
			_, maxAbsDiff := computeDiffStatsF32(got, want)
			if maxAbsDiff > 3e-3 {
				t.Fatalf("api-rate CELT multistream max abs diff=%g want <=3e-3", maxAbsDiff)
			}
		})
	}
}
