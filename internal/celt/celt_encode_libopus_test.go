package celt

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"runtime"
	"strconv"
	"testing"
)

type libopusCELTEncodeCase struct {
	name       string
	channels   int
	frameSize  int
	bitrate    int32
	complexity int32
	// maxPayloadBytes, when > 0, caps the CBR payload (mirrors libopus
	// nbCompressedBytes); used to exercise the low-budget transient_got_disabled
	// path. 0 means "derive from bitrate".
	maxPayloadBytes int
	pcm             []float32
}

var libopusCELTEncodeHelper libopustest.HelperCache

func getLibopusCELTEncodeHelperPath() (string, error) {
	return libopusCELTEncodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "celt encode",
		OutputBase:  "gopus_libopus_celt_encode",
		SourceFile:  "libopus_celt_encode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// targetBytesForCase mirrors Encoder.cbrPayloadBytes for the chosen config so
// the libopus side receives the identical nbCompressedBytes budget.
func targetBytesForCase(tc libopusCELTEncodeCase) int {
	enc := newCELTEncodeCaseEncoder(tc)
	return enc.cbrPayloadBytes(tc.frameSize)
}

func newCELTEncodeCaseEncoder(tc libopusCELTEncodeCase) *Encoder {
	enc := NewEncoder(tc.channels)
	// Restrict to the pure CELT codec: disable the top-level Opus
	// preprocessing stages that celt_encode_with_ec() does not perform.
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetDelayCompensationEnabled(false)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	enc.SetComplexity(int(tc.complexity))
	enc.SetBitrate(int(tc.bitrate))
	if tc.maxPayloadBytes > 0 {
		enc.SetMaxPayloadBytes(tc.maxPayloadBytes)
	}
	return enc
}

func probeLibopusCELTEncode(cases []libopusCELTEncodeCase) ([][]byte, error) {
	binPath, err := getLibopusCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GCEI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(targetBytesForCase(tc)))
		payload.I32(tc.bitrate)
		payload.I32(tc.complexity)
		for _, sample := range tc.pcm {
			payload.Float32(sample)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt encode", "GCEO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]byte, count)
	for i := range count {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestCELTEncodeMatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTEncodeCase{
		{
			name:       "mono_20ms_64k_tone",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 960, 0.0),
		},
		{
			name:       "mono_20ms_64k_phase",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 960, 0.7),
		},
		{
			name:       "stereo_20ms_128k",
			channels:   2,
			frameSize:  960,
			bitrate:    128000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(2, 960, 0.3),
		},
		{
			name:       "mono_10ms_96k",
			channels:   1,
			frameSize:  480,
			bitrate:    96000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 480, 1.1),
		},
		{
			name:       "mono_5ms_64k",
			channels:   1,
			frameSize:  240,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 240, 0.2),
		},
		{
			name:       "mono_2_5ms_64k",
			channels:   1,
			frameSize:  120,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeOraclePCM(1, 120, 0.9),
		},
		{
			name:       "mono_20ms_transient",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 10,
			pcm:        celtEncodeTransientPCM(1, 960),
		},
		{
			name:       "stereo_20ms_transient_320k",
			channels:   2,
			frameSize:  960,
			bitrate:    320000,
			complexity: 10,
			pcm:        celtEncodeTransientPCM(2, 960),
		},
		{
			name:       "mono_20ms_noise_low_bitrate",
			channels:   1,
			frameSize:  960,
			bitrate:    16000,
			complexity: 10,
			pcm:        celtEncodeNoisePCM(1, 960, 12345),
		},
		{
			name:       "stereo_20ms_noise_510k",
			channels:   2,
			frameSize:  960,
			bitrate:    510000,
			complexity: 10,
			pcm:        celtEncodeNoisePCM(2, 960, 0xC0FFEE),
		},
		{
			name:       "mono_20ms_complexity5",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 5,
			pcm:        celtEncodeOraclePCM(1, 960, 0.55),
		},
		{
			name:       "mono_20ms_complexity0",
			channels:   1,
			frameSize:  960,
			bitrate:    64000,
			complexity: 0,
			pcm:        celtEncodeOraclePCM(1, 960, 0.55),
		},
		{
			// Mirrors TestEncodeFrameBudgetDisabledTransientAdvancesConsecTransient:
			// complexity 0, 2-byte budget, 440 Hz sine onset from silence.
			name:            "mono_20ms_complexity0_budget2_sine",
			channels:        1,
			frameSize:       960,
			bitrate:         64000,
			complexity:      0,
			maxPayloadBytes: 2,
			pcm:             float32Slice(generateSineWave(440.0, 960)),
		},
	}
	want, err := probeLibopusCELTEncode(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt encode", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := newCELTEncodeCaseEncoder(tc)
			got, err := enc.EncodeFrame(append([]float32(nil), tc.pcm...), tc.frameSize)
			if err != nil {
				t.Fatalf("gopus EncodeFrame: %v", err)
			}
			ref := want[i]
			if len(got) != len(ref) {
				t.Fatalf("packet length=%d want %d", len(got), len(ref))
			}
			for j := range got {
				if got[j] != ref[j] {
					t.Fatalf("packet byte[%d]=0x%02x want 0x%02x (first diff)", j, got[j], ref[j])
				}
			}
		})
	}
}

// TestCELTEncodeMultiFrameMatchesLibopusC drives several consecutive frames
// through one encoder instance on each side, verifying inter-frame state
// (energy prediction, prefilter, preemphasis memory, VBR/CVBR reservoir)
// stays byte-exact. The libopus side encodes the frames sequentially with a
// single celt_encode_with_ec() encoder (see libopus_celt_encode_info.c, which
// resets per case; here we use one case per frame but a persistent gopus
// encoder, and compare each frame against an independently-seeded libopus
// encoder driven through the multi-frame helper).
func TestCELTEncodeMultiFrameMatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 1
		frameSize  = 960
		bitrate    = 64000
		complexity = 10
		nFrames    = 6
	)
	frames := make([][]float32, nFrames)
	for f := range nFrames {
		frames[f] = celtEncodeOraclePCM(channels, frameSize, 0.21*float64(f))
	}
	want, err := probeLibopusCELTEncodeStream(channels, frameSize, bitrate, complexity, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt encode stream", err)
	}
	tc := libopusCELTEncodeCase{channels: channels, frameSize: frameSize, bitrate: bitrate, complexity: complexity}
	enc := newCELTEncodeCaseEncoder(tc)
	for f := range nFrames {
		got, err := enc.EncodeFrame(append([]float32(nil), frames[f]...), frameSize)
		if err != nil {
			t.Fatalf("frame %d EncodeFrame: %v", f, err)
		}
		ref := want[f]
		if len(got) != len(ref) {
			t.Fatalf("frame %d packet length=%d want %d", f, len(got), len(ref))
		}
		for j := range got {
			if got[j] != ref[j] {
				t.Fatalf("frame %d packet byte[%d]=0x%02x want 0x%02x", f, j, got[j], ref[j])
			}
		}
	}
}

// probeLibopusCELTEncodeStream encodes a sequence of frames through a single
// libopus encoder (no per-frame reset) so inter-frame state is exercised.
func probeLibopusCELTEncodeStream(channels, frameSize int, bitrate, complexity int32, frames [][]float32) ([][]byte, error) {
	binPath, err := getLibopusCELTEncodeHelperPath()
	if err != nil {
		return nil, err
	}
	tc := libopusCELTEncodeCase{channels: channels, frameSize: frameSize, bitrate: bitrate, complexity: complexity}
	target := targetBytesForCase(tc)
	payload := libopustest.NewOraclePayloadVersion("GCSI", 1, uint32(len(frames)))
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(target))
	payload.I32(bitrate)
	payload.I32(complexity)
	for _, frame := range frames {
		for _, sample := range frame {
			payload.Float32(sample)
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt encode stream", "GCSO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(frames))
	out := make([][]byte, count)
	for i := range count {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func celtEncodeOraclePCM(channels, frames int, phase float64) []float32 {
	pcm := make([]float32, frames*channels)
	for i := range frames {
		tm := float64(i) * 0.013
		l := 0.42*math.Sin(2*math.Pi*440*float64(i)/48000+phase) +
			0.09*math.Cos(0.31*tm) + 0.03*math.Sin(2*math.Pi*1830*float64(i)/48000)
		pcm[i*channels] = float32(l)
		if channels == 2 {
			r := 0.35*math.Sin(2*math.Pi*523*float64(i)/48000+0.4) -
				0.07*math.Cos(0.19*tm+phase)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// celtEncodeTransientPCM builds a quiet-then-burst signal to exercise the
// transient analysis / short-block MDCT path.
func celtEncodeTransientPCM(channels, frames int) []float32 {
	pcm := make([]float32, frames*channels)
	for i := range frames {
		var amp float64
		if i >= frames/2 && i < frames/2+40 {
			amp = 0.8
		} else {
			amp = 0.02
		}
		l := amp * math.Sin(2*math.Pi*1000*float64(i)/48000)
		pcm[i*channels] = float32(l)
		if channels == 2 {
			r := amp * math.Sin(2*math.Pi*1500*float64(i)/48000+0.3)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// celtEncodeNoisePCM builds a deterministic pseudo-random noise signal.
func celtEncodeNoisePCM(channels, frames int, seed uint32) []float32 {
	pcm := make([]float32, frames*channels)
	state := seed
	next := func() float32 {
		state = state*1664525 + 1013904223
		return float32(int32(state)) / float32(math.MaxInt32) * 0.5
	}
	for i := 0; i < frames*channels; i++ {
		pcm[i] = next()
	}
	return pcm
}

const (
	libopusCELTFilterModeDeemphasis      = uint32(0)
	libopusCELTFilterModeCombFilter      = uint32(1)
	libopusCELTFilterModeCombFilterInput = uint32(2)
)

var libopusCELTFilterHelper libopustest.HelperCache
var libopusCELTFilterScalarHelper libopustest.HelperCache

type libopusDeemphasisResult struct {
	mem []float32
	pcm []float32
}

func buildLibopusCELTFilterHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT filter",
		OutputBase:  "gopus_libopus_celt_filter",
		SourceFile:  "libopus_celt_filter_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		SIMDRef:     true,
		// celt.c calls comb_filter_const through the host libopus feature
		// macros. On an x86 SIMD build that resolves through
		// COMB_FILTER_CONST_IMPL to comb_filter_const_sse; compile the matching
		// implementation and map alongside the two codec translation units.
		// Both files compile to empty/scalar sections when x86 SIMD is disabled.
		RefSources: []string{
			"celt/celt_decoder.c",
			"celt/celt.c",
			"celt/x86/pitch_sse.c",
			"celt/x86/x86_celt_map.c",
		},
		Libs:      []string{"-lm"},
		DeadStrip: true,
	})
}

func buildLibopusCELTFilterScalarHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:          "CELT filter scalar",
		OutputBase:     "gopus_libopus_celt_filter_scalar",
		SourceFile:     "libopus_celt_filter_info.c",
		CFlags:         []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
		RefIncludes:    []string{"src", "celt", "silk", "silk/float"},
		ForceScalarRef: true,
		RefSources: []string{
			"celt/celt_decoder.c",
			"celt/celt.c",
		},
		Libs:      []string{"-lm"},
		DeadStrip: true,
	})
}

func runLibopusCELTFilter(t *testing.T, payload *libopustest.OraclePayload, simdKernel bool) *libopustest.OracleReader {
	t.Helper()
	helper := &libopusCELTFilterScalarHelper
	builder := buildLibopusCELTFilterScalarHelper
	if simdKernel {
		helper = &libopusCELTFilterHelper
		builder = buildLibopusCELTFilterHelper
	}
	binPath, err := helper.Path(builder)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT filter", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT filter", "GCFO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT filter", err)
	}
	return reader
}

