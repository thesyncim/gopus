//go:build gopus_qext

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeWithFEC96kValidatesPacketAndUsesRequestedPLCSize(t *testing.T) {
	const (
		channels   = 1
		frameSize  = 1920
		maxSamples = 5760
	)
	libopustest.RequireOracle(t)
	input := make([]float32, frameSize*2)
	for i := range input {
		input[i] = float32(0.3*math.Sin(2*math.Pi*6000*float64(i)/96000) +
			0.25*math.Sin(2*math.Pi*30000*float64(i)/96000))
	}
	encoded, err := libopustest.ProbeQEXTEncode96k(libopustest.QEXTEncode96kParams{
		Channels: channels, FrameSize: frameSize, Bitrate: 256000,
		Complexity: 10, MaxPacketSize: 8000, PCM: input, FrameCount: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT encoder", err)
		return
	}
	if len(encoded.Packets) != 2 || len(encoded.Packets[0]) <= 1 || len(encoded.Packets[1]) <= 1 {
		t.Fatalf("QEXT encoder packets have lengths %v, want two non-DTX packets", packetLengths(encoded.Packets))
	}

	malformed := []byte{0x01, 0x00}
	packets := [][]byte{encoded.Packets[0], malformed, encoded.Packets[1], encoded.Packets[1], nil}
	decodeFEC := []bool{false, true, true, false, true}
	want, err := probeDecodeWithFEC96kOracle(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     channels,
		SampleRate:   96000,
		MaxFrameSize: maxSamples,
		Packets:      packets,
		DecodeFEC:    decodeFEC,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC decoder", err)
		return
	}
	wantStatus := []int32{frameSize, -4, maxSamples, frameSize, maxSamples}
	if len(want.Status) != len(wantStatus) {
		t.Fatalf("C returned statuses %v, want %v", want.Status, wantStatus)
	}
	for i := range wantStatus {
		if want.Status[i] != wantStatus[i] {
			t.Fatalf("C step%d status=%d, want %d", i, want.Status[i], wantStatus[i])
		}
	}
	if want.FinalRanges[1] != want.FinalRanges[0] {
		t.Fatalf("C malformed FEC call changed final range: before=%08x after=%08x", want.FinalRanges[0], want.FinalRanges[1])
	}

	dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, maxSamples)
	pcmOffset := 0
	previousRange := uint32(0)
	previousDuration := 0
	previousDTX := false
	for step, packet := range packets {
		for i := range pcm {
			pcm[i] = float32(i) + 0.375
		}
		before := append([]float32(nil), pcm...)
		var n int
		var decodeErr error
		if decodeFEC[step] {
			n, decodeErr = dec.DecodeWithFEC(packet, pcm, true)
		} else {
			n, decodeErr = dec.Decode(packet, pcm)
		}
		if want.Status[step] < 0 {
			if n != 0 || decodeErr != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d; want malformed-packet rejection", step, n, decodeErr, want.Status[step])
			}
			if got := dec.FinalRange(); got != previousRange {
				t.Fatalf("step%d failed call changed final range to %08x, preceding range %08x", step, got, previousRange)
			}
			if got := dec.LastPacketDuration(); got != previousDuration {
				t.Fatalf("step%d failed call changed duration to %d, preceding duration %d", step, got, previousDuration)
			}
			if got := dec.InDTX(); got != previousDTX {
				t.Fatalf("step%d failed call changed DTX state to %v, preceding state %v", step, got, previousDTX)
			}
			for i := range pcm {
				if pcm[i] != before[i] {
					t.Fatalf("step%d failed call wrote PCM sample %d", step, i)
				}
			}
			continue
		}
		if decodeErr != nil || int32(n) != want.Status[step] {
			t.Fatalf("step%d decode=(%d,%v), C=%d", step, n, decodeErr, want.Status[step])
		}
		if got := dec.FinalRange(); got != want.FinalRanges[step] {
			t.Fatalf("step%d final range=%08x C=%08x", step, got, want.FinalRanges[step])
		}
		if got := dec.LastPacketDuration(); got != n {
			t.Fatalf("step%d duration=%d, decode returned %d", step, got, n)
		}
		count := n * channels
		if pcmOffset+count > len(want.PCM) {
			t.Fatalf("step%d C PCM ended at %d, need %d", step, len(want.PCM), pcmOffset+count)
		}
		for i := 0; i < count; i++ {
			if got, expected := math.Float32bits(pcm[i]), math.Float32bits(want.PCM[pcmOffset+i]); got != expected {
				t.Fatalf("step%d sample[%d]=%08x C=%08x", step, i, got, expected)
			}
		}
		pcmOffset += count
		previousRange = want.FinalRanges[step]
		previousDuration = n
		previousDTX = dec.InDTX()
	}
	if pcmOffset != len(want.PCM) {
		t.Fatalf("compared %d PCM samples, C returned %d", pcmOffset, len(want.PCM))
	}

	allocDec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
	if err != nil {
		t.Fatalf("NewDecoder for allocation check: %v", err)
	}
	allocPCM := make([]float32, maxSamples)
	allocN, allocErr := allocDec.DecodeWithFEC(encoded.Packets[1], allocPCM, true)
	if allocErr != nil || allocN != maxSamples {
		t.Fatalf("warm FEC decode=(%d,%v), want (%d,nil)", allocN, allocErr, maxSamples)
	}
	allocs := testing.AllocsPerRun(20, func() {
		allocN, allocErr = allocDec.DecodeWithFEC(encoded.Packets[1], allocPCM, true)
	})
	if allocErr != nil || allocN != maxSamples {
		t.Fatalf("measured FEC decode=(%d,%v), want (%d,nil)", allocN, allocErr, maxSamples)
	}
	if allocs != 0 {
		t.Fatalf("warmed 96 kHz CELT FEC decode allocations=%v, want 0", allocs)
	}
}

