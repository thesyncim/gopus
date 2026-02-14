//go:build ignore
// +build ignore

package main

/*
#cgo CFLAGS: -I${SRCDIR}/opus-1.6.1/include
#cgo LDFLAGS: ${SRCDIR}/opus-1.6.1/.libs/libopus.a -lm
#include <stdlib.h>
#include <opus.h>

static OpusEncoder* gopus_mode_trace_encoder_create(int channels) {
  int err = 0;
  OpusEncoder *enc = opus_encoder_create(48000, channels, OPUS_APPLICATION_AUDIO, &err);
  if (enc == NULL || err != OPUS_OK) {
    return NULL;
  }
  return enc;
}

static int gopus_mode_trace_encoder_configure(OpusEncoder *enc, int bitrate, int bandwidth) {
  int err = OPUS_OK;
  err = opus_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_AUTO));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_VBR(1));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(10));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_DTX(0));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(0));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(0));
  if (err != OPUS_OK) return err;
  err = opus_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(24));
  if (err != OPUS_OK) return err;
  return OPUS_OK;
}

static int gopus_mode_trace_encode(OpusEncoder *enc, const float *pcm, int frame_size, unsigned char *data, int max_data_bytes) {
  return opus_encode_float(enc, pcm, frame_size, data, max_data_bytes);
}

static void gopus_mode_trace_encoder_destroy(OpusEncoder *enc) {
  if (enc != NULL) {
    opus_encoder_destroy(enc);
  }
}
*/
import "C"

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	modeTraceFixtureVersion = 1
	modeTraceSampleRate     = 48000
	modeTraceOutputPath     = "encoder/testdata/libopus_mode_trace_fixture.json"
)

type modeTraceFixtureFile struct {
	Version    int             `json:"version"`
	SampleRate int             `json:"sample_rate"`
	Generator  string          `json:"generator"`
	Cases      []modeTraceCase `json:"cases"`
	Variants   []string        `json:"variants"`
}

type modeTraceCase struct {
	Name         string                `json:"name"`
	Variant      string                `json:"variant"`
	FrameSize    int                   `json:"frame_size"`
	Channels     int                   `json:"channels"`
	Bitrate      int                   `json:"bitrate"`
	Bandwidth    string                `json:"bandwidth"`
	SignalFrames int                   `json:"signal_frames"`
	SignalSHA256 string                `json:"signal_sha256"`
	Frames       []modeTraceFrameEntry `json:"frames"`
}

type modeTraceFrameEntry struct {
	Mode      string `json:"mode"`
	TOCConfig int    `json:"toc_config"`
}

type modeTraceCaseSpec struct {
	Name      string
	FrameSize int
	Channels  int
	Bitrate   int
	Bandwidth string
}

func clampToOpusDemoF32(in []float32) {
	const inv24 = 1.0 / 8388608.0
	for i, s := range in {
		q := float32(int64(0.5 + float64(s)*8388608.0))
		in[i] = q * float32(inv24)
	}
}

func frameSizeLabel(frameSize int) string {
	ms := 1000.0 * float64(frameSize) / float64(modeTraceSampleRate)
	if math.Abs(ms-math.Round(ms)) < 1e-9 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.1fms", ms)
}

func modeLabelFromConfig(cfg int) string {
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}

func bandwidthCtlValue(label string) (C.int, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "narrowband":
		return C.int(C.OPUS_BANDWIDTH_NARROWBAND), nil
	case "mediumband":
		return C.int(C.OPUS_BANDWIDTH_MEDIUMBAND), nil
	case "wideband":
		return C.int(C.OPUS_BANDWIDTH_WIDEBAND), nil
	case "superwideband":
		return C.int(C.OPUS_BANDWIDTH_SUPERWIDEBAND), nil
	case "fullband":
		return C.int(C.OPUS_BANDWIDTH_FULLBAND), nil
	default:
		return 0, fmt.Errorf("unsupported bandwidth %q", label)
	}
}

