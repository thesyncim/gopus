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
