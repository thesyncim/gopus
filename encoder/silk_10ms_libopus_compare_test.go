package encoder

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msGopusVsLibopusPackets encodes with gopus and libopus, decodes
// both with opusdec, and compares quality. This isolates the encoder from decoder.
func TestSILK10msGopusVsLibopusPackets(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
	}
	opusenc := findOpusenc()
	if opusenc == "" {
		t.Skip("opusenc not found")
	}

	for _, frameSize := range []int{480, 960} {
		fsName := "10ms"
		if frameSize == 960 {
			fsName = "20ms"
		}
		t.Run(fsName, func(t *testing.T) {
			// Generate chirp WAV file
			totalSamples := 2 * 48000 // 2 seconds
			wavData := generateWAV(totalSamples, 48000, 1)

			origSamples := make([]float32, totalSamples)
			for i := 0; i < totalSamples; i++ {
				tm := float64(i) / 48000.0
				phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
				origSamples[i] = 0.5 * float32(math.Sin(phase))
			}

			// Encode with libopus via opusenc
			tmpWav, _ := os.CreateTemp("", "silk_compare_*.wav")
			tmpWav.Write(wavData)
			tmpWav.Close()
			defer os.Remove(tmpWav.Name())

			tmpLibOpus, _ := os.CreateTemp("", "silk_libopus_*.opus")
			tmpLibOpus.Close()
			defer os.Remove(tmpLibOpus.Name())

			cmd := exec.Command(opusenc,
				"--bitrate", "32",
				"--speech",
				"--framesize", fsName[:len(fsName)-2], // "10" or "20"
				"--comp", "10",
				tmpWav.Name(),
				tmpLibOpus.Name(),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("opusenc output: %s", out)
				t.Fatalf("opusenc failed: %v", err)
			}

			// Decode libopus file with opusdec
			tmpDecoded, _ := os.CreateTemp("", "silk_decoded_*.wav")
			tmpDecoded.Close()
			defer os.Remove(tmpDecoded.Name())

			cmd = exec.Command(opusdec, tmpLibOpus.Name(), tmpDecoded.Name())
			out, err = cmd.CombinedOutput()
			if err != nil {
				t.Logf("opusdec output: %s", out)
				t.Fatalf("opusdec failed: %v", err)
			}
			decodedWavData, _ := os.ReadFile(tmpDecoded.Name())
			libopusSamples := parseWAVFloat32(decodedWavData)

			// Encode with gopus
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetBitrate(32000)

			numFrames := totalSamples / frameSize
			var packets [][]byte
			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, frameSize)
				for j := 0; j < frameSize; j++ {
					sampleIdx := i*frameSize + j
					tm := float64(sampleIdx) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
				}
				pkt, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("gopus encode frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("gopus nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
			gopusSamples := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			// Strip pre-skip from both
			preSkip := 312
			if len(libopusSamples) > preSkip {
				libopusSamples = libopusSamples[preSkip:]
			}
			if len(gopusSamples) > preSkip {
				gopusSamples = gopusSamples[preSkip:]
			}

			// Compute SNR for both
			margin := 2000
			for _, label := range []string{"libopus", "gopus"} {
				var decoded []float32
				if label == "libopus" {
					decoded = libopusSamples
				} else {
					decoded = gopusSamples
				}

				bestSNR := math.Inf(-1)
				bestDelay := 0
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decoded)-margin {
							ref := float64(origSamples[i])
							dec := float64(decoded[di])
							sig += ref * ref
							n := dec - ref
							noise += n * n
							count++
						}
					}
					if count > 1000 && sig > 0 && noise > 0 {
						snr := 10 * math.Log10(sig/noise)
						if snr > bestSNR {
							bestSNR = snr
							bestDelay = d
						}
					}
				}

				t.Logf("%s %s: SNR=%.2f dB at delay=%d", label, fsName, bestSNR, bestDelay)
			}
		})
	}
}

func findOpusenc() string {
	paths := []string{
		"opusenc",
		"/usr/local/bin/opusenc",
		"/usr/bin/opusenc",
		"/opt/homebrew/bin/opusenc",
	}
	for _, p := range paths {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

func generateWAV(totalSamples, sampleRate, channels int) []byte {
	var buf bytes.Buffer

	// WAV header
	dataLen := totalSamples * channels * 2
	fileLen := 36 + dataLen

	buf.WriteString("RIFF")
	writeUint32LE(&buf, uint32(fileLen))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	writeUint32LE(&buf, 16) // chunk size
	writeUint16LE(&buf, 1)  // PCM
	writeUint16LE(&buf, uint16(channels))
	writeUint32LE(&buf, uint32(sampleRate))
	writeUint32LE(&buf, uint32(sampleRate*channels*2)) // byte rate
	writeUint16LE(&buf, uint16(channels*2))            // block align
	writeUint16LE(&buf, 16)                            // bits per sample

	// data chunk
	buf.WriteString("data")
	writeUint32LE(&buf, uint32(dataLen))

	// PCM data - chirp signal
	for i := 0; i < totalSamples; i++ {
		tm := float64(i) / float64(sampleRate)
		phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
		s := int16(0.5 * 32767 * math.Sin(phase))
		writeUint16LE(&buf, uint16(s))
	}

	return buf.Bytes()
}

func writeUint32LE(buf *bytes.Buffer, v uint32) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v >> 16))
	buf.WriteByte(byte(v >> 24))
}

func writeUint16LE(buf *bytes.Buffer, v uint16) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
}
