//go:build gopus_custom_modes && gopus_qext

package custom_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type customSignallingQEXTResult struct {
	packet             []byte
	pcm                []float32
	encRange, decRange uint32
}

var customSignallingQEXTHelper libopustest.HelperCache

func runCustomSignallingQEXTOracle(t *testing.T, pcm []float32, channels, maxBytes int) customSignallingQEXTResult {
	t.Helper()
	path, err := customSignallingQEXTHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "custom signalled QEXT decode",
			OutputBase:  "gopus_libopus_custom_signalling_qext",
			SourceFile:  "libopus_custom_signalling_qext.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom signalled QEXT decode", err)
		return customSignallingQEXTResult{}
	}
	var request bytes.Buffer
	request.WriteString("GCQX")
	for _, value := range []int{48000, 960, channels, maxBytes, len(pcm)} {
		writeU32(&request, uint32(value))
	}
	for _, sample := range pcm {
		writef32(&request, sample)
	}
	response, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom QEXT oracle: %v", err)
	}
	r := bytes.NewReader(response)
	magic := make([]byte, 4)
	if _, err := r.Read(magic); err != nil || string(magic) != "GCQX" {
		t.Fatalf("custom QEXT oracle magic=%q err=%v", magic, err)
	}
	var packetSize, sampleCount int32
	var result customSignallingQEXTResult
	for _, value := range []any{&packetSize, &result.encRange, &sampleCount, &result.decRange} {
		if err := binary.Read(r, binary.LittleEndian, value); err != nil {
			t.Fatalf("custom QEXT oracle result: %v", err)
		}
	}
	if packetSize < 2 || int(packetSize) > maxBytes || int(sampleCount) != 960 {
		t.Fatalf("custom QEXT oracle packet/samples=%d/%d", packetSize, sampleCount)
	}
	result.packet = make([]byte, int(packetSize))
	if _, err := r.Read(result.packet); err != nil {
		t.Fatalf("custom QEXT packet: %v", err)
	}
	result.pcm = make([]float32, int(sampleCount)*channels)
	for i := range result.pcm {
		var bits uint32
		if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
			t.Fatalf("custom QEXT PCM[%d]: %v", i, err)
		}
		result.pcm[i] = math.Float32frombits(bits)
	}
	if r.Len() != 0 {
		t.Fatalf("custom QEXT oracle has %d trailing bytes", r.Len())
	}
	return result
}

func TestCustomSignalledQEXTPaddingMatchesLibopus(t *testing.T) {
	pcm := make([]float32, 960)
	for i := range pcm {
		pcm[i] = float32(.23*math.Sin(2*math.Pi*419*float64(i)/48000) + .12*math.Sin(2*math.Pi*1271*float64(i)/48000))
	}
	want := runCustomSignallingQEXTOracle(t, pcm, 1, 800)
	if want.packet[0]&3 != 3 || len(want.packet) < 4 || want.packet[1]&0x40 == 0 {
		t.Fatalf("C packet does not exercise padded code-3 framing: toc=%02x len=%d", want.packet[0], len(want.packet))
	}

	// celt_decode_with_ec locates the extension ID at the first padding byte.
	// The extension coder receives the bytes after that ID, excluding it.
	pos := 2
	paddingBytes := 0
	for {
		if pos >= len(want.packet) {
			t.Fatal("C packet ends inside its padding length")
		}
		count := want.packet[pos]
		pos++
		if count == 255 {
			paddingBytes += 254
		} else {
			paddingBytes += int(count)
		}
		if count != 255 {
			break
		}
	}
	mainLength := len(want.packet) - pos - paddingBytes
	if mainLength <= 0 || paddingBytes < 2 || pos+mainLength >= len(want.packet) || want.packet[pos+mainLength] != 124<<1 {
		t.Fatalf("C packet has no nonempty QEXT payload: pos=%d main=%d padding=%d packet=%x", pos, mainLength, paddingBytes, want.packet)
	}

	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.DecodeFloat(want.packet, 960)
	if err != nil {
		t.Fatalf("DecodeFloat: %v", err)
	}
	if len(got) != len(want.pcm) || dec.FinalRange() != want.decRange {
		t.Fatalf("decoded samples/range=%d/%08x want=%d/%08x", len(got), dec.FinalRange(), len(want.pcm), want.decRange)
	}
	assertCustomDecodeExact(t, "QEXT padding", got, want.pcm)
}
