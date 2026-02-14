//go:build ignore
// +build ignore

package main

/*
#cgo CFLAGS: -I${SRCDIR}/opus-1.6.1/include -I${SRCDIR}/opus-1.6.1/src -I${SRCDIR}/opus-1.6.1/celt
#cgo LDFLAGS: ${SRCDIR}/opus-1.6.1/.libs/libopus.a -lm
#include <stdlib.h>
#include "analysis.h"
#include "opus_private.h"
#include "celt.h"
#include "opus_custom.h"

static const CELTMode* gopus_get_mode_48k_20ms(void) {
  int err = 0;
  return opus_custom_mode_create(48000, 960, &err);
}

static void gopus_analysis_init(TonalityAnalysisState *st) {
  tonality_analysis_init(st, 48000);
}

static void gopus_analysis_set_audio_application(TonalityAnalysisState *st) {
  st->application = OPUS_APPLICATION_AUDIO;
}

static void gopus_run_analysis(TonalityAnalysisState *st, const CELTMode *mode, const float *pcm, int frame_size, int channels, int lsb_depth, AnalysisInfo *out) {
  run_analysis(st, mode, pcm, frame_size, frame_size, 0, -2, channels, 48000, lsb_depth, downmix_float, out);
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
	fixtureVersion    = 1
	sampleRate        = 48000
	defaultOutputPath = "encoder/testdata/libopus_analysis_trace_fixture.json"
)

type analysisTraceFixtureFile struct {
	Version    int                 `json:"version"`
	SampleRate int                 `json:"sample_rate"`
	Generator  string              `json:"generator"`
	Cases      []analysisTraceCase `json:"cases"`
}

type analysisTraceCase struct {
	Name         string               `json:"name"`
	Variant      string               `json:"variant"`
	FrameSize    int                  `json:"frame_size"`
	Channels     int                  `json:"channels"`
	Bitrate      int                  `json:"bitrate"`
	SignalFrames int                  `json:"signal_frames"`
	SignalSHA256 string               `json:"signal_sha256"`
	Frames       []analysisTraceFrame `json:"frames"`
}

type analysisTraceFrame struct {
	Valid         bool    `json:"valid"`
	Tonality      float32 `json:"tonality"`
	TonalitySlope float32 `json:"tonality_slope"`
	Noisiness     float32 `json:"noisiness"`
	Activity      float32 `json:"activity"`
	MusicProb     float32 `json:"music_prob"`
	MusicProbMin  float32 `json:"music_prob_min"`
	MusicProbMax  float32 `json:"music_prob_max"`
	Bandwidth     int     `json:"bandwidth"`
	ActivityProb  float32 `json:"activity_probability"`
	MaxPitchRatio float32 `json:"max_pitch_ratio"`
	LeakBoostB64  string  `json:"leak_boost_b64"`
}

type analysisCaseSpec struct {
	Name      string
	FrameSize int
	Channels  int
	Bitrate   int
}

func modeLabel(toc byte) string {
	cfg := int(toc >> 3)
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}

func frameSizeLabel(frameSize int) string {
	ms := 1000.0 * float64(frameSize) / float64(sampleRate)
	if math.Abs(ms-math.Round(ms)) < 1e-9 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.1fms", ms)
}

func buildSpecs() []analysisCaseSpec {
	// Keep analyzer trace coverage aligned with active libopus parity profiles
	// so mode-control updates are source-backed across mono/stereo and long frames.
	return []analysisCaseSpec{
		{
			Name:      fmt.Sprintf("HYBRID-SWB-%s-mono-48k", frameSizeLabel(480)),
			FrameSize: 480,
			Channels:  1,
			Bitrate:   48000,
		},
		{
			Name:      fmt.Sprintf("HYBRID-SWB-%s-mono-48k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   48000,
		},
		{
			Name:      fmt.Sprintf("HYBRID-SWB-%s-mono-48k", frameSizeLabel(1920)),
			FrameSize: 1920,
			Channels:  1,
			Bitrate:   48000,
		},
		{
			Name:      fmt.Sprintf("HYBRID-FB-%s-mono-64k", frameSizeLabel(480)),
			FrameSize: 480,
			Channels:  1,
			Bitrate:   64000,
		},
		{
			Name:      fmt.Sprintf("HYBRID-FB-%s-mono-64k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   64000,
		},
		{
			Name:      fmt.Sprintf("HYBRID-FB-%s-mono-64k", frameSizeLabel(2880)),
			FrameSize: 2880,
			Channels:  1,
			Bitrate:   64000,
		},
		{
			Name:      fmt.Sprintf("HYBRID-FB-%s-stereo-96k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  2,
			Bitrate:   96000,
		},
		{
			Name:      fmt.Sprintf("CELT-FB-%s-stereo-128k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  2,
			Bitrate:   128000,
		},
		{
			Name:      fmt.Sprintf("SILK-WB-%s-stereo-48k", frameSizeLabel(960)),
			FrameSize: 960,
			Channels:  2,
			Bitrate:   48000,
		},
	}
}

func clampToOpusDemoF32(in []float32) {
	const inv24 = 1.0 / 8388608.0
	for i, s := range in {
		q := float32(int64(0.5 + float64(s)*8388608.0))
		in[i] = q * float32(inv24)
	}
}

func runCase(mode *C.CELTMode, spec analysisCaseSpec, variant string) (analysisTraceCase, error) {
	signalFrames := sampleRate / spec.FrameSize
	totalSamples := signalFrames * spec.FrameSize * spec.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(variant, sampleRate, totalSamples, spec.Channels)
	if err != nil {
		return analysisTraceCase{}, err
	}
	clampToOpusDemoF32(signal)
	signalHash := testsignal.HashFloat32LE(signal)

	var st C.TonalityAnalysisState
	C.gopus_analysis_init(&st)
	C.gopus_analysis_set_audio_application(&st)

	frames := make([]analysisTraceFrame, 0, signalFrames)
	samplesPerFrame := spec.FrameSize * spec.Channels
	for i := 0; i < signalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		frame := signal[start:end]

		var info C.AnalysisInfo
		C.gopus_run_analysis(
			&st,
			mode,
			(*C.float)(unsafe.Pointer(&frame[0])),
			C.int(spec.FrameSize),
			C.int(spec.Channels),
			24,
			&info,
		)

		leakBoost := C.GoBytes(unsafe.Pointer(&info.leak_boost[0]), 19)
		frames = append(frames, analysisTraceFrame{
			Valid:         int(info.valid) != 0,
			Tonality:      float32(info.tonality),
			TonalitySlope: float32(info.tonality_slope),
			Noisiness:     float32(info.noisiness),
			Activity:      float32(info.activity),
			MusicProb:     float32(info.music_prob),
			MusicProbMin:  float32(info.music_prob_min),
			MusicProbMax:  float32(info.music_prob_max),
			Bandwidth:     int(info.bandwidth),
			ActivityProb:  float32(info.activity_probability),
			MaxPitchRatio: float32(info.max_pitch_ratio),
			LeakBoostB64:  encodeBase64(leakBoost),
		})
	}

	return analysisTraceCase{
		Name:         spec.Name,
		Variant:      variant,
		FrameSize:    spec.FrameSize,
		Channels:     spec.Channels,
		Bitrate:      spec.Bitrate,
		SignalFrames: signalFrames,
		SignalSHA256: signalHash,
		Frames:       frames,
	}, nil
}

func encodeBase64(b []byte) string {
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	if len(b) == 0 {
		return ""
	}
	out := make([]byte, 0, (len(b)+2)/3*4)
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		n = uint32(b[i]) << 16
		if rem > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(b[i+2])
		}
		out = append(out, table[(n>>18)&63], table[(n>>12)&63])
		if rem > 1 {
			out = append(out, table[(n>>6)&63])
		} else {
			out = append(out, '=')
		}
		if rem > 2 {
			out = append(out, table[n&63])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}

func normalizePath(p string) string {
	return strings.ReplaceAll(filepath.Clean(p), "\\", "/")
}

func main() {
	outPath := flag.String("out", defaultOutputPath, "output fixture path")
	flag.Parse()

	mode := C.gopus_get_mode_48k_20ms()
	if mode == nil {
		fmt.Fprintln(os.Stderr, "failed to obtain CELT mode for 48k/20ms")
		os.Exit(1)
	}

	variants := testsignal.EncoderSignalVariants()
	specs := buildSpecs()
	cases := make([]analysisTraceCase, 0, len(specs)*len(variants))
	for _, spec := range specs {
		for _, variant := range variants {
			fmt.Fprintf(os.Stderr, "generating %s variant=%s...\n", spec.Name, variant)
			c, err := runCase(mode, spec, variant)
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

	fixture := analysisTraceFixtureFile{
		Version:    fixtureVersion,
		SampleRate: sampleRate,
		Generator:  fmt.Sprintf("libopus-1.6.1 run_analysis (%s/%s)", runtime.GOOS, runtime.GOARCH),
		Cases:      cases,
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
