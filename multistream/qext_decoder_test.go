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

func TestDecoderQEXTTwoStreamPacketMatchesExplicitStreamPayloads(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	parseQEXTFrame := func(label string, packet []byte) (rawFrame []byte, payload []byte, toc streamTOC) {
		parsed, err := parseOpusPacket(packet, false)
		if err != nil {
			t.Fatalf("parseOpusPacket(%s): %v", label, err)
		}
		if len(parsed.frames) != 1 {
			t.Fatalf("%s frame count=%d want 1", label, len(parsed.frames))
		}
		extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
		if err != nil {
			t.Fatalf("parsePacketExtensionList(%s): %v", label, err)
		}
		for _, ext := range extensions {
			if ext.ID == qextPacketExtensionID && ext.Frame == 0 {
				payload = ext.Data
				break
			}
		}
		if len(payload) == 0 {
			t.Fatalf("%s missing QEXT payload", label)
		}
		return parsed.frames[0], payload, parseStreamTOC(packet[0])
	}

	stereoPCM := make([]float32, 960*2)
	monoPCM := make([]float32, 960)
	for i := 0; i < 960; i++ {
		tm := float64(i) / 48000.0
		stereoPCM[2*i] = float32(0.45 * math.Sin(2*math.Pi*440*tm))
		stereoPCM[2*i+1] = float32(0.35 * math.Sin(2*math.Pi*660*tm+0.37))
		monoPCM[i] = float32(0.40 * math.Sin(2*math.Pi*550*tm+0.19))
	}

	coupledPacket := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 2, stereoPCM)
	monoPacket := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 1, monoPCM)
	coupledFrame, coupledPayload, coupledTOC := parseQEXTFrame("coupled", coupledPacket)
	monoFrame, monoPayload, monoTOC := parseQEXTFrame("mono", monoPacket)

	selfDelimitedCoupled, err := makeSelfDelimitedPacket(coupledPacket)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket: %v", err)
	}
	packet := make([]byte, 0, len(selfDelimitedCoupled)+len(monoPacket))
	packet = append(packet, selfDelimitedCoupled...)
	packet = append(packet, monoPacket...)

	for _, ignore := range []bool{false, true} {
		coupledWant := newStreamDecoder(48000, 2)
		coupledPayloadForDecode := coupledPayload
		if ignore {
			coupledPayloadForDecode = nil
		}
		coupledWant.recordDecodeCall(960, len(coupledPacket))
		coupledOut, err := coupledWant.finishDecode(coupledWant.decodeFramePayload(coupledFrame, 960, coupledTOC, coupledPayloadForDecode))
		if err != nil {
			t.Fatalf("decode coupled ignore=%v: %v", ignore, err)
		}

		monoWant := newStreamDecoder(48000, 1)
		monoPayloadForDecode := monoPayload
		if ignore {
			monoPayloadForDecode = nil
		}
		monoWant.recordDecodeCall(960, len(monoPacket))
		monoOut, err := monoWant.finishDecode(monoWant.decodeFramePayload(monoFrame, 960, monoTOC, monoPayloadForDecode))
		if err != nil {
			t.Fatalf("decode mono ignore=%v: %v", ignore, err)
		}

		want := make([]float64, 960*3)
		for i := 0; i < 960; i++ {
			want[3*i] = coupledOut[2*i]
			want[3*i+1] = coupledOut[2*i+1]
			want[3*i+2] = monoOut[i]
		}

		dec, err := NewDecoder(48000, 3, 2, 1, []byte{0, 1, 2})
		if err != nil {
			t.Fatalf("NewDecoder(ignore=%v): %v", ignore, err)
		}
		dec.SetIgnoreExtensions(ignore)
		got, err := dec.Decode(packet, 960)
		if err != nil {
			t.Fatalf("Decode(ignore=%v): %v", ignore, err)
		}
		if len(got) != len(want) {
			t.Fatalf("Decode(ignore=%v) len=%d want %d", ignore, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("Decode(ignore=%v) sample[%d]=%v want %v", ignore, i, got[i], want[i])
			}
		}
	}
}
