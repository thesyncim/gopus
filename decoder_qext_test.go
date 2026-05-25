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

	"github.com/thesyncim/gopus/celt"
	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/types"
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

func TestDecodeLibopusQEXTPacketFinalRangeMatchesLibopus(t *testing.T) {
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

			packet, wantRange := encodeLibopusQEXTPacketWithFinalRange(t, opusDemo, channels, pcm32, false)
			if wantRange == 0 {
				t.Fatal("libopus packet final range is zero")
			}
			_, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
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
			pcm := make([]float32, 960*channels)
			n, err := dec.Decode(packet, pcm)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}
			if got := dec.FinalRange(); got != wantRange {
				t.Fatalf("FinalRange()=0x%08x want libopus 0x%08x", got, wantRange)
			}
		})
	}
}

func TestDecodeLibopusQEXTPacketIgnoreExtensionsMatchesInactiveCELT(t *testing.T) {
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

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantN, err := wantDec.decodeOpusFrameIntoWithQEXT(want, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}

			gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got := make([]float32, 960*channels)
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if gotN != wantN {
				t.Fatalf("Decode samples=%d want %d", gotN, wantN)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want inactive CELT range 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive CELT %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeLibopusQEXTOpaquePaddingMatchesInactiveCELT(t *testing.T) {
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
			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeCELT || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want CELT single frame", info.TOC.Mode, len(frames))
			}

			malformed := make([]byte, len(packet)+8)
			n, err := buildPacketFromFramesAndPadding(packet[0]&^byte(0x03), frames, []byte{0xFF, 0xFF}, malformed, false)
			if err != nil {
				t.Fatalf("build malformed CELT QEXT padding packet: %v", err)
			}
			malformed = malformed[:n]

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantN, err := wantDec.decodeOpusFrameIntoWithQEXT(want, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}

			for _, ignore := range []bool{false, true} {
				gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(got): %v", err)
				}
				gotDec.SetIgnoreExtensions(ignore)
				got := make([]float32, 960*channels)
				gotN, err := gotDec.Decode(malformed, got)
				if err != nil {
					t.Fatalf("Decode(malformed opaque padding, ignore=%v): %v", ignore, err)
				}
				if gotN != wantN {
					t.Fatalf("Decode samples=%d want %d (ignore=%v)", gotN, wantN, ignore)
				}
				if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
					t.Fatalf("FinalRange()=0x%08x want inactive CELT range 0x%08x (ignore=%v)", gotRange, wantRange, ignore)
				}
				for i := 0; i < gotN*channels; i++ {
					if got[i] != want[i] {
						t.Fatalf("sample[%d]=%v want inactive CELT %v (ignore=%v)", i, got[i], want[i], ignore)
					}
				}
			}
		})
	}
}

