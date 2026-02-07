package encoder

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/types"
)

const silk10msLibopusDecodedFixturePath = "testdata/silk_10ms_libopus_decoded_fixture.json"

type silk10msLibopusDecodedFixture struct {
	Version         int                              `json:"version"`
	SampleRate      int                              `json:"sample_rate"`
	Channels        int                              `json:"channels"`
	DurationSamples int                              `json:"duration_samples"`
	PreSkip         int                              `json:"pre_skip"`
	Cases           []silk10msLibopusDecodedCaseFile `json:"cases"`
}

type silk10msLibopusDecodedCaseFile struct {
	FrameSize int    `json:"frame_size"`
	PCMBase64 string `json:"pcm_s16le_base64"`
}

// TestSILK10msGopusVsLibopusPackets encodes with gopus and libopus, decodes
// both with opusdec, and compares quality. This isolates the encoder from decoder.
func TestSILK10msGopusVsLibopusPackets(t *testing.T) {
	opusdec := findOpusdec()
	opusenc := findOpusenc()
	if opusdec == "" || opusenc == "" {
		t.Log("opusenc/opusdec not found; using frozen libopus decoded fixture")
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

			libopusSamples, err := loadSILK10msLibopusDecodedFixture(frameSize)
			if err != nil {
				if opusenc == "" || opusdec == "" {
					t.Fatalf("load fixture for frameSize=%d: %v", frameSize, err)
				}
				libopusSamples, err = generateLibopusDecodedViaCLI(opusenc, opusdec, frameSize, wavData)
				if err != nil {
					t.Fatalf("generate libopus reference: %v", err)
				}
			} else if opusenc != "" && opusdec != "" {
				// Best-effort drift visibility when CLI tools are available.
				live, err := generateLibopusDecodedViaCLI(opusenc, opusdec, frameSize, wavData)
				if err == nil {
					compareLen := len(live)
					if len(libopusSamples) < compareLen {
						compareLen = len(libopusSamples)
					}
					if compareLen > 0 {
						var mad float64
						for i := 0; i < compareLen; i++ {
							diff := float64(live[i] - libopusSamples[i])
							if diff < 0 {
								diff = -diff
							}
							mad += diff
						}
						mad /= float64(compareLen)
						t.Logf("fixture drift check (%s): mean abs diff=%.8f", fsName, mad)
					}
				}
			}

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

func generateLibopusDecodedViaCLI(opusenc, opusdec string, frameSize int, wavData []byte) ([]float32, error) {
	fs := "10"
	if frameSize == 960 {
		fs = "20"
	}

	tmpWav, err := os.CreateTemp("", "silk_compare_*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpWav.Name())
	if _, err := tmpWav.Write(wavData); err != nil {
		tmpWav.Close()
		return nil, err
	}
	if err := tmpWav.Close(); err != nil {
		return nil, err
	}

	tmpLibOpus, err := os.CreateTemp("", "silk_libopus_*.opus")
	if err != nil {
		return nil, err
	}
	tmpLibOpus.Close()
	defer os.Remove(tmpLibOpus.Name())

	cmd := exec.Command(opusenc,
		"--bitrate", "32",
		"--speech",
		"--framesize", fs,
		"--comp", "10",
		tmpWav.Name(),
		tmpLibOpus.Name(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opusenc failed: %v (%s)", err, string(out))
	}

	tmpDecoded, err := os.CreateTemp("", "silk_decoded_*.wav")
	if err != nil {
		return nil, err
	}
	tmpDecoded.Close()
	defer os.Remove(tmpDecoded.Name())

	cmd = exec.Command(opusdec, tmpLibOpus.Name(), tmpDecoded.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opusdec failed: %v (%s)", err, string(out))
	}

	decodedWavData, err := os.ReadFile(tmpDecoded.Name())
	if err != nil {
		return nil, err
	}
	return parseWAVFloat32(decodedWavData), nil
}

func loadSILK10msLibopusDecodedFixture(frameSize int) ([]float32, error) {
	data, err := os.ReadFile(silk10msLibopusDecodedFixturePath)
	if err != nil {
		return nil, err
	}

	var fixture silk10msLibopusDecodedFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return nil, err
	}
	if fixture.Version != 1 {
		return nil, fmt.Errorf("unsupported fixture version %d", fixture.Version)
	}

	for _, c := range fixture.Cases {
		if c.FrameSize != frameSize {
			continue
		}
		pcmBytes, err := base64.StdEncoding.DecodeString(c.PCMBase64)
		if err != nil {
			return nil, err
		}
		samples := make([]float32, len(pcmBytes)/2)
		for i := 0; i < len(samples); i++ {
			s := int16(binary.LittleEndian.Uint16(pcmBytes[i*2 : i*2+2]))
			samples[i] = float32(s) / 32768.0
		}
		return samples, nil
	}

	return nil, fmt.Errorf("fixture frame_size=%d not found", frameSize)
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