func probeLibopusDeemphasis(t *testing.T, channels int, samples [][]float32, mem []float32) libopusDeemphasisResult {
	return probeLibopusDeemphasisWithOptions(t, channels, samples, mem, 1, nil)
}

func probeLibopusDeemphasisWithOptions(t *testing.T, channels int, samples [][]float32, mem []float32, downsample int, accum []float32) libopusDeemphasisResult {
	t.Helper()
	n := len(samples[0])
	if channels < 1 || channels > 2 || len(samples) != channels || len(mem) != channels || downsample < 1 || downsample > n {
		t.Fatalf("invalid deemphasis probe dimensions: channels=%d samples=%d mem=%d downsample=%d n=%d", channels, len(samples), len(mem), downsample, n)
	}
	count := channels * (n / downsample)
	accumFlag := uint32(0)
	if accum != nil {
		if len(accum) != count {
			t.Fatalf("accum length=%d want %d", len(accum), count)
		}
		accumFlag = 1
	}
	payload := libopustest.NewOraclePayload("GCFI", libopusCELTFilterModeDeemphasis)
	payload.U32(uint32(channels))
	payload.U32(uint32(n))
	payload.U32(uint32(downsample))
	payload.U32(accumFlag)
	payload.Float32(float32(PreemphCoef))
	payload.Float32(0)
	payload.Float32(1)
	payload.Float32(1)
	for i := range channels {
		payload.Float32(mem[i])
	}
	for ch := range channels {
		for _, sample := range samples[ch] {
			payload.Float32(sample)
		}
	}
	if accumFlag != 0 {
		payload.Float32s(accum...)
	}
	variant := requirePairedCELTOracleMode(t)
	reader := runLibopusCELTFilter(t, payload, variant == libopustooling.LibopusReferenceSIMD)
	if gotMode := reader.U32(); gotMode != libopusCELTFilterModeDeemphasis {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTFilterModeDeemphasis)
	}
	count = int(reader.U32())
	out := libopusDeemphasisResult{
		mem: make([]float32, channels),
		pcm: make([]float32, count),
	}
	for i := range out.mem {
		out.mem[i] = reader.Float32()
	}
	reader.ExpectRemaining(count * 4)
	for i := range out.pcm {
		out.pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDeemphasisMatchesLibopus drives Decoder.deemphasis through every input
// layout the decoder uses (interleaved into a separate buffer, interleaved in
// place, planar) and every libopus branch (plain, accumulating onto a SILK
// lowband, downsampling) against libopus deemphasis().
func TestDeemphasisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	type signal struct {
		name  string
		left  func(i int) float32
		right func(i int) float32
		mem   [2]float32
	}
	signals := []signal{
		{"loud", func(i int) float32 {
			return float32(math.Sin(float64(i+4)*0.113)*81000 + math.Cos(float64(i+9)*0.047)*23000)
		}, func(i int) float32 {
			return float32(math.Cos(float64(i+6)*0.151)*1600 - math.Sin(float64(i+2)*0.083)*810)
		}, [2]float32{-512.25, 311.5}},
		{"small", func(i int) float32 {
			return float32(math.Sin(float64(i+1)*0.5) * 1e-3)
		}, func(i int) float32 {
			return float32(math.Cos(float64(i+1)*0.3) * 7e-4)
		}, [2]float32{0, 3e-31}},
		{"silence", func(int) float32 { return 0 }, func(int) float32 { return 0 }, [2]float32{0, 0}},
	}
	for _, sig := range signals {
		for _, channels := range []int{1, 2} {
			for _, downsample := range []int{1, 2, 3} {
				for _, accum := range []bool{false, true} {
					name := fmt.Sprintf("%s/ch%d/ds%d/accum=%t", sig.name, channels, downsample, accum)
					t.Run(name, func(t *testing.T) {
						const n = 120
						planes := make([][]float32, channels)
						interleaved := make([]float32, n*channels)
						for c := range channels {
							planes[c] = make([]float32, n)
							gen := sig.left
							if c == 1 {
								gen = sig.right
							}
							for i := range n {
								planes[c][i] = gen(i)
								interleaved[i*channels+c] = planes[c][i]
							}
						}
						seed := make([]float32, (n/downsample)*channels)
						var accumSeed []float32
						if accum {
							for i := range seed {
								seed[i] = float32(math.Sin(float64(i+3)*0.019)*0.31 + math.Cos(float64(i+11)*0.0073)*0.12)
							}
							accumSeed = seed
						}
						want := probeLibopusDeemphasisWithOptions(t, channels, planes, sig.mem[:channels], downsample, accumSeed)

						run := func(label string, fn func(d *Decoder, out []float32)) {
							d := NewDecoder(channels)
							copy(d.preemphState, sig.mem[:channels])
							out := append([]float32(nil), seed...)
							fn(d, out)
							assertCELTFilterFloat32Bits(t, label+" pcm", out, want.pcm)
							assertCELTFilterMemBits(t, d, want.mem)
						}
						run("interleaved", func(d *Decoder, out []float32) {
							src := append([]float32(nil), interleaved...)
							x1 := src
							if channels == 2 {
								x1 = src[1:]
							}
							d.deemphasis(out, src, x1, channels, n, downsample, accum)
						})
						right := planes[0]
						if channels == 2 {
							right = planes[1]
						}
						run("planar", func(d *Decoder, out []float32) {
							d.deemphasis(out, planes[0], right, 1, n, downsample, accum)
						})
						if downsample == 1 && !accum {
							run("in-place", func(d *Decoder, out []float32) {
								copy(out, interleaved)
								if got := d.deemphasisInterleaved(out, n); len(got) != len(out) {
									t.Fatalf("in-place len=%d want %d", len(got), len(out))
								}
							})
						}
					})
				}
			}
		}
	}
}

func TestDeemphasisSilenceTransitionsAndDownsampleStateMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels=%d/transition", channels), func(t *testing.T) {
			dec := NewDecoder(channels)
			var oracleMem = make([]float32, channels)
			for _, segment := range []struct {
				name    string
				length  int
				silence bool
			}{{"initial-silence", 17, true}, {"signal", 67, false}, {"silence-with-state", 23, true}, {"signal-again", 72, false}} {
				t.Run(segment.name, func(t *testing.T) {
					planes := make([][]float32, channels)
					for ch := range channels {
						planes[ch] = make([]float32, segment.length)
						if !segment.silence {
							for i := range segment.length {
								planes[ch][i] = float32(math.Sin(float64(i+2+ch*7)*0.137)*1800 + math.Cos(float64(i+5+ch*11)*0.191)*900)
							}
						}
					}
					want := probeLibopusDeemphasis(t, channels, planes, oracleMem)
					interleaved := interleaveDeemphasisPlanes(planes)
					dec.deemphasisInterleaved(interleaved, segment.length)
					assertCELTFilterFloat32Bits(t, "pcm", interleaved, want.pcm)
					assertCELTFilterMemBits(t, dec, want.mem)
					copy(oracleMem, want.mem)
				})
			}
		})

		t.Run(fmt.Sprintf("channels=%d/downsample-state", channels), func(t *testing.T) {
			const (
				n          = 30
				downsample = 3
				outFrames  = n / downsample
			)
			planes := make([][]float32, channels)
			for ch := range channels {
				planes[ch] = make([]float32, n)
				for i := range n {
					planes[ch][i] = float32(math.Sin(float64(i+3+ch*5)*0.089)*950 + math.Cos(float64(i+1+ch*13)*0.127)*430)
				}
			}
			initialMem := make([]float32, channels)
			for ch := range channels {
				initialMem[ch] = float32(41.25 - float64(ch)*83.5)
			}
			want := probeLibopusDeemphasisWithOptions(t, channels, planes, initialMem, downsample, nil)
			dec := NewDecoder(channels)
			copy(dec.preemphState[:], initialMem)
			got := make([]float32, outFrames*channels)
			interleaved := interleaveDeemphasisPlanes(planes)
			dec.deemphasis(got, interleaved, interleaved[channels-1:], channels, n, downsample, false)
			assertCELTFilterFloat32Bits(t, "downsample pcm", got, want.pcm)
			assertCELTFilterMemBits(t, dec, want.mem)

			accum := make([]float32, len(got))
			for i := range accum {
				accum[i] = float32(0.125*float64(i+1) - 0.75)
			}
			wantAccum := probeLibopusDeemphasisWithOptions(t, channels, planes, initialMem, downsample, accum)
			dec = NewDecoder(channels)
			copy(dec.preemphState[:], initialMem)
			gotAccum := make([]float32, len(accum))
			copy(gotAccum, accum)
			dec.deemphasis(gotAccum, interleaved, interleaved[channels-1:], channels, n, downsample, true)
			assertCELTFilterFloat32Bits(t, "downsample accumulated pcm", gotAccum, wantAccum.pcm)
			assertCELTFilterMemBits(t, dec, wantAccum.mem)
		})
	}
}

