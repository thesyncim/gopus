// Package testvectors provides audio quality tests.
package testvectors

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

// TestAudioAudibility encodes audio with gopus, decodes with libopus,
// and measures quality. Run with: go test -v -run TestAudioAudibility ./internal/testvectors/
func TestAudioAudibility(t *testing.T) {
	if !checkOpusdecAvailable() {
		t.Skip("opusdec not found in PATH")
	}

	// Generate a 3-second test signal: A major chord + sweep
	sampleRate := 48000
	duration := 3.0
	numSamples := int(float64(sampleRate) * duration)

	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)

		// A major chord: A4 (440Hz) + C#5 (554Hz) + E5 (659Hz)
		val := 0.3 * math.Sin(2*math.Pi*440*t)
		val += 0.25 * math.Sin(2*math.Pi*554*t)
		val += 0.2 * math.Sin(2*math.Pi*659*t)

		// Add a gentle frequency sweep
		sweepFreq := 200 + 400*t/duration
		val += 0.1 * math.Sin(2*math.Pi*sweepFreq*t)

		// Fade in/out
		envelope := 1.0
		fadeTime := 0.1
		if t < fadeTime {
			envelope = t / fadeTime
		} else if t > duration-fadeTime {
			envelope = (duration - t) / fadeTime
		}

		pcm[i] = float32(val * envelope * 0.8)
	}

	// Save original WAV
	originalWav := "/tmp/gopus_original.wav"
	saveTestWAV(originalWav, pcm, sampleRate, 1)
	t.Logf("Original audio saved: %s", originalWav)

	// Encode with gopus
	enc := encoder.NewEncoder(sampleRate, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)
	enc.SetBitrateMode(encoder.ModeCBR) // Use CBR for consistent packet sizes

	frameSize := 960 // 20ms
	var packets [][]byte

	for i := 0; i+frameSize <= len(pcm); i += frameSize {
		frame := pcm[i : i+frameSize]
		pcmF64 := make([]float64, frameSize)
		for j, v := range frame {
			pcmF64[j] = float64(v)
		}

		packet, err := enc.Encode(pcmF64, frameSize)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		packets = append(packets, packet)
	}

	t.Logf("Encoded %d frames", len(packets))
	t.Logf("Average packet size: %.1f bytes", avgSize(packets))

	// Write Ogg Opus file
	var buf bytes.Buffer
	writeOggOpusAudible(&buf, packets, 1)

	opusFile := "/tmp/gopus_output.opus"
	if err := os.WriteFile(opusFile, buf.Bytes(), 0644); err != nil {
		t.Fatalf("Write opus file: %v", err)
	}
	t.Logf("Opus file saved: %s", opusFile)

	// Decode with libopus
	decodedWav := "/tmp/gopus_decoded.wav"
	cmd := exec.Command("opusdec", "--quiet", opusFile, decodedWav)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opusdec failed: %v\nOutput: %s", err, output)
	}
	t.Logf("Decoded WAV saved: %s", decodedWav)

	// Read decoded and compute quality
	decoded := readTestWAV(decodedWav)
	if len(decoded) > 312 {
		decoded = decoded[312:] // Skip pre-skip
	}

	compareLen := len(pcm)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	snr := computeTestSNR(pcm[:compareLen], decoded[:compareLen])
	t.Logf("\n=== AUDIO QUALITY RESULTS ===")
	t.Logf("SNR: %.2f dB", snr)

	if snr > 20 {
		t.Logf("Status: EXCELLENT - Audio is clearly audible and high quality")
	} else if snr > 10 {
		t.Logf("Status: GOOD - Audio is audible with minor artifacts")
	} else if snr > 5 {
		t.Logf("Status: FAIR - Audio is recognizable but degraded")
	} else if snr > 0 {
		t.Logf("Status: POOR - Audio barely recognizable")
	} else {
		t.Logf("Status: BAD - Audio likely corrupted or inaudible")
	}

	t.Logf("\n=== TO LISTEN ===")
	t.Logf("Original: afplay %s  (or: play %s)", originalWav, originalWav)
	t.Logf("Decoded:  afplay %s  (or: play %s)", decodedWav, decodedWav)
	t.Logf("Opus:     opusdec %s - | play -", opusFile)

	// Fail if SNR is too low
	if snr < 0 {
		t.Errorf("Audio quality too low: SNR=%.2f dB (expected > 0 dB)", snr)
	}
}

func avgSize(packets [][]byte) float64 {
	total := 0
	for _, p := range packets {
		total += len(p)
	}
	return float64(total) / float64(len(packets))
}

func computeTestSNR(original, decoded []float32) float64 {
	var signalPower, noisePower float64

	for i := 0; i < len(original) && i < len(decoded); i++ {
		sig := float64(original[i])
		noise := float64(original[i] - decoded[i])
		signalPower += sig * sig
		noisePower += noise * noise
	}

	if noisePower < 1e-10 {
		return 100
	}

	return 10 * math.Log10(signalPower/noisePower)
}

func saveTestWAV(filename string, samples []float32, sampleRate, channels int) {
	f, _ := os.Create(filename)
	defer f.Close()

	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		scaled := float64(s) * 32768.0
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		val := int16(math.RoundToEven(scaled))
		binary.LittleEndian.PutUint16(data[i*2:], uint16(val))
	}

	dataSize := len(data)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(header[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	f.Write(header)
	f.Write(data)
}

func readTestWAV(filename string) []float32 {
	data, _ := os.ReadFile(filename)
	if len(data) < 44 {
		return nil
	}

	offset := 12
	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if chunkID == "data" {
			offset += 8
			samples := make([]float32, chunkSize/2)
			for i := 0; i < len(samples) && offset+2 <= len(data); i++ {
				val := int16(binary.LittleEndian.Uint16(data[offset : offset+2]))
				samples[i] = float32(val) / 32768.0
				offset += 2
			}
			return samples
		}
		offset += 8 + chunkSize
	}
	return nil
}

func checkOpusdecAvailable() bool {
	_, err := exec.LookPath("opusdec")
	return err == nil
}

func writeOggOpusAudible(w *bytes.Buffer, packets [][]byte, channels int) {
	serialNo := uint32(12345)

	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1
	opusHead[9] = byte(channels)
	binary.LittleEndian.PutUint16(opusHead[10:12], 312)
	binary.LittleEndian.PutUint32(opusHead[12:16], 48000)
	writeOggPageAudible(w, serialNo, 0, 2, 0, [][]byte{opusHead})

	tags := []byte("OpusTags\x05\x00\x00\x00gopus\x00\x00\x00\x00")
	writeOggPageAudible(w, serialNo, 1, 0, 0, [][]byte{tags})

	granulePos := uint64(312)
	for i, pkt := range packets {
		granulePos += 960
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4
		}
		writeOggPageAudible(w, serialNo, uint32(2+i), headerType, granulePos, [][]byte{pkt})
	}
}

func writeOggPageAudible(w *bytes.Buffer, serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) {
	var segTable []byte
	for _, seg := range segments {
		remaining := len(seg)
		for remaining >= 255 {
			segTable = append(segTable, 255)
			remaining -= 255
		}
		segTable = append(segTable, byte(remaining))
	}

	header := make([]byte, 27+len(segTable))
	copy(header[0:4], "OggS")
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	header[26] = byte(len(segTable))
	copy(header[27:], segTable)

	crc := oggCRC(header)
	for _, seg := range segments {
		crc = oggCRCUpdate(crc, seg)
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	w.Write(header)
	for _, seg := range segments {
		w.Write(seg)
	}
}
