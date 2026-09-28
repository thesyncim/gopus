package gopus

import (
	"math"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeWithFECHybridToSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	for _, sampleRate := range []int{16000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, gainQ8 := range []int{0, 768} {
				for _, hasLBRR := range []bool{false, true} {
					name := "no_lbrr"
					if hasLBRR {
						name = "lbrr"
					}
					t.Run(strconv.Itoa(sampleRate)+"hz/ch"+strconv.Itoa(channels)+"/gain"+strconv.Itoa(gainQ8)+"/"+name, func(t *testing.T) {
						const primeFrameSize48 = 960
						frameSize := sampleRate * 3 / 25 // 120 ms, including the PLC prefix.
						primeFrameSize := primeFrameSize48 * sampleRate / 48000
						prime := encodeAPIRateHybridPacketFrameSize(t, channels, primeFrameSize48)
						var recovery []byte
						if hasLBRR {
							_, recovery = encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, 960)
							if !PacketHasLBRR(recovery) {
								t.Fatal("recovery packet does not carry LBRR")
							}
						} else {
							recovery = encodeAPIRateSILKPacketFrameSize(t, channels, 480)
							if len(recovery) <= 1 || PacketHasLBRR(recovery) {
								t.Fatalf("no-LBRR SILK packet length=%d hasLBRR=%v", len(recovery), PacketHasLBRR(recovery))
							}
						}
						if got := ParseTOC(recovery[0]).Mode; got != ModeSILK {
							t.Fatalf("recovery packet mode=%v want SILK", got)
						}

						steps := []libopusAPIRateDecodeStep{
							{packet: prime},
							{packet: recovery, fec: true},
						}
						want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, gainQ8, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "Hybrid-to-SILK FEC transition reference", err)
						}
						stride := frameSize * channels
						if len(want) != (primeFrameSize+frameSize)*channels || len(ranges) != len(steps) {
							t.Fatalf("reference samples/ranges=%d/%d want=%d/%d", len(want), len(ranges), (primeFrameSize+frameSize)*channels, len(steps))
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gainQ8); err != nil {
							t.Fatal(err)
						}
						out := make([]float32, stride)
						if n, err := dec.Decode(prime, out); err != nil || n != primeFrameSize {
							t.Fatalf("prime decode=(%d,%v), want %d", n, err, primeFrameSize)
						}
						assertHybridSILKFECPCM(t, out[:primeFrameSize*channels], want[:primeFrameSize*channels], "prime")
						if got := dec.FinalRange(); got != ranges[0] {
							t.Fatalf("prime range=%08x want %08x", got, ranges[0])
						}
						n, err := dec.DecodeWithFEC(recovery, out, true)
						if err != nil || n != frameSize {
							t.Fatalf("FEC decode=(%d,%v), want %d", n, err, frameSize)
						}
						assertHybridSILKFECPCM(t, out, want[primeFrameSize*channels:], "FEC")
						if got := dec.FinalRange(); got != ranges[1] {
							t.Fatalf("FEC range=%08x want %08x", got, ranges[1])
						}
					})
				}
			}
		}
	}
}

func assertHybridSILKFECPCM(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d want=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x want=%08x", label, i, gotBits, wantBits)
		}
	}
}

func TestDecodeWithFECHybridToSILKWarmZeroAllocs(t *testing.T) {
	_, recovery := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 960)
	if !PacketHasLBRR(recovery) {
		t.Fatal("warm SILK recovery packet has no LBRR")
	}
	prime := encodeAPIRateHybridPacketFrameSize(t, 1, 960)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(768); err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, 5760)
	decodeSequence := func() {
		dec.Reset()
		if n, err := dec.Decode(prime, frame); err != nil || n != 960 {
			t.Fatalf("prime decode=(%d,%v)", n, err)
		}
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != 5760 {
			t.Fatalf("FEC decode=(%d,%v)", n, err)
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(20, decodeSequence); allocs != 0 {
		t.Fatalf("warm Hybrid-to-SILK FEC allocations=%g, want zero", allocs)
	}
}
