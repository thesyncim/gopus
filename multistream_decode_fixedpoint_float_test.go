//go:build gopus_fixed_point

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedMultistreamDecodeFloat32MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, frameSize = 48000, 960
	for _, mode := range []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	} {
		for _, channels := range []int{1, 2} {
			t.Run(mode.name+"_ch_"+itoaSmall(channels), func(t *testing.T) {
				packet := mode.packet(t, channels)
				frameSizeAtRate, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packet duration: %v", err)
				}
				if frameSizeAtRate != frameSize {
					t.Fatalf("packet duration=%d want %d", frameSizeAtRate, frameSize)
				}
				coupled := 0
				mapping := []byte{0}
				if channels == 2 {
					coupled = 1
					mapping = []byte{0, 1}
				}
				want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, 1, coupled, frameSize, mapping, [][]byte{packet})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-point multistream float decode", err)
				}

				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				pcm := make([]float32, frameSize*channels)
				if n, err := dec.Decode(packet, pcm); err != nil || n != frameSize {
					t.Fatalf("Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
				}
				assertMultistreamCallerFloatBits(t, pcm, want)

				// An undersized attempt must leave decoder state ready for retry.
				retry := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				short := make([]float32, frameSize*channels-channels)
				if _, err := retry.Decode(packet, short); err != ErrBufferTooSmall {
					t.Fatalf("Decode undersized buffer error=%v, want %v", err, ErrBufferTooSmall)
				}
				retryPCM := make([]float32, frameSize*channels)
				if n, err := retry.Decode(packet, retryPCM); err != nil || n != frameSize {
					t.Fatalf("Decode retry=(%d,%v), want (%d,nil)", n, err, frameSize)
				}
				assertMultistreamCallerFloatBits(t, retryPCM, want)

				for range 2 {
					if n, err := dec.Decode(packet, pcm); err != nil || n != frameSize {
						t.Fatalf("warmup Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
					}
				}
				if allocs := testing.AllocsPerRun(100, func() {
					if n, err := dec.Decode(packet, pcm); err != nil || n != frameSize {
						t.Fatalf("warm Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
					}
				}); allocs != 0 {
					t.Fatalf("warm fixed-point float multistream decode allocations=%g want 0", allocs)
				}
			})
		}
	}
}

func TestFixedMultistreamDecodeFloat32PLCRecoveryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, frameSize = 48000, 960
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)
			coupled := channels - 1
			mapping := []byte{0}
			if channels == 2 {
				mapping = []byte{0, 1}
			}
			sequence := [][]byte{packet, nil, packet, nil, packet}
			want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, 1, coupled, frameSize, mapping, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed-point multistream Float32 PLC/recovery", err)
			}

			dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
			pcm := make([]float32, frameSize*channels)
			for i, input := range sequence {
				n, err := dec.Decode(input, pcm)
				if err != nil || n != frameSize {
					t.Fatalf("Decode sequence frame %d=(%d,%v), want (%d,nil)", i, n, err, frameSize)
				}
				start := i * len(pcm)
				assertMultistreamCallerFloatBits(t, pcm, want[start:start+len(pcm)])
			}

			warm := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
			warmPCM := make([]float32, frameSize*channels)
			decodeCycle := func() {
				for _, input := range sequence[:3] {
					if n, err := warm.Decode(input, warmPCM); err != nil || n != frameSize {
						t.Fatalf("warm Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
					}
				}
			}
			decodeCycle()
			decodeCycle()
			if allocs := testing.AllocsPerRun(100, decodeCycle); allocs != 0 {
				t.Fatalf("warm fixed-point float multistream PLC/recovery allocations=%g want 0", allocs)
			}
		})
	}
}
