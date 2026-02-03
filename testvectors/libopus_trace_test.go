package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

type libopusPacket struct {
	data       []byte
	finalRange uint32
}

func findOpusDemo(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("OPUS_DEMO"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	root := "."
	if gomod := os.Getenv("GOMOD"); gomod != "" {
		root = filepath.Dir(gomod)
	} else {
		if wd, err := os.Getwd(); err == nil {
			root = wd
			for {
				if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
					break
				}
				parent := filepath.Dir(root)
				if parent == root {
					break
				}
				root = parent
			}
		}
	}
	path := filepath.Join(root, "tmp_check", "opus-1.6.1", "opus_demo")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("opus_demo not found at %s (set OPUS_DEMO to override)", path)
	}
	return path
}

func writeRawPCM16(path string, pcm []float32) error {
	buf := &bytes.Buffer{}
	for _, s := range pcm {
		v := int32(s * 32768.0)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		if err := binary.Write(buf, binary.LittleEndian, int16(v)); err != nil {
			return err
		}
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func parseOpusDemoBitstream(path string) ([]libopusPacket, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var packets []libopusPacket
	offset := 0
	for {
		if offset+8 > len(data) {
			break
		}
		length := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
		finalRange := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
		if offset+int(length) > len(data) {
			return nil, fmt.Errorf("truncated packet: need %d bytes, have %d", length, len(data)-offset)
		}
		payload := make([]byte, length)
		copy(payload, data[offset:offset+int(length)])
		offset += int(length)
		packets = append(packets, libopusPacket{data: payload, finalRange: finalRange})
	}
	return packets, nil
}

func TestLibopusTraceSILKWB(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}
	opusDemo := findOpusDemo(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	// Generate 1 second of test signal.
	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Encode with gopus.
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	gopusPackets := make([][]byte, 0, numFrames)
	gopusRanges := make([]uint32, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		packet, err := goEnc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		gopusPackets = append(gopusPackets, packetCopy)
		gopusRanges = append(gopusRanges, goEnc.FinalRange())
	}

	// Encode with libopus (opus_demo).
	tmpdir := t.TempDir()
	inRaw := filepath.Join(tmpdir, "input.pcm")
	outBit := filepath.Join(tmpdir, "output.bit")
	if err := writeRawPCM16(inRaw, original); err != nil {
		t.Fatalf("write input pcm: %v", err)
	}

	args := []string{
		"-e", "restricted-silk",
		fmt.Sprintf("%d", sampleRate),
		fmt.Sprintf("%d", channels),
		fmt.Sprintf("%d", bitrate),
		"-bandwidth", "WB",
		"-framesize", "20",
		"-16",
		inRaw, outBit,
	}
	cmd := exec.Command(opusDemo, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("opus_demo failed: %v\n%s", err, stderr.String())
	}

	libPackets, err := parseOpusDemoBitstream(outBit)
	if err != nil {
		t.Fatalf("parse opus_demo output: %v", err)
	}

	t.Logf("gopus packets: %d, libopus packets: %d", len(gopusPackets), len(libPackets))

	compareCount := len(gopusPackets)
	if len(libPackets) < compareCount {
		compareCount = len(libPackets)
	}
	if compareCount == 0 {
		t.Fatal("no packets to compare")
	}

	// Compare packet sizes and final ranges for the first few frames.
	maxLog := 5
	if compareCount < maxLog {
		maxLog = compareCount
	}
	for i := 0; i < maxLog; i++ {
		t.Logf("frame %02d: gopus=%4d bytes rng=0x%08x | libopus=%4d bytes rng=0x%08x",
			i, len(gopusPackets[i]), gopusRanges[i], len(libPackets[i].data), libPackets[i].finalRange)
	}

	var totalDiff int
	for i := 0; i < compareCount; i++ {
		diff := len(gopusPackets[i]) - len(libPackets[i].data)
		if diff < 0 {
			diff = -diff
		}
		totalDiff += diff
	}
	avgDiff := float64(totalDiff) / float64(compareCount)
	t.Logf("avg packet size diff: %.2f bytes", avgDiff)
}

func TestDecoderParityLibopusPacketsSILKWB(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}
	opusDemo := findOpusDemo(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	// Generate 1 second of test signal.
	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Encode with libopus (opus_demo).
	tmpdir := t.TempDir()
	inRaw := filepath.Join(tmpdir, "input.pcm")
	outBit := filepath.Join(tmpdir, "output.bit")
	if err := writeRawPCM16(inRaw, original); err != nil {
		t.Fatalf("write input pcm: %v", err)
	}
	args := []string{
		"-e", "restricted-silk",
		fmt.Sprintf("%d", sampleRate),
		fmt.Sprintf("%d", channels),
		fmt.Sprintf("%d", bitrate),
		"-bandwidth", "WB",
		"-framesize", "20",
		"-16",
		inRaw, outBit,
	}
	cmd := exec.Command(opusDemo, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("opus_demo failed: %v\n%s", err, stderr.String())
	}
	libPackets, err := parseOpusDemoBitstream(outBit)
	if err != nil {
		t.Fatalf("parse opus_demo output: %v", err)
	}
	if len(libPackets) == 0 {
		t.Fatal("no libopus packets produced")
	}

	// Convert to [][]byte for decoder.
	packetBytes := make([][]byte, len(libPackets))
	for i := range libPackets {
		packetBytes[i] = libPackets[i].data
	}

	// Decode with opusdec (libopus) via Ogg container.
	oggBuf, err := buildOggForPackets(packetBytes, channels, sampleRate, frameSize)
	if err != nil {
		t.Fatalf("build Ogg: %v", err)
	}
	libDecoded, err := decodeWithOpusdec(oggBuf.Bytes())
	if err != nil {
		if err.Error() == "opusdec blocked by macOS provenance" {
			t.Skip("opusdec blocked by macOS provenance - skipping")
		}
		t.Fatalf("decode with opusdec: %v", err)
	}
	if len(libDecoded) == 0 {
		t.Fatal("opusdec returned no samples")
	}

	// Decode the same packets with the internal decoder.
	internalDecoded := decodeWithInternalDecoder(t, packetBytes, channels)
	if len(internalDecoded) == 0 {
		t.Fatal("internal decoder returned no samples")
	}

	// Apply pre-skip to internal decode for parity.
	preSkip := OpusPreSkip * channels
	if len(internalDecoded) > preSkip {
		internalDecoded = internalDecoded[preSkip:]
	}

	compareLen := len(libDecoded)
	if len(internalDecoded) < compareLen {
		compareLen = len(internalDecoded)
	}
	q, delay := ComputeQualityFloat32WithDelay(libDecoded[:compareLen], internalDecoded[:compareLen], sampleRate, 2000)
	t.Logf("decoder parity (libopus packets): Q=%.2f (SNR=%.2f dB), delay=%d samples", q, SNRFromQuality(q), delay)
}

func TestSILKParamTraceAgainstLibopus(t *testing.T) {
	opusDemo := findOpusDemo(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	// Generate 1 second of test signal.
	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Encode with gopus.
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	gopusPackets := make([][]byte, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		packet, err := goEnc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		gopusPackets = append(gopusPackets, packetCopy)
	}

	// Encode with libopus (opus_demo).
	tmpdir := t.TempDir()
	inRaw := filepath.Join(tmpdir, "input.pcm")
	outBit := filepath.Join(tmpdir, "output.bit")
	if err := writeRawPCM16(inRaw, original); err != nil {
		t.Fatalf("write input pcm: %v", err)
	}
	args := []string{
		"-e", "restricted-silk",
		fmt.Sprintf("%d", sampleRate),
		fmt.Sprintf("%d", channels),
		fmt.Sprintf("%d", bitrate),
		"-bandwidth", "WB",
		"-framesize", "20",
		"-16",
		inRaw, outBit,
	}
	cmd := exec.Command(opusDemo, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("opus_demo failed: %v\n%s", err, stderr.String())
	}
	libPackets, err := parseOpusDemoBitstream(outBit)
	if err != nil {
		t.Fatalf("parse opus_demo output: %v", err)
	}
	if len(libPackets) == 0 {
		t.Fatal("no libopus packets produced")
	}

	// Compare decoded SILK parameters using our decoder.
	goDec := silk.NewDecoder()
	libDec := silk.NewDecoder()
	compareCount := len(gopusPackets)
	if len(libPackets) < compareCount {
		compareCount = len(libPackets)
	}
	if compareCount == 0 {
		t.Fatal("no packets to compare")
	}

	var gainDiffSum int
	var gainCount int
	var ltpScaleDiff int
	var interpDiff int
	var perIndexDiff int
	var ltpIndexDiff int
	var ltpIndexCount int
	var signalTypeDiff int

	for i := 0; i < compareCount; i++ {
		goPayload := gopusPackets[i]
		libPayload := libPackets[i].data
		if len(goPayload) < 2 || len(libPayload) < 2 {
			continue
		}
		// Skip TOC byte for SILK-only packets.
		goPayload = goPayload[1:]
		libPayload = libPayload[1:]

		var rdGo, rdLib rangecoding.Decoder
		rdGo.Init(goPayload)
		rdLib.Init(libPayload)

		_, err := goDec.DecodeFrame(&rdGo, silk.BandwidthWideband, silk.Frame20ms, true)
		if err != nil {
			t.Fatalf("gopus decode failed at frame %d: %v", i, err)
		}
		_, err = libDec.DecodeFrame(&rdLib, silk.BandwidthWideband, silk.Frame20ms, true)
		if err != nil {
			t.Fatalf("libopus decode failed at frame %d: %v", i, err)
		}

		goParams := goDec.GetLastFrameParams()
		libParams := libDec.GetLastFrameParams()
		if goDec.GetLastSignalType() != libDec.GetLastSignalType() {
			signalTypeDiff++
		}

		if goParams.LTPScaleIndex != libParams.LTPScaleIndex {
			ltpScaleDiff++
		}
		if goParams.NLSFInterpCoefQ2 != libParams.NLSFInterpCoefQ2 {
			interpDiff++
		}
		if goParams.PERIndex != libParams.PERIndex {
			perIndexDiff++
		}
		n := len(goParams.GainIndices)
		if len(libParams.GainIndices) < n {
			n = len(libParams.GainIndices)
		}
		for j := 0; j < n; j++ {
			diff := goParams.GainIndices[j] - libParams.GainIndices[j]
			if diff < 0 {
				diff = -diff
			}
			gainDiffSum += diff
			gainCount++
		}
		nLtp := len(goParams.LTPIndices)
		if len(libParams.LTPIndices) < nLtp {
			nLtp = len(libParams.LTPIndices)
		}
		for j := 0; j < nLtp; j++ {
			if goParams.LTPIndices[j] != libParams.LTPIndices[j] {
				ltpIndexDiff++
			}
			ltpIndexCount++
		}
	}

	if gainCount > 0 {
		avgGainDiff := float64(gainDiffSum) / float64(gainCount)
		t.Logf("gain index avg abs diff: %.2f (frames=%d)", avgGainDiff, compareCount)
	}
	t.Logf("LTP scale index mismatches: %d/%d", ltpScaleDiff, compareCount)
	t.Logf("NLSF interp coef mismatches: %d/%d", interpDiff, compareCount)
	t.Logf("PER index mismatches: %d/%d", perIndexDiff, compareCount)
	if ltpIndexCount > 0 {
		t.Logf("LTP index mismatches: %d/%d", ltpIndexDiff, ltpIndexCount)
	}
	t.Logf("Signal type mismatches: %d/%d", signalTypeDiff, compareCount)
}