func TestDecodeWithFEC96kInvalidDurationPrecedesMalformedFraming(t *testing.T) {
	libopustest.RequireOracle(t)
	malformed := []byte{0x01, 0x00}
	want, err := probeDecodeWithFEC96kOracle(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     1,
		SampleRate:   96000,
		MaxFrameSize: 241,
		Packets:      [][]byte{malformed},
		DecodeFEC:    []bool{true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC decoder", err)
		return
	}
	if len(want.Status) != 1 || want.Status[0] != -1 {
		t.Fatalf("C status=%v, want [-1] for invalid FEC duration", want.Status)
	}
	if len(want.PCM) != 0 || want.FinalRanges[0] != 0 {
		t.Fatalf("C invalid-duration result emitted %d PCM samples or changed range to %08x", len(want.PCM), want.FinalRanges[0])
	}

	dec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]float32, 241)
	for i := range pcm {
		pcm[i] = float32(i) + 0.25
	}
	before := append([]float32(nil), pcm...)
	if n, err := dec.DecodeWithFEC(malformed, pcm, true); n != 0 || err != ErrInvalidFrameSize {
		t.Fatalf("DecodeWithFEC invalid duration=(%d,%v), want (0,%v)", n, err, ErrInvalidFrameSize)
	}
	if dec.LastPacketDuration() != 0 || dec.FinalRange() != want.FinalRanges[0] {
		t.Fatalf("invalid-duration call changed decoder history: duration=%d range=%08x", dec.LastPacketDuration(), dec.FinalRange())
	}
	for i := range pcm {
		if pcm[i] != before[i] {
			t.Fatalf("invalid-duration call wrote PCM sample %d", i)
		}
	}

	// MaxPacketBytes is a Go decoder configuration limit that libopus does not
	// expose. It continues to take precedence over framing and duration parsing.
	cfg := DefaultDecoderConfig(96000, 1)
	cfg.MaxPacketBytes = 1
	limited, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder with MaxPacketBytes=1: %v", err)
	}
	if n, err := limited.DecodeWithFEC(malformed, pcm, true); n != 0 || err != ErrPacketTooLarge {
		t.Fatalf("DecodeWithFEC over MaxPacketBytes=(%d,%v), want (0,%v)", n, err, ErrPacketTooLarge)
	}
}

