package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/celt"
	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/types"
)

func firstOpusDemoPacket(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, os.ErrInvalid
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n < 0 || len(data) < 8+n {
		return nil, os.ErrInvalid
	}
	return append([]byte(nil), data[8:8+n]...), nil
}

func encodeLibopusPacket(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool) []byte {
	t.Helper()

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext.f32")
	bitstreamPath := filepath.Join(tmpDir, "qext.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm32, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}

	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), "256000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
	}
	if qext {
		args = append(args, "-qext")
	}
	if cbr {
		args = append(args, "-cbr")
	}
	args = append(args, inputPath, bitstreamPath)
	cmd := exec.Command(opusDemo, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo encode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	packet, err := firstOpusDemoPacket(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoPacket: %v", err)
	}
	return packet
}

func encodeLibopusQEXTPacket(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool) []byte {
	t.Helper()
	return encodeLibopusPacket(t, opusDemo, channels, pcm32, cbr, true)
}

func firstDiffByte(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func TestDecodeGopusQEXTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			enc := internalenc.NewEncoder(48000, channels)
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(256000)
			enc.SetQEXT(true)

			pcm := make([]float64, 960*channels)
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm[i*channels] = left
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					right := 0.35 * math.Sin(phase+0.37)
					pcm[i*channels+1] = right
					pcm32[i*channels+1] = float32(right)
				}
			}

			packet, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(packet) == 0 {
				t.Fatal("Encode returned empty packet")
			}
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			if info.Padding == 0 {
				t.Fatal("encoded packet missing extension padding")
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("encoded packet missing QEXT payload")
			}

			refPacket := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			_, refFrames, refPadding, refNBFrames, err := parsePacketFramesAndPadding(refPacket)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding(ref): %v", err)
			}
			refExt, refOK, err := findPacketExtension(refPadding, refNBFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension(ref): %v", err)
			}
			if !refOK || len(refExt.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameIntoWithQEXT(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, ext.Data)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			withoutQEXT := make([]float32, len(got))
			decNoQEXT, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(no qext): %v", err)
			}
			nNoQEXT, err := decNoQEXT.decodeOpusFrameIntoWithQEXT(withoutQEXT, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}
			if nNoQEXT != n {
				t.Fatalf("Decode(no qext) samples=%d want %d", nNoQEXT, n)
			}

			tmpDir := t.TempDir()
			bitstreamPath := filepath.Join(tmpDir, "qext.bit")
			outputPath := filepath.Join(tmpDir, "qext.raw")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}

			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var (
				maxDiff     float64
				signalPower float64
				errorPower  float64
				deltaNoQEXT float64
			)
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
				}
				s := float64(ref)
				signalPower += s * s
				errorPower += diff * diff
				delta := math.Abs(float64(quantized - withoutQEXT[i]))
				if delta > deltaNoQEXT {
					deltaNoQEXT = delta
				}
			}

			snr := 200.0
			if errorPower > 0 {
				snr = 10 * math.Log10(signalPower/errorPower)
			}
			if maxDiff > 2.0/32768.0 {
				packetDiff := firstDiffByte(packet, refPacket)
				frameDiff := -1
				if len(refFrames) == 1 {
					frameDiff = firstDiffByte(frames[0], refFrames[0])
				}
				extDiff := firstDiffByte(ext.Data, refExt.Data)
				t.Logf("packet lengths: gopus=%d libopus=%d", len(packet), len(refPacket))
				t.Logf("frame lengths: gopus=%d libopus=%d", len(frames[0]), len(refFrames[0]))
				t.Logf("qext lengths: gopus=%d libopus=%d", len(ext.Data), len(refExt.Data))
				t.Logf("first diff: packet=%d frame=%d qext=%d", packetDiff, frameDiff, extDiff)
				if packetDiff >= 0 && packetDiff < len(packet) && packetDiff < len(refPacket) {
					t.Logf("packet bytes at diff: gopus=%02x libopus=%02x", packet[packetDiff], refPacket[packetDiff])
				}
				if extDiff >= 0 && extDiff < len(ext.Data) && extDiff < len(refExt.Data) {
					t.Logf("qext bytes at diff: gopus=%02x libopus=%02x", ext.Data[extDiff], refExt.Data[extDiff])
				}
				t.Fatalf("max diff too high: got %.2e want <= %.2e (delta vs no-qext decode %.2e)", maxDiff, 2.0/32768.0, deltaNoQEXT)
			}
			if snr < 60 {
				t.Fatalf("snr too low: got %.1f dB want >= 60 dB (delta vs no-qext decode %.2e)", snr, deltaNoQEXT)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			outputPath := filepath.Join(t.TempDir(), "qext.raw")

			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameIntoWithQEXT(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, ext.Data)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			withoutQEXT := make([]float32, len(got))
			decNoQEXT, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(no qext): %v", err)
			}
			nNoQEXT, err := decNoQEXT.decodeOpusFrameIntoWithQEXT(withoutQEXT, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}
			if nNoQEXT != n {
				t.Fatalf("Decode(no qext) samples=%d want %d", nNoQEXT, n)
			}

			bitstreamPath := filepath.Join(t.TempDir(), "qext.bit")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}
			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var (
				maxDiff         float64
				baseMaxDiff     float64
				signalPower     float64
				errorPower      float64
				baseErrorPower  float64
				deltaNoQEXT     float64
				maxDiffIdx      int
				baseMaxDiffIdx  int
			)
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
				baseQuantized := float32(float32ToInt16(withoutQEXT[i])) / 32768.0
				baseDiff := math.Abs(float64(baseQuantized - ref))
				if baseDiff > baseMaxDiff {
					baseMaxDiff = baseDiff
					baseMaxDiffIdx = i
				}
				s := float64(ref)
				signalPower += s * s
				errorPower += diff * diff
				baseErrorPower += baseDiff * baseDiff
				delta := math.Abs(float64(quantized - withoutQEXT[i]))
				if delta > deltaNoQEXT {
					deltaNoQEXT = delta
				}
			}

			snr := 200.0
			if errorPower > 0 {
				snr = 10 * math.Log10(signalPower/errorPower)
			}
			baseSNR := 200.0
			if baseErrorPower > 0 {
				baseSNR = 10 * math.Log10(signalPower/baseErrorPower)
			}
			if maxDiff > 2.0/32768.0 {
				refWorst := float32(int16(binary.LittleEndian.Uint16(refData[maxDiffIdx*2:]))) / 32768.0
				gotWorst := float32(float32ToInt16(got[maxDiffIdx])) / 32768.0
				baseWorst := float32(float32ToInt16(withoutQEXT[maxDiffIdx])) / 32768.0
				t.Logf("worst sample idx=%d frame=%d ch=%d got=%.8f ref=%.8f base=%.8f", maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels, gotWorst, refWorst, baseWorst)
				refBaseWorst := float32(int16(binary.LittleEndian.Uint16(refData[baseMaxDiffIdx*2:]))) / 32768.0
				gotBaseWorst := float32(float32ToInt16(got[baseMaxDiffIdx])) / 32768.0
				baseQuantWorst := float32(float32ToInt16(withoutQEXT[baseMaxDiffIdx])) / 32768.0
				t.Logf("base worst idx=%d frame=%d ch=%d got=%.8f ref=%.8f base=%.8f", baseMaxDiffIdx, baseMaxDiffIdx/channels, baseMaxDiffIdx%channels, gotBaseWorst, refBaseWorst, baseQuantWorst)
				t.Fatalf("max diff too high: got %.2e want <= %.2e (delta vs no-qext decode %.2e, base max diff %.2e, base snr %.1f dB)", maxDiff, 2.0/32768.0, deltaNoQEXT, baseMaxDiff, baseSNR)
			}
			if snr < 60 {
				t.Fatalf("snr too low: got %.1f dB want >= 60 dB (delta vs no-qext decode %.2e)", snr, deltaNoQEXT)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketCELTFloat32FastPathMatchesFloat64(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			float64Dec := celt.NewDecoder(channels)
			float64Dec.SetBandwidth(celt.BandwidthFromOpusConfig(int(info.TOC.Bandwidth)))
			float64Dec.SetQEXTPayload(ext.Data)
			want, err := float64Dec.DecodeFrameWithPacketStereo(frames[0], info.TOC.FrameSize, info.TOC.Stereo)
			if err != nil {
				t.Fatalf("DecodeFrameWithPacketStereo: %v", err)
			}

			float32Dec := celt.NewDecoder(channels)
			float32Dec.SetBandwidth(celt.BandwidthFromOpusConfig(int(info.TOC.Bandwidth)))
			float32Dec.SetQEXTPayload(ext.Data)
			got := make([]float32, len(want))
			if err := float32Dec.DecodeFrameWithPacketStereoToFloat32(frames[0], info.TOC.FrameSize, info.TOC.Stereo, got); err != nil {
				t.Fatalf("DecodeFrameWithPacketStereoToFloat32: %v", err)
			}

			var (
				maxDiff    float64
				maxDiffIdx int
			)
			for i := range got {
				diff := math.Abs(float64(got[i] - float32(want[i])))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			if maxDiff != 0 {
				t.Fatalf("max diff %.2e at idx=%d frame=%d ch=%d", maxDiff, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketWrapperMatchesDirectCELT(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm32, false)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("libopus packet missing QEXT payload")
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameIntoWithQEXT(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, ext.Data)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}

			direct := celt.NewDecoder(channels)
			direct.SetBandwidth(celt.BandwidthFromOpusConfig(int(info.TOC.Bandwidth)))
			direct.SetQEXTPayload(ext.Data)
			want := make([]float32, 960*channels)
			if err := direct.DecodeFrameWithPacketStereoToFloat32(frames[0], info.TOC.FrameSize, info.TOC.Stereo, want); err != nil {
				t.Fatalf("DecodeFrameWithPacketStereoToFloat32: %v", err)
			}

			var (
				maxDiff    float64
				maxDiffIdx int
			)
			for i := range got {
				diff := math.Abs(float64(got[i] - want[i]))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			if maxDiff != 0 {
				t.Fatalf("max diff %.2e at idx=%d frame=%d ch=%d", maxDiff, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)
			}
		})
	}
}

func TestDecodeStereoLibopusQEXTPacketToMonoMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	pcm32 := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm32[i*2] = float32(0.45 * math.Sin(phase))
		pcm32[i*2+1] = float32(0.35 * math.Sin(phase+0.37))
	}

	packet := encodeLibopusQEXTPacket(t, opusDemo, 2, pcm32, false)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 960)
	n, err := dec.Decode(packet, got)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 960 {
		t.Fatalf("Decode samples=%d want 960", n)
	}

	tmpDir := t.TempDir()
	bitstreamPath := filepath.Join(tmpDir, "stereo_qext.bit")
	outputPath := filepath.Join(tmpDir, "stereo_qext_mono.raw")
	if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
	}
	cmd := exec.Command(opusDemo, "-d", "48000", "1", bitstreamPath, outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	refData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read opus_demo output: %v", err)
	}
	if len(refData) < len(got)*2 {
		t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
	}
	refData = refData[:len(got)*2]

	maxDiff := 0.0
	for i := range got {
		ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
		quantized := float32(float32ToInt16(got[i])) / 32768.0
		diff := math.Abs(float64(quantized - ref))
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	if maxDiff > 2.0/32768.0 {
		t.Fatalf("max diff too high: got %.2e want <= %.2e", maxDiff, 2.0/32768.0)
	}
}

func TestDecodeLibopusRestrictedCELTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm32[i*channels] = float32(left)
				if channels == 2 {
					pcm32[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
				}
			}

			packet := encodeLibopusPacket(t, opusDemo, channels, pcm32, false, false)
			outputPath := filepath.Join(t.TempDir(), "restricted.raw")

			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("frame count=%d want 1", len(frames))
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.decodeOpusFrameInto(got, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo)
			if err != nil {
				t.Fatalf("decodeOpusFrameInto: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}

			bitstreamPath := filepath.Join(t.TempDir(), "restricted.bit")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}
			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprint(channels), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refData, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			if len(refData) < len(got)*2 {
				t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), len(got)*2)
			}
			refData = refData[:len(got)*2]

			var maxDiff float64
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
				}
			}
			if maxDiff > 2.0/32768.0 {
				t.Fatalf("max diff too high: got %.2e want <= %.2e", maxDiff, 2.0/32768.0)
			}
		})
	}
}
