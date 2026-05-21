package gopus

import (
	"testing"

	mspkg "github.com/thesyncim/gopus/multistream"
)

func TestMultistreamDecodeUsesAPIRatePacketDuration(t *testing.T) {
	modes := []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	}
	for _, mode := range modes {
		mode := mode
		for _, channels := range []int{1, 2} {
			channels := channels
			packet := mode.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				sampleRate := sampleRate
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					want, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					if got, err := mspkg.PacketDurationAtRate(packet, 1, sampleRate); err != nil || got != want {
						t.Fatalf("multistream PacketDurationAtRate()=(%d,%v) want (%d,nil)", got, err, want)
					}

					smallDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					if _, err := smallDec.Decode(packet, make([]float32, want*channels-1)); err != ErrBufferTooSmall {
						t.Fatalf("Decode small-buffer error=%v want %v", err, ErrBufferTooSmall)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					pcm := make([]float32, want*channels)
					n, err := dec.Decode(packet, pcm)
					if err != nil {
						t.Fatalf("Decode: %v", err)
					}
					if n != want {
						t.Fatalf("Decode samples=%d want %d", n, want)
					}
					if got := dec.LastPacketDuration(); got != want {
						t.Fatalf("LastPacketDuration()=%d want %d", got, want)
					}

					plc := make([]float32, want*channels)
					n, err = dec.Decode(nil, plc)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != want {
						t.Fatalf("Decode(nil) samples=%d want %d", n, want)
					}
					if got := dec.LastPacketDuration(); got != want {
						t.Fatalf("LastPacketDuration() after PLC=%d want %d", got, want)
					}

					intDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					pcm16 := make([]int16, want*channels)
					n, err = intDec.DecodeInt16(packet, pcm16)
					if err != nil {
						t.Fatalf("DecodeInt16: %v", err)
					}
					if n != want {
						t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
					}
				})
			}
		}
	}
}

func TestMultistreamColdPLCAfterResetUsesAPIRateDefault(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				want := sampleRate / 50

				pcm := make([]float32, want*channels)
				n, err := dec.Decode(nil, pcm)
				if err != nil {
					t.Fatalf("cold Decode(nil): %v", err)
				}
				if n != want {
					t.Fatalf("cold Decode(nil) samples=%d want %d", n, want)
				}
				if got := dec.LastPacketDuration(); got != want {
					t.Fatalf("cold LastPacketDuration()=%d want %d", got, want)
				}

				dec.Reset()
				n, err = dec.Decode(nil, pcm)
				if err != nil {
					t.Fatalf("Decode(nil) after Reset: %v", err)
				}
				if n != want {
					t.Fatalf("Decode(nil) after Reset samples=%d want %d", n, want)
				}
				if got := dec.LastPacketDuration(); got != want {
					t.Fatalf("LastPacketDuration() after reset cold PLC=%d want %d", got, want)
				}
			})
		}
	}
}
