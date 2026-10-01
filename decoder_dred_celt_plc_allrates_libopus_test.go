//go:build gopus_dred || gopus_osce

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecoderCELTNeuralPLCAPIRatesMatchesLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	dredBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED decoder model", err)
	}
	for _, channels := range []int{1, 2} {
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband,
			Channels: channels, ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "CELT DRED carrier", err)
		}
		if ParseTOC(packetInfo.packet[0]).Stereo != (channels == 2) {
			t.Fatalf("carrier TOC stereo=%t want %t", ParseTOC(packetInfo.packet[0]).Stereo, channels == 2)
		}
		var nextPacket []byte
		if channels == 1 {
			nextPacket = makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 960)
		} else {
			nextPacket = makeValidStereoCELTPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthFullband)
		}
		for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, durationMS := range []int{20, 40, 60} {
				t.Run(fmt.Sprintf("%dch_%dHz_%dms", channels, rate, durationMS), func(t *testing.T) {
					dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					setDecoderComplexityForLibopusDREDParityTest(t, dec)
					if err := dec.SetDNNBlob(decoderBlob); err != nil {
						t.Fatalf("SetDNNBlob: %v", err)
					}
					setDREDDecoderBlobFromBytesForTest(t, dec, dredBlob)
					carrierSize := rate / 50
					lostSize := rate * durationMS / 1000
					pcm := make([]float32, lostSize*channels)
					carrierPCM := make([]float32, carrierSize*channels)
					recoveryPCM := make([]float32, carrierSize*channels)
					n, err := dec.Decode(packetInfo.packet, carrierPCM)
					if err != nil || n != carrierSize {
						t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", n, err, carrierSize)
					}
					maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, rate)
					want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket,
						maxDRED, oracleRate, lostSize, libopusDecoderDREDSequenceSourceLost, lostSize,
						libopusDecoderDREDSequenceSourceNone, 0, true)
					if err != nil {
						t.Fatalf("live C decoder: %v", err)
					}
					if want.carrierRet != n || want.step0.ret != lostSize || want.next.ret != carrierSize {
						t.Fatalf("C returns carrier/loss/recovery=%d/%d/%d want %d/%d/%d",
							want.carrierRet, want.step0.ret, want.next.ret, n, lostSize, carrierSize)
					}
					gotN, err := dec.Decode(nil, pcm)
					if err != nil || gotN != want.step0.ret {
						t.Fatalf("Decode(nil)=(%d,%v) C=%d", gotN, err, want.step0.ret)
					}
					assertFloat32BitsEqual(t, pcm[:gotN*channels], want.step0.pcm, "lost PCM")
					gotNext, err := dec.Decode(nextPacket, recoveryPCM)
					if err != nil || gotNext != want.next.ret {
						t.Fatalf("Decode(recovery)=(%d,%v) C=%d", gotNext, err, want.next.ret)
					}
					assertFloat32BitsEqual(t, recoveryPCM[:gotNext*channels], want.next.pcm, "recovery PCM")

					// Warm the retained decoder and exercise active carrier→neural
					// loss→recovery cycles with caller-owned buffers. Each duration
					// drives the same 20 ms CELT chunk cadence tested above.
					cycleOK := true
					cycle := func() {
						if n, err := dec.Decode(packetInfo.packet, carrierPCM); err != nil || n != carrierSize {
							cycleOK = false
						}
						if n, err := dec.Decode(nil, pcm); err != nil || n != lostSize {
							cycleOK = false
						}
						if n, err := dec.Decode(nextPacket, recoveryPCM); err != nil || n != carrierSize {
							cycleOK = false
						}
					}
					cycle()
					cycle()
					if allocs := testing.AllocsPerRun(20, cycle); allocs != 0 {
						t.Fatalf("warm carrier/loss/recovery allocations=%v want 0", allocs)
					}
					if !cycleOK {
						t.Fatal("warm carrier/loss/recovery return changed")
					}
					nonzero := false
					for i, sample := range pcm {
						if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
							t.Fatalf("loss sample[%d]=%v is nonfinite", i, sample)
						}
						if sample != 0 {
							nonzero = true
						}
					}
					if !nonzero {
						t.Fatal("warm neural loss output is silent")
					}
				})
			}
		}
	}
}