func buildModeTraceSpecs() []modeTraceCaseSpec {
	return []modeTraceCaseSpec{
		{
			Name:      fmt.Sprintf("AUTO-NB-%s-mono-12k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   12000,
			Bandwidth: "narrowband",
		},
		{
			Name:      fmt.Sprintf("AUTO-WB-%s-mono-24k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   24000,
			Bandwidth: "wideband",
		},
		{
			Name:      fmt.Sprintf("AUTO-SWB-%s-mono-48k", frameSizeLabel(480)),
			FrameSize: 480,
			Channels:  1,
			Bitrate:   48000,
			Bandwidth: "superwideband",
		},
		{
			Name:      fmt.Sprintf("AUTO-SWB-%s-mono-48k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   48000,
			Bandwidth: "superwideband",
		},
		{
			Name:      fmt.Sprintf("AUTO-SWB-%s-mono-48k", frameSizeLabel(1920)),
			FrameSize: 1920,
			Channels:  1,
			Bitrate:   48000,
			Bandwidth: "superwideband",
		},
		{
			Name:      fmt.Sprintf("AUTO-SWB-%s-stereo-64k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  2,
			Bitrate:   64000,
			Bandwidth: "superwideband",
		},
		{
			Name:      fmt.Sprintf("AUTO-FB-%s-mono-64k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   64000,
			Bandwidth: "fullband",
		},
		{
			Name:      fmt.Sprintf("AUTO-FB-%s-stereo-96k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  2,
			Bitrate:   96000,
			Bandwidth: "fullband",
		},
	}
}

func runModeTraceCase(spec modeTraceCaseSpec, variant string) (modeTraceCase, error) {
	bw, err := bandwidthCtlValue(spec.Bandwidth)
	if err != nil {
		return modeTraceCase{}, err
	}

	signalFrames := modeTraceSampleRate / spec.FrameSize
	totalSamples := signalFrames * spec.FrameSize * spec.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(variant, modeTraceSampleRate, totalSamples, spec.Channels)
	if err != nil {
		return modeTraceCase{}, err
	}
	clampToOpusDemoF32(signal)
	signalHash := testsignal.HashFloat32LE(signal)

	enc := C.gopus_mode_trace_encoder_create(C.int(spec.Channels))
	if enc == nil {
		return modeTraceCase{}, fmt.Errorf("opus_encoder_create failed (channels=%d)", spec.Channels)
	}
	defer C.gopus_mode_trace_encoder_destroy(enc)

	if rc := C.gopus_mode_trace_encoder_configure(enc, C.int(spec.Bitrate), bw); rc != C.OPUS_OK {
		return modeTraceCase{}, fmt.Errorf("opus encoder configure failed: %d", int(rc))
	}

	out := make([]byte, 1276)
	frames := make([]modeTraceFrameEntry, 0, signalFrames)
	samplesPerFrame := spec.FrameSize * spec.Channels
	for i := 0; i < signalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		frame := signal[start:end]
		if len(frame) == 0 {
			return modeTraceCase{}, fmt.Errorf("empty frame at index %d", i)
		}
		n := int(C.gopus_mode_trace_encode(
			enc,
			(*C.float)(unsafe.Pointer(&frame[0])),
			C.int(spec.FrameSize),
			(*C.uchar)(unsafe.Pointer(&out[0])),
			C.int(len(out)),
		))
		if n <= 0 {
			return modeTraceCase{}, fmt.Errorf("opus encode failed at frame %d: %d", i, n)
		}
		cfg := int(out[0] >> 3)
		frames = append(frames, modeTraceFrameEntry{
			Mode:      modeLabelFromConfig(cfg),
			TOCConfig: cfg,
		})
	}

	return modeTraceCase{
		Name:         spec.Name,
		Variant:      variant,
		FrameSize:    spec.FrameSize,
		Channels:     spec.Channels,
		Bitrate:      spec.Bitrate,
		Bandwidth:    spec.Bandwidth,
		SignalFrames: signalFrames,
		SignalSHA256: signalHash,
		Frames:       frames,
	}, nil
}

func normalizePath(p string) string {
	return strings.ReplaceAll(filepath.Clean(p), "\\", "/")
}

func main() {
	outPath := flag.String("out", modeTraceOutputPath, "output fixture path")
	flag.Parse()

	specs := buildModeTraceSpecs()
	variants := testsignal.EncoderSignalVariants()
	cases := make([]modeTraceCase, 0, len(specs)*len(variants))

	for _, spec := range specs {
		for _, variant := range variants {
			fmt.Fprintf(os.Stderr, "generating mode trace %s variant=%s...\n", spec.Name, variant)
			c, err := runModeTraceCase(spec, variant)
			if err != nil {
				fmt.Fprintf(os.Stderr, "generate failed %s/%s: %v\n", spec.Name, variant, err)
				os.Exit(1)
			}
			cases = append(cases, c)
		}
	}

	sort.Slice(cases, func(i, j int) bool {
		a, b := cases[i], cases[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Variant < b.Variant
	})

	fixture := modeTraceFixtureFile{
		Version:    modeTraceFixtureVersion,
		SampleRate: modeTraceSampleRate,
		Generator:  fmt.Sprintf("libopus-1.6.1 opus_encode_float mode-trace (%s/%s)", runtime.GOOS, runtime.GOARCH),
		Cases:      cases,
		Variants:   variants,
	}

	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal fixture: %v\n", err)
		os.Exit(1)
	}

	path := normalizePath(*outPath)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write fixture: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d cases)\n", path, len(cases))
}
