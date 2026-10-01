//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func fixedOuterRawPCM(frameSize, channels, frames int) []int16 {
	pcm := make([]int16, frameSize*channels*frames)
	state := uint32(6716)
	for i := range pcm {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		pcm[i] = int16(int32(state) >> 19)
	}
	return pcm
}

func TestFixedOuterOpusEncodeRecordsPreserveCalls(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	p := libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize, FrameCount: frames,
		PCM: fixedOuterRawPCM(frameSize, 1, frames),
	}
	records, err := libopustest.ProbeOpusEncodeFixedRecords(p)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := libopustest.ProbeOpusEncodeFixed(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != frames || len(legacy) != frames {
		t.Fatalf("C records=%d legacy=%d want=%d", len(records), len(legacy), frames)
	}
	for i, record := range records {
		if record.Status != 0 || !bytes.Equal(record.Packet, legacy[i]) {
			t.Fatalf("C record %d status=%d len=%d legacy=%d", i, record.Status, len(record.Packet), len(legacy[i]))
		}
	}
}

func TestFixedDCRejectQ8MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			input := make([]int32, rate/50*channels)
			for i := range input {
				input[i] = int32((i*317)%65535-32768)<<8 + int32(i%251)
			}
			memory := [4]int32{637281, -111, -625541, 222}
			want, wantMem, err := libopustest.ProbeFixedDCReject(input, memory, rate, channels, 3)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int32, len(input))
			fixedDCRejectRes(input, got, &memory, rate, channels, 3)
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("rate=%d channels=%d sample=%d got=%d want=%d", rate, channels, i, got[i], want[i])
				}
			}
			if memory != wantMem {
				t.Fatalf("rate=%d channels=%d memory=%v want=%v", rate, channels, memory, wantMem)
			}
		}
	}
}

func TestFixedOuterOpusEncodeRawInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	p := libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize, FrameCount: frames,
		PCM: fixedOuterRawPCM(frameSize, 1, frames),
	}
	inputFrames := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
	for frame := range inputFrames {
		inputFrames[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format:   0,
			ShortPCM: p.PCM[frame*frameSize : (frame+1)*frameSize],
		}
	}
	want, err := probePublicFixedMixedRecords(p, inputFrames)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEncoder(48000, 1)
	e.SetMode(ModeCELT)
	e.SetBandwidth(types.BandwidthFullband)
	e.SetComplexity(0)
	e.SetBitrate(64000)
	e.SetBitrateMode(ModeCBR)
	e.SetForceChannels(1)
	pcm := make([]float32, frameSize)
	for frame := range frames {
		for i := range pcm {
			pcm[i] = float32(p.PCM[frame*frameSize+i]) * (1.0 / 32768.0)
		}
		got, err := e.EncodeShortMixedWithAnalysisMaxBytes(pcm, frameSize, pcm, 4000)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
		}
		if want[frame].Status != 0 {
			t.Fatalf("C frame %d status=%d", frame, want[frame].Status)
		}
		if gotRange := e.FinalRange(); gotRange != want[frame].FinalRange {
			t.Fatalf("frame %d range=%08x want=%08x", frame, gotRange, want[frame].FinalRange)
		}
		if !bytes.Equal(got, want[frame].Packet) {
			first := 0
			for first < min(len(got), len(want[frame].Packet)) && got[first] == want[frame].Packet[first] {
				first++
			}
			t.Fatalf("frame %d packet differs at byte %d, len=%d want=%d", frame, first, len(got), len(want[frame].Packet))
		}
	}
}
