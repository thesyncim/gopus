//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

func TestOggDecodeWithOpusdec(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	numFrames := 10
	channels := 1

	// Generate simple sine wave
	totalSamples := frameSize * numFrames
	original := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		ti := float64(i) / float64(sampleRate)
		original[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	// Create encoder and encode all frames
	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(gopus.BandwidthFullband)
	enc.SetBitrate(64000)

	packets := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", f, err)
		}
		packets[f] = packet
	}

	// Write to Ogg Opus file
	var oggBuf bytes.Buffer
	err := writeOggOpus(&oggBuf, packets, channels, 48000, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpus failed: %v", err)
	}

	t.Logf("Ogg file size: %d bytes", oggBuf.Len())

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "gopus_ogg_test_*.opus")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	_, err = tmpFile.Write(oggBuf.Bytes())
	tmpFile.Close()
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Try to decode with opusdec
	outPath := tmpPath + ".wav"
	defer os.Remove(outPath)

	cmd := exec.Command("opusdec", tmpPath, outPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("opusdec output: %s", output)
		t.Fatalf("opusdec failed: %v", err)
	}
	t.Logf("opusdec output: %s", output)

	// Read and parse WAV
	wavData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("Read WAV failed: %v", err)
	}

	decoded := parseWAV(wavData)
	if len(decoded) == 0 {
		t.Fatal("No samples decoded")
	}

	t.Logf("Decoded %d samples (raw)", len(decoded))

	// Total expected samples from encoding
	expectedTotal := numFrames * frameSize
	t.Logf("Expected: %d samples encoded", expectedTotal)
	t.Logf("Decoded samples - expected preskip = %d", len(decoded)-OpusPreSkip)

	// Don't skip pre-skip for now, let's see raw alignment
	// The Opus decoder adds pre-skip to output, and we declared pre-skip in header
	// opusdec should handle this automatically

	// Find best delay alignment
	bestDelay := 0
	bestSNR := -999.0
	for delay := -500; delay <= 500; delay++ {
		var signalPower, noisePower float64
		count := 0
		for i := 500; i < len(original)-500 && i+delay >= 0 && i+delay < len(decoded); i++ {
			signalPower += original[i] * original[i]
			noise := original[i] - float64(decoded[i+delay])
			noisePower += noise * noise
			count++
		}
		if count > 0 && signalPower > 0 {
			snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
			if snr > bestSNR {
				bestSNR = snr
				bestDelay = delay
			}
		}
	}
	q := (bestSNR - 48.0) * (100.0 / 48.0)
	t.Logf("Best delay: %d samples", bestDelay)
	t.Logf("Best SNR: %.2f dB, Q: %.2f", bestSNR, q)

	// Show some samples (with best delay applied)
	t.Log("\nSample comparison (around sample 2000, with delay):")
	for i := 2000; i < 2010; i++ {
		if i < len(original) && i+bestDelay >= 0 && i+bestDelay < len(decoded) {
			t.Logf("  [%d] orig=%.5f decoded=%.5f", i, original[i], decoded[i+bestDelay])
		}
	}
}

const OpusPreSkip = 312

func writeOggOpus(w io.Writer, packets [][]byte, channels, sampleRate, frameSize int) error {
	serialNo := uint32(12345)
	var granulePos uint64

	// Page 1: OpusHead
	opusHead := makeOpusHead(channels, sampleRate)
	if err := writeOggPage(w, serialNo, 0, 2, 0, [][]byte{opusHead}); err != nil {
		return err
	}

	// Page 2: OpusTags
	opusTags := makeOpusTags()
	if err := writeOggPage(w, serialNo, 1, 0, 0, [][]byte{opusTags}); err != nil {
		return err
	}

	// Data pages
	pageNo := uint32(2)
	granulePos = uint64(OpusPreSkip)

	for i, packet := range packets {
		granulePos += uint64(frameSize)
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4 // EOS
		}
		if err := writeOggPage(w, serialNo, pageNo, headerType, granulePos, [][]byte{packet}); err != nil {
			return err
		}
		pageNo++
	}

	return nil
}

func makeOpusHead(channels, sampleRate int) []byte {
	head := make([]byte, 19)
	copy(head[0:8], "OpusHead")
	head[8] = 1 // Version
	head[9] = byte(channels)
	binary.LittleEndian.PutUint16(head[10:12], uint16(OpusPreSkip))
	binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(head[16:18], 0) // Output gain
	head[18] = 0                                  // Channel mapping
	return head
}

func makeOpusTags() []byte {
	vendor := "gopus"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(vendor)))
	copy(tags[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(tags[12+len(vendor):], 0)
	return tags
}

func writeOggPage(w io.Writer, serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) error {
	// Segment table
	var segTable []byte
	for _, seg := range segments {
		remaining := len(seg)
		for remaining >= 255 {
			segTable = append(segTable, 255)
			remaining -= 255
		}
		segTable = append(segTable, byte(remaining))
	}

	// Header
	header := make([]byte, 27+len(segTable))
	copy(header[0:4], "OggS")
	header[4] = 0
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	header[26] = byte(len(segTable))
	copy(header[27:], segTable)

	// CRC
	crc := oggCRC(header)
	for _, seg := range segments {
		crc = oggCRCUpdate(crc, seg)
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	if _, err := w.Write(header); err != nil {
		return err
	}
	for _, seg := range segments {
		if _, err := w.Write(seg); err != nil {
			return err
		}
	}
	return nil
}

var oggCRCTable [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		oggCRCTable[i] = r
	}
}

func oggCRC(data []byte) uint32 {
	return oggCRCUpdate(0, data)
}

func oggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

func parseWAV(data []byte) []float32 {
	if len(data) < 44 {
		return nil
	}

	offset := 12
	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		if chunkID == "data" {
			dataStart := offset + 8
			dataLen := int(chunkSize)
			if dataStart+dataLen > len(data) {
				dataLen = len(data) - dataStart
			}

			pcmData := data[dataStart : dataStart+dataLen]
			samples := make([]float32, len(pcmData)/2)
			for i := 0; i < len(pcmData)/2; i++ {
				s := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
				samples[i] = float32(s) / 32768.0
			}
			return samples
		}

		offset += 8 + int(chunkSize)
		if chunkSize%2 != 0 {
			offset++
		}
	}

	return nil
}
