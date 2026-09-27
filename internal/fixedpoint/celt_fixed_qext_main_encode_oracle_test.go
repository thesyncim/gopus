//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestCELTFixedQEXTMainPayloadMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range [...]int{1, 2} {
		t.Run(fmt.Sprintf("channels_%d", channels), func(t *testing.T) {
			pcm := make([]int32, channels*960)
			for i := range pcm {
				x := int32((i*7919+17)%65536 - 32768)
				pcm[i] = x * 64
				if i/channels >= 410 && i/channels < 438 {
					pcm[i] *= 3
				}
			}
			const maxBytes = 16 // QEXT's extension reservation is inactive at this capacity.
			params := libopustest.CELTFixedQEXTMainParams{
				Channels: channels, StreamChannels: channels, FrameSize: 960,
				Start: 0, End: 21, MaxBytes: maxBytes,
				Bitrate: 64000, Complexity: 0, SampleRate: 48000,
				LSBDepth: 24, PCM: pcm,
			}
			want, err := libopustest.ProbeCELTFixedQEXTMain(params)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT CELT main-payload", err)
				return
			}

			enc := NewCELTEncoderRate(channels, 48000)
			enc.SetQEXTEnabled(true)
			enc.SetStreamChannels(int32(channels))
			enc.SetBandRange(0, 21)
			enc.SetBitrate(64000)
			enc.SetComplexity(0)
			enc.SetLSBDepth(24)
			enc.SetVBR(false)
			enc.SetConstrainedVBR(false)
			buffer := make([]byte, maxBytes)
			rng := &rangecoding.Encoder{}
			rng.Init(buffer)
			n := enc.EncodeWithECRes(pcm, 960, rng, maxBytes)
			got := rng.Buffer()[:n]
			if n != len(want.Packet) || rng.Range() != want.FinalRange || enc.FinalRange() != want.FinalRange ||
				len(enc.LastQEXTPayload()) != 0 || !bytes.Equal(got, want.Packet) {
				first := 0
				for first < len(got) && first < len(want.Packet) && got[first] == want.Packet[first] {
					first++
				}
				t.Fatalf("fixed-QEXT CELT main differs: len=%d/%d range=%08x/%08x first_byte=%d got=% x want=% x",
					n, len(want.Packet), rng.Range(), want.FinalRange, first, got, want.Packet)
			}
		})
	}
}

func TestCELTFixedQEXTReservedMainPayloadMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range [...]int{1, 2} {
		t.Run(fmt.Sprintf("channels_%d", channels), func(t *testing.T) {
			pcm := make([]int32, channels*960)
			for i := range pcm {
				x := int32((i*7919+17)%65536 - 32768)
				pcm[i] = x * 64
				if i/channels >= 410 && i/channels < 438 {
					pcm[i] *= 3
				}
			}
			const maxBytes = 640
			params := libopustest.CELTFixedQEXTMainParams{
				Channels: channels, StreamChannels: channels, FrameSize: 960,
				Start: 0, End: 21, MaxBytes: maxBytes,
				Bitrate: 256000, Complexity: 0, SampleRate: 48000,
				LSBDepth: 24, PCM: pcm,
			}
			want, err := libopustest.ProbeCELTFixedQEXTMain(params)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT CELT main-payload", err)
				return
			}
			if !want.HasQEXT || len(want.QEXTPayload) < 2 || want.QEXTPayload[0] != 0xF8 || len(want.MainPacket) == 0 {
				t.Fatalf("reference did not produce separated QEXT/main payloads: packet=% x", want.Packet)
			}
			wantSidePayload := want.QEXTPayload[1:] // The framing parser includes the QEXT extension ID.

			enc := NewCELTEncoderRate(channels, 48000)
			enc.SetQEXTEnabled(true)
			enc.SetStreamChannels(int32(channels))
			enc.SetBandRange(0, 21)
			enc.SetBitrate(256000)
			enc.SetComplexity(0)
			enc.SetLSBDepth(24)
			enc.SetVBR(false)
			enc.SetConstrainedVBR(false)
			// The C reservation block sees the caller's original capacity, then
			// shrinks the main coder after reserving its side payload.
			buffer := make([]byte, maxBytes)
			rng := &rangecoding.Encoder{}
			rng.Init(buffer)
			n := enc.EncodeWithECRes(pcm, 960, rng, maxBytes)
			got := rng.Buffer()[:n]
			gotQEXT := enc.LastQEXTPayload()
			if n != len(want.MainPacket) || rng.Range() != want.MainRange || enc.FinalRange() != want.FinalRange ||
				!bytes.Equal(got, want.MainPacket) || !bytes.Equal(gotQEXT, wantSidePayload) {
				first := 0
				for first < len(got) && first < len(want.MainPacket) && got[first] == want.MainPacket[first] {
					first++
				}
				end := first + 8
				if end > len(got) {
					end = len(got)
				}
				wantEnd := end
				if wantEnd > len(want.MainPacket) {
					wantEnd = len(want.MainPacket)
				}
				qextFirst := 0
				for qextFirst < len(gotQEXT) && qextFirst < len(wantSidePayload) && gotQEXT[qextFirst] == wantSidePayload[qextFirst] {
					qextFirst++
				}
				t.Fatalf("reserved-main mismatch: len=%d/%d range=%08x/%08x final=%08x/%08x tell=%d/%d tellFrac=%d/%d first_byte=%d diffs=%d window=% x/% x qext=%d/%d qext_first=%d qext_diffs=%d",
					n, len(want.MainPacket), rng.Range(), want.MainRange, enc.FinalRange(), want.FinalRange, rng.Tell(), want.Tell, rng.TellFrac(), want.TellFrac, first,
					countByteDiffs(got, want.MainPacket), got[max(0, first-4):end], want.MainPacket[max(0, first-4):wantEnd],
					len(gotQEXT), len(wantSidePayload), qextFirst, countByteDiffs(gotQEXT, wantSidePayload))
			}
		})
	}
}

func countByteDiffs(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	diffs := len(a) - n + len(b) - n
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			diffs++
		}
	}
	return diffs
}
