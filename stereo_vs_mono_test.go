package gopus_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestStereoVsMonoQuality(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		numFrames  = 20
		measureStart = 10
	)

	// Test mono
	{
		enc, _ := gopus.NewEncoder(sampleRate, 1, gopus.ApplicationAudio)
		enc.SetBitrate(64000)
		dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))

		pcmIn := make([]float32, frameSize*numFrames)
		for i := range pcmIn {
			t := float64(i) / float64(sampleRate)
			pcmIn[i] = float32(0.5 * math.Sin(2*math.Pi*440*t))
		}

		packet := make([]byte, 4000)
		pcmOut := make([]float32, frameSize)

		var energy, errEnergy float64
		for f := 0; f < numFrames; f++ {
			start := f * frameSize
			end := start + frameSize
			n, _ := enc.Encode(pcmIn[start:end], packet)
			if n > 0 {
				dec.Decode(packet[:n], pcmOut)
				if f >= measureStart {
					for i := 0; i < frameSize; i++ {
						in := float64(pcmIn[start+i])
						out := float64(pcmOut[i])
						energy += in * in
						errEnergy += (in - out) * (in - out)
					}
				}
			}
		}
		snr := 10 * math.Log10(energy / errEnergy)
		t.Logf("MONO (1ch, 64kbps): SNR = %.1f dB", snr)
	}

	// Test stereo at 128kbps (same per-channel rate)
	{
		enc, _ := gopus.NewEncoder(sampleRate, 2, gopus.ApplicationAudio)
		enc.SetBitrate(128000)
		dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 2))

		pcmIn := make([]float32, frameSize*2*numFrames)
		for i := 0; i < frameSize*numFrames; i++ {
			t := float64(i) / float64(sampleRate)
			pcmIn[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*t))
			pcmIn[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*554.37*t))
		}

		packet := make([]byte, 4000)
		pcmOut := make([]float32, frameSize*2)

		var energyL, errEnergyL, energyR, errEnergyR float64
		for f := 0; f < numFrames; f++ {
			start := f * frameSize * 2
			end := start + frameSize*2
			n, err := enc.Encode(pcmIn[start:end], packet)
			if err != nil {
				t.Fatalf("Encode frame %d: %v", f, err)
			}
			if n > 0 {
				toc := gopus.ParseTOC(packet[0])
				if f == 0 {
					t.Logf("Stereo TOC: config=%d mode=%d stereo=%v", toc.Config, toc.Mode, toc.Stereo)
				}
				dec.Decode(packet[:n], pcmOut)
				if f >= measureStart {
					for i := 0; i < frameSize; i++ {
						inL := float64(pcmIn[start+i*2])
						inR := float64(pcmIn[start+i*2+1])
						outL := float64(pcmOut[i*2])
						outR := float64(pcmOut[i*2+1])
						energyL += inL * inL
						energyR += inR * inR
						errEnergyL += (inL - outL) * (inL - outL)
						errEnergyR += (inR - outR) * (inR - outR)
					}
				}
			}
		}
		snrL := 10 * math.Log10(energyL / errEnergyL)
		snrR := 10 * math.Log10(energyR / errEnergyR)
		t.Logf("STEREO (2ch, 128kbps): L SNR = %.1f dB, R SNR = %.1f dB", snrL, snrR)
	}

	// Test with same signal in both channels (should be good because of M/S)
	{
		enc, _ := gopus.NewEncoder(sampleRate, 2, gopus.ApplicationAudio)
		enc.SetBitrate(128000)
		dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 2))

		pcmIn := make([]float32, frameSize*2*numFrames)
		for i := 0; i < frameSize*numFrames; i++ {
			t := float64(i) / float64(sampleRate)
			val := float32(0.5 * math.Sin(2*math.Pi*440*t))
			pcmIn[i*2] = val
			pcmIn[i*2+1] = val
		}

		packet := make([]byte, 4000)
		pcmOut := make([]float32, frameSize*2)

		var energyL, errEnergyL float64
		for f := 0; f < numFrames; f++ {
			start := f * frameSize * 2
			end := start + frameSize*2
			n, _ := enc.Encode(pcmIn[start:end], packet)
			if n > 0 {
				dec.Decode(packet[:n], pcmOut)
				if f >= measureStart {
					for i := 0; i < frameSize; i++ {
						inL := float64(pcmIn[start+i*2])
						outL := float64(pcmOut[i*2])
						energyL += inL * inL
						errEnergyL += (inL - outL) * (inL - outL)
					}
				}
			}
		}
		snr := 10 * math.Log10(energyL / errEnergyL)
		t.Logf("STEREO same-signal (2ch, 128kbps): SNR = %.1f dB", snr)
	}
}
