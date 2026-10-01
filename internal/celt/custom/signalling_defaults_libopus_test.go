//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type customSignallingDefaultsCase struct {
	frameSize int
	channels  int
	pcm       []float32
}

type customSignallingDefaultsResult struct {
	packet                   []byte
	pcm                      []float32
	encRange, decRange       uint32
	lsbDepth, decoderComplex int
}

var customSignallingDefaultsHelper libopustest.HelperCache

func runCustomSignallingDefaultsOracle(t *testing.T, cases []customSignallingDefaultsCase) []customSignallingDefaultsResult {
	t.Helper()
	path, err := customSignallingDefaultsHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "opus custom constructor defaults",
			OutputBase:  "gopus_libopus_custom_signalling_defaults",
			SourceFile:  "libopus_custom_signalling_defaults.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "opus custom constructor defaults", err)
		return nil
	}
	var request bytes.Buffer
	request.WriteString("GCDF")
	writeU32(&request, uint32(len(cases)))
	for _, tc := range cases {
		for _, value := range []int{48000, tc.frameSize, tc.channels, 400, len(tc.pcm)} {
			writeU32(&request, uint32(value))
		}
		for _, sample := range tc.pcm {
			writef32(&request, sample)
		}
	}
	response, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom constructor defaults oracle: %v", err)
	}
	r := bytes.NewReader(response)
	magic := make([]byte, 4)
	if _, err := r.Read(magic); err != nil || string(magic) != "GCDF" {
		t.Fatalf("custom constructor defaults magic=%q err=%v", magic, err)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || int(count) != len(cases) {
		t.Fatalf("custom constructor defaults count=%d want=%d err=%v", count, len(cases), err)
	}
	results := make([]customSignallingDefaultsResult, len(cases))
	for i, tc := range cases {
		result := &results[i]
		var packetSize, sampleCount, lsbDepth, decoderComplex int32
		for _, value := range []any{&packetSize, &result.encRange, &lsbDepth, &decoderComplex, &sampleCount, &result.decRange} {
			if err := binary.Read(r, binary.LittleEndian, value); err != nil {
				t.Fatalf("case %d read C result: %v", i, err)
			}
		}
		result.lsbDepth = int(lsbDepth)
		result.decoderComplex = int(decoderComplex)
		if packetSize < 2 || int(packetSize) > 400 || int(sampleCount) != tc.frameSize {
			t.Fatalf("case %d invalid C packet/samples=%d/%d", i, packetSize, sampleCount)
		}
		result.packet = make([]byte, int(packetSize))
		if _, err := r.Read(result.packet); err != nil {
			t.Fatalf("case %d packet: %v", i, err)
		}
		result.pcm = make([]float32, int(sampleCount)*tc.channels)
		for j := range result.pcm {
			var sampleBits uint32
			if err := binary.Read(r, binary.LittleEndian, &sampleBits); err != nil {
				t.Fatalf("case %d PCM[%d]: %v", i, j, err)
			}
			result.pcm[j] = math.Float32frombits(sampleBits)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom constructor defaults oracle has %d trailing bytes", r.Len())
	}
	return results
}

func TestCustomSignallingConstructorDefaultsMatchLibopus(t *testing.T) {
	cases := make([]customSignallingDefaultsCase, 0, 4)
	for _, frameSize := range []int{960, 640} {
		for _, channels := range []int{1, 2} {
			pcm := customSignallingSequenceInput(frameSize, channels, 3)
			for i := range pcm {
				pcm[i] += float32(0.000013 * math.Sin(2*math.Pi*37*float64(i)/48000))
			}
			cases = append(cases, customSignallingDefaultsCase{frameSize, channels, pcm})
		}
	}
	oracle := runCustomSignallingDefaultsOracle(t, cases)
	for i, tc := range cases {
		t.Run(testCaseName(tc.frameSize, tc.channels), func(t *testing.T) {
			mode, err := custom.NewMode(48000, tc.frameSize)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := custom.NewEncoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			if !enc.Signalling() || enc.Complexity() != 5 || enc.LSBDepth() != 24 || enc.Bitrate() != -1 || enc.VBR() || !enc.ConstrainedVBR() || enc.Prediction() != 2 || enc.PacketLoss() != 0 {
				t.Fatalf("encoder defaults signalling=%v complexity=%d lsb=%d bitrate=%d vbr=%v cvbr=%v prediction=%d loss=%d",
					enc.Signalling(), enc.Complexity(), enc.LSBDepth(), enc.Bitrate(), enc.VBR(), enc.ConstrainedVBR(), enc.Prediction(), enc.PacketLoss())
			}
			packet, err := enc.EncodeFloat(tc.pcm, 400)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			want := oracle[i]
			if !bytes.Equal(packet, want.packet) || enc.FinalRange() != want.encRange {
				t.Fatalf("default encoder packet/range=%x/%08x want=%x/%08x", packet, enc.FinalRange(), want.packet, want.encRange)
			}

			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			if !dec.Signalling() || dec.Complexity() != 0 {
				t.Fatalf("decoder defaults signalling=%v complexity=%d want true/0", dec.Signalling(), dec.Complexity())
			}
			got, err := dec.DecodeFloat(want.packet, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if len(got) != len(want.pcm) || dec.FinalRange() != want.decRange {
				t.Fatalf("default decoder samples/range=%d/%08x want=%d/%08x (C lsb=%d complexity=%d)",
					len(got), dec.FinalRange(), len(want.pcm), want.decRange, want.lsbDepth, want.decoderComplex)
			}
			if want.lsbDepth != 24 || want.decoderComplex != 0 {
				t.Fatalf("libopus defaults lsb=%d decoder complexity=%d want 24/0", want.lsbDepth, want.decoderComplex)
			}
			assertCustomDecodeExact(t, "constructor defaults", got, want.pcm)
		})
	}
}

func testCaseName(frameSize, channels int) string {
	return fmt.Sprintf("frame%d_ch%d", frameSize, channels)
}