func interleaveDeemphasisPlanes(planes [][]float32) []float32 {
	n := len(planes[0])
	interleaved := make([]float32, n*len(planes))
	for i := range n {
		for ch := range planes {
			interleaved[i*len(planes)+ch] = planes[ch][i]
		}
	}
	return interleaved
}

func assertCELTFilterFloat32Bits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func assertCELTFilterMemBits(t *testing.T, dec *Decoder, want []float32) {
	t.Helper()
	for i := range want {
		if math.Float32bits(dec.preemphState[i]) != math.Float32bits(want[i]) {
			t.Fatalf("mem[%d]=%08x want %08x", i, math.Float32bits(dec.preemphState[i]), math.Float32bits(want[i]))
		}
	}
}

func probeLibopusCombFilter(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window, buf []float32) []float32 {
	t.Helper()
	return probeLibopusCombFilterMode(t, libopusCELTFilterModeCombFilter, start, n, t0, t1, tapset0, tapset1, overlap, g0, g1, window, buf)
}

func probeLibopusCombFilterInput(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window, buf []float32) []float32 {
	t.Helper()
	return probeLibopusCombFilterMode(t, libopusCELTFilterModeCombFilterInput, start, n, t0, t1, tapset0, tapset1, overlap, g0, g1, window, buf)
}

func probeLibopusCombFilterMode(t *testing.T, mode uint32, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window, buf []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCFI", mode)
	payload.U32(uint32(start))
	payload.U32(uint32(n))
	payload.U32(uint32(t0))
	payload.U32(uint32(t1))
	payload.U32(uint32(tapset0))
	payload.U32(uint32(tapset1))
	payload.U32(uint32(overlap))
	payload.Float32(g0)
	payload.Float32(g1)
	for i := range overlap {
		payload.Float32(window[i])
	}
	for _, sample := range buf {
		payload.Float32(sample)
	}
	reader := runLibopusCELTFilter(t, payload, celtFilterOracleUsesSIMD(mode))
	if gotMode := reader.U32(); gotMode != mode {
		t.Fatalf("helper mode=%d want %d", gotMode, mode)
	}
	count := int(reader.U32())
	reader.ExpectRemaining(count * 4)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

// celtFilterOracleUsesSIMD pairs the comb-filter oracles with the libopus
// build for the Go tier. libopus routes both the postfilter and the
// prefilter/PLC comb through comb_filter(), whose constant-gain body x86 SIMD
// builds bind to comb_filter_const_sse; ARM builds have no NEON comb kernel, so
// their SIMD helper still compiles comb_filter_const_c.
func celtFilterOracleUsesSIMD(mode uint32) bool {
	isComb := mode == libopusCELTFilterModeCombFilter || mode == libopusCELTFilterModeCombFilterInput
	return isComb && (combUsesNeon || combUsesSSE)
}

func TestCELTFilterOracleDispatchIdentity(t *testing.T) {
	const n = 12
	dst := make([]float32, n)
	delay := make([]float32, n)
	_, _, _, _, dispatched := combFilterConstDispatch(dst, delay, 0.25, 0.5, 0.75, 0, 0, 0, 0)
	if dispatched != combUsesNeon {
		t.Fatalf("comb filter SIMD dispatch=%t, build selection=%t", dispatched, combUsesNeon)
	}
	// combFilterConstFloat32* and combFilterWithInputSig both follow
	// combUsesSSE, so both comb oracles pair with the SIMD reference exactly
	// when the Go build selects SSE or NEON.
	for _, mode := range []uint32{libopusCELTFilterModeCombFilter, libopusCELTFilterModeCombFilterInput} {
		if oracleSIMD := celtFilterOracleUsesSIMD(mode); oracleSIMD != (dispatched || combUsesSSE) {
			t.Fatalf("comb mode %d oracle SIMD=%t, Go SSE/NEON selection=%t", mode, oracleSIMD, dispatched || combUsesSSE)
		}
	}
	requirePairedCELTOracleMode(t)
}

func TestCombFilterWithSquareMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start   = combFilterHistory
		n       = 192
		t0      = 37
		t1      = 40
		overlap = Overlap
	)
	windowF32 := GetWindowBufferF32(overlap)
	windowSq := GetWindowSquareBufferF32(overlap)
	buf := make([]float32, start+n+2)
	for i := range buf {
		buf[i] = float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170)
	}
	want := probeLibopusCombFilter(t, start, n, t0, t1, 0, 0, overlap, 0.28125, 0.65625, windowF32, buf)

	got := append([]float32(nil), buf...)
	combFilterInPlace(got, start, t0, t1, n, 0.28125, 0.65625, 0, 0, windowSq, overlap)
	for i := range n {
		if math.Float32bits(got[start+i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample[%d]=%08x want %08x", i,
				math.Float32bits(got[start+i]), math.Float32bits(want[i]))
		}
	}
}

func TestCombFilterConstantBodyHistorySeamMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start = combFilterHistory
		n     = 192
		gain  = float32(0.65625)
	)
	window := GetWindowBufferF32(Overlap)
	windowSq := GetWindowSquareBufferF32(Overlap)
	buf := make([]float32, start+n+2)
	for i := range buf {
		buf[i] = float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170)
	}
	// Equal parameters suppress the overlap ramp. The constant body's delay
	// line moves from decoded history into filtered output at period-2, at
	// each residue modulo four.
	for _, period := range []int{73, 74, 75, 76, 117, 118, 119, 120} {
		t.Run(fmt.Sprintf("period=%d/seam_mod4=%d", period, (period-2)&3), func(t *testing.T) {
			want := probeLibopusCombFilter(t, start, n, period, period, 0, 0, Overlap, gain, gain, window, buf)
			got := append([]float32(nil), buf...)
			combFilterInPlace(got, start, period, period, n, gain, gain, 0, 0, windowSq, Overlap)
			for i := range n {
				if math.Float32bits(got[start+i]) != math.Float32bits(want[i]) {
					t.Fatalf("sample[%d]=%08x want %08x", i, math.Float32bits(got[start+i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}

func TestCombFilterRampedHistorySeamMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		history = combFilterHistory
		n       = 360
		overlap = Overlap
	)
	window := GetWindowBufferF32(overlap)
	windowSq := GetWindowSquareBufferF32(overlap)
	for _, frameOffset := range []int{0, 120} {
		t.Run(fmt.Sprintf("frame_offset=%d", frameOffset), func(t *testing.T) {
			start := history + frameOffset
			t1 := frameOffset + 247 // constant body reaches filtered output at sample 245
			t0 := t1 + 4
			buf := make([]float32, start+n+2)
			for i := range buf {
				buf[i] = float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170)
			}
			want := probeLibopusCombFilter(t, start, n, t0, t1, 0, 1, overlap,
				0.28125, 0.65625, window, buf)
			got := append([]float32(nil), buf...)
			combFilterInPlace(got, start, t0, t1, n, 0.28125, 0.65625, 0, 1, windowSq, overlap)
			for i := range n {
				if math.Float32bits(got[start+i]) != math.Float32bits(want[i]) {
					t.Fatalf("sample[%d]=%08x want %08x", i, math.Float32bits(got[start+i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}

func TestCombFilterWithInputF32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	window32 := GetWindowBufferF32(Overlap)
	cases := []struct {
		name      string
		seed      uint32
		n         int
		t0        int
		t1        int
		g0        float32
		g1        float32
		tapset0   int
		tapset1   int
		overlap   int
		useWindow bool
	}{
		{"changed_filter_overlap", 0xabc101, 480, 151, 143, 0.21875, 0.34375, 0, 1, Overlap, true},
		{"steady_filter_no_overlap", 0xabc102, 960, 320, 320, 0.4375, 0.4375, 2, 2, 0, false},
		{"g1_zero_tail_copy", 0xabc103, 240, 77, 97, 0.3125, 0, 1, 0, 120, true},
		{"short_frame", 0xabc104, 120, 47, 91, -0.125, 0.5625, 2, 0, 64, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := combFilterHistory
			srcSig := makeCELTPLCTestSignal(start+tc.n+2, tc.seed, 1.0)
			overlap := tc.overlap
			var win32 []float32
			if tc.useWindow {
				win32 = window32
			} else {
				overlap = 0
			}

			want := probeLibopusCombFilterInput(t, start, tc.n, tc.t0, tc.t1, tc.tapset0, tc.tapset1, overlap, tc.g0, tc.g1, win32, srcSig)
			got := append([]celtSig(nil), srcSig...)
			combFilterWithInputSig(got, srcSig, start, tc.t0, tc.t1, tc.n, tc.g0, tc.g1, tc.tapset0, tc.tapset1, win32, overlap)
			got32 := make([]float32, tc.n)
			copySigToFloat32(got32, got[start:start+tc.n])
			assertFloat32Bits(t, "comb", got32, want)
		})
	}
}

// Hybrid accumulation preserves both silent and tiny SILK lowbands while
// carrying CELT deemphasis memory through the same selected C operation.
func TestDeemphasisHybridLowbandEdgesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)
	for _, name := range []string{"zero-lowband", "tiny-both-bands"} {
		t.Run(name, func(t *testing.T) {
			const n = 120
			celtSignal := make([]float32, n)
			lowband := make([]float32, n)
			for i := range n {
				celtSignal[i] = float32(math.Sin(float64(i+3)*0.13) * 810)
				if name == "tiny-both-bands" {
					celtSignal[i] *= 1e-8
					lowband[i] = float32(math.Cos(float64(i+7)*0.07) * 1e-9)
				}
			}
			mem := []float32{0}
			want := probeLibopusDeemphasisWithOptions(t, 1, [][]float32{celtSignal}, mem, 1, lowband)
			dec := NewDecoder(1)
			dec.deemphasis(lowband, celtSignal, celtSignal, 1, n, 1, true)
			assertCELTFilterFloat32Bits(t, "hybrid lowband", lowband, want.pcm)
			assertCELTFilterMemBits(t, dec, want.mem)
		})
	}
}

const (
	libopusCELTPLCModeLPC             = uint32(0)
	libopusCELTPLCModeFIR             = uint32(1)
	libopusCELTPLCModeIIR             = uint32(2)
	libopusCELTPLCModePitchDownsample = uint32(3)
	libopusCELTPLCModePitchSearch     = uint32(4)
	libopusCELTPLCModeRemoveDoubling  = uint32(5)
	libopusCELTPLCModePeriodicConceal = uint32(6)
	libopusCELTPLCModeRawAutocorr     = uint32(7)
)

var libopusCELTPLCHelper libopustest.HelperCache

func buildLibopusCELTPLCHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "CELT PLC",
		OutputBase:  "gopus_libopus_celt_plc",
		SourceFile:  "libopus_celt_plc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return libopustest.BuildCHelper(cfg)
}

func runLibopusCELTPLC(t *testing.T, payload *libopustest.OraclePayload) *libopustest.OracleReader {
	t.Helper()
	binPath, err := libopusCELTPLCHelper.Path(buildLibopusCELTPLCHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT PLC", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT PLC", "GCPO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT PLC", err)
	}
	return reader
}

type libopusPLCLPCResult struct {
	lpc []float32
	ac  []float32
}

func probeLibopusPLCLPC(t *testing.T, frame []celtSig, window []float32) libopusPLCLPCResult {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeLPC)
	payload.U32(uint32(len(frame)))
	payload.U32(uint32(len(window)))
	for _, w := range window {
		payload.Float32(w)
	}
	for _, sample := range frame {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeLPC {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeLPC)
	}
	lpcCount := int(reader.U32())
	lpc := make([]float32, lpcCount)
	for i := range lpc {
		lpc[i] = reader.Float32()
	}
	acCount := int(reader.U32())
	ac := make([]float32, acCount)
	reader.ExpectRemaining(acCount * 4)
	for i := range ac {
		ac[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return libopusPLCLPCResult{lpc: lpc, ac: ac}
}

func probeLibopusPLCFIR(t *testing.T, exc []float32, start, length int, lpc []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeFIR)
	payload.U32(uint32(len(exc)))
	payload.U32(uint32(start))
	payload.U32(uint32(length))
	for _, coeff := range lpc {
		payload.Float32(coeff)
	}
	for _, sample := range exc {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeFIR {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeFIR)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func probeLibopusPLCIIR(t *testing.T, x []celtSig, hist []celtSig, lpc []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeIIR)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(len(hist)))
	for _, coeff := range lpc {
		payload.Float32(coeff)
	}
	for _, sample := range hist {
		payload.Float32(float32(sample))
	}
	for _, sample := range x {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeIIR {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeIIR)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func probeLibopusPLCPitchDownsample(t *testing.T, x []celtSig, length, channels, factor int) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePitchDownsample)
	payload.U32(uint32(channels))
	payload.U32(uint32(length))
	payload.U32(uint32(factor))
	for _, sample := range x {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePitchDownsample {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePitchDownsample)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func probeLibopusRawAutocorr(t *testing.T, x []celtSig, lag int, window []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeRawAutocorr)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(lag))
	payload.U32(uint32(len(window)))
	for _, w := range window {
		payload.Float32(w)
	}
	for _, sample := range x {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeRawAutocorr {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeRawAutocorr)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func requireScalarCELTReference(t *testing.T) {
	t.Helper()
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatalf("resolve paired libopus reference: %v", err)
	}
	if variant != libopustooling.LibopusReferenceScalar {
		t.Skipf("scalar autocorrelation probe requires the scalar libopus pairing; selected %s", variant)
	}
}

func TestPitchAutocorrTrailingLagMatchesLibopus(t *testing.T) {
	requireScalarCELTReference(t)
	libopustest.RequireOracle(t)

	const length = plcDecodeBufferSize >> 1
	xLP := makeCELTPLCTestSignal(length, 0x52accc04, 2600)
	want := probeLibopusRawAutocorr(t, xLP, 4, nil)
	var got [5]float32
	pitchAutocorr5F32(xLP, length, &got)
	for lag, wantValue := range want {
		if gotBits := math.Float32bits(got[lag]); gotBits != math.Float32bits(wantValue) {
			t.Fatalf("raw autocorr lag %d=%08x %.10g want %08x %.10g", lag,
				gotBits, got[lag], math.Float32bits(wantValue), wantValue)
		}
	}
}

func TestPLCAutocorrStagesMatchLibopusBits(t *testing.T) {
	requireScalarCELTReference(t)
	libopustest.RequireOracle(t)

	frame := makeCELTPLCTestSignal(combFilterMaxPeriod, 0x42504c43, 1800)
	window := GetWindowBufferF32(Overlap)

	wantRaw := probeLibopusRawAutocorr(t, frame, celtPLCLPCOrder, window)
	d := NewDecoder(1)
	var gotRaw [celtPLCLPCOrder + 1]float32
	d.computePLCRawAutocorr(frame, window, gotRaw[:])
	assertFloat32BitExact(t, "raw autocorr", gotRaw[:], wantRaw)

	want := probeLibopusPLCLPC(t, frame, window)
	gotWindowed := gotRaw
	applyCELTPLCLagWindow32(gotWindowed[:], celtPLCLPCOrder)
	assertFloat32BitExact(t, "lag-windowed autocorr", gotWindowed[:], want.ac)
	gotLPC := make([]float32, celtPLCLPCOrder)
	plcLPCFromAutocorr(gotWindowed[:], gotLPC)
	assertFloat32BitExact(t, "LPC coefficients", gotLPC, want.lpc)
}

func TestPeriodicPLCSynthesisStagesMatchLibopusBits(t *testing.T) {
	requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "frame_120_stereo", frameSize: 120, channels: 2},
		{name: "frame_960_mono", frameSize: 960, channels: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hist := makeCELTPLCTestSignal(plcDecodeBufferSize*tc.channels, 0x9000+uint32(tc.frameSize)+uint32(tc.channels), 2400)
			periodic := probeLibopusPLCPeriodicConceal(t, hist, tc.channels, tc.frameSize)
			excLength := min(2*periodic.period, combFilterMaxPeriod)
			firStart := celtPLCLPCOrder + combFilterMaxPeriod - excLength
			window := GetWindowBufferF32(Overlap)
			for ch := range tc.channels {
				channel := hist[ch*plcDecodeBufferSize : (ch+1)*plcDecodeBufferSize]
				frame := channel[plcDecodeBufferSize-combFilterMaxPeriod:]
				wantLPC := probeLibopusPLCLPC(t, frame, window).lpc
				gotLPC := make([]float32, celtPLCLPCOrder)
				NewDecoder(1).computePLCLPC(frame, gotLPC, window)
				assertFloat32BitExact(t, "lpc/ch"+itoaChN(ch), gotLPC, wantLPC)

				exc := channel[plcDecodeBufferSize-combFilterMaxPeriod-celtPLCLPCOrder:]
				excAfterFIR := append([]celtSig(nil), exc...)
				exc32 := make([]float32, len(exc))
				copySigToFloat32(exc32, exc)
				wantFIR := probeLibopusPLCFIR(t, exc32, firStart, excLength, wantLPC)
				gotFIR := make([]celtSig, excLength)
				celtFIRFloat32(gotFIR, exc, firStart, excLength, wantLPC)
				copy(excAfterFIR[firStart:firStart+excLength], gotFIR)
				gotFIR32 := make([]float32, len(gotFIR))
				copySigToFloat32(gotFIR32, gotFIR)
				assertFloat32BitExact(t, "fir/ch"+itoaChN(ch), gotFIR32, wantFIR)

				decayLength := excLength >> 1
				energy1, energy2 := float32(1), float32(1)
				base1 := celtPLCLPCOrder + combFilterMaxPeriod - decayLength
				base2 := celtPLCLPCOrder + combFilterMaxPeriod - 2*decayLength
				energy1 = periodicPLCDecayEnergy(energy1, excAfterFIR[base1:base1+decayLength])
				energy2 = periodicPLCDecayEnergy(energy2, excAfterFIR[base2:base2+decayLength])
				if energy1 > energy2 {
					energy1 = energy2
				}
				assertFloat32BitExact(t, "energy1/ch"+itoaChN(ch), []float32{energy1}, []float32{periodic.energy1[ch]})
				assertFloat32BitExact(t, "energy2/ch"+itoaChN(ch), []float32{energy2}, []float32{periodic.energy2[ch]})
				decay := opusmath.SqrtF32(energy1 / energy2)
				assertFloat32BitExact(t, "decay/ch"+itoaChN(ch), []float32{decay}, []float32{periodic.decay[ch]})
			}
		})
	}
}

func TestDecodedSeedPeriodicPLCMatchesLibopusBits(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	packet, err := hex.DecodeString(seedCELTMonoPacketHex)
	if err != nil {
		t.Fatal(err)
	}
	dec := NewDecoder(1)
	if err := dec.SetAPISampleRate(48000); err != nil {
		t.Fatal(err)
	}
	dec.SetBandwidth(CELTFullband)
	if _, err := dec.DecodeFrame(packet[1:], 960); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	hist := append([]celtSig(nil), dec.decodeMemChannel(0)[:plcDecodeBufferSize]...)
	want := probeLibopusPLCPeriodicConceal(t, hist, 1, 960)

	got := make([]float32, 960+Overlap)
	if !dec.concealPeriodicPLCWithLimit(got, 960, 1, false, false, true) {
		t.Fatal("Go periodic PLC declined the seeded history")
	}
	if dec.plcLastPitchPeriod != int32(want.period) {
		t.Fatalf("pitch period=%d want %d", dec.plcLastPitchPeriod, want.period)
	}
	assertFloat32BitExact(t, "decoded seed periodic PLC", got, want.out[0])
}

func probeLibopusPLCPitchSearch(t *testing.T, xLP, y []float32, length, maxPitch int) int {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePitchSearch)
	payload.U32(uint32(length))
	payload.U32(uint32(maxPitch))
	for _, sample := range xLP[:length] {
		payload.Float32(sample)
	}
	for _, sample := range y[:length+maxPitch] {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePitchSearch {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePitchSearch)
	}
	out := int(int32(reader.U32()))
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func probeLibopusRemoveDoubling(t *testing.T, x []float32, maxPeriod, minPeriod, n, t0, prevPeriod int, prevGain float32) (int, float32) {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeRemoveDoubling)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(maxPeriod))
	payload.U32(uint32(minPeriod))
	payload.U32(uint32(n))
	payload.U32(uint32(int32(t0)))
	payload.U32(uint32(prevPeriod))
	payload.Float32(prevGain)
	for _, sample := range x {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeRemoveDoubling {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeRemoveDoubling)
	}
	outT0 := int(int32(reader.U32()))
	gain := reader.Float32()
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return outT0, gain
}

type libopusPLCPeriodicConcealResult struct {
	period  int
	out     [][]float32
	energy1 []float32
	energy2 []float32
	decay   []float32
}

func probeLibopusPLCPeriodicConceal(t *testing.T, hist []celtSig, channels, frameSize int) libopusPLCPeriodicConcealResult {
	return probeLibopusPLCPeriodicConcealWithContinuation(t, hist, channels, frameSize, false, 0)
}

func probeLibopusPLCPeriodicConcealWithContinuation(t *testing.T, hist []celtSig, channels, frameSize int, continuePeriodic bool, lastPitchPeriod int) libopusPLCPeriodicConcealResult {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePeriodicConceal)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(Overlap))
	if continuePeriodic {
		payload.U32(1)
	} else {
		payload.U32(0)
	}
	payload.U32(uint32(lastPitchPeriod))
	for _, w := range GetWindowBufferF32(Overlap) {
		payload.Float32(w)
	}
	for _, sample := range hist {
		payload.Float32(float32(sample))
	}
	if continuePeriodic {
		for range channels * celtPLCLPCOrder {
			payload.Float32(0)
		}
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePeriodicConceal {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePeriodicConceal)
	}
	period := int(reader.U32())
	count := int(reader.U32())
	out := make([][]float32, channels)
	energy1 := make([]float32, channels)
	energy2 := make([]float32, channels)
	decay := make([]float32, channels)
	reader.ExpectRemaining(channels * (count*4 + 12))
	for ch := range channels {
		out[ch] = make([]float32, count)
		for i := range out[ch] {
			out[ch][i] = reader.Float32()
		}
	}
	for ch := range channels {
		energy1[ch] = reader.Float32()
		energy2[ch] = reader.Float32()
		decay[ch] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return libopusPLCPeriodicConcealResult{period: period, out: out, energy1: energy1, energy2: energy2, decay: decay}
}

func TestPeriodicPLCEnergyMatchesLibopusVectorRemainders(t *testing.T) {
	requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)

	for _, period := range []int{101, 102, 103, 104} {
		t.Run(strconv.Itoa(period), func(t *testing.T) {
			hist := make([]celtSig, plcDecodeBufferSize)
			firstPeriod := plcDecodeBufferSize - 2*period
			secondPeriod := plcDecodeBufferSize - period
			for i := firstPeriod; i < secondPeriod; i++ {
				hist[i] = celtSig(2)
			}
			hist[plcDecodeBufferSize-1] = celtSig(math.Float32frombits(0x3f000025))

			want := probeLibopusPLCPeriodicConcealWithContinuation(t, hist, 1, 120, true, period)
			if want.period != period {
				t.Fatalf("libopus period=%d want %d", want.period, period)
			}
			decayLength := min(2*period, combFilterMaxPeriod) >> 1
			exc := hist[plcDecodeBufferSize-combFilterMaxPeriod-celtPLCLPCOrder:]
			base1 := celtPLCLPCOrder + combFilterMaxPeriod - decayLength
			base2 := celtPLCLPCOrder + combFilterMaxPeriod - 2*decayLength
			gotE1 := periodicPLCDecayEnergy(1, exc[base1:base1+decayLength])
			gotE2 := periodicPLCDecayEnergy(1, exc[base2:base2+decayLength])
			assertFloat32BitExact(t, "raw E1", []float32{gotE1}, want.energy1)
			assertFloat32BitExact(t, "raw E2", []float32{gotE2}, want.energy2)

			if libopusFloatInnerProdUsesNeonOrder && period < 104 {
				if gotBits := math.Float32bits(want.energy1[0]); gotBits != 0x3fa00013 {
					t.Fatalf("paired arm64 SIMD libopus raw E1 bits=0x%08x want 0x3fa00013", gotBits)
				}
			}
			wantE2 := float32(1 + 4*period)
			if got := want.energy2[0]; got != wantE2 {
				t.Fatalf("libopus raw E2=%g want %g", got, wantE2)
			}
		})
	}
}

