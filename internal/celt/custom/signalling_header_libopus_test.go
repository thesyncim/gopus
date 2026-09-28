//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type customSignallingDecodeCase struct {
	modeFrameSize, channels, outputCapacity int
	packet                                  []byte
}

type customSignallingDecodeResult struct {
	pcm         []float32
	sampleCount int
	finalRange  uint32
}

var customSignallingDecodeHelper libopustest.HelperCache

func customSignallingDecodeHelperPath() (string, error) {
	return customSignallingDecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "custom signalling header parser",
			OutputBase:  "gopus_libopus_custom_signalling_decode",
			SourceFile:  "libopus_custom_signalling_decode.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
}

func runCustomSignallingDecodeOracle(t *testing.T, cases []customSignallingDecodeCase) []customSignallingDecodeResult {
	t.Helper()
	path, err := customSignallingDecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "custom signalling header parser", err)
		return nil
	}
	var request bytes.Buffer
	request.WriteString("GCSD")
	writeU32(&request, uint32(len(cases)))
	for _, tc := range cases {
		for _, value := range []int{48000, tc.modeFrameSize, tc.channels, tc.outputCapacity, len(tc.packet)} {
			writeU32(&request, uint32(value))
		}
		request.Write(tc.packet)
	}
	output, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom signalling decode oracle: %v", err)
	}
	r := bytes.NewReader(output)
	magic := make([]byte, 4)
	if _, err := r.Read(magic); err != nil || string(magic) != "GCSD" {
		t.Fatalf("custom signalling decode oracle magic=%q err=%v", magic, err)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || int(count) != len(cases) {
		t.Fatalf("custom signalling decode oracle count=%d want=%d err=%v", count, len(cases), err)
	}
	results := make([]customSignallingDecodeResult, len(cases))
	for caseIndex, tc := range cases {
		var sampleCount int32
		if err := binary.Read(r, binary.LittleEndian, &sampleCount); err != nil {
			t.Fatalf("case %d sample count: %v", caseIndex, err)
		}
		if sampleCount < 0 || int(sampleCount)*tc.channels > 4096 {
			t.Fatalf("case %d invalid C sample count %d", caseIndex, sampleCount)
		}
		result := &results[caseIndex]
		result.sampleCount = int(sampleCount)
		if err := binary.Read(r, binary.LittleEndian, &result.finalRange); err != nil {
			t.Fatalf("case %d final range: %v", caseIndex, err)
		}
		result.pcm = make([]float32, result.sampleCount*tc.channels)
		for sample := range result.pcm {
			var bits uint32
			if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
				t.Fatalf("case %d pcm[%d]: %v", caseIndex, sample, err)
			}
			result.pcm[sample] = math.Float32frombits(bits)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom signalling decode oracle has %d trailing bytes", r.Len())
	}
	return results
}

func customSignallingTOC(celtHeader byte) byte {
	toOpus := [...]byte{
		0xe0, 0xe8, 0xf0, 0xf8,
		0xc0, 0xc8, 0xd0, 0xd8,
		0xa0, 0xa8, 0xb0, 0xb8,
		0, 0, 0, 0,
		0x80, 0x88, 0x90, 0x98,
	}
	return toOpus[celtHeader>>3] | (celtHeader & 7)
}

func TestCustomSignallingHeaderLMEndBandAndPaddingMatchLibopus(t *testing.T) {
	pcm := make([]float32, 960)
	for i := range pcm {
		pcm[i] = float32(.23*math.Sin(2*math.Pi*419*float64(i)/48000) + .12*math.Sin(2*math.Pi*1271*float64(i)/48000))
	}
	oracle := runCustomSignallingOracle(t, []customSignallingCase{{
		fs: 48000, frameSize: 960, channels: 1, decodeChannels: 1,
		maxBytes: 200, pcm: pcm,
	}})
	base := oracle[0][0].packet
	if len(base) < 3 {
		t.Fatalf("base signalled packet too short: %x", base)
	}

	makePacket := func(header byte) []byte {
		packet := append([]byte(nil), base...)
		packet[0] = customSignallingTOC(header)
		return packet
	}
	cases := []customSignallingDecodeCase{
		{modeFrameSize: 960, channels: 1, outputCapacity: 960, packet: makePacket(0x10)}, // LM=2
		{modeFrameSize: 960, channels: 1, outputCapacity: 960, packet: makePacket(0x38)}, // end band 19
		{modeFrameSize: 960, channels: 1, outputCapacity: 960, packet: []byte{customSignallingTOC(0x38)}},
	}
	padded := make([]byte, 0, len(base)+4)
	padded = append(padded, customSignallingTOC(0x1b), 0x40, 2)
	padded = append(padded, base[1:]...)
	padded = append(padded, 0xa5, 0x5a)
	cases = append(cases, customSignallingDecodeCase{
		modeFrameSize: 960, channels: 1, outputCapacity: 960, packet: padded,
	})
	want := runCustomSignallingDecodeOracle(t, cases)

	for i, tc := range cases {
		t.Run([]string{"lm", "end-band", "header-only", "padding"}[i], func(t *testing.T) {
			mode, err := custom.NewMode(48000, tc.modeFrameSize)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			got, err := dec.DecodeFloat(tc.packet, tc.outputCapacity)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if len(got) != len(want[i].pcm) || len(got) != want[i].sampleCount*tc.channels || dec.FinalRange() != want[i].finalRange {
				t.Fatalf("samples/range=%d/%08x want=%d/%08x", len(got), dec.FinalRange(), len(want[i].pcm), want[i].finalRange)
			}
			assertCustomDecodeExact(t, "custom header", got, want[i].pcm)
		})
	}
}

func TestCustomSignallingRejectedHeaderPreservesDecoderState(t *testing.T) {
	pcm := make([]float32, 960)
	for i := range pcm {
		pcm[i] = float32(.2 * math.Sin(2*math.Pi*613*float64(i)/48000))
	}
	oracle := runCustomSignallingOracle(t, []customSignallingCase{{
		fs: 48000, frameSize: 960, channels: 1, decodeChannels: 1,
		maxBytes: 200, pcm: pcm,
	}})
	valid := oracle[0][0].packet
	invalid := append([]byte(nil), valid...)
	invalid[0] = 0x7f // fromOpus rejects headers below 0x80 before changing CELT state.

	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	afterReject, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := afterReject.DecodeFloat(invalid, 960); err == nil {
		t.Fatal("invalid custom header decoded without an error")
	}
	got, err := afterReject.DecodeFloat(valid, 960)
	if err != nil {
		t.Fatalf("decode after rejected header: %v", err)
	}
	fresh, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fresh.DecodeFloat(valid, 960)
	if err != nil {
		t.Fatalf("fresh decode: %v", err)
	}
	if afterReject.FinalRange() != fresh.FinalRange() {
		t.Fatalf("range after rejected packet=%08x want fresh %08x", afterReject.FinalRange(), fresh.FinalRange())
	}
	assertCustomDecodeExact(t, "decode after rejected header", got, want)
}
