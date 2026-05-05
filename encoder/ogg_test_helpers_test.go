package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"

	oggcontainer "github.com/thesyncim/gopus/container/ogg"
	"github.com/thesyncim/gopus/silk"
)

func writeTestOgg(w *bytes.Buffer, packets [][]byte, channels, sampleRate, frameSize, preSkip int) {
	serialNo := uint32(12345)

	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1
	opusHead[9] = byte(channels)
	binary.LittleEndian.PutUint16(opusHead[10:12], uint16(preSkip))
	binary.LittleEndian.PutUint32(opusHead[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(opusHead[16:18], 0)
	opusHead[18] = 0
	writeSimpleOggPage(w, serialNo, 0, 2, 0, opusHead)

	vendor := "gopus"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(vendor)))
	copy(tags[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(tags[12+len(vendor):], 0)
	writeSimpleOggPage(w, serialNo, 1, 0, 0, tags)

	granulePos := uint64(preSkip)
	for i, pkt := range packets {
		granulePos += uint64(frameSize)
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4
		}
		writeSimpleOggPage(w, serialNo, uint32(i+2), headerType, granulePos, pkt)
	}
}

func writeSimpleOggPage(w *bytes.Buffer, serialNo, pageNo uint32, headerType byte, granulePos uint64, data []byte) {
	header := make([]byte, 27+1)
	copy(header[0:4], "OggS")
	header[4] = 0
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	header[26] = 1
	header[27] = byte(len(data))
	if len(data) > 255 {
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

	binary.LittleEndian.PutUint32(header[22:26], 0)
	crc := uint32(0)
	for _, b := range header {
		crc = (crc << 8) ^ oggCRCTable[(crc>>24)^uint32(b)]
	}
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[(crc>>24)^uint32(b)]
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	w.Write(header)
	w.Write(data)
}

var oggCRCTable [256]uint32

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
		oggCRCTable[i] = r
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
	if raceEnabled {
		t.Log("race detector active; using internal SILK decode fallback instead of opusdec")
		return decodeOggWithInternalSILK(t, oggData)
	}
	if opusdec == "" {
		return decodeOggWithInternalSILK(t, oggData)
	}

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
		if bytes.Contains(out, []byte("provenance")) ||
			bytes.Contains(out, []byte("quarantine")) ||
			bytes.Contains(out, []byte("killed")) ||
			bytes.Contains(out, []byte("Operation not permitted")) ||
			bytes.Contains(out, []byte("Failed to open")) {
			t.Log("opusdec blocked; using internal SILK decode fallback")
			return decodeOggWithInternalSILK(t, oggData)
		}
		t.Logf("opusdec output: %s", out)
		t.Fatalf("opusdec failed: %v", err)
	}

	wavData, err := os.ReadFile(wavFile.Name())
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}

	return parseWAVFloat32(wavData)
}

func decodeOggWithInternalSILK(t *testing.T, oggData []byte) []float32 {
	t.Helper()

	r, err := oggcontainer.NewReader(bytes.NewReader(oggData))
	if err != nil {
		t.Fatalf("internal fallback reader: %v", err)
	}

	dec := silk.NewDecoder()
	decoded := make([]float32, 0, 48000)

	for {
		packet, _, err := r.ReadPacket()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("internal fallback read packet: %v", err)
		}
		if len(packet) < 2 {
			continue
		}

		bw, frameSize, err := decodeSILKTOC(packet[0])
		if err != nil {
			t.Fatalf("internal fallback toc parse: %v", err)
		}

		out, err := dec.Decode(packet[1:], bw, frameSize, true)
		if err != nil {
			t.Fatalf("internal fallback decode: %v", err)
		}
		decoded = append(decoded, out...)
	}

	return decoded
}

func decodeSILKTOC(toc byte) (silk.Bandwidth, int, error) {
	config := toc >> 3
	if config > 11 {
		return 0, 0, fmt.Errorf("unsupported non-SILK config %d", config)
	}

	var bw silk.Bandwidth
	switch config / 4 {
	case 0:
		bw = silk.BandwidthNarrowband
	case 1:
		bw = silk.BandwidthMediumband
	default:
		bw = silk.BandwidthWideband
	}

	var frameSize int
	switch config % 4 {
	case 0:
		frameSize = 480
	case 1:
		frameSize = 960
	case 2:
		frameSize = 1920
	default:
		frameSize = 2880
	}

	return bw, frameSize, nil
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
