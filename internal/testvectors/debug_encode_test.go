package testvectors

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

func TestDebugEncode(t *testing.T) {
	// Generate 1 frame of simple sine wave
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Save original as WAV
	saveDebugWAV("/tmp/test_original.wav", pcm, sampleRate)

	// Encode with gopus
	enc := encoder.NewEncoder(sampleRate, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	t.Logf("Encoded packet: %d bytes", len(packet))
	hexStr := "First 20 bytes (hex): "
	for i := 0; i < 20 && i < len(packet); i++ {
		hexStr += fmt.Sprintf("%02x ", packet[i])
	}
	t.Log(hexStr)

	// Save as ogg opus file for decoding
	writeDebugOggOpus("/tmp/test_gopus.opus", [][]byte{packet}, 1)

	// Also encode with libopus for comparison
	exec.Command("opusenc", "--hard-cbr", "--bitrate", "64", "--framesize", "20",
		"/tmp/test_original.wav", "/tmp/test_libopus.opus").Run()

	// Now decode both
	exec.Command("opusdec", "--quiet", "/tmp/test_gopus.opus", "/tmp/test_gopus_dec.wav").Run()
	exec.Command("opusdec", "--quiet", "/tmp/test_libopus.opus", "/tmp/test_libopus_dec.wav").Run()

	// Read decoded samples
	gopusDec := readDebugWAV("/tmp/test_gopus_dec.wav")
	libopusDec := readDebugWAV("/tmp/test_libopus_dec.wav")

	// Skip pre-skip (312 samples)
	if len(gopusDec) > 312 {
		gopusDec = gopusDec[312:]
	}
	if len(libopusDec) > 312 {
		libopusDec = libopusDec[312:]
	}

	// Compare first 20 samples
	t.Log("\nFirst 20 decoded samples:")
	t.Log("  i      original     gopus    libopus")
	for i := 0; i < 20 && i < len(pcm); i++ {
		goVal := 0.0
		libVal := 0.0
		if i < len(gopusDec) {
			goVal = gopusDec[i]
		}
		if i < len(libopusDec) {
			libVal = libopusDec[i]
		}
		t.Logf("%3d  %10.5f  %10.5f  %10.5f", i, pcm[i], goVal, libVal)
	}

	// Compute SNR for both
	snrGo := computeDebugSNR(pcm, gopusDec)
	snrLib := computeDebugSNR(pcm, libopusDec)
	t.Logf("\nSNR: gopus=%.2f dB, libopus=%.2f dB", snrGo, snrLib)

	// Check max values
	maxOrig, maxGo, maxLib := 0.0, 0.0, 0.0
	for _, v := range pcm {
		if math.Abs(v) > maxOrig {
			maxOrig = math.Abs(v)
		}
	}
	for _, v := range gopusDec {
		if math.Abs(v) > maxGo {
			maxGo = math.Abs(v)
		}
	}
	for _, v := range libopusDec {
		if math.Abs(v) > maxLib {
			maxLib = math.Abs(v)
		}
	}

	t.Logf("Max amplitudes: orig=%.4f, gopus=%.4f, libopus=%.4f", maxOrig, maxGo, maxLib)
}

func saveDebugWAV(filename string, samples []float64, sampleRate int) {
	f, _ := os.Create(filename)
	defer f.Close()

	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		val := int16(s * 32767)
		binary.LittleEndian.PutUint16(data[i*2:], uint16(val))
	}

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(data)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(data)))

	f.Write(header)
	f.Write(data)
}

func readDebugWAV(filename string) []float64 {
	data, _ := os.ReadFile(filename)
	if len(data) < 44 {
		return nil
	}

	offset := 12
	for offset < len(data)-8 {
		if string(data[offset:offset+4]) == "data" {
			offset += 8
			break
		}
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8 + chunkSize
	}

	samples := make([]float64, 0)
	for i := offset; i+2 <= len(data); i += 2 {
		val := float64(int16(binary.LittleEndian.Uint16(data[i:i+2]))) / 32767.0
		samples = append(samples, val)
	}
	return samples
}

func computeDebugSNR(orig, dec []float64) float64 {
	n := len(orig)
	if len(dec) < n {
		n = len(dec)
	}

	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		signalPower += orig[i] * orig[i]
		noise := orig[i] - dec[i]
		noisePower += noise * noise
	}

	if noisePower < 1e-10 {
		return 100
	}
	return 10 * math.Log10(signalPower/noisePower)
}

func writeDebugOggOpus(filename string, packets [][]byte, channels int) {
	f, _ := os.Create(filename)
	defer f.Close()

	serialNo := uint32(12345)

	// OpusHead
	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1
	opusHead[9] = byte(channels)
	binary.LittleEndian.PutUint16(opusHead[10:12], 312)
	binary.LittleEndian.PutUint32(opusHead[12:16], 48000)
	writeDebugOggPage(f, serialNo, 0, 2, 0, [][]byte{opusHead})

	// OpusTags
	tags := []byte("OpusTags\x05\x00\x00\x00gopus\x00\x00\x00\x00")
	writeDebugOggPage(f, serialNo, 1, 0, 0, [][]byte{tags})

	// Audio packets
	granulePos := uint64(312)
	for i, pkt := range packets {
		granulePos += 960
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4
		}
		writeDebugOggPage(f, serialNo, uint32(2+i), headerType, granulePos, [][]byte{pkt})
	}
}

func writeDebugOggPage(f *os.File, serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) {
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

	// Compute CRC
	crc := debugOggCRC(header)
	for _, seg := range segments {
		crc = debugOggCRCUpdate(crc, seg)
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	f.Write(header)
	for _, seg := range segments {
		f.Write(seg)
	}
}

var debugOggCRCTable = generateDebugOggCRCTable()

func generateDebugOggCRCTable() [256]uint32 {
	var table [256]uint32
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		table[i] = r
	}
	return table
}

func debugOggCRC(data []byte) uint32 {
	crc := uint32(0)
	for _, b := range data {
		crc = (crc << 8) ^ debugOggCRCTable[(crc>>24)^uint32(b)]
	}
	return crc
}

func debugOggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ debugOggCRCTable[(crc>>24)^uint32(b)]
	}
	return crc
}
