//go:build gopus_qext

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

	"github.com/thesyncim/gopus/internal/benchutil"
)

func firstOpusDemoPacket(path string) ([]byte, error) {
	packet, _, err := firstOpusDemoPacketWithFinalRange(path)
	return packet, err
}

func firstOpusDemoPacketWithFinalRange(path string) ([]byte, uint32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 8 {
		return nil, 0, os.ErrInvalid
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n < 0 || len(data) < 8+n {
		return nil, 0, os.ErrInvalid
	}
	finalRange := binary.BigEndian.Uint32(data[4:8])
	return append([]byte(nil), data[8:8+n]...), finalRange, nil
}

func encodeLibopusPacket(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool) []byte {
	t.Helper()
	packet, _ := encodeLibopusPacketWithFinalRange(t, opusDemo, channels, pcm32, cbr, qext)
	return packet
}

func encodeLibopusPacketWithFinalRange(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool) ([]byte, uint32) {
	t.Helper()
	return encodeLibopusPacketWithFinalRangeAtBitrate(t, opusDemo, channels, pcm32, cbr, qext, 256000)
}

func encodeLibopusPacketAtBitrate(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool, bitrate int) []byte {
	t.Helper()
	packet, _ := encodeLibopusPacketWithFinalRangeAtBitrate(t, opusDemo, channels, pcm32, cbr, qext, bitrate)
	return packet
}

func encodeLibopusPacketWithFinalRangeAtBitrate(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool, qext bool, bitrate int) ([]byte, uint32) {
	t.Helper()

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext.f32")
	bitstreamPath := filepath.Join(tmpDir, "qext.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm32, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}

	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), fmt.Sprint(bitrate),
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

	packet, finalRange, err := firstOpusDemoPacketWithFinalRange(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoPacketWithFinalRange: %v", err)
	}
	return packet, finalRange
}

func encodeLibopusQEXTPacket(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool) []byte {
	t.Helper()
	return encodeLibopusPacket(t, opusDemo, channels, pcm32, cbr, true)
}

func encodeLibopusQEXTPacketWithFinalRange(t *testing.T, opusDemo string, channels int, pcm32 []float32, cbr bool) ([]byte, uint32) {
	t.Helper()
	return encodeLibopusPacketWithFinalRange(t, opusDemo, channels, pcm32, cbr, true)
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

type qextTestFrame struct {
	rawFrame    []byte
	qextPayload []byte
	frameSize   int
	mode        Mode
	bandwidth   Bandwidth
	stereo      bool
}

func makeQEXTSinePCMForTest(channels int, freq, phaseShift float64) []float32 {
	pcm := make([]float32, 960*channels)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * freq * float64(i) / 48000.0
		pcm[i*channels] = float32(0.43 * math.Sin(phase+phaseShift))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.31 * math.Sin(phase+phaseShift+0.41))
		}
	}
	return pcm
}

func parseLibopusQEXTFrameForTest(t *testing.T, label string, packet []byte) qextTestFrame {
	t.Helper()
	info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(%s): %v", label, err)
	}
	if len(frames) != 1 {
		t.Fatalf("%s frames=%d want 1", label, len(frames))
	}
	ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
	if err != nil {
		t.Fatalf("findPacketExtension(%s): %v", label, err)
	}
	if !ok || len(ext.Data) == 0 {
		t.Fatalf("%s missing QEXT payload", label)
	}
	return qextTestFrame{
		rawFrame:    frames[0],
		qextPayload: ext.Data,
		frameSize:   info.TOC.FrameSize,
		mode:        info.TOC.Mode,
		bandwidth:   info.TOC.Bandwidth,
		stereo:      info.TOC.Stereo,
	}
}

