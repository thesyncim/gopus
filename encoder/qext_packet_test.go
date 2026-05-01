//go:build gopus_qext
// +build gopus_qext

package encoder

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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

func TestBuildPacketWithSingleExtensionInto(t *testing.T) {
	frame := []byte{0xAA, 0xBB}
	payload := []byte{0x11, 0x22, 0x33}
	dst := make([]byte, 32)

	n, err := buildPacketWithSingleExtensionInto(dst, frame, types.ModeCELT, types.BandwidthFullband, 960, false, qextExtensionID, payload, 0, false)
	if err != nil {
		t.Fatalf("buildPacketWithSingleExtensionInto: %v", err)
	}

	want := []byte{0xFB, 0x41, 0x04, 0xAA, 0xBB, 0xF8, 0x11, 0x22, 0x33}
	if n != len(want) {
		t.Fatalf("len=%d want=%d", n, len(want))
	}
	if got := dst[:n]; !bytes.Equal(got, want) {
		t.Fatalf("packet=%x want=%x", got, want)
	}
}

func TestBuildPacketWithSingleExtensionIntoTargetLen(t *testing.T) {
	frame := []byte{0xAA, 0xBB}
	payload := []byte{0x11, 0x22, 0x33}
	dst := make([]byte, 32)

	n, err := buildPacketWithSingleExtensionInto(dst, frame, types.ModeCELT, types.BandwidthFullband, 960, false, qextExtensionID, payload, 12, true)
	if err != nil {
		t.Fatalf("buildPacketWithSingleExtensionInto(target): %v", err)
	}

	want := []byte{0xFB, 0x41, 0x07, 0xAA, 0xBB, 0x01, 0x01, 0x01, 0xF8, 0x11, 0x22, 0x33}
	if n != len(want) {
		t.Fatalf("len=%d want=%d", n, len(want))
	}
	if got := dst[:n]; !bytes.Equal(got, want) {
		t.Fatalf("packet=%x want=%x", got, want)
	}
}

func TestBuildPacketWithMultipleExtensionsInto(t *testing.T) {
	frame := []byte{0xAA, 0xBB}
	dst := make([]byte, 32)
	extensions := []packetExtension{
		{ID: qextExtensionID, Data: []byte{0x11, 0x22, 0x33}},
		{ID: 126, Data: []byte{'D', 12, 0x44}},
	}

	n, err := buildPacketWithExtensionsInto(dst, frame, types.ModeCELT, types.BandwidthFullband, 960, false, extensions, 0, false)
	if err != nil {
		t.Fatalf("buildPacketWithExtensionsInto: %v", err)
	}

	want := []byte{0xFB, 0x41, 0x09, 0xAA, 0xBB, 0xF9, 0x03, 0x11, 0x22, 0x33, 0xFC, 'D', 12, 0x44}
	if n != len(want) {
		t.Fatalf("len=%d want=%d", n, len(want))
	}
	if got := dst[:n]; !bytes.Equal(got, want) {
		t.Fatalf("packet=%x want=%x", got, want)
	}
}

func TestBuildPacketWithMultipleExtensionsIntoTargetLen(t *testing.T) {
	frame := []byte{0xAA, 0xBB}
	dst := make([]byte, 32)
	extensions := []packetExtension{
		{ID: qextExtensionID, Data: []byte{0x11, 0x22, 0x33}},
		{ID: 126, Data: []byte{'D', 12, 0x44}},
	}

	n, err := buildPacketWithExtensionsInto(dst, frame, types.ModeCELT, types.BandwidthFullband, 960, false, extensions, 17, true)
	if err != nil {
		t.Fatalf("buildPacketWithExtensionsInto(target): %v", err)
	}

	want := []byte{0xFB, 0x41, 0x0C, 0xAA, 0xBB, 0x01, 0x01, 0x01, 0xF9, 0x03, 0x11, 0x22, 0x33, 0xFC, 'D', 12, 0x44}
	if n != len(want) {
		t.Fatalf("len=%d want=%d", n, len(want))
	}
	if got := dst[:n]; !bytes.Equal(got, want) {
		t.Fatalf("packet=%x want=%x", got, want)
	}
}

