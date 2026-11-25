package gopus_test

import (
	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/multistream"
	"math"
	"testing"
)

func TestHybridStereoFloatSameArchParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960 // 20 ms @ 48k
		frameCount = 6
	)

	// Distinct, decorrelated L/R content so the SILK stereo MS->LR unmixing and the
	// CELT highband both carry meaningful energy.
	makePCM := func(channels int) []float32 {
		pcm := make([]float32, frameSize*channels*frameCount)
		for f := range frameCount {
			for i := range frameSize {
				tt := float64(f*frameSize+i) / float64(sampleRate)
				base := f*frameSize + i
				if channels == 2 {
					pcm[(base)*2] = float32(0.5*math.Sin(2*math.Pi*440*tt) + 0.1*math.Sin(2*math.Pi*1900*tt))
					pcm[(base)*2+1] = float32(0.4*math.Sin(2*math.Pi*523*tt) + 0.12*math.Sin(2*math.Pi*2600*tt))
				} else {
					pcm[base] = float32(0.5*math.Sin(2*math.Pi*440*tt) + 0.1*math.Sin(2*math.Pi*1900*tt))
				}
			}
		}
		return pcm
	}

	cases := []struct {
		name      string
		channels  int
		forceCh   int
		bandwidth int
	}{
		{"coupled-stereo/FB", 2, 2, libopustest.OpusBandwidthFullband},
		{"coupled-stereo/SWB", 2, 2, libopustest.OpusBandwidthSuperwideband},
		{"mono/FB", 1, 1, libopustest.OpusBandwidthFullband},
		{"mono/SWB", 1, 1, libopustest.OpusBandwidthSuperwideband},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := makePCM(tc.channels)
			for _, bitrate := range []int{24000, 32000, 48000, 64000, 96000, 128000} {
				recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
					SampleRate: sampleRate, Channels: tc.channels,
					Application:   libopustest.EncodeDiffApplicationAudio,
					ForceMode:     libopustest.OpusForceModeHybrid,
					Bandwidth:     tc.bandwidth,
					Bitrate:       bitrate,
					Complexity:    10,
					Signal:        3002, // OPUS_SIGNAL_MUSIC
					VBR:           true,
					VBRConstraint: false,
					ForceChannels: tc.forceCh,
					FrameSize:     frameSize,
					FrameCount:    frameCount,
					PCM:           pcm,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "hybrid encode oracle", err)
				}

				coupled := 0
				mapping := []byte{0}
				if tc.channels == 2 {
					coupled = 1
					mapping = []byte{0, 1}
				}

				compared := 0
				for fi, rec := range recs {
					if rec.Ret <= 1 || len(rec.Packet) == 0 {
						t.Fatalf("br=%d frame=%d: invalid oracle packet result=%d bytes=%d", bitrate, fi, rec.Ret, len(rec.Packet))
					}
					pkt := rec.Packet
					cfg := pkt[0] >> 3
					if cfg < 12 || cfg > 15 {
						continue // Check required Hybrid coverage for this bitrate below.
					}

					// Fresh multistream decoder per packet isolates frame state and
					// matches the oracle's fresh decoder for each case.
					dec, err := multistream.NewDecoder(sampleRate, tc.channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got, err := dec.DecodeToFloat32(pkt, frameSize)
					if err != nil {
						t.Fatalf("br=%d frame=%d decode: %v", bitrate, fi, err)
					}

					res, err := libopustest.ProbeDecodeDiff(sampleRate, tc.channels, []libopustest.DecodeDiffCase{{
						Packet:    pkt,
						Format:    libopustest.DecodeDiffFormatFloat32,
						FrameSize: uint32(frameSize),
					}})
					if err != nil {
						libopustest.HelperUnavailable(t, "hybrid decode oracle", err)
					}
					if res[0].Code <= 0 {
						t.Fatalf("oracle decode code=%d", res[0].Code)
					}
					want := res[0].Float32()
					if len(got) != len(want) {
						t.Fatalf("length mismatch gopus=%d libopus=%d", len(got), len(want))
					}

					for j := range want {
						if math.Float32bits(got[j]) != math.Float32bits(want[j]) {
							t.Fatalf("br=%d frame=%d TOC=0x%02x sample=%d bits=%08x want=%08x",
								bitrate, fi, pkt[0], j, math.Float32bits(got[j]), math.Float32bits(want[j]))
						}
					}
					compared++

					if tc.name == "mono/FB" && bitrate == 24000 && fi == 0 {
						into := make([]float32, frameSize*tc.channels)
						for range 2 {
							if n, err := dec.DecodeIntoFloat32(pkt, into, frameSize); err != nil || n != frameSize {
								t.Fatalf("warm DecodeIntoFloat32=(%d,%v), want (%d,nil)", n, err, frameSize)
							}
						}
						if allocs := testing.AllocsPerRun(100, func() {
							if n, err := dec.DecodeIntoFloat32(pkt, into, frameSize); err != nil || n != frameSize {
								t.Fatalf("warm DecodeIntoFloat32=(%d,%v), want (%d,nil)", n, err, frameSize)
							}
						}); allocs != 0 {
							t.Fatalf("warm DecodeIntoFloat32 allocations=%g want 0", allocs)
						}
					}
				}
				if compared == 0 {
					t.Fatalf("br=%d: no Hybrid packets compared among %d oracle records", bitrate, len(recs))
				}
			}
		})
	}
}