func readCELTPLCFloat32Vector(t *testing.T, reader *libopustest.OracleReader) []float32 {
	t.Helper()
	count := int(reader.U32())
	reader.ExpectRemaining(count * 4)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConcealPeriodicPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "frame_120_mono", frameSize: 120, channels: 1},
		{name: "frame_120_stereo", frameSize: 120, channels: 2},
		{name: "frame_240_mono", frameSize: 240, channels: 1},
		{name: "frame_480_stereo", frameSize: 480, channels: 2},
		{name: "frame_960_mono", frameSize: 960, channels: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hist := makeCELTPLCTestSignal(plcDecodeBufferSize*tc.channels, 0x9000+uint32(tc.frameSize)+uint32(tc.channels), 2400)
			want := probeLibopusPLCPeriodicConceal(t, hist, tc.channels, tc.frameSize)
			excLength := min(2*want.period, combFilterMaxPeriod)
			t.Logf("libopus period=%d excitation_length=%d decay_length=%d", want.period, excLength, excLength>>1)

			dec := NewDecoder(tc.channels)
			dec.setDecodeHistory(hist)
			dec.plcLPC = make([]float32, celtPLCLPCOrder*tc.channels)
			gotInterleaved := make([]float32, (tc.frameSize+Overlap)*tc.channels)
			if !dec.concealPeriodicPLC(gotInterleaved, tc.frameSize, 1, false, false) {
				t.Fatal("concealPeriodicPLC returned false")
			}
			if int(dec.plcLastPitchPeriod) != want.period {
				t.Fatalf("period=%d want libopus %d", dec.plcLastPitchPeriod, want.period)
			}
			for ch := 0; ch < tc.channels; ch++ {
				for i, wantSample := range want.out[ch] {
					got := float32(gotInterleaved[i*tc.channels+ch])
					if math.Float32bits(got) != math.Float32bits(wantSample) {
						t.Fatalf("ch=%d out[%d]=%08x %.10g want %08x %.10g",
							ch, i,
							math.Float32bits(got), got,
							math.Float32bits(wantSample), wantSample)
					}
				}
			}
		})
	}
}

func TestPitchDownsampleSigMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	for _, factor := range []int{2, 4} {
		for _, channels := range []int{1, 2} {
			// The 2x cases keep the original subtest names the SIMD A/B gate
			// tracks; other factors add a prefix.
			name := map[int]string{1: "mono", 2: "stereo"}[channels]
			if factor != 2 {
				name = strconv.Itoa(factor) + "x/" + name
			}
			t.Run(name, func(t *testing.T) {
				const length = plcDecodeBufferSize >> 1
				x := makeCELTPLCTestSignal(length*factor*channels, uint32(0x5100+factor*10+channels), 2600)
				want := probeLibopusPLCPitchDownsample(t, x, length, channels, factor)

				got := make([]float32, length)
				pitchDownsampleSig(x, got, length, channels, factor)
				assertFloat32Bits(t, "xLP", got, want)
			})
		}
	}

	t.Run("short_fir_head_and_tails", func(t *testing.T) {
		// pitch_downsample calls _celt_autocorr with lag=4. That helper calls
		// celt_pitch_xcorr with fastN=length-4, whose public C implementation
		// requires fastN>=3.
		for length := 7; length <= 16; length++ {
			for _, channels := range []int{1, 2} {
				t.Run("len"+strconv.Itoa(length)+"/channels"+strconv.Itoa(channels), func(t *testing.T) {
					x := makeCELTPLCTestSignal(length*2*channels,
						uint32(0x5f150000+length*4+channels), 2600)
					want := probeLibopusPLCPitchDownsample(t, x, length, channels, 2)
					got := make([]float32, length)
					pitchDownsampleSig(x, got, length, channels, 2)
					assertFloat32Bits(t, "short xLP", got, want)
				})
			}
		}
	})
}

func TestPitchDownsampleFloatInputMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			const length = (combFilterMaxPeriod + 480) >> 1
			xSig := makeCELTPLCTestSignal(length*2*channels, uint32(0x6100+channels), 2100)
			x := make([]float64, len(xSig))
			for i := range xSig {
				x[i] = float64(xSig[i])
			}
			want := probeLibopusPLCPitchDownsample(t, xSig, length, channels, 2)

			got := make([]float32, length)
			pitchDownsample(x, got, length, channels, 2)
			assertFloat32Bits(t, "xLP", got, want)
		})
	}
}

func TestPitchSearchPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		length   = plcDecodeBufferSize - 720
		maxPitch = 720 - 100
	)
	for _, factor := range []int{2, 4} {
		t.Run(strconv.Itoa(factor)+"x", func(t *testing.T) {
			lp := make([]float32, plcDecodeBufferSize>>1)
			src := makeCELTPLCTestSignal(len(lp)*factor, uint32(0x71ab23cd+factor), 1.0)
			pitchDownsampleSig(src, lp, len(lp), 1, factor)
			y := make([]float32, length+maxPitch)
			copy(y, lp)
			xLP := make([]float32, length)
			copy(xLP, lp[720/2:])

			var scratch plcPitchSearchScratch
			scratch.xcorr = make([]float32, maxPitch>>1)
			for i := range scratch.xcorr {
				scratch.xcorr[i] = float32(math.NaN())
			}
			got := pitchSearchPLC(xLP, y, length, maxPitch, &scratch)
			want := probeLibopusPLCPitchSearch(t, xLP, y, length, maxPitch)
			if got != want {
				t.Fatalf("pitch search result=%d want %d", got, want)
			}
		})
	}
}

func TestRemoveDoublingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	cases := []struct {
		name       string
		seed       uint32
		maxPeriod  int
		minPeriod  int
		n          int
		t0         int
		prevPeriod int
		prevGain   float32
	}{
		{"fullband_low_prev", 0x9201, combFilterMaxPeriod, combFilterMinPeriod, 960, 231, 197, 0.375},
		{"medium_continuity", 0x9202, 640, 30, 480, 188, 190, 0.5625},
		{"short_period", 0x9203, 320, combFilterMinPeriod, 240, 77, 80, 0.21875},
		{"wide_search", 0x9204, 900, 40, 720, 415, 300, 0.6875},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xSig := makeCELTPLCTestSignal(tc.maxPeriod+tc.n, tc.seed, 1.0)
			x := make([]float32, len(xSig))
			for i := range xSig {
				x[i] = float32(xSig[i])
			}
			wantT0, wantGain := probeLibopusRemoveDoubling(t, x, tc.maxPeriod, tc.minPeriod, tc.n, tc.t0, tc.prevPeriod, tc.prevGain)
			gotT0 := tc.t0
			var scratch encoderScratch
			gotGain := removeDoubling(x, tc.maxPeriod, tc.minPeriod, tc.n, &gotT0, tc.prevPeriod, tc.prevGain, &scratch)
			if gotT0 != wantT0 {
				t.Fatalf("pitch=%d want %d", gotT0, wantT0)
			}
			if math.Float32bits(gotGain) != math.Float32bits(wantGain) {
				t.Fatalf("gain=%08x %0.10g want %08x %0.10g",
					math.Float32bits(gotGain), gotGain,
					math.Float32bits(wantGain), wantGain)
			}
		})
	}
}

func TestComputePLCLPCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name  string
		seed  uint32
		scale float32
	}{
		{name: "baseline", seed: 0x42504c43, scale: 1800},
		{name: "plc_frame_120_stereo_ch0", seed: 0x9000 + 120 + 2, scale: 2400},
		{name: "plc_frame_240_stereo_ch0", seed: 0x9000 + 240 + 2, scale: 2400},
		{name: "plc_frame_480_mono", seed: 0x9000 + 480 + 1, scale: 2400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hist := makeCELTPLCTestSignal(plcDecodeBufferSize, tc.seed, tc.scale)
			frame := hist[plcDecodeBufferSize-combFilterMaxPeriod:]
			window32 := GetWindowBufferF32(Overlap)
			want := probeLibopusPLCLPC(t, frame, window32)

			dec := NewDecoder(1)
			gotAC := make([]float32, celtPLCLPCOrder+1)
			dec.computePLCAutocorr(frame, window32, gotAC)
			assertFloat32Bits(t, "ac", gotAC, want.ac)

			got := make([]float32, celtPLCLPCOrder)
			dec.computePLCLPC(frame, got, window32)
			assertFloat32Bits(t, "lpc", got, want.lpc)
		})
	}
}

func TestCELTPLCFIRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	exc := makeCELTPLCTestSignal(combFilterMaxPeriod+celtPLCLPCOrder, 0xf17f1e, 1400)
	exc32 := make([]float32, len(exc))
	for i := range exc {
		exc32[i] = float32(exc[i])
	}
	lpc := makeCELTPLPCTestCoeffs()
	for _, length := range []int{200, 230, 286, 320, 360, 600, 720, 1024} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			start := celtPLCLPCOrder + combFilterMaxPeriod - length
			want := probeLibopusPLCFIR(t, exc32, start, length, lpc)

			gotSig := make([]celtSig, length)
			celtFIRFloat32(gotSig, exc32, start, length, lpc)
			got := make([]float32, len(gotSig))
			copySigToFloat32(got, gotSig)
			assertFloat32Bits(t, "fir", got, want)
			if allocs := testing.AllocsPerRun(100, func() {
				celtFIRFloat32(gotSig, exc32, start, length, lpc)
			}); allocs != 0 {
				t.Fatalf("celtFIRFloat32 allocs/run=%g want 0", allocs)
			}
		})
	}
}

func TestCELTPLCIIRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	hist := makeCELTPLCTestSignal(plcDecodeBufferSize, 0x11a5011, 1300)
	lpc := makeCELTPLPCTestCoeffs()
	for _, length := range []int{240, 360, 600, 1080, 2160} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			in := makeCELTPLCTestSignal(length, 0x119911+uint32(length), 900)
			want := probeLibopusPLCIIR(t, in, hist, lpc)

			dec := NewDecoder(1)
			gotSig := append([]celtSig(nil), in...)
			dec.celtIIRFloat32(gotSig, hist, lpc, len(gotSig))
			got := make([]float32, len(gotSig))
			copySigToFloat32(got, gotSig)
			assertFloat32Bits(t, "iir", got, want)
			if allocs := testing.AllocsPerRun(100, func() {
				dec.celtIIRFloat32(gotSig, hist, lpc, len(gotSig))
			}); allocs != 0 {
				t.Fatalf("celtIIRFloat32 allocs/run=%g want 0", allocs)
			}
		})
	}
}

func TestCELTPLCFIRActualPeriodicPLCInputsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "frame_120_stereo_ch0", frameSize: 120, channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			histAll := makeCELTPLCTestSignal(plcDecodeBufferSize*tc.channels, 0x9000+uint32(tc.frameSize)+uint32(tc.channels), 2400)
			hist := histAll[:plcDecodeBufferSize]

			dec := NewDecoder(tc.channels)
			dec.setDecodeHistory(histAll)
			period := dec.searchPLCPitchPeriod()
			if period <= 0 {
				t.Fatal("no PLC pitch period")
			}

			lpc := make([]float32, celtPLCLPCOrder)
			dec.computePLCLPC(hist[plcDecodeBufferSize-combFilterMaxPeriod:], lpc, GetWindowBufferF32(Overlap))

			const maxPeriod = combFilterMaxPeriod
			excLength := min(2*period, maxPeriod)
			exc := makeCELTPLCTestSignal(0, 0, 0)
			exc = append(exc, hist[plcDecodeBufferSize-maxPeriod-celtPLCLPCOrder:]...)
			exc32 := make([]float32, len(exc))
			for i := range exc {
				exc32[i] = float32(exc[i])
			}
			firStart := celtPLCLPCOrder + maxPeriod - excLength
			wantFIR := probeLibopusPLCFIR(t, exc32, firStart, excLength, lpc)
			firTmp := make([]celtSig, excLength)
			celtFIRFloat32(firTmp, exc32, firStart, excLength, lpc)
			gotFIR := make([]float32, len(firTmp))
			copySigToFloat32(gotFIR, firTmp)
			assertFloat32Bits(t, "actual periodic fir", gotFIR, wantFIR)
		})
	}
}

func makeCELTPLCTestSignal(n int, seed uint32, scale float32) []celtSig {
	out := make([]celtSig, n)
	state := seed
	for i := range out {
		state = 1664525*state + 1013904223
		centered := float32(int32(state>>9)-int32(1<<22)) / float32(1<<22)
		wave := float32(math.Sin(float64(i+3)*0.037))*0.41 + float32(math.Cos(float64(i+7)*0.019))*0.23
		out[i] = celtSig((centered*0.36 + wave) * scale)
	}
	return out
}

func makeCELTPLPCTestCoeffs() []float32 {
	coeffs := make([]float32, celtPLCLPCOrder)
	for i := range coeffs {
		sign := float32(1)
		if i%2 != 0 {
			sign = -1
		}
		coeffs[i] = sign * float32(0.018/(1+float64(i)))
	}
	return coeffs
}

const selectedAutocorrProjectionStream5Packet = "f47e514de2f9194f4026d0a4b5842deb193aac7ed9983382a85471106bf1dded268f39daeec89ea9"

var selectedPLCReadAutocorrHelper libopustest.HelperCache

func selectedPLCReadAutocorrConfig() libopustest.CHelperConfig {
	cfg := libopustest.CHelperConfig{
		Label:       "selected CELT raw autocorrelation",
		OutputBase:  "gopus_selected_celt_raw_autocorr",
		SourceFile:  "libopus_celt_plc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return cfg
}

func buildSelectedPLCReadAutocorrHelper() (string, error) {
	return libopustest.BuildCHelper(selectedPLCReadAutocorrConfig())
}

func probeSelectedPLCRawAutocorr(t *testing.T, x []celtSig, lag int) []float32 {
	t.Helper()
	binPath, err := selectedPLCReadAutocorrHelper.Path(buildSelectedPLCReadAutocorrHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected CELT raw autocorrelation", err)
	}
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeRawAutocorr)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(lag))
	payload.U32(0)
	for _, sample := range x {
		payload.Float32(float32(sample))
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "selected CELT raw autocorrelation", "GCPO")
	if err != nil {
		libopustest.HelperUnavailable(t, "selected CELT raw autocorrelation", err)
	}
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeRawAutocorr {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeRawAutocorr)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func selectedPLCRawAutocorrWindowedInput(frame []celtSig, window []float32, overlap int) []celtSig {
	x := append([]celtSig(nil), frame...)
	for i := 0; i < overlap && i < len(window) && i < len(x)/2; i++ {
		w := float32(window[i])
		x[i] = celtSig(float32(x[i]) * w)
		x[len(x)-1-i] = celtSig(float32(x[len(x)-1-i]) * w)
	}
	return x
}

func assertSelectedPLCACEqual(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x %.10g, selected C=%08x %.10g", label, i,
				math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}

func makeSelectedPLCTailSignal(seed uint32) []celtSig {
	x := make([]celtSig, celtPLCLPCOrder+1)
	state := seed
	for i := 1; i < len(x); i++ {
		state = 1664525*state + 1013904223
		exponent := uint32(120) + ((state >> 25) & 15)
		bits := exponent<<23 | (state & 0x007fffff)
		if state&0x100 != 0 {
			bits |= 0x80000000
		}
		x[i] = celtSig(math.Float32frombits(bits))
	}
	return x
}

