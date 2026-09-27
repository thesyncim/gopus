//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var fixedQEXTNative96KEncodeAllocSink int

func TestCELTFixedQEXTNative96KFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range [...]int{240, 480, 960, 1920} {
		for _, channels := range [...]int{1, 2} {
			t.Run(fmt.Sprintf("frame_%d/channels_%d", frameSize, channels), func(t *testing.T) {
				pcm := make([]int32, channels*frameSize)
				for i := range pcm {
					x := int32((i*7919+17)%65536 - 32768)
					pcm[i] = x * 64
					if i/channels >= frameSize/3 && i/channels < frameSize/3+frameSize/32 {
						pcm[i] *= 3
					}
				}
				const maxBytes = 640
				params := libopustest.CELTFixedQ8Params{
					SampleRate: 96000, Channels: channels, StreamChannels: channels,
					FrameSize: frameSize, Start: 0, End: 21, Bitrate: 256000,
					Complexity: 0, LSBDepth: 24, Frames: []libopustest.CELTFixedQ8Frame{{
						PCM: pcm, MaxBytes: maxBytes,
					}},
				}
				want, err := libopustest.ProbeCELTFixedQEXTQ8(params)
				if err != nil {
					libopustest.HelperUnavailable(t, "native 96 kHz fixed-QEXT CELT Q8", err)
					return
				}

				enc := NewCELTEncoderRate(channels, 96000)
				enc.SetQEXTEnabled(false)
				enc.SetStreamChannels(int32(channels))
				enc.SetBandRange(0, 21)
				enc.SetBitrate(params.Bitrate)
				enc.SetComplexity(params.Complexity)
				enc.SetLSBDepth(params.LSBDepth)
				enc.SetVBR(false)
				enc.SetConstrainedVBR(false)
				buffer := make([]byte, maxBytes)
				rng := &rangecoding.Encoder{}
				rng.Init(buffer)
				n := enc.EncodeWithECRes(pcm, frameSize, rng, maxBytes)
				got := rng.Buffer()[:n]
				if n != len(want[0].Packet) || rng.Range() != want[0].FinalRange ||
					enc.FinalRange() != want[0].FinalRange || !bytes.Equal(got, want[0].Packet) {
					first := 0
					for first < len(got) && first < len(want[0].Packet) && got[first] == want[0].Packet[first] {
						first++
					}
					t.Fatalf("native 96 kHz fixed-QEXT frame differs: n=%d/%d range=%08x/%08x first_byte=%d got=% x want=% x",
						n, len(want[0].Packet), rng.Range(), want[0].FinalRange, first, got, want[0].Packet)
				}
			})
		}
	}
}

func TestCELTFixedQEXTNative96KSidePayloadMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range [...]int{240, 480, 960, 1920} {
		for _, channels := range [...]int{1, 2} {
			t.Run(fmt.Sprintf("frame_%d/channels_%d", frameSize, channels), func(t *testing.T) {
				pcm := make([]int32, channels*frameSize)
				for i := range pcm {
					x := int32((i*7919+17)%65536 - 32768)
					pcm[i] = x * 64
					if i/channels >= frameSize/3 && i/channels < frameSize/3+frameSize/32 {
						pcm[i] *= 3
					}
				}
				const maxBytes = 640
				params := libopustest.CELTFixedQEXTMainParams{
					Channels: channels, StreamChannels: channels, FrameSize: frameSize,
					Start: 0, End: 21, MaxBytes: maxBytes,
					Bitrate: 256000, Complexity: 0, SampleRate: 96000,
					LSBDepth: 24, PCM: pcm,
				}
				want, err := libopustest.ProbeCELTFixedQEXTMain(params)
				if err != nil {
					libopustest.HelperUnavailable(t, "native 96 kHz fixed-QEXT CELT side payload", err)
					return
				}
				if !want.HasQEXT || len(want.QEXTPayload) < 2 || want.QEXTPayload[0] != 0xF8 {
					t.Fatalf("native 96 kHz C frame did not reserve QEXT: packet=% x", want.Packet)
				}

				enc := NewCELTEncoderRate(channels, 96000)
				enc.SetQEXTEnabled(true)
				enc.SetStreamChannels(int32(channels))
				enc.SetBandRange(0, 21)
				enc.SetBitrate(params.Bitrate)
				enc.SetComplexity(params.Complexity)
				enc.SetLSBDepth(params.LSBDepth)
				enc.SetVBR(params.VBR)
				enc.SetConstrainedVBR(params.ConstrainedVBR)
				buffer := make([]byte, maxBytes)
				rng := &rangecoding.Encoder{}
				rng.Init(buffer)
				n := enc.EncodeWithECRes(pcm, frameSize, rng, maxBytes)
				got := rng.Buffer()[:n]
				gotSide := enc.LastQEXTPayload()
				wantSide := want.QEXTPayload[1:]
				if n != len(want.MainPacket) || rng.Range() != want.MainRange ||
					enc.FinalRange() != want.FinalRange || !bytes.Equal(got, want.MainPacket) || !bytes.Equal(gotSide, wantSide) {
					t.Fatalf("native 96 kHz QEXT differs: main=%d/%d mainRange=%08x/%08x final=%08x/%08x side=%d/%d firstMain=%d firstSide=%d",
						n, len(want.MainPacket), rng.Range(), want.MainRange, enc.FinalRange(), want.FinalRange,
						len(gotSide), len(wantSide), firstByteDiff(got, want.MainPacket), firstByteDiff(gotSide, wantSide))
				}
			})
		}
	}
}

