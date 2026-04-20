package testvectors

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// TestSILKChirpSweepDiagPacket15 isolates the SILK-WB chirp_sweep packet-15
// divergence cluster (60ms and 40ms) and dumps per-packet libopus-vs-gopus
// payload, signal RMS, and signal max so we can localize what input characteristic
// flips the encoder decision.
//
// Run with: go test ./testvectors -run TestSILKChirpSweepDiagPacket15 -v
func TestSILKChirpSweepDiagPacket15(t *testing.T) {
	requireTestTier(t, testTierParity)

	cases := []struct {
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		channels  int
		bitrate   int
	}{
		{encoder.ModeSILK, types.BandwidthWideband, 2880, 1, 32000}, // 60ms
		{encoder.ModeSILK, types.BandwidthWideband, 1920, 1, 32000}, // 40ms
	}

	for _, cc := range cases {
		t.Run(fmt.Sprintf("frame=%d", cc.frameSize), func(t *testing.T) {
			fc, ok := findEncoderVariantsFixtureCase(cc.mode, cc.bandwidth, cc.frameSize, cc.channels, cc.bitrate, testsignal.EncoderVariantChirpSweepV1)
			if !ok {
				t.Fatalf("missing fixture for frame=%d", cc.frameSize)
			}
			totalSamples := fc.SignalFrames * fc.FrameSize * fc.Channels
			signal, err := testsignal.GenerateEncoderSignalVariant(fc.Variant, 48000, totalSamples, fc.Channels)
			if err != nil {
				t.Fatalf("gen signal: %v", err)
			}
			libPackets, _, err := decodeEncoderVariantsFixturePackets(fc)
			if err != nil {
				t.Fatalf("decode lib packets: %v", err)
			}
			goPackets, err := encodeGopusForVariantsCase(fc, signal)
			if err != nil {
				t.Fatalf("encode gopus packets: %v", err)
			}

			n := len(libPackets)
			if len(goPackets) < n {
				n = len(goPackets)
			}
			samplesPerFrame := fc.FrameSize * fc.Channels
			for i := 0; i < n; i++ {
				start := i * samplesPerFrame
				end := start + samplesPerFrame
				if end > len(signal) {
					break
				}
				frame := signal[start:end]
				rms, peak := frameRMSPeak(frame)
				eq := bytes.Equal(libPackets[i], goPackets[i])
				marker := ""
				if !eq {
					marker = " *** MISMATCH"
				}
				t.Logf("packet %02d t=%.3fs len(lib=%d go=%d) rms=%.4f peak=%.4f%s",
					i,
					float64(i*fc.FrameSize)/48000.0,
					len(libPackets[i]),
					len(goPackets[i]),
					rms,
					peak,
					marker,
				)
				if !eq {
					nb := len(libPackets[i])
					if len(goPackets[i]) < nb {
						nb = len(goPackets[i])
					}
					firstByteDiff := -1
					for k := 0; k < nb; k++ {
						if libPackets[i][k] != goPackets[i][k] {
							firstByteDiff = k
							break
						}
					}
					t.Logf("  firstByteDiff=%d", firstByteDiff)
					t.Logf("  lib[%02d]=%s", i, hexFirstN(libPackets[i], 24))
					t.Logf("  go [%02d]=%s", i, hexFirstN(goPackets[i], 24))
				}
			}
		})
	}
}

func frameRMSPeak(samples []float32) (rms, peak float32) {
	if len(samples) == 0 {
		return 0, 0
	}
	var sumSq float64
	var p float64
	for _, s := range samples {
		v := float64(s)
		if v < 0 {
			v = -v
		}
		if v > p {
			p = v
		}
		sumSq += float64(s) * float64(s)
	}
	rmsv := sumSq / float64(len(samples))
	rms = float32(sqrtf(rmsv))
	peak = float32(p)
	return
}

func sqrtf(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton-Raphson, simple
	g := x
	for i := 0; i < 20; i++ {
		g = 0.5 * (g + x/g)
	}
	return g
}

func hexFirstN(b []byte, n int) string {
	if n > len(b) {
		n = len(b)
	}
	out := make([]byte, 0, n*3)
	const hex = "0123456789abcdef"
	for i := 0; i < n; i++ {
		out = append(out, hex[b[i]>>4], hex[b[i]&0x0f], ' ')
	}
	return string(out)
}