func TestDecodeLibopusQEXTIgnoreExtensionsToggleSequenceMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type transitionFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		frameSize   int
		mode        Mode
		bandwidth   Bandwidth
		stereo      bool
		ignore      bool
	}

	newSine := func(channels int, freq float64, rightPhase float64, rightGain float64) []float32 {
		pcm := make([]float32, 960*channels)
		for i := 0; i < 960; i++ {
			phase := 2 * math.Pi * freq * float64(i) / 48000.0
			pcm[i*channels] = float32(0.45 * math.Sin(phase))
			if channels == 2 {
				pcm[i*channels+1] = float32(rightGain * math.Sin(phase+rightPhase))
			}
		}
		return pcm
	}

	plans := []struct {
		channels int
		pcm      []float32
		ignore   bool
	}{
		{1, newSine(1, 320.0, 0, 0), false},
		{2, newSine(2, 640.0, 0.37, 0.35), true},
		{1, newSine(1, 800.0, 0, 0), false},
	}

	sequence := make([]transitionFrame, 0, len(plans))
	for i, tc := range plans {
		packet := encodeLibopusQEXTPacket(t, opusDemo, tc.channels, tc.pcm, false)
		if len(packet) == 0 {
			t.Fatalf("encodeLibopusQEXTPacket[%d] empty", i)
		}
		info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
		if err != nil {
			t.Fatalf("parsePacketFramesAndPadding[%d]: %v", i, err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame count[%d]=%d want 1", i, len(frames))
		}
		ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
		if err != nil {
			t.Fatalf("findPacketExtension[%d]: %v", i, err)
		}
		if !ok || len(ext.Data) == 0 {
			t.Fatalf("packet[%d] missing QEXT extension payload", i)
		}
		sequence = append(sequence, transitionFrame{
			packet:      packet,
			rawFrame:    frames[0],
			qextPayload: ext.Data,
			frameSize:   info.TOC.FrameSize,
			mode:        info.TOC.Mode,
			bandwidth:   info.TOC.Bandwidth,
			stereo:      info.TOC.Stereo,
			ignore:      tc.ignore,
		})
	}

	wantDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(want): %v", err)
	}
	gotDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(got): %v", err)
	}

	for i, tc := range sequence {
		payload := tc.qextPayload
		if tc.ignore {
			payload = nil
		}
		want := make([]float32, 960*2)
		wantN, err := wantDec.decodeOpusFrameIntoWithQEXT(want, tc.rawFrame, tc.frameSize, tc.frameSize, tc.mode, tc.bandwidth, tc.stereo, payload)
		if err != nil {
			t.Fatalf("decodeOpusFrameIntoWithQEXT[%d]: %v", i, err)
		}

		gotDec.SetIgnoreExtensions(tc.ignore)
		got := make([]float32, 960*2)
		gotN, err := gotDec.Decode(tc.packet, got)
		if err != nil {
			t.Fatalf("Decode[%d] ignore=%v: %v", i, tc.ignore, err)
		}
		if gotN != wantN {
			t.Fatalf("Decode[%d] samples=%d want %d", i, gotN, wantN)
		}
		if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
			t.Fatalf("Decode[%d] FinalRange()=0x%08x want explicit payload range 0x%08x", i, gotRange, wantRange)
		}
		for j := 0; j < gotN*2; j++ {
			if got[j] != want[j] {
				t.Fatalf("Decode[%d] sample[%d]=%v want %v", i, j, got[j], want[j])
			}
		}
	}
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

