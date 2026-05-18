//go:build gopus_qext
// +build gopus_qext

package multistream

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

func firstOpusDemoQEXTPacketForMultistreamTest(path string) ([]byte, error) {
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

func encodeLibopusQEXTPacketForMultistreamTest(t *testing.T, opusDemo string, channels int, pcm []float32) []byte {
	t.Helper()

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "qext.f32")
	bitstreamPath := filepath.Join(tmpDir, "qext.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inputPath, pcm, 1); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32: %v", err)
	}

	args := []string{
		"-e", "restricted-celt", "48000", fmt.Sprint(channels), "256000",
		"-f32", "-complexity", "10", "-bandwidth", "FB", "-framesize", "20", "-qext",
		inputPath, bitstreamPath,
	}
	cmd := exec.Command(opusDemo, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo encode failed: %v (%s)", err, bytes.TrimSpace(out))
	}

	packet, err := firstOpusDemoQEXTPacketForMultistreamTest(bitstreamPath)
	if err != nil {
		t.Fatalf("firstOpusDemoQEXTPacketForMultistreamTest: %v", err)
	}
	return packet
}

func TestDecoderQEXTIgnoreExtensionsToggleMatchesExplicitStreamPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	type streamFrame struct {
		packet      []byte
		rawFrame    []byte
		qextPayload []byte
		toc         streamTOC
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

	sequence := make([]streamFrame, 0, len(plans))
	for i, tc := range plans {
		packet := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, tc.channels, tc.pcm)
		parsed, err := parseOpusPacket(packet, false)
		if err != nil {
			t.Fatalf("parseOpusPacket[%d]: %v", i, err)
		}
		if len(parsed.frames) != 1 {
			t.Fatalf("frame count[%d]=%d want 1", i, len(parsed.frames))
		}
		extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
		if err != nil {
			t.Fatalf("parsePacketExtensionList[%d]: %v", i, err)
		}
		var qextPayload []byte
		for _, ext := range extensions {
			if ext.ID == qextPacketExtensionID && ext.Frame == 0 {
				qextPayload = ext.Data
				break
			}
		}
		if len(qextPayload) == 0 {
			t.Fatalf("packet[%d] missing QEXT payload", i)
		}
		sequence = append(sequence, streamFrame{
			packet:      packet,
			rawFrame:    parsed.frames[0],
			qextPayload: qextPayload,
			toc:         parseStreamTOC(packet[0]),
			ignore:      tc.ignore,
		})
	}

	wantStream := newStreamDecoder(48000, 2)
	gotDec, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	for i, tc := range sequence {
		payload := tc.qextPayload
		if tc.ignore {
			payload = nil
		}
		wantStream.recordDecodeCall(960, len(tc.packet))
		want, err := wantStream.finishDecode(wantStream.decodeFramePayload(tc.rawFrame, 960, tc.toc, payload))
		if err != nil {
			t.Fatalf("decodeFramePayload[%d]: %v", i, err)
		}

		gotDec.SetIgnoreExtensions(tc.ignore)
		got, err := gotDec.Decode(tc.packet, 960)
		if err != nil {
			t.Fatalf("Decode[%d] ignore=%v: %v", i, tc.ignore, err)
		}
		if len(got) != len(want) {
			t.Fatalf("Decode[%d] len=%d want %d", i, len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("Decode[%d] sample[%d]=%v want %v", i, j, got[j], want[j])
			}
		}
	}
}