func TestCELTFixedQEXTNative96KShortFrameNoSidePayloadMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range [...]int{240, 480} {
		for _, channels := range [...]int{1, 2} {
			t.Run(fmt.Sprintf("frame_%d/channels_%d", frameSize, channels), func(t *testing.T) {
				pcm := make([]int32, channels*frameSize)
				for i := range pcm {
					pcm[i] = int32((i*7919+17)%65536-32768) * 64
				}
				const maxBytes = 640
				params := libopustest.CELTFixedQEXTMainParams{
					Channels: channels, StreamChannels: channels, FrameSize: frameSize,
					Start: 0, End: 21, MaxBytes: maxBytes,
					Bitrate: 64000, Complexity: 0, SampleRate: 96000,
					LSBDepth: 24, PCM: pcm,
				}
				want, err := libopustest.ProbeCELTFixedQEXTMain(params)
				if err != nil {
					libopustest.HelperUnavailable(t, "native 96 kHz fixed-QEXT no-side CELT frame", err)
					return
				}
				if want.HasQEXT {
					t.Fatalf("C reserved QEXT at short-frame low budget: frame=%d channels=%d packet=% x", frameSize, channels, want.Packet)
				}

				enc := NewCELTEncoderRate(channels, 96000)
				enc.SetQEXTEnabled(true)
				enc.SetStreamChannels(int32(channels))
				enc.SetBandRange(0, 21)
				enc.SetBitrate(params.Bitrate)
				enc.SetComplexity(params.Complexity)
				enc.SetLSBDepth(params.LSBDepth)
				enc.SetVBR(false)
				enc.SetConstrainedVBR(false)
				buffer := make([]byte, maxBytes)
				rng := &rangecoding.Encoder{}
				rng.Init(buffer)
				n := enc.EncodeWithECRes(pcm, frameSize, rng, maxBytes)
				got := rng.Buffer()[:n]
				if len(enc.LastQEXTPayload()) != 0 || n != len(want.Packet) ||
					rng.Range() != want.MainRange || enc.FinalRange() != want.FinalRange || !bytes.Equal(got, want.Packet) {
					t.Fatalf("native 96 kHz no-side frame differs: n=%d/%d range=%08x/%08x final=%08x/%08x got=% x want=% x",
						n, len(want.Packet), rng.Range(), want.MainRange, enc.FinalRange(), want.FinalRange, got, want.Packet)
				}
			})
		}
	}
}

