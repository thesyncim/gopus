package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamForceChannelsStateHelper libopustest.HelperCache

type forceChannelsState struct {
	first     int32
	children  []int32
	getStatus int32
	childCode []int32
}

type forceChannelsPacket struct {
	status     int32
	rangeCode  int32
	finalRange uint32
	bytes      []byte
}

func TestMultistreamForceChannelsPartialFailureMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 3
		frameSize  = sampleRate / 50
		frameCount = 3
		maxBytes   = 8000
	)

	helper, err := multistreamForceChannelsStateHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "multistream force channels partial failure",
			OutputBase: "gopus_libopus_multistream_force_channels_state",
			SourceFile: "libopus_multistream_force_channels_state.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream force channels state", err)
	}
	for _, complexity := range []int{7, 9, 10} {
		t.Run(fmt.Sprintf("complexity_%d", complexity), func(t *testing.T) {
			frames := forceChannelsStatePCM(channels, frameSize, frameCount)
			input := libopustest.NewOraclePayloadVersion("GFCI", 2, uint32(complexity), frameSize, frameCount)
			for _, frame := range frames {
				for _, sample := range frame {
					input.I16(sample)
				}
			}

			reader, err := libopustest.RunOracleVersion(helper, input.Bytes(), "multistream force channels state", "GFCO", 2)
			if err != nil {
				t.Fatalf("run libopus force channels helper: %v", err)
			}
			if got := reader.U32(); got != 2 {
				t.Fatalf("libopus streams=%d, want 2", got)
			}
			if got := reader.U32(); got != 1 {
				t.Fatalf("libopus coupled streams=%d, want 1", got)
			}
			if got := reader.U32(); got != channels {
				t.Fatalf("libopus channels=%d, want %d", got, channels)
			}
			if got := reader.Bytes(channels); !bytes.Equal(got, []byte{0, 1, 2}) {
				t.Fatalf("libopus mapping=%v, want [0 1 2]", got)
			}
			initialState := readForceChannelsState(reader, 2)
			initialPacket := readForceChannelsPacket(reader)
			setStatus := reader.I32()
			failedState := readForceChannelsState(reader, 2)
			failedPacket := readForceChannelsPacket(reader)
			resetStatus := reader.I32()
			resetState := readForceChannelsState(reader, 2)
			recoveryStatus := reader.I32()
			recoveredState := readForceChannelsState(reader, 2)
			recoveredPacket := readForceChannelsPacket(reader)
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatalf("parse libopus force channels result: %v", err)
			}
			if setStatus != -1 {
				t.Fatalf("libopus SetForceChannels(2) status=%d, want OPUS_BAD_ARG (-1)", setStatus)
			}
			assertForceChannelsState(t, "libopus before control", initialState, []int32{-1000, -1000})
			assertForceChannelsState(t, "libopus after failed stereo broadcast", failedState, []int32{2, -1000})
			assertForceChannelsState(t, "libopus after reset", resetState, []int32{2, -1000})
			assertForceChannelsState(t, "libopus after auto recovery", recoveredState, []int32{-1000, -1000})
			if resetStatus != 0 {
				t.Fatalf("libopus reset status=%d, want OPUS_OK", resetStatus)
			}
			if recoveryStatus != 0 {
				t.Fatalf("libopus SetForceChannels(OPUS_AUTO) status=%d, want OPUS_OK", recoveryStatus)
			}

			enc, err := NewMultistreamEncoder(sampleRate, channels, 2, 1, []byte{0, 1, 2}, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewMultistreamEncoder: %v", err)
			}
			if err := enc.SetBitrate(256000); err != nil {
				t.Fatalf("SetBitrate(256000): %v", err)
			}
			if err := enc.SetComplexity(complexity); err != nil {
				t.Fatalf("SetComplexity(%d): %v", complexity, err)
			}
			if got := enc.ForceChannels(); got != -1 {
				t.Fatalf("ForceChannels() before control=%d, want -1", got)
			}
			goInitialPacket := encodeForceChannelsFrame(t, enc, frames[0], maxBytes)
			assertForceChannelsPacket(t, "before control", goInitialPacket, initialPacket)

			if got := enc.SetForceChannels(2); got != ErrInvalidForceChannels {
				t.Fatalf("SetForceChannels(2) error=%v, want %v", got, ErrInvalidForceChannels)
			}
			if got := enc.ForceChannels(); got != 2 {
				t.Fatalf("ForceChannels() after failed broadcast=%d, want 2 from first coupled stream", got)
			}
			goFailedPacket := encodeForceChannelsFrame(t, enc, frames[1], maxBytes)
			assertForceChannelsPacket(t, "after failed broadcast", goFailedPacket, failedPacket)

			enc.Reset()
			if got := enc.ForceChannels(); got != 2 {
				t.Fatalf("ForceChannels() after reset=%d, want 2", got)
			}
			if err := enc.SetForceChannels(-1); err != nil {
				t.Fatalf("SetForceChannels(-1) recovery: %v", err)
			}
			if got := enc.ForceChannels(); got != -1 {
				t.Fatalf("ForceChannels() after recovery=%d, want -1", got)
			}
			goRecoveredPacket := encodeForceChannelsFrame(t, enc, frames[2], maxBytes)
			assertForceChannelsPacket(t, "after reset and recovery", goRecoveredPacket, recoveredPacket)
		})
	}
}