func TestSelectedSIMDPLCRawAutocorrMatchesArchive(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("selected ARM64 archive witness")
	}
	cfg := selectedPLCReadAutocorrConfig()
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if cfg.DREDQEXTRef {
		variant, err = libopustooling.ResolveLibopusDREDQEXTReferenceVariant()
	} else if cfg.QEXTRef {
		variant, err = libopustooling.ResolveLibopusQEXTReferenceVariant()
	}
	if err != nil {
		t.Fatalf("resolve paired libopus archive: %v", err)
	}
	if variant != libopustooling.LibopusReferenceSIMD &&
		variant != libopustooling.LibopusReferenceQEXTSIMD &&
		variant != libopustooling.LibopusReferenceDREDQEXTSIMD {
		t.Skipf("selected SIMD archive witness requires the SIMD Go lane, got %s", variant)
	}
	libopustest.RequireOracle(t)
	t.Logf("calling _celt_autocorr from selected %s libopus archive", variant)

	t.Run("projection_stream5_public_history", func(t *testing.T) {
		packet, err := hex.DecodeString(selectedAutocorrProjectionStream5Packet)
		if err != nil {
			t.Fatal(err)
		}
		dec := NewDecoder(2)
		if _, err := dec.DecodeFrame(packet[1:], 480); err != nil {
			t.Fatalf("decode projection stream 5 CELT seed: %v", err)
		}
		decodeBufferSize := dec.plcDecodeBufferLen()
		maxPeriod := dec.plcCombFilterMaxPeriod()
		window := dec.scratchIMDCTF32.modeWindow(Overlap)
		for ch := range int(dec.channels) {
			hist := dec.decodeMemChannel(ch)[:decodeBufferSize]
			exc := hist[decodeBufferSize-maxPeriod-celtPLCLPCOrder:]
			frame := exc[celtPLCLPCOrder:]
			windowed := selectedPLCRawAutocorrWindowedInput(frame, window, dec.synthOverlapLen())

			var got [celtPLCLPCOrder + 1]float32
			dec.computePLCRawAutocorr(windowed, nil, got[:])
			want := probeSelectedPLCRawAutocorr(t, windowed, celtPLCLPCOrder)
			assertSelectedPLCACEqual(t, "raw autocorrelation", got[:], want)
			dec.computePLCRawAutocorr(windowed, nil, got[:])
			if allocs := testing.AllocsPerRun(100, func() {
				dec.computePLCRawAutocorr(windowed, nil, got[:])
			}); allocs != 0 {
				t.Fatalf("warmed raw autocorrelation allocs/run=%g, want 0", allocs)
			}
		}
	})

	t.Run("remainder_patterns", func(t *testing.T) {
		dec := NewDecoder(1)
		for _, tc := range []struct {
			name  string
			seed  uint32
			scale float32
		}{
			{name: "midrange_a", seed: 0x4a91, scale: 1800},
			{name: "midrange_b", seed: 0xa535, scale: 2600},
			{name: "highrange", seed: 0x7777, scale: 7200},
			{name: "lowrange", seed: 0x19d3, scale: 140},
		} {
			t.Run(tc.name, func(t *testing.T) {
				x := makeCELTPLCTestSignal(1024, tc.seed, tc.scale)
				var got [celtPLCLPCOrder + 1]float32
				dec.computePLCRawAutocorr(x, nil, got[:])
				want := probeSelectedPLCRawAutocorr(t, x, celtPLCLPCOrder)
				assertSelectedPLCACEqual(t, "raw autocorrelation", got[:], want)
			})
		}
	})

	t.Run("isolated_tail_lengths", func(t *testing.T) {
		// With n=order+1 and x[0]=0, the vector prefix contributes zero. Each
		// output lag then exercises only the C tail of length order-lag.
		dec := NewDecoder(1)
		for _, seed := range []uint32{0x143e, 0x9c72, 0x15a91, 0xb734e} {
			t.Run(strconv.FormatUint(uint64(seed), 16), func(t *testing.T) {
				x := makeSelectedPLCTailSignal(seed)
				var got [celtPLCLPCOrder + 1]float32
				dec.computePLCRawAutocorr(x, nil, got[:])
				want := probeSelectedPLCRawAutocorr(t, x, celtPLCLPCOrder)
				assertSelectedPLCACEqual(t, "isolated raw autocorrelation tails", got[:], want)
			})
		}
	})
}

var pvqGridHelper libopustest.HelperCache

// pvqGridHelperPath returns the path to the CELT encode helper that supports
// the GCGI/GCGO grid protocol (libopus_celt_encode_info.c).
func pvqGridHelperPath() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "celt pvq grid",
		OutputBase:  "gopus_libopus_celt_pvq_grid",
		SourceFile:  "libopus_celt_encode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return pvqGridHelper.CHelperPath(cfg)
}

// pvqGridBandwidth describes one bandwidth dimension of the grid.
type pvqGridBandwidth struct {
	label   string
	bw      CELTBandwidth
	endBand uint32 // CELT_SET_END_BAND value (libopus src/opus_encoder.c)
}

// pvqGridSignal describes one signal class exercised in the grid.
type pvqGridSignal struct {
	label string
	pcmFn func(channels, frameSize int) []float32
}

// pvqGridCase is one cell in the exhaustive PVQ/bands grid.
type pvqGridCase struct {
	label     string
	channels  int
	frameSize int    // samples at 48 kHz
	bitrate   int32  // bits per second
	endBand   uint32 // CELT_SET_END_BAND
	bw        CELTBandwidth
	signal    string
	pcm       []float32
}

// pvqGridBandwidths lists all five CELT bandwidths with the end_band the gopus
// standalone CELT encoder uses for each (CELTBandwidth.EffectiveBands), so the
// libopus oracle is driven with the same coded-band count.
//
//	NB  → 13  (4 kHz)
//	MB  → 15  (CELTMediumband native CELT end_band)
//	WB  → 17  (8 kHz)
//	SWB → 19  (12 kHz)
//	FB  → 21  (20 kHz)
//
// The Opus top-level encoder maps both MB and WB to end_band 17, but the gopus
// standalone CELT encoder (which this grid drives directly via SetBandwidth)
// uses end_band 15 for CELTMediumband, so the oracle must match that count for
// an apples-to-apples comparison.
var pvqGridBandwidths = []pvqGridBandwidth{
	{"NB", CELTNarrowband, 13},
	{"MB", CELTMediumband, 15},
	{"WB", CELTWideband, 17},
	{"SWB", CELTSuperwideband, 19},
	{"FB", CELTFullband, 21},
}

// pvqGridFrameSizes lists all valid CELT frame sizes (samples at 48 kHz).
var pvqGridFrameSizes = []struct {
	label string
	size  int
}{
	{"2p5ms", 120},
	{"5ms", 240},
	{"10ms", 480},
	{"20ms", 960},
}

// pvqGridBitratesForBW returns a set of bitrates that exercise diverse K
// allocations for the given bandwidth and frame size.
//
// The selection targets:
//   - Very low: forces most bands to K=0 (folding-dominated, skip-band path)
//   - Low-mid:  a few bands get K=1–3 (sparse PVQ, alg_quant trivial paths)
//   - Mid:      typical allocation spread with K=3–20 per band
//   - High:     dense PVQ with K>20 in many bands (rotation path exercised)
//   - Very high (stereo only): exercises intensity-stereo threshold
func pvqGridBitratesForBW(endBand uint32, frameSize, channels int) []int32 {
	// Scale baselines by frame size (more samples → fewer kbps needed for same quality)
	// and channel count.
	fsScale := float64(frameSize) / 960.0 // normalize to 20ms
	chScale := float64(channels)

	// Base bitrates anchored to fullband mono 20ms, then scale down for
	// narrower bandwidths.
	var bwFraction float64
	switch endBand {
	case 13: // NB — 4 kHz
		bwFraction = 0.25
	case 17: // MB/WB — 6-8 kHz
		bwFraction = 0.45
	case 19: // SWB — 12 kHz
		bwFraction = 0.65
	default: // FB — 20 kHz
		bwFraction = 1.0
	}

	// Spread across 5 bitrates per dimension pair.
	// The effective per-frame byte budget governs K; we want to hit:
	//   very-low (folding), low (sparse), mid, high, dense.
	budgets := []float64{
		6000,  // very-low: folding, most bands skip or K=0
		16000, // low:  sparse PVQ, K=1-3 in low bands
		32000, // mid:  typical per-band K spread
		64000, // high: K>10 in most bands, rotation active
		96000, // dense: K>20, spread=SPREAD_AGGRESSIVE
	}

	rates := make([]int32, len(budgets))
	for i, base := range budgets {
		r := base * bwFraction * chScale / fsScale
		// Round to nearest 400 bps.
		r = math.Round(r/400) * 400
		// Clamp to [6000, 510000]. The gopus standalone CELT encoder clamps the
		// CBR bitrate to >=6000 inside cbrPayloadBytes; below that gopus pads to
		// the 6000-derived byte budget while the raw celt_encode_with_ec oracle
		// would use the smaller budget, producing different packet lengths that
		// are not a like-with-like comparison.
		if r < 6000 {
			r = 6000
		}
		if r > 510000 {
			r = 510000
		}
		rates[i] = int32(r)
	}
	return rates
}