func TestCELTFixedQEXTNative96KStatefulResetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels = 2
	const frameSize = 1920
	const maxBytes = 640
	makePCM := func(seed int) []int32 {
		pcm := make([]int32, channels*frameSize)
		for i := range pcm {
			x := int32((i*7919+seed*104729+17)%65536 - 32768)
			pcm[i] = x * 64
			if i/channels >= frameSize/3 && i/channels < frameSize/3+frameSize/32 {
				pcm[i] *= 3
			}
		}
		return pcm
	}
	frames := []libopustest.CELTFixedQ8Frame{
		{PCM: makePCM(0), MaxBytes: maxBytes},
		{PCM: makePCM(1), MaxBytes: maxBytes},
		{PCM: makePCM(2), MaxBytes: maxBytes, ResetBefore: true},
	}
	params := libopustest.CELTFixedQ8Params{
		SampleRate: 96000, Channels: channels, StreamChannels: channels,
		FrameSize: frameSize, Start: 0, End: 21, Bitrate: 256000,
		Complexity: 0, LSBDepth: 24, QEXTEnabled: true, Frames: frames,
	}
	want, err := libopustest.ProbeCELTFixedQEXTQ8State(params)
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz fixed-QEXT stateful CELT Q8", err)
		return
	}

	enc := NewCELTEncoderRate(channels, 96000)
	enc.SetQEXTEnabled(true)
	enc.SetStreamChannels(channels)
	enc.SetBandRange(0, 21)
	enc.SetBitrate(params.Bitrate)
	enc.SetComplexity(params.Complexity)
	enc.SetLSBDepth(params.LSBDepth)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	for i, frame := range frames {
		if frame.ResetBefore {
			enc.Reset()
			enc.SetQEXTEnabled(true)
		}
		buffer := make([]byte, maxBytes)
		rng := &rangecoding.Encoder{}
		rng.Init(buffer)
		n := enc.EncodeWithECRes(frame.PCM, frameSize, rng, frame.MaxBytes)
		main := rng.Buffer()[:n]
		packet := fixedQEXTPacket(main, enc.LastQEXTPayload())
		if !bytes.Equal(packet, want[i].Packet) || enc.FinalRange() != want[i].State.RNG {
			t.Fatalf("native 96 kHz QEXT history frame %d differs: packet=%d/%d range=%08x/%08x first_byte=%d",
				i, len(packet), len(want[i].Packet), enc.FinalRange(), want[i].State.RNG,
				firstByteDiff(packet, want[i].Packet))
		}
	}
}

func TestCELTFixedQEXTNative96KEncodeDoesNotAllocateAfterWarmup(t *testing.T) {
	const channels = 2
	const frameSize = 1920
	const maxBytes = 640
	pcm := make([]int32, channels*frameSize)
	for i := range pcm {
		pcm[i] = int32((i*7919+17)%65536-32768) * 64
	}
	enc := NewCELTEncoderRate(channels, 96000)
	enc.SetQEXTEnabled(true)
	enc.SetStreamChannels(channels)
	enc.SetBandRange(0, 21)
	enc.SetBitrate(256000)
	enc.SetComplexity(0)
	enc.SetLSBDepth(24)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	buffer := make([]byte, maxBytes)
	rng := &rangecoding.Encoder{}
	encode := func() {
		rng.Init(buffer)
		fixedQEXTNative96KEncodeAllocSink = enc.EncodeWithECRes(pcm, frameSize, rng, maxBytes)
	}
	encode()
	encode()
	if got := testing.AllocsPerRun(50, encode); got != 0 {
		t.Fatalf("warmed native 96 kHz fixed-QEXT encode allocs = %g, want 0", got)
	}
}

func fixedQEXTPacket(main, side []byte) []byte {
	extBytes := 1 + len(side) // QEXT extension ID plus its range-coded payload.
	paddingBytes := (extBytes + 253) / 254
	packet := make([]byte, 1+paddingBytes+len(main)+extBytes)
	packet[0] = 0x41 // C celt_encoder.c marks the QEXT packet as padded.
	for i := 0; i < paddingBytes-1; i++ {
		packet[1+i] = 255
	}
	lastPadding := extBytes % 254
	if lastPadding == 0 {
		lastPadding = 254
	}
	packet[paddingBytes] = byte(lastPadding)
	mainStart := 1 + paddingBytes
	copy(packet[mainStart:], main)
	qextStart := mainStart + len(main)
	packet[qextStart] = 0xF8
	copy(packet[qextStart+1:], side)
	return packet
}

func firstByteDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}