func makeLibopusQEXTMultiFramePacketForTest(t *testing.T, opusDemo string, channels int) ([]byte, []qextTestFrame) {
	t.Helper()
	packetA := encodeLibopusQEXTPacket(t, opusDemo, channels, makeQEXTSinePCMForTest(channels, 997, 0.0), false)
	packetB := encodeLibopusQEXTPacket(t, opusDemo, channels, makeQEXTSinePCMForTest(channels, 1237, 0.23), false)
	frameA := parseLibopusQEXTFrameForTest(t, "frameA", packetA)
	frameB := parseLibopusQEXTFrameForTest(t, "frameB", packetB)
	if frameA.frameSize != frameB.frameSize || frameA.mode != frameB.mode || frameA.bandwidth != frameB.bandwidth || frameA.stereo != frameB.stereo {
		t.Fatalf("source frames are not repacketizable: A=%+v B=%+v", frameA, frameB)
	}

	rawFrames := [][]byte{frameA.rawFrame, frameB.rawFrame}
	packet := make([]byte, len(packetA)+len(packetB)+len(frameA.qextPayload)+len(frameB.qextPayload)+128)
	n, err := buildRepacketizedPacketWithOptions(
		packetA[0]&^byte(0x03),
		rawFrames,
		packet,
		0,
		false,
		[]packetExtensionData{
			{ID: qextPacketExtensionID, Frame: 0, Data: frameA.qextPayload},
			{ID: qextPacketExtensionID, Frame: 1, Data: frameB.qextPayload},
		},
	)
	if err != nil {
		t.Fatalf("build multi-frame QEXT packet: %v", err)
	}
	packet = packet[:n]

	info, builtFrames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(built): %v", err)
	}
	if info.FrameCount != 2 || len(builtFrames) != 2 || nbFrames != 2 {
		t.Fatalf("built packet frame count info=%d frames=%d paddingFrames=%d want 2", info.FrameCount, len(builtFrames), nbFrames)
	}
	extensions, err := parsePacketExtensionList(padding, nbFrames)
	if err != nil {
		t.Fatalf("parsePacketExtensionList(built): %v", err)
	}
	if len(extensions) != 2 || extensions[0].Frame != 0 || extensions[1].Frame != 1 {
		t.Fatalf("built extensions=%+v want one QEXT payload per frame", extensions)
	}
	return packet, []qextTestFrame{frameA, frameB}
}

func makeHybridQEXTPacketForTest(t *testing.T, opusDemo string, channels int) []byte {
	t.Helper()

	const frameSize = 960
	hybridPCM := make([]float32, frameSize*channels)
	qextPCM := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		left := 0.28*math.Sin(2*math.Pi*173*tm) +
			0.17*math.Sin(2*math.Pi*347*tm+0.13) +
			0.09*math.Sin(2*math.Pi*521*tm+0.29)
		hybridPCM[i*channels] = float32(left)
		qextPCM[i*channels] = float32(0.45 * math.Sin(2*math.Pi*997*tm))
		if channels == 2 {
			right := 0.22*math.Sin(2*math.Pi*211*tm+0.19) +
				0.15*math.Sin(2*math.Pi*389*tm+0.31)
			hybridPCM[i*channels+1] = float32(right)
			qextPCM[i*channels+1] = float32(0.35 * math.Sin(2*math.Pi*997*tm+0.37))
		}
	}

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "hybrid.f32")
	bitstreamPath := filepath.Join(tmpDir, "hybrid.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, hybridPCM, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32(Hybrid): %v", err)
	}
	args := []string{
		"-e", "audio", "48000", fmt.Sprint(channels), "32000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20",
		inputPath, bitstreamPath,
	}
	cmd := exec.Command(opusDemo, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo hybrid encode failed: %v (%s)", err, bytes.TrimSpace(out))
	}
	hybridPacket, err := firstOpusDemoPacket(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoPacket(Hybrid): %v", err)
	}
	info, frames, _, _, err := parsePacketFramesAndPadding(hybridPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(Hybrid): %v", err)
	}
	if info.TOC.Mode != ModeHybrid || info.TOC.Bandwidth != BandwidthFullband || len(frames) != 1 {
		t.Fatalf("Hybrid packet mode=%v bandwidth=%v frames=%d", info.TOC.Mode, info.TOC.Bandwidth, len(frames))
	}

	qextPacket := encodeLibopusPacketAtBitrate(t, opusDemo, channels, qextPCM, false, true, 96000)
	_, _, padding, nbFrames, err := parsePacketFramesAndPadding(qextPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(QEXT): %v", err)
	}
	ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
	if err != nil {
		t.Fatalf("findPacketExtension(QEXT): %v", err)
	}
	if !ok || len(ext.Data) == 0 {
		t.Fatal("libopus packet missing QEXT payload")
	}

	packet := make([]byte, len(hybridPacket)+len(ext.Data)+64)
	n, err := buildRepacketizedPacketWithOptions(
		hybridPacket[0]&^byte(0x03),
		frames,
		packet,
		0,
		false,
		[]packetExtensionData{{
			ID:    qextPacketExtensionID,
			Frame: 0,
			Data:  ext.Data,
		}},
	)
	if err != nil {
		t.Fatalf("build hybrid QEXT packet: %v", err)
	}
	packet = packet[:n]

	builtInfo, _, builtPadding, builtFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(built): %v", err)
	}
	if builtInfo.TOC.Mode != ModeHybrid {
		t.Fatalf("built packet mode=%v want Hybrid", builtInfo.TOC.Mode)
	}
	if _, ok, err := findPacketExtension(builtPadding, builtFrames, qextPacketExtensionID); err != nil || !ok {
		t.Fatalf("built packet missing QEXT extension ok=%v err=%v", ok, err)
	}
	return packet
}
