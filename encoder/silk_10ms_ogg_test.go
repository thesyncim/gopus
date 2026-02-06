package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msOggDebug creates Ogg files for 10ms and 20ms SILK
// and decodes with opusdec, then compares quality to isolate the delay issue.
func TestSILK10msOggDebug(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		silkBW    silk.Bandwidth
		frameSize int
		bitrate   int
	}{
		{"WB-10ms-32k", types.BandwidthWideband, silk.BandwidthWideband, 480, 32000},
		{"WB-20ms-32k", types.BandwidthWideband, silk.BandwidthWideband, 960, 32000},
		{"NB-10ms-32k", types.BandwidthNarrowband, silk.BandwidthNarrowband, 480, 32000},
		{"NB-20ms-32k", types.BandwidthNarrowband, silk.BandwidthNarrowband, 960, 32000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(tc.bitrate)

			numFrames := 48000 / tc.frameSize // 1 second
			var packets [][]byte
			var origSamples []float32

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				for _, v := range pcm {
					origSamples = append(origSamples, float32(v))
				}

				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode error at frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("Nil packet at frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			t.Logf("Encoded %d frames, total original samples: %d", len(packets), len(origSamples))

			// Write to Ogg Opus
			var oggBuf bytes.Buffer
			preSkip := 312
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, preSkip)

			// Decode with opusdec
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())
			if len(decoded) == 0 {
				t.Fatal("No decoded samples")
			}

			t.Logf("Decoded samples: %d (expected ~%d + pre-skip)", len(decoded), len(origSamples))

			// Strip pre-skip
			preSkipSamples := preSkip
			if len(decoded) > preSkipSamples {
				decoded = decoded[preSkipSamples:]
			}
			t.Logf("After pre-skip strip: %d samples", len(decoded))

			// Find best delay alignment
			bestSNR := math.Inf(-1)
			bestDelay := 0
			maxSearch := 3000

			for d := -maxSearch; d <= maxSearch; d++ {
				var sig, noise float64
				margin := 120
				count := 0
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= 0 && di < len(decoded) {
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

			t.Logf("Best SNR=%.2f dB at delay=%d", bestSNR, bestDelay)

			// Check correlation at a few specific delays
			for _, d := range []int{0, 100, 200, 300, 500, 750, 1000, 1073, 1200, 1500} {
				var sig, noise float64
				count := 0
				for i := 500; i < len(origSamples)-500; i++ {
					di := i + d
					if di >= 0 && di < len(decoded) {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
						count++
					}
				}
				if count > 100 && sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					if d == bestDelay || snr > 5 {
						t.Logf("  delay=%d: SNR=%.2f dB (%d samples)", d, snr, count)
					}
				}
			}
		})
	}
}

func writeTestOgg(w *bytes.Buffer, packets [][]byte, channels, sampleRate, frameSize, preSkip int) {
	serialNo := uint32(12345)

	// OpusHead
	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1
	opusHead[9] = byte(channels)
	binary.LittleEndian.PutUint16(opusHead[10:12], uint16(preSkip))
	binary.LittleEndian.PutUint32(opusHead[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(opusHead[16:18], 0)
	opusHead[18] = 0
	writeSimpleOggPage(w, serialNo, 0, 2, 0, opusHead)

	// OpusTags
	vendor := "gopus"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(vendor)))
	copy(tags[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(tags[12+len(vendor):], 0)
	writeSimpleOggPage(w, serialNo, 1, 0, 0, tags)

	// Data pages
	granulePos := uint64(preSkip)
	for i, pkt := range packets {
		granulePos += uint64(frameSize)
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4 // EOS
		}
		writeSimpleOggPage(w, serialNo, uint32(i+2), headerType, granulePos, pkt)
	}
}

func writeSimpleOggPage(w *bytes.Buffer, serialNo, pageNo uint32, headerType byte, granulePos uint64, data []byte) {
	// Ogg page header
	header := make([]byte, 27+1) // 27 byte header + 1 segment table entry
	copy(header[0:4], "OggS")
	header[4] = 0 // version
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	// CRC will be computed after
	header[26] = 1 // number of segments
	header[27] = byte(len(data))
	if len(data) > 255 {
		// Need multiple segments for large packets
		nSegs := (len(data) + 254) / 255
		header = make([]byte, 27+nSegs)
		copy(header[0:4], "OggS")
		header[4] = 0
		header[5] = headerType
		binary.LittleEndian.PutUint64(header[6:14], granulePos)
		binary.LittleEndian.PutUint32(header[14:18], serialNo)
		binary.LittleEndian.PutUint32(header[18:22], pageNo)
		header[26] = byte(nSegs)
		remaining := len(data)
		for i := 0; i < nSegs; i++ {
			if remaining >= 255 {
				header[27+i] = 255
				remaining -= 255
			} else {
				header[27+i] = byte(remaining)
				remaining = 0
			}
		}
	}

	// Write with zero CRC first
	binary.LittleEndian.PutUint32(header[22:26], 0)

	// Compute CRC
	crc := uint32(0)
	for _, b := range header {
		crc = (crc << 8) ^ oggCRCTableDebug[(crc>>24)^uint32(b)]
	}
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTableDebug[(crc>>24)^uint32(b)]
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	w.Write(header)
	w.Write(data)
}

var oggCRCTableDebug [256]uint32

func init() {
	const poly = 0x04C11DB7
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ poly
			} else {
				r <<= 1
			}
		}
		oggCRCTableDebug[i] = r
	}
}

func findOpusdec() string {
	paths := []string{
		"opusdec",
		"/usr/local/bin/opusdec",
		"/usr/bin/opusdec",
		"/opt/homebrew/bin/opusdec",
	}
	for _, p := range paths {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

func decodeOggWithOpusdec(t *testing.T, opusdec string, oggData []byte) []float32 {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "silk10ms_*.opus")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(oggData)
	tmpFile.Close()
	_ = exec.Command("xattr", "-c", tmpFile.Name()).Run()

	wavFile, err := os.CreateTemp("", "silk10ms_*.wav")
	if err != nil {
		t.Fatalf("create wav temp: %v", err)
	}
	defer os.Remove(wavFile.Name())
	wavFile.Close()

	cmd := exec.Command(opusdec, tmpFile.Name(), wavFile.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("opusdec output: %s", out)
		t.Fatalf("opusdec failed: %v", err)
	}

	wavData, err := os.ReadFile(wavFile.Name())
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}

	return parseWAVFloat32(wavData)
}

func parseWAVFloat32(data []byte) []float32 {
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

// TestSILK10msCompareOggFileSize checks the Ogg file sizes are similar
func TestSILK10msCompareOggFileSize(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 48000 / tc.frameSize
			var packets [][]byte
			var totalPktBytes int

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, _ := enc.Encode(pcm, tc.frameSize)
				if pkt != nil {
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
					totalPktBytes += len(cp)
				}
			}

			avgPktSize := float64(totalPktBytes) / float64(len(packets))
			t.Logf("%d packets, avgSize=%.1f bytes, total=%d bytes",
				len(packets), avgPktSize, totalPktBytes)

			// Check per-frame bits
			bitsPerFrame := avgPktSize * 8
			bitsPerSecond := bitsPerFrame * float64(48000) / float64(tc.frameSize)
			t.Logf("bits/frame=%.0f bps=%.0f (target=%d)", bitsPerFrame, bitsPerSecond, 32000)
		})
	}
}

func init() {
	_ = fmt.Sprintf // suppress unused import
}