func TestDecodeWithFEC96kSuppliedSILKAndHybridPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		withLBRR  bool
	}{
		{name: "silk", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 16000},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, withLBRR: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seed, recovery []byte
			if tc.withLBRR {
				seed, recovery = encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, 1, 960)
			} else {
				seed = encodeAPIRateSILKPacket(t, 1)
				recovery = encodeAPIRateSILKPacket(t, 1)
			}
			packets := [][]byte{seed, recovery, recovery}
			decodeFEC := []bool{false, true, false}
			want, err := probeDecodeWithFEC96kOracle(libopustest.QEXTDecode96kParams{
				SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
				Channels:     1,
				SampleRate:   96000,
				MaxFrameSize: 1920,
				Packets:      packets,
				DecodeFEC:    decodeFEC,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC decoder", err)
				return
			}
			if len(want.Status) != len(packets) || want.Status[0] != 1920 || want.Status[1] != 1920 || want.Status[2] != 1920 {
				t.Fatalf("C statuses=%v, want [1920 1920 1920]", want.Status)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			pcm := make([]float32, 1920)
			pcmOffset := 0
			for step, packet := range packets {
				var n int
				if decodeFEC[step] {
					n, err = dec.DecodeWithFEC(packet, pcm, true)
				} else {
					n, err = dec.Decode(packet, pcm)
				}
				if err != nil || int32(n) != want.Status[step] {
					t.Fatalf("step%d decode=(%d,%v), C=%d", step, n, err, want.Status[step])
				}
				if got := dec.FinalRange(); got != want.FinalRanges[step] {
					t.Fatalf("step%d final range=%08x C=%08x", step, got, want.FinalRanges[step])
				}
				for i := 0; i < n; i++ {
					if got, expected := math.Float32bits(pcm[i]), math.Float32bits(want.PCM[pcmOffset+i]); got != expected {
						t.Fatalf("step%d sample[%d]=%08x C=%08x", step, i, got, expected)
					}
				}
				pcmOffset += n
			}
			if pcmOffset != len(want.PCM) {
				t.Fatalf("compared %d PCM samples, C returned %d", pcmOffset, len(want.PCM))
			}
			if tc.withLBRR {
				allocDec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
				if err != nil {
					t.Fatalf("NewDecoder for allocation check: %v", err)
				}
				allocPCM := make([]float32, 1920)
				if _, err := allocDec.Decode(seed, allocPCM); err != nil {
					t.Fatalf("warm seed decode: %v", err)
				}
				allocN, allocErr := allocDec.DecodeWithFEC(recovery, allocPCM, true)
				if allocErr != nil || allocN != 1920 {
					t.Fatalf("warm Hybrid FEC decode=(%d,%v), want (1920,nil)", allocN, allocErr)
				}
				allocs := testing.AllocsPerRun(20, func() {
					allocN, allocErr = allocDec.DecodeWithFEC(recovery, allocPCM, true)
				})
				if allocErr != nil || allocN != 1920 {
					t.Fatalf("measured Hybrid FEC decode=(%d,%v), want (1920,nil)", allocN, allocErr)
				}
				if allocs != 0 {
					t.Fatalf("warmed 96 kHz Hybrid FEC allocations=%v, want 0", allocs)
				}
			}
		})
	}
}

func packetLengths(packets [][]byte) []int {
	lens := make([]int, len(packets))
	for i := range packets {
		lens[i] = len(packets[i])
	}
	return lens
}