func TestEncodeCELTPacketCarriesQEXTPayload(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	enc.SetQEXT(true)

	pcm := make([]float64, 960*2)
	for i := 0; i < 960; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[2*i] = 0.45 * math.Sin(phase)
		pcm[2*i+1] = 0.35 * math.Sin(phase+0.37)
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}
	if packet[0]&0x03 != 0x03 {
		t.Fatalf("toc code=%d want=3", packet[0]&0x03)
	}
	if packet[1]&0x40 == 0 {
		t.Fatalf("count byte=0x%02x missing padding flag", packet[1])
	}

	payload := enc.celtEncoder.LastQEXTPayload()
	if len(payload) == 0 {
		t.Fatal("CELT encoder retained empty QEXT payload")
	}
	extStart := len(packet) - 1 - len(payload)
	if extStart < 0 {
		t.Fatalf("invalid extStart=%d for len=%d payload=%d", extStart, len(packet), len(payload))
	}
	if packet[extStart] != byte(qextExtensionID<<1) {
		t.Fatalf("extension id byte=0x%02x want=0x%02x", packet[extStart], byte(qextExtensionID<<1))
	}
	if !bytes.Equal(packet[extStart+1:], payload) {
		t.Fatalf("packet tail payload mismatch:\ngot=%x\nwant=%x", packet[extStart+1:], payload)
	}
}

func TestEncodeCELTPacketCarriesQEXTPayloadLibopusDecode(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			enc := NewEncoder(48000, channels)
			enc.SetMode(ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(256000)
			enc.SetQEXT(true)

			pcm := make([]float64, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				pcm[channels*i] = 0.45 * math.Sin(phase)
				if channels == 2 {
					pcm[channels*i+1] = 0.35 * math.Sin(phase+0.37)
				}
			}

			packet, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			payload := enc.celtEncoder.LastQEXTPayload()
			if len(payload) == 0 {
				t.Fatal("CELT encoder retained empty QEXT payload")
			}

			tmpDir := t.TempDir()
			bitstreamPath := filepath.Join(tmpDir, "qext.bit")
			outputPath := filepath.Join(tmpDir, "qext.raw")
			if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
				t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
			}

			cmd := exec.Command(opusDemo, "-d", "48000", string(rune('0'+channels)), bitstreamPath, outputPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
			}
			info, err := os.Stat(outputPath)
			if err != nil {
				t.Fatalf("stat decoded output: %v", err)
			}
			if info.Size() == 0 {
				t.Fatal("opus_demo produced empty decoded output")
			}
		})
	}
}

func TestEncodeCELTPacketQEXTSizeTracksLibopus(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			enc := NewEncoder(48000, channels)
			enc.SetMode(ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(256000)
			enc.SetQEXT(true)

			pcm64 := make([]float64, 960*channels)
			pcm32 := make([]float32, 960*channels)
			for i := 0; i < 960; i++ {
				phase := 2 * math.Pi * 997 * float64(i) / 48000.0
				left := 0.45 * math.Sin(phase)
				pcm64[channels*i] = left
				pcm32[channels*i] = float32(left)
				if channels == 2 {
					right := 0.35 * math.Sin(phase+0.37)
					pcm64[channels*i+1] = right
					pcm32[channels*i+1] = float32(right)
				}
			}

			packet, err := enc.Encode(pcm64, 960)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			tmpDir := t.TempDir()
			inputPath := filepath.Join(tmpDir, "qext.f32")
			bitstreamPath := filepath.Join(tmpDir, "qext.bit")
			if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm32, 1); err != nil {
				t.Fatalf("WriteRepeatedRawFloat32: %v", err)
			}

			cmd := exec.Command(
				opusDemo,
				"-e", "restricted-celt", "48000", string(rune('0'+channels)), "256000",
				"-f32", "-cbr", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20", "-qext",
				inputPath, bitstreamPath,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo encode failed: %v (%s)", err, bytes.TrimSpace(out))
			}

			refPacket, err := firstOpusDemoPacket(bitstreamPath)
			if err != nil {
				t.Fatalf("firstOpusDemoPacket: %v", err)
			}

			diff := len(packet) - len(refPacket)
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 {
				t.Fatalf("packet length drift too high: gopus=%d libopus=%d", len(packet), len(refPacket))
			}
		})
	}
}