func TestStereoDetail(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		numFrames  = 20
	)

	enc, _ := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	enc.SetBitrate(128000)
	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))

	// Use chirp signal that varies in frequency to avoid alignment ambiguity
	pcmIn := make([]float32, frameSize*channels*numFrames)
	for i := range frameSize * numFrames {
		t := float64(i) / float64(sampleRate)
		freq := 200.0 + 2000.0*t // chirp from 200Hz to 200+2000*dur
		pcmIn[i*2] = float32(0.4 * math.Sin(2*math.Pi*freq*t))
		// Right channel: same signal but 0.5x amplitude
		pcmIn[i*2+1] = float32(0.2 * math.Sin(2*math.Pi*freq*t))
	}

	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	// Encode and decode
	for f := range numFrames {
		start := f * frameSize * channels
		end := start + frameSize*channels
		n, _ := enc.Encode(pcmIn[start:end], packet)
		if n > 0 {
			dec.Decode(packet[:n], pcmOut)
		}
	}

	// Analyze last decoded frame
	lastFrame := numFrames - 1
	start := lastFrame * frameSize * channels

	// Check amplitude ratio between channels
	var maxInL, maxInR, maxOutL, maxOutR float64
	var rmsInL, rmsInR, rmsOutL, rmsOutR float64
	for i := range frameSize {
		inL := math.Abs(float64(pcmIn[start+i*2]))
		inR := math.Abs(float64(pcmIn[start+i*2+1]))
		outL := math.Abs(float64(pcmOut[i*2]))
		outR := math.Abs(float64(pcmOut[i*2+1]))
		if inL > maxInL {
			maxInL = inL
		}
		if inR > maxInR {
			maxInR = inR
		}
		if outL > maxOutL {
			maxOutL = outL
		}
		if outR > maxOutR {
			maxOutR = outR
		}
		rmsInL += inL * inL
		rmsInR += inR * inR
		rmsOutL += outL * outL
		rmsOutR += outR * outR
	}
	rmsInL = math.Sqrt(rmsInL / float64(frameSize))
	rmsInR = math.Sqrt(rmsInR / float64(frameSize))
	rmsOutL = math.Sqrt(rmsOutL / float64(frameSize))
	rmsOutR = math.Sqrt(rmsOutR / float64(frameSize))

	t.Logf("Input:  L max=%.4f rms=%.4f, R max=%.4f rms=%.4f", maxInL, rmsInL, maxInR, rmsInR)
	t.Logf("Output: L max=%.4f rms=%.4f, R max=%.4f rms=%.4f", maxOutL, rmsOutL, maxOutR, rmsOutR)
	t.Logf("Input ratio L/R:  max=%.2f rms=%.2f", maxInL/maxInR, rmsInL/rmsInR)
	t.Logf("Output ratio L/R: max=%.2f rms=%.2f", maxOutL/maxOutR, rmsOutL/rmsOutR)

	// Check that L is approximately 2x louder than R (as encoded)
	inputRatio := rmsInL / rmsInR
	outputRatio := rmsOutL / rmsOutR
	ratioDiff := math.Abs(outputRatio-inputRatio) / inputRatio
	t.Logf("Ratio preservation: input=%.2f output=%.2f (%.1f%% error)", inputRatio, outputRatio, ratioDiff*100)

	if ratioDiff > 0.5 {
		t.Errorf("Stereo amplitude ratio poorly preserved: expected %.2f got %.2f (>50%% error)", inputRatio, outputRatio)
	}

	// Check correlation: L and R should be highly correlated (same shape, different amplitude)
	var corr, sumL2, sumR2 float64
	for i := range frameSize {
		l := float64(pcmOut[i*2])
		r := float64(pcmOut[i*2+1])
		corr += l * r
		sumL2 += l * l
		sumR2 += r * r
	}
	if sumL2 > 0 && sumR2 > 0 {
		corrCoef := corr / math.Sqrt(sumL2*sumR2)
		t.Logf("Output L/R correlation: %.4f", corrCoef)
		if corrCoef < 0.8 {
			t.Errorf("Stereo channels should be correlated (same signal, different amplitude): got %.4f", corrCoef)
		}
	}

	// Test with truly distinct signals (different frequencies)
	t.Log("\n--- Test with distinct L/R frequencies ---")
	enc2, _ := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	enc2.SetBitrate(128000)
	dec2, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))

	pcmIn2 := make([]float32, frameSize*channels*numFrames)
	for i := range frameSize * numFrames {
		t := float64(i) / float64(sampleRate)
		pcmIn2[i*2] = float32(0.4 * math.Sin(2*math.Pi*300*t))    // L: 300Hz
		pcmIn2[i*2+1] = float32(0.4 * math.Sin(2*math.Pi*1000*t)) // R: 1000Hz
	}

	for f := range numFrames {
		start := f * frameSize * channels
		end := start + frameSize*channels
		n, _ := enc2.Encode(pcmIn2[start:end], packet)
		if n > 0 {
			dec2.Decode(packet[:n], pcmOut)
		}
	}

	// Measure frequency content of decoded L and R
	// Simple: count zero crossings to estimate frequency
	var zerosL, zerosR int
	for i := 1; i < frameSize; i++ {
		if pcmOut[(i-1)*2]*pcmOut[i*2] < 0 {
			zerosL++
		}
		if pcmOut[(i-1)*2+1]*pcmOut[i*2+1] < 0 {
			zerosR++
		}
	}
	estFreqL := float64(zerosL) / 2.0 * float64(sampleRate) / float64(frameSize)
	estFreqR := float64(zerosR) / 2.0 * float64(sampleRate) / float64(frameSize)
	t.Logf("Distinct signals: L est freq=%.0fHz (expected 300), R est freq=%.0fHz (expected 1000)", estFreqL, estFreqR)

	if math.Abs(estFreqL-300) > 100 {
		t.Errorf("Left channel frequency wrong: expected ~300Hz, got ~%.0fHz", estFreqL)
	}
	if math.Abs(estFreqR-1000) > 200 {
		t.Errorf("Right channel frequency wrong: expected ~1000Hz, got ~%.0fHz", estFreqR)
	}
}