func readForceChannelsState(reader *libopustest.OracleReader, streams int) forceChannelsState {
	state := forceChannelsState{
		getStatus: reader.I32(),
		first:     reader.I32(),
		children:  make([]int32, streams),
		childCode: make([]int32, streams),
	}
	if got := reader.Count(streams); got != streams {
		return state
	}
	for i := range streams {
		state.childCode[i] = reader.I32()
		state.children[i] = reader.I32()
	}
	return state
}

func assertForceChannelsState(t *testing.T, label string, got forceChannelsState, want []int32) {
	t.Helper()
	if got.getStatus != 0 {
		t.Fatalf("%s first-child GET status=%d, want OPUS_OK", label, got.getStatus)
	}
	if len(got.children) != len(want) {
		t.Fatalf("%s child count=%d, want %d", label, len(got.children), len(want))
	}
	if got.first != want[0] {
		t.Fatalf("%s first-child force channels=%d, want %d", label, got.first, want[0])
	}
	for i := range want {
		if got.childCode[i] != 0 {
			t.Fatalf("%s child %d GET status=%d, want OPUS_OK", label, i, got.childCode[i])
		}
		if got.children[i] != want[i] {
			t.Fatalf("%s child %d force channels=%d, want %d", label, i, got.children[i], want[i])
		}
	}
}

func readForceChannelsPacket(reader *libopustest.OracleReader) forceChannelsPacket {
	packet := forceChannelsPacket{
		status:     reader.I32(),
		rangeCode:  reader.I32(),
		finalRange: reader.U32(),
	}
	n := reader.Count(-1)
	packet.bytes = append([]byte(nil), reader.Bytes(n)...)
	return packet
}

func encodeForceChannelsFrame(t *testing.T, enc *MultistreamEncoder, pcm []int16, maxBytes int) forceChannelsPacket {
	t.Helper()
	out := make([]byte, maxBytes)
	n, err := enc.EncodeInt16(pcm, out)
	if err != nil {
		t.Fatalf("EncodeInt16: %v", err)
	}
	return forceChannelsPacket{
		status:     int32(n),
		rangeCode:  0,
		finalRange: enc.GetFinalRange(),
		bytes:      append([]byte(nil), out[:n]...),
	}
}

func assertForceChannelsPacket(t *testing.T, label string, got, want forceChannelsPacket) {
	t.Helper()
	if want.status <= 0 || want.rangeCode != 0 {
		t.Fatalf("libopus %s encode status/range status=%d rangeStatus=%d", label, want.status, want.rangeCode)
	}
	if got.status != want.status {
		t.Fatalf("%s packet length=%d, want libopus %d (final range Go/C=%08x/%08x)",
			label, got.status, want.status, got.finalRange, want.finalRange)
	}
	if got.finalRange != want.finalRange {
		t.Fatalf("%s final range=%08x, want libopus %08x", label, got.finalRange, want.finalRange)
	}
	if !bytes.Equal(got.bytes, want.bytes) {
		diff := firstForceChannelsByteDifference(got.bytes, want.bytes)
		t.Fatalf("%s packet differs from libopus: Go=%d bytes C=%d bytes first difference %s", label, len(got.bytes), len(want.bytes), diff)
	}
}

func firstForceChannelsByteDifference(got, want []byte) string {
	limit := min(len(got), len(want))
	for i := range limit {
		if got[i] != want[i] {
			return fmt.Sprintf("at %d Go=%02x C=%02x", i, got[i], want[i])
		}
	}
	return fmt.Sprintf("at end Go=%d C=%d", len(got), len(want))
}

func forceChannelsStatePCM(channels, frameSize, frameCount int) [][]int16 {
	frames := make([][]int16, frameCount)
	for frame := range frameCount {
		pcm := make([]int16, channels*frameSize)
		for sample := range frameSize {
			for channel := range channels {
				value := (sample*(97+channel*34) + frame*7919 + channel*11939) % 60001
				pcm[sample*channels+channel] = int16(value - 30000)
			}
		}
		frames[frame] = pcm
	}
	return frames
}