func TestDecodeLibopusQEXTMultiFramePacketMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet, frames := makeLibopusQEXTMultiFramePacketForTest(t, opusDemo, channels)

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, frames[0].frameSize*channels*len(frames))
			wantOffset := 0
			for i, frame := range frames {
				n, err := wantDec.decodeOpusFrameIntoWithQEXT(want[wantOffset*channels:], frame.rawFrame, frame.frameSize, frame.frameSize, frame.mode, frame.bandwidth, frame.stereo, frame.qextPayload)
				if err != nil {
					t.Fatalf("decodeOpusFrameIntoWithQEXT[%d]: %v", i, err)
				}
				wantOffset += n
				wantDec.prevPacketStereo = frame.stereo
			}

			gotCfg := DefaultDecoderConfig(48000, channels)
			gotCfg.MaxPacketBytes = len(packet)
			gotDec, err := NewDecoder(gotCfg)
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			got := make([]float32, len(want))
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if gotN != wantOffset {
				t.Fatalf("Decode samples=%d want %d", gotN, wantOffset)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeLibopusQEXTMultiFrameIgnoreExtensionsMatchesInactivePayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet, frames := makeLibopusQEXTMultiFramePacketForTest(t, opusDemo, channels)

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, frames[0].frameSize*channels*len(frames))
			wantOffset := 0
			for i, frame := range frames {
				n, err := wantDec.decodeOpusFrameIntoWithQEXT(want[wantOffset*channels:], frame.rawFrame, frame.frameSize, frame.frameSize, frame.mode, frame.bandwidth, frame.stereo, nil)
				if err != nil {
					t.Fatalf("decodeOpusFrameIntoWithQEXT[%d]: %v", i, err)
				}
				wantOffset += n
				wantDec.prevPacketStereo = frame.stereo
			}

			gotCfg := DefaultDecoderConfig(48000, channels)
			gotCfg.MaxPacketBytes = len(packet)
			gotDec, err := NewDecoder(gotCfg)
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got := make([]float32, len(want))
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if gotN != wantOffset {
				t.Fatalf("Decode samples=%d want %d", gotN, wantOffset)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want inactive payload range 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive payload %v", i, got[i], want[i])
				}
			}
		})
	}
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

			pcm := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm[i*channels] = float32(left)
				if channels == 2 {
					right := 0.35 * math.Sin(phase+0.37)
					pcm[i*channels+1] = float32(right)
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

			refPacket := encodeLibopusQEXTPacket(t, opusDemo, channels, pcm, false)
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
				maxDiff        float64
				baseMaxDiff    float64
				signalPower    float64
				errorPower     float64
				baseErrorPower float64
				deltaNoQEXT    float64
				maxDiffIdx     int
				baseMaxDiffIdx int
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

func TestDecodeLibopusQEXTChannelTransitionSequenceMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type transitionFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		frameSize   int
		stereo      bool
	}

	newSine := func(channels int, freq float64, rightPhase float64, rightGain float64) []float32 {
		pcm := make([]float32, 960*channels)
		for i := 0; i < 960; i++ {
			phase := 2 * math.Pi * freq * float64(i) / 48000.0
			pcm[i*channels] = float32(0.45 * math.Sin(phase))
			if channels == 2 {
				pcm[i*channels+1] = float32(rightGain * math.Sin(phase+rightPhase))
			}
		}
		return pcm
	}

	plans := []struct {
		channels int
		pcm      []float32
	}{
		{1, newSine(1, 320.0, 0, 0)},
		{2, newSine(2, 640.0, 0.37, 0.35)},
		{1, newSine(1, 800.0, 0.12, 0)},
	}

	sequence := make([]transitionFrame, 0, len(plans))
	packets := make([][]byte, 0, len(plans))
	for i, tc := range plans {
		packet := encodeLibopusQEXTPacket(t, opusDemo, tc.channels, tc.pcm, false)
		if len(packet) == 0 {
			t.Fatalf("encodeLibopusQEXTPacket[%d] empty", i)
		}
		info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
		if err != nil {
			t.Fatalf("parsePacketFramesAndPadding[%d]: %v", i, err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame count[%d]=%d want 1", i, len(frames))
		}
		ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
		if err != nil {
			t.Fatalf("findPacketExtension[%d]: %v", i, err)
		}
		if !ok || len(ext.Data) == 0 {
			t.Fatalf("packet[%d] missing QEXT extension payload", i)
		}
		sequence = append(sequence, transitionFrame{
			packet:      packet,
			rawFrame:    frames[0],
			qextPayload: ext.Data,
			frameSize:   info.TOC.FrameSize,
			stereo:      info.TOC.Stereo,
		})
		packets = append(packets, packet)
	}

	celtDec := celt.NewDecoder(2)
	got := make([]float32, 960*2*len(sequence))
	gotSamples := 0
	for i, tc := range sequence {
		celtDec.SetQEXTPayload(tc.qextPayload)
		decoded, err := celtDec.DecodeFrameWithPacketStereo(tc.rawFrame, tc.frameSize, tc.stereo)
		if err != nil {
			t.Fatalf("decode frame[%d]: %v", i, err)
		}
		gotLen := len(decoded)
		if gotLen != tc.frameSize*2 {
			t.Fatalf("decoded len[%d]=%d want %d", i, gotLen, tc.frameSize*2)
		}
		start := gotSamples * 2
		copy(got[start:start+gotLen], decoded)
		gotSamples += tc.frameSize
	}

	tmpDir := t.TempDir()
	bitstreamPath := filepath.Join(tmpDir, "transition.bit")
	outputPath := filepath.Join(tmpDir, "transition.raw")
	if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, packets, 1); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
	}
	cmd := exec.Command(opusDemo, "-d", "48000", "2", bitstreamPath, outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	refData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read opus_demo output: %v", err)
	}
	gotBytes := gotSamples * 2 * 2
	if len(refData) < gotBytes {
		t.Fatalf("opus_demo output bytes=%d want at least %d", len(refData), gotBytes)
	}
	refData = refData[:gotBytes]

	var maxDiff float64
	var maxDiffIdx int
	for i := 0; i < gotBytes/2; i++ {
		ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
		q := float32(float32ToInt16(float32(got[i]))) / 32768.0
		diff := math.Abs(float64(q - ref))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	if maxDiff > 2.0/32768.0 {
		t.Fatalf("max diff too high: got %.2e want <= %.2e at sample=%d", maxDiff, 2.0/32768.0, maxDiffIdx)
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

func TestDecodeHybridLibopusQEXTPacketMatchesLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 960*channels)
			n, err := dec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != 960 {
				t.Fatalf("Decode samples=%d want 960", n)
			}

			tmpDir := t.TempDir()
			bitstreamPath := filepath.Join(tmpDir, "hybrid_qext.bit")
			outputPath := filepath.Join(tmpDir, "hybrid_qext.raw")
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
				maxDiff    float64
				maxDiffIdx int
			)
			for i := range got {
				ref := float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
				quantized := float32(float32ToInt16(got[i])) / 32768.0
				diff := math.Abs(float64(quantized - ref))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			if maxDiff > 2.0/32768.0 {
				t.Fatalf("max diff too high: got %.2e want <= %.2e at sample=%d frame=%d ch=%d", maxDiff, 2.0/32768.0, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)
			}
		})
	}
}