func TestStereoRoundTripRegression(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		numFrames  = 20
		measureAt  = 10
	)

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetBitrate(128000)

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	var totalLeftEnergy, totalRightEnergy float64
	var totalLeftErrEnergy, totalRightErrEnergy float64

	for f := range numFrames {
		frame := make([]float32, frameSize*channels)
		base := f * frameSize
		for i := range frameSize {
			tm := float64(base+i) / float64(sampleRate)
			frame[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*tm))
			frame[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*554.37*tm))
		}

		n, err := enc.Encode(frame, packet)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", f, err)
		}
		if n == 0 {
			continue
		}

		samples, err := dec.Decode(packet[:n], pcmOut)
		if err != nil {
			t.Fatalf("Decode frame %d: %v", f, err)
		}
		if samples != frameSize {
			t.Fatalf("samples=%d want %d", samples, frameSize)
		}

		if f < measureAt {
			continue
		}
		for i := range frameSize {
			inL := float64(frame[i*2])
			inR := float64(frame[i*2+1])
			outL := float64(pcmOut[i*2])
			outR := float64(pcmOut[i*2+1])

			totalLeftEnergy += inL * inL
			totalRightEnergy += inR * inR
			totalLeftErrEnergy += (inL - outL) * (inL - outL)
			totalRightErrEnergy += (inR - outR) * (inR - outR)
		}
	}

	if totalLeftErrEnergy <= 0 || totalRightErrEnergy <= 0 {
		t.Fatal("unexpected zero error energy in stereo roundtrip")
	}

	leftSNR := 10 * math.Log10(totalLeftEnergy/totalLeftErrEnergy)
	rightSNR := 10 * math.Log10(totalRightEnergy/totalRightErrEnergy)
	if math.IsNaN(leftSNR) || math.IsNaN(rightSNR) {
		t.Fatalf("invalid SNR values: L=%.2f R=%.2f", leftSNR, rightSNR)
	}
	if leftSNR < -30 || rightSNR < -30 {
		t.Fatalf("stereo SNR collapsed: L=%.2f dB R=%.2f dB", leftSNR, rightSNR)
	}

	var maxL, maxR float64
	var diffCount int
	for i := range frameSize {
		l := math.Abs(float64(pcmOut[i*2]))
		r := math.Abs(float64(pcmOut[i*2+1]))
		if l > maxL {
			maxL = l
		}
		if r > maxR {
			maxR = r
		}
		if math.Abs(float64(pcmOut[i*2]-pcmOut[i*2+1])) > 0.01 {
			diffCount++
		}
	}

	if maxL < 0.01 && maxR < 0.01 {
		t.Fatal("stereo output is near-silent")
	}
	if diffCount < frameSize/10 {
		t.Fatalf("stereo separation too low: diff=%d/%d", diffCount, frameSize)
	}
}
