//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type customSignallingRateCase struct {
	name                                   string
	frameSize, channels, maxBytes, bitrate int
	vbr, cvbr                              bool
	frames                                 [][]float32
}

type customSignallingRateResult struct {
	packet []byte
	range_ uint32
}

var customSignallingRateHelper libopustest.HelperCache

func customSignallingRateHelperPath() (string, error) {
	return customSignallingRateHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "opus custom finite-rate signalling",
			OutputBase:  "gopus_libopus_custom_signalling_rate",
			SourceFile:  "libopus_custom_signalling_rate.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
}

func runCustomSignallingRateOracle(t *testing.T, cases []customSignallingRateCase) [][]customSignallingRateResult {
	t.Helper()
	path, err := customSignallingRateHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "opus custom finite-rate signalling", err)
		return nil
	}
	var request bytes.Buffer
	request.WriteString("GCSR")
	writeU32(&request, uint32(len(cases)))
	for _, tc := range cases {
		for _, value := range []int{48000, tc.frameSize, tc.channels, tc.maxBytes, tc.bitrate, customSignallingRateBool(tc.vbr), customSignallingRateBool(tc.cvbr), len(tc.frames)} {
			writeU32(&request, uint32(value))
		}
		for _, frame := range tc.frames {
			for _, sample := range frame {
				writef32(&request, sample)
			}
		}
	}
	output, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom finite-rate signalling oracle: %v", err)
	}
	r := bytes.NewReader(output)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "GCSR" {
		t.Fatalf("custom finite-rate signalling magic=%q err=%v", magic, err)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil || int(count) != len(cases) {
		t.Fatalf("custom finite-rate signalling count=%d want=%d err=%v", count, len(cases), err)
	}
	results := make([][]customSignallingRateResult, len(cases))
	for caseIndex, tc := range cases {
		var frameCount uint32
		if err := binary.Read(r, binary.LittleEndian, &frameCount); err != nil || int(frameCount) != len(tc.frames) {
			t.Fatalf("case %d frame count=%d want=%d err=%v", caseIndex, frameCount, len(tc.frames), err)
		}
		results[caseIndex] = make([]customSignallingRateResult, len(tc.frames))
		for frameIndex := range tc.frames {
			result := &results[caseIndex][frameIndex]
			var packetSize int32
			if err := binary.Read(r, binary.LittleEndian, &packetSize); err != nil {
				t.Fatalf("case %d frame %d packet size: %v", caseIndex, frameIndex, err)
			}
			if err := binary.Read(r, binary.LittleEndian, &result.range_); err != nil {
				t.Fatalf("case %d frame %d final range: %v", caseIndex, frameIndex, err)
			}
			if packetSize < 2 || int(packetSize) > tc.maxBytes {
				t.Fatalf("case %d frame %d invalid packet size=%d max=%d", caseIndex, frameIndex, packetSize, tc.maxBytes)
			}
			result.packet = make([]byte, int(packetSize))
			if _, err := io.ReadFull(r, result.packet); err != nil {
				t.Fatalf("case %d frame %d packet: %v", caseIndex, frameIndex, err)
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom finite-rate signalling oracle has %d trailing bytes", r.Len())
	}
	return results
}

func TestCustomSignallingFiniteBitrateMatchesLibopus(t *testing.T) {
	var cases []customSignallingRateCase
	for _, frameSize := range []int{960, 640} {
		for _, channels := range []int{1, 2} {
			frames := make([][]float32, 4)
			for frame := range frames {
				frames[frame] = customSignallingRateInput(frameSize, channels, frame)
			}
			for _, cfg := range []struct {
				name    string
				bitrate int
				vbr     bool
				cvbr    bool
			}{
				{name: "CBR", bitrate: 48000},
				{name: "VBR", bitrate: 64000, vbr: true},
				{name: "CVBR", bitrate: 96000, vbr: true, cvbr: true},
			} {
				cases = append(cases, customSignallingRateCase{
					name:      fmt.Sprintf("frame%d_ch%d_%s", frameSize, channels, cfg.name),
					frameSize: frameSize, channels: channels, maxBytes: 240,
					bitrate: cfg.bitrate, vbr: cfg.vbr, cvbr: cfg.cvbr, frames: frames,
				})
			}
		}
	}
	oracle := runCustomSignallingRateOracle(t, cases)
	for caseIndex, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := custom.NewMode(48000, tc.frameSize)
			if err != nil {
				t.Fatalf("NewMode: %v", err)
			}
			enc, err := custom.NewEncoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetVBR(tc.vbr); err != nil {
				t.Fatalf("SetVBR: %v", err)
			}
			if err := enc.SetConstrainedVBR(tc.cvbr); err != nil {
				t.Fatalf("SetConstrainedVBR: %v", err)
			}
			for frameIndex, pcm := range tc.frames {
				want := oracle[caseIndex][frameIndex]
				packet, err := enc.EncodeFloat(pcm, tc.maxBytes)
				if err != nil {
					t.Fatalf("frame %d EncodeFloat: %v", frameIndex, err)
				}
				if !bytes.Equal(packet, want.packet) || enc.FinalRange() != want.range_ {
					t.Fatalf("frame %d packet/range=%x/%08x want=%x/%08x",
						frameIndex, packet, enc.FinalRange(), want.packet, want.range_)
				}
			}
		})
	}
}

func customSignallingRateInput(frameSize, channels, frame int) []float32 {
	pcm := customSignallingSequenceInput(frameSize, channels, frame)
	amp := float32(0.12)
	switch frame {
	case 1:
		amp = 0.72
	case 2:
		amp = 0.035
	case 3:
		amp = 0.31
	}
	for i := range pcm {
		pcm[i] *= amp / 0.3
		pcm[i] += float32(0.00001 * math.Sin(2*math.Pi*17*float64(i)/48000))
	}
	return pcm
}

func customSignallingRateBool(value bool) int {
	if value {
		return 1
	}
	return 0
}