func TestDecodeHybridLibopusQEXTPacketIgnoreExtensionsMatchesInactiveHybrid(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeHybrid || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want Hybrid single frame", info.TOC.Mode, len(frames))
			}
			ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
			if err != nil {
				t.Fatalf("findPacketExtension: %v", err)
			}
			if !ok || len(ext.Data) == 0 {
				t.Fatal("hybrid packet missing QEXT payload")
			}

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantN, err := wantDec.decodeOpusFrameIntoWithQEXT(want, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}

			gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			gotDec.SetIgnoreExtensions(true)
			got := make([]float32, 960*channels)
			gotN, err := gotDec.Decode(packet, got)
			if err != nil {
				t.Fatalf("Decode(ignore extensions): %v", err)
			}
			if gotN != wantN {
				t.Fatalf("Decode samples=%d want %d", gotN, wantN)
			}
			if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
				t.Fatalf("FinalRange()=0x%08x want inactive Hybrid range 0x%08x", gotRange, wantRange)
			}
			for i := 0; i < gotN*channels; i++ {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%v want inactive Hybrid %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestDecodeHybridLibopusQEXTOpaquePaddingMatchesInactiveHybrid(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
			info, frames, _, _, err := parsePacketFramesAndPadding(packet)
			if err != nil {
				t.Fatalf("parsePacketFramesAndPadding: %v", err)
			}
			if info.TOC.Mode != ModeHybrid || len(frames) != 1 {
				t.Fatalf("packet mode=%v frames=%d want Hybrid single frame", info.TOC.Mode, len(frames))
			}

			malformed := make([]byte, len(packet)+8)
			n, err := buildPacketFromFramesAndPadding(packet[0]&^byte(0x03), frames, []byte{0xFF, 0xFF}, malformed, false)
			if err != nil {
				t.Fatalf("build malformed Hybrid QEXT padding packet: %v", err)
			}
			malformed = malformed[:n]

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			want := make([]float32, 960*channels)
			wantN, err := wantDec.decodeOpusFrameIntoWithQEXT(want, frames[0], info.TOC.FrameSize, info.TOC.FrameSize, info.TOC.Mode, info.TOC.Bandwidth, info.TOC.Stereo, nil)
			if err != nil {
				t.Fatalf("decodeOpusFrameIntoWithQEXT(nil): %v", err)
			}

			for _, ignore := range []bool{false, true} {
				gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatalf("NewDecoder(got): %v", err)
				}
				gotDec.SetIgnoreExtensions(ignore)
				got := make([]float32, 960*channels)
				gotN, err := gotDec.Decode(malformed, got)
				if err != nil {
					t.Fatalf("Decode(malformed opaque padding, ignore=%v): %v", ignore, err)
				}
				if gotN != wantN {
					t.Fatalf("Decode samples=%d want %d (ignore=%v)", gotN, wantN, ignore)
				}
				if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
					t.Fatalf("FinalRange()=0x%08x want inactive Hybrid range 0x%08x (ignore=%v)", gotRange, wantRange, ignore)
				}
				for i := 0; i < gotN*channels; i++ {
					if got[i] != want[i] {
						t.Fatalf("sample[%d]=%v want inactive Hybrid %v (ignore=%v)", i, got[i], want[i], ignore)
					}
				}
			}
		})
	}
}

