package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// libopusRefdecodeMSFormatInt24 selects opus_multistream_decode24() in the
// multistream C helper (SAMPLE_FORMAT_INT24 = 2, libopus_refdecode_multistream.c).
const libopusRefdecodeMSFormatInt24 = 2

// decodeLibopusMultistreamInt24 drives libopus opus_multistream_decode24()
// through the refdecode_multistream C helper.
func decodeLibopusMultistreamInt24(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]int32, error) {
	reader, err := runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, 0, libopusRefdecodeMSFormatInt24, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]int32, nSamples)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// TestMultistreamDecodeInt24MatchesLibopus verifies that MultistreamDecoder.DecodeInt24
// produces near-exact output vs libopus opus_multistream_decode24() for stereo packets.
//
// The ≤1 LSB tolerance absorbs the documented darwin/arm64 1-ULP float drift
// in the CELT path (same budget as the float32/int16 multistream decode tests).
func TestMultistreamDecodeInt24MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 2
	)
	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	frameSize := enc.FrameSize()

	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	buf := make([]byte, 4000)
	n, err := enc.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encodedPacket := buf[:n]

	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	streams := dec.Streams()
	coupled := dec.CoupledStreams()
	mapping := defaultVorbisMapping(channels)

	want, err := decodeLibopusMultistreamInt24(sampleRate, channels, streams, coupled, frameSize, mapping, [][]byte{encodedPacket})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream reference decode int24", err)
	}

	got := make([]int32, frameSize*channels)
	nDec, err := dec.DecodeInt24(encodedPacket, got)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	got = got[:nDec*channels]

	// Apply the trusted near-exact quality bar (absorbs arm64 ≤1 LSB drift).
	assertInt24ParityNearExact(t, got, want, sampleRate, channels, "multistream int24 decode")
}

// TestMultistreamDecodeInt24SliceMatchesDecodeInt24 verifies that
// MultistreamDecoder.DecodeInt24Slice returns the same samples as DecodeInt24.
func TestMultistreamDecodeInt24SliceMatchesDecodeInt24(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
	)
	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	pcm := generateSurroundTestSignal(sampleRate, enc.FrameSize(), channels)
	buf := make([]byte, 4000)
	n, err := enc.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encodedPacket := buf[:n]

	dec1 := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	got24 := make([]int32, enc.FrameSize()*channels)
	n24, err := dec1.DecodeInt24(encodedPacket, got24)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	want := got24[:n24*channels]

	dec2 := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	got, err := dec2.DecodeInt24Slice(encodedPacket)
	if err != nil {
		t.Fatalf("DecodeInt24Slice: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

// defaultVorbisMapping returns the standard Vorbis channel mapping for the
// given channel count; used to build oracle payloads.
func defaultVorbisMapping(channels int) []byte {
	// RFC 7845 / Vorbis channel mappings for channels 1-8.
	mappings := [][]byte{
		{0},
		{0, 1},
		{0, 2, 1},
		{0, 1, 2, 3},
		{0, 4, 1, 2, 3},
		{0, 4, 1, 2, 3, 5},
		{0, 4, 1, 2, 3, 5, 6},
		{0, 6, 1, 2, 3, 4, 5, 7},
	}
	if channels >= 1 && channels <= 8 {
		return mappings[channels-1]
	}
	m := make([]byte, channels)
	for i := range m {
		m[i] = byte(i)
	}
	return m
}