// pvqTonalPCM generates a deterministic tonal signal (440 Hz + harmonics)
// that exercises the spread/rotation path in alg_quant.
func pvqTonalPCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		s := 0.42*math.Sin(2*math.Pi*440*float64(i)/48000) +
			0.18*math.Sin(2*math.Pi*880*float64(i)/48000) +
			0.07*math.Sin(2*math.Pi*1760*float64(i)/48000+0.5)
		pcm[i*channels] = float32(s)
		if channels == 2 {
			r := 0.35*math.Sin(2*math.Pi*523*float64(i)/48000+0.3) +
				0.12*math.Sin(2*math.Pi*1046*float64(i)/48000)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// pvqTransientPCM generates a quiet-then-burst signal to exercise
// the transient analysis / short-block MDCT path (tf_select=1).
func pvqTransientPCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	onset := frameSize / 2
	burstLen := min(40, frameSize-onset)
	for i := range frameSize {
		amp := 0.02
		if i >= onset && i < onset+burstLen {
			amp = 0.75
		}
		l := amp * math.Sin(2*math.Pi*800*float64(i)/48000)
		pcm[i*channels] = float32(l)
		if channels == 2 {
			r := amp * math.Sin(2*math.Pi*1200*float64(i)/48000+0.2)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// pvqNoisePCM generates deterministic pseudo-random noise to exercise
// folding and intra-energy paths.
func pvqNoisePCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	var state uint32 = 0xDEADBEEF
	for i := range pcm {
		state = state*1664525 + 1013904223
		pcm[i] = float32(int32(state)) / float32(math.MaxInt32) * 0.45
	}
	return pcm
}

// pvqMixedPCM combines tonal + noise to exercise the intensity-stereo
// and spread decisions in stereo cells.
func pvqMixedPCM(channels, frameSize int) []float32 {
	tonal := pvqTonalPCM(channels, frameSize)
	noise := pvqNoisePCM(channels, frameSize)
	out := make([]float32, frameSize*channels)
	for i := range out {
		out[i] = 0.6*tonal[i] + 0.35*noise[i]
	}
	return out
}

// pvqGridSignals lists signal classes. Each exercises different PVQ/bands paths.
var pvqGridSignals = []pvqGridSignal{
	{"tonal", pvqTonalPCM},
	{"transient", pvqTransientPCM},
	{"noise", pvqNoisePCM},
	{"mixed", pvqMixedPCM},
}

// buildPVQGrid returns the full exhaustive grid of test cases.
func buildPVQGrid() []pvqGridCase {
	var cases []pvqGridCase
	for _, bw := range pvqGridBandwidths {
		for _, fs := range pvqGridFrameSizes {
			for _, ch := range []int{1, 2} {
				chLabel := "mono"
				if ch == 2 {
					chLabel = "stereo"
				}
				bitrates := pvqGridBitratesForBW(bw.endBand, fs.size, ch)
				for _, br := range bitrates {
					for _, sig := range pvqGridSignals {
						label := fmt.Sprintf("%s-%s-%s-%dk-%s",
							bw.label, fs.label, chLabel, br/1000, sig.label)
						cases = append(cases, pvqGridCase{
							label:     label,
							channels:  ch,
							frameSize: fs.size,
							bitrate:   br,
							endBand:   bw.endBand,
							bw:        bw.bw,
							signal:    sig.label,
							pcm:       sig.pcmFn(ch, fs.size),
						})
					}
				}
			}
		}
	}
	return cases
}

// pvqGridTargetBytes mirrors cbrPayloadBytes for the oracle side so both
// encoder and oracle receive the same nbCompressedBytes budget.
func pvqGridTargetBytes(tc pvqGridCase) int {
	enc := newPVQGridEncoder(tc)
	return enc.cbrPayloadBytes(tc.frameSize)
}

// newPVQGridEncoder sets up a gopus CELT encoder matching the standalone
// configuration used by libopus_celt_encode_info.c:configure_encoder().
func newPVQGridEncoder(tc pvqGridCase) *Encoder {
	enc := NewEncoder(tc.channels)
	// Disable Opus top-level preprocessing stages (same as existing libopus
	// oracle tests; configure_encoder in the C oracle does not call them).
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetDelayCompensationEnabled(false)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	enc.SetComplexity(10)
	enc.SetBitrate(int(tc.bitrate))
	enc.SetBandwidth(tc.bw)
	return enc
}

// probeLibopusCELTPVQGrid calls the libopus GCGI/GCGO grid oracle for a
// slice of grid cases. Returns one packet []byte per case.
func probeLibopusCELTPVQGrid(binPath string, cases []pvqGridCase) ([][]byte, error) {
	payload := libopustest.NewOraclePayload("GCGI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(pvqGridTargetBytes(tc)))
		payload.I32(tc.bitrate)
		payload.I32(10) // complexity=10
		payload.U32(tc.endBand)
		for _, sample := range tc.pcm {
			payload.Float32(sample)
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt pvq grid", "GCGO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]byte, count)
	for i := range count {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// pvqGridDecode decodes a standalone CELT packet for one grid cell with the
// decoder configured to match the encoder (same channels and bandwidth/end_band).
func pvqGridDecode(tc pvqGridCase, packet []byte) ([]float32, error) {
	dec := NewDecoder(tc.channels)
	dec.SetBandwidth(tc.bw)
	return dec.DecodeFrame(append([]byte(nil), packet...), tc.frameSize)
}

// pvqGridCellOutcome records exact packet equality and diagnostic decode
// differences for a failed cell. Only byteExact satisfies the parity gate.
type pvqGridCellOutcome struct {
	byteExact  bool
	firstDiff  int
	gotLen     int
	wantLen    int
	pcmMaxDiff float64
	pcmDiffAt  int
	decodeErr  error
}

func classifyPVQGridCell(tc pvqGridCase, got, ref []byte) pvqGridCellOutcome {
	out := pvqGridCellOutcome{gotLen: len(got), wantLen: len(ref), firstDiff: -1, pcmDiffAt: -1}
	if bytes.Equal(got, ref) {
		out.byteExact = true
		return out
	}

	lim := min(len(ref), len(got))
	for j := 0; j < lim; j++ {
		if got[j] != ref[j] {
			out.firstDiff = j
			break
		}
	}
	if out.firstDiff < 0 {
		out.firstDiff = lim // pure length mismatch
	}

	// Decode both packets to describe the impact of the packet mismatch.
	pcmGot, errGot := pvqGridDecode(tc, got)
	pcmRef, errRef := pvqGridDecode(tc, ref)
	if errGot != nil || errRef != nil {
		out.decodeErr = errors.Join(errGot, errRef)
		return out
	}
	if len(pcmGot) != len(pcmRef) {
		out.pcmMaxDiff = math.Inf(1)
		return out
	}
	for j := range pcmGot {
		d := math.Abs(float64(pcmGot[j]) - float64(pcmRef[j]))
		if d > out.pcmMaxDiff {
			out.pcmMaxDiff = d
			out.pcmDiffAt = j
		}
	}
	return out
}

// TestCELTPVQBandsGridMatchesLibopus requires exact packet bytes from the
// standalone CELT encoder and the selected libopus oracle on every architecture.
//
// Grid dimensions:
//   - Bandwidth:  NB / MB / WB / SWB / FB (end_band 13/15/17/19/21)
//   - Frame size: 2.5 / 5 / 10 / 20 ms
//   - Channels:   mono, stereo
//   - Bitrates:   5 levels per (bw, fs, ch) covering sparse→dense PVQ
//   - Signals:    tonal, transient, noise, mixed
//
// References exercised:
//
//	celt/rate.c          interp_bits2pulses (per-band K allocation)
//	celt/bands.c         quant_all_bands / quant_band / alg_quant (PVQ encode)
//	celt/vq.c            alg_quant + op_pvq_search + exp_rotation
//	celt/bands.c         skip-band / folding path (low-K cells)
//	celt/bands.c         intensity_stereo gate (stereo cells)
//	celt/celt_encoder.c  tf_encode (entropy budget guard at minimum packet size)
func TestCELTPVQBandsGridMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	binPath, err := pvqGridHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid", err)
		return
	}

	grid := buildPVQGrid()

	// Fetch all oracle packets in one shot (avoid per-case process overhead).
	wantPackets, err := probeLibopusCELTPVQGrid(binPath, grid)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid oracle", err)
		return
	}

	type cellResult struct {
		label   string
		outcome pvqGridCellOutcome
	}

	results := make([]cellResult, len(grid))

	t.Run("cells", func(t *testing.T) {
		for i, tc := range grid {
			ref := wantPackets[i]
			t.Run(tc.label, func(t *testing.T) {
				t.Parallel()
				enc := newPVQGridEncoder(tc)
				got, encErr := enc.EncodeFrame(append([]float32(nil), tc.pcm...), tc.frameSize)
				if encErr != nil {
					t.Fatalf("EncodeFrame: %v", encErr)
				}

				outcome := classifyPVQGridCell(tc, got, ref)
				pass := outcome.byteExact
				results[i] = cellResult{label: tc.label, outcome: outcome}
				if pass {
					return
				}
				if outcome.decodeErr != nil {
					t.Errorf("FAIL byte[%d] len got=%d want=%d: decode error: %v",
						outcome.firstDiff, outcome.gotLen, outcome.wantLen, outcome.decodeErr)
					return
				}
				t.Errorf("FAIL byte[%d] len got=%d want=%d: packet bytes differ (decoded maxDiff=%g at sample %d)",
					outcome.firstDiff, outcome.gotLen, outcome.wantLen, outcome.pcmMaxDiff, outcome.pcmDiffAt)
			})
		}
	})

	byteExact := 0
	for _, r := range results {
		if r.outcome.byteExact {
			byteExact++
		} else {
			t.Logf("%s: byte[%d], lengths Go=%d C=%d, decoded maxDiff=%g",
				r.label, r.outcome.firstDiff, r.outcome.gotLen, r.outcome.wantLen, r.outcome.pcmMaxDiff)
		}
	}
	t.Logf("PVQ/Bands parity grid: byte-exact=%d fail=%d total=%d arch=%s/%s",
		byteExact, len(grid)-byteExact, len(grid), runtime.GOOS, runtime.GOARCH)
}

// TestCELTPVQBandsGridSummary is a summary-only variant that never fails the
// test but always prints the full per-cell table, useful for CI diff tracking
// without blocking builds.
func TestCELTPVQBandsGridSummary(t *testing.T) {
	libopustest.RequireOracle(t)

	binPath, err := pvqGridHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid", err)
		return
	}

	grid := buildPVQGrid()
	t.Logf("Total cells: %d", len(grid))

	wantPackets, err := probeLibopusCELTPVQGrid(binPath, grid)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid oracle", err)
		return
	}

	byteExact, fail := 0, 0
	for i, tc := range grid {
		enc := newPVQGridEncoder(tc)
		got, encErr := enc.EncodeFrame(append([]float32(nil), tc.pcm...), tc.frameSize)
		if encErr != nil {
			t.Errorf("cell %s EncodeFrame: %v", tc.label, encErr)
			fail++
			continue
		}
		outcome := classifyPVQGridCell(tc, got, wantPackets[i])
		switch {
		case outcome.byteExact:
			byteExact++
		default:
			fail++
		}
	}
	t.Logf("arch=%s/%s  byte-exact=%d  fail=%d  total=%d",
		runtime.GOOS, runtime.GOARCH, byteExact, fail, len(grid))
}