func TestDecodeHybridLibopusQEXTIgnoreExtensionsToggleSequenceMatchesExplicitPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type transitionFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		frameSize   int
		mode        Mode
		bandwidth   Bandwidth
		stereo      bool
		ignore      bool
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			ignores := []bool{false, true, false}
			sequence := make([]transitionFrame, 0, len(ignores))
			for i, ignore := range ignores {
				packet := makeHybridQEXTPacketForTest(t, opusDemo, channels)
				info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
				if err != nil {
					t.Fatalf("parsePacketFramesAndPadding[%d]: %v", i, err)
				}
				if info.TOC.Mode != ModeHybrid || len(frames) != 1 {
					t.Fatalf("packet[%d] mode=%v frames=%d want Hybrid single frame", i, info.TOC.Mode, len(frames))
				}
				ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
				if err != nil {
					t.Fatalf("findPacketExtension[%d]: %v", i, err)
				}
				if !ok || len(ext.Data) == 0 {
					t.Fatalf("packet[%d] missing QEXT payload", i)
				}
				sequence = append(sequence, transitionFrame{
					packet:      packet,
					rawFrame:    frames[0],
					qextPayload: ext.Data,
					frameSize:   info.TOC.FrameSize,
					mode:        info.TOC.Mode,
					bandwidth:   info.TOC.Bandwidth,
					stereo:      info.TOC.Stereo,
					ignore:      ignore,
				})
			}

			wantDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(want): %v", err)
			}
			gotDec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder(got): %v", err)
			}
			for i, tc := range sequence {
				payload := tc.qextPayload
				if tc.ignore {
					payload = nil
				}
				want := make([]float32, tc.frameSize*channels)
				wantN, err := wantDec.decodeOpusFrameIntoWithQEXT(want, tc.rawFrame, tc.frameSize, tc.frameSize, tc.mode, tc.bandwidth, tc.stereo, payload)
				if err != nil {
					t.Fatalf("decodeOpusFrameIntoWithQEXT[%d]: %v", i, err)
				}
				wantDec.prevPacketStereo = tc.stereo

				gotDec.SetIgnoreExtensions(tc.ignore)
				got := make([]float32, tc.frameSize*channels)
				gotN, err := gotDec.Decode(tc.packet, got)
				if err != nil {
					t.Fatalf("Decode[%d] ignore=%v: %v", i, tc.ignore, err)
				}
				if gotN != wantN {
					t.Fatalf("Decode[%d] samples=%d want %d", i, gotN, wantN)
				}
				if gotRange, wantRange := gotDec.FinalRange(), wantDec.mainDecodeRng; gotRange != wantRange {
					t.Fatalf("Decode[%d] FinalRange()=0x%08x want 0x%08x", i, gotRange, wantRange)
				}
				for j := 0; j < gotN*channels; j++ {
					if got[j] != want[j] {
						t.Fatalf("Decode[%d] sample[%d]=%v want %v", i, j, got[j], want[j])
					}
				}
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
