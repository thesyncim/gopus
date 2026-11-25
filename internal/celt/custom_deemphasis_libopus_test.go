//go:build gopus_custom_modes

package celt

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

var customDeemphasisHelper libopustest.HelperCache

func probeCustomDeemphasis(t *testing.T, planes [][]float32, mem []float32, coef [4]float32, downsample int, seed []float32) libopusDeemphasisResult {
	t.Helper()
	helper, err := customDeemphasisHelper.Path(func() (string, error) {
		return buildCustomFloatKernelHelper(libopustest.CHelperConfig{
			Label: "custom deemphasis", OutputBase: "gopus_custom_deemphasis", SourceFile: "libopus_celt_filter_info.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"src", "celt", "silk", "silk/float"},
			RefSources:  []string{"celt/celt_decoder.c", "celt/celt.c", "celt/x86/pitch_sse.c", "celt/x86/x86_celt_map.c"},
			Libs:        []string{"-lm"}, DeadStrip: true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom deemphasis", err)
		return libopusDeemphasisResult{}
	}
	channels, n := len(planes), len(planes[0])
	payload := libopustest.NewOraclePayload("GCFI", libopusCELTFilterModeDeemphasis, uint32(channels), uint32(n), uint32(downsample))
	accum := uint32(0)
	if seed != nil {
		accum = 1
	}
	payload.U32(accum)
	payload.Float32s(coef[:]...)
	payload.Float32s(mem...)
	for _, plane := range planes {
		payload.Float32s(plane...)
	}
	payload.Float32s(seed...)
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "custom deemphasis", "GCFO")
	if err != nil {
		t.Fatal(err)
	}
	if mode := reader.U32(); mode != libopusCELTFilterModeDeemphasis {
		t.Fatalf("C mode=%d", mode)
	}
	count := channels * (n / downsample)
	if got := reader.U32(); got != uint32(count) {
		t.Fatalf("C samples=%d want=%d", got, count)
	}
	out := libopusDeemphasisResult{mem: make([]float32, channels), pcm: make([]float32, count)}
	for i := range out.mem {
		out.mem[i] = reader.Float32()
	}
	for i := range out.pcm {
		out.pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The two-tap branch is compiled by CUSTOM_MODES. Compare its memory and
// samples with the actual celt_decoder.c function in the paired C build.
func TestCustomDeemphasisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// celt/modes.c:opus_custom_mode_create coefficients below 12 kHz and at 96 kHz.
	for _, coef := range [][4]float32{{0.3500061035, -0.1799926758, 0.2719968125, 3.6765136719}, {0.9230041504, 0.2200012207, 1.5128347184, 0.6610107422}} {
		for _, channels := range []int{1, 2} {
			for _, downsample := range []int{1, 2, 3} {
				for _, accum := range []bool{false, true} {
					t.Run(fmt.Sprintf("coef%g/ch%d/ds%d/accum%t", coef[0], channels, downsample, accum), func(t *testing.T) {
						const n = 121
						dec := NewDecoder(channels)
						dec.deemphCoef, dec.deemphCoef1, dec.deemphCoef3 = coef[0], coef[1], coef[3]
						oracleMem := make([]float32, channels)
						for c := range oracleMem {
							oracleMem[c] = float32(c+1) * -0.03125
						}
						copy(dec.preemphState, oracleMem)
						for frame := range 3 {
							planes := make([][]float32, channels)
							interleaved := make([]float32, n*channels)
							for c := range channels {
								planes[c] = make([]float32, n)
								for i := range n {
									x := float32((i*37+c*17)%101-50) * 0.1234567
									if frame == 1 {
										x *= 1e-30
									}
									if frame == 2 {
										x = 0
									}
									planes[c][i], interleaved[i*channels+c] = x, x
								}
							}
							count := channels * (n / downsample)
							var seed []float32
							if accum {
								seed = make([]float32, count)
								for i := range seed {
									seed[i] = float32(i%7-3) * 0.017321
								}
							}
							want := probeCustomDeemphasis(t, planes, oracleMem, coef, downsample, seed)
							for _, layout := range []string{"planar", "interleaved", "in-place"} {
								if layout == "in-place" && (accum || downsample != 1) {
									continue
								}
								copy(dec.preemphState, oracleMem)
								out := make([]float32, count)
								copy(out, seed)
								x0, x1, stride := planes[0], planes[0], 1
								if channels == 2 {
									x1 = planes[1]
								}
								if layout != "planar" {
									x0, stride = interleaved, channels
									if layout == "in-place" {
										copy(out, interleaved)
										x0 = out
									}
									x1 = x0
									if channels == 2 {
										x1 = x0[1:]
									}
								}
								run := func() { dec.deemphasis(out, x0, x1, stride, n, downsample, accum) }
								run()
								assertCELTFilterFloat32Bits(t, fmt.Sprintf("frame%d/%s", frame, layout), out, want.pcm)
								assertCELTFilterMemBits(t, dec, want.mem)
								if allocs := testing.AllocsPerRun(20, run); allocs != 0 {
									t.Fatalf("%s warm allocations=%g", layout, allocs)
								}
							}
							copy(oracleMem, want.mem)
						}
					})
				}
			}
		}
	}
}

var customFFTTwiddlesHelper libopustest.HelperCache

func TestCustomFFTTwiddlesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := customFFTTwiddlesHelper.Path(func() (string, error) {
		return buildCustomFloatKernelHelper(libopustest.CHelperConfig{
			Label:       "custom FFT twiddles",
			OutputBase:  "gopus_custom_fft_twiddles",
			SourceFile:  "libopus_custom_fft_twiddles.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom FFT twiddles", err)
		return
	}
	for _, nfft := range []int{20, 30, 40, 50, 60, 90, 100, 120, 160, 180, 240, 320, 360, 480, 512} {
		t.Run(fmt.Sprint(nfft), func(t *testing.T) {
			request := libopustest.NewOraclePayload("GFTI", uint32(nfft))
			reader, err := libopustest.RunOracle(helper, request.Bytes(), "custom FFT twiddles", "GFTO")
			if err != nil {
				t.Fatal(err)
			}
			if count := reader.U32(); count != uint32(nfft) {
				t.Fatalf("C table size=%d want=%d", count, nfft)
			}
			for i, got := range computeTwiddles(nfft) {
				wantR, wantI := reader.U32(), reader.U32()
				if math.Float32bits(got.r) != wantR || math.Float32bits(got.i) != wantI {
					t.Errorf("twiddle %d bits=(%08x,%08x) want=(%08x,%08x)", i, math.Float32bits(got.r), math.Float32bits(got.i), wantR, wantI)
				}
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func buildCustomFloatKernelHelper(cfg libopustest.CHelperConfig) (string, error) {
	var archive string
	if extsupport.QEXT {
		cfg.CustomQEXTRef = true
		archive = libopustest.CustomQEXTRefPath(".libs", "libopus.a")
	} else {
		cfg.CustomRef = true
		archive = libopustest.CustomRefPath(".libs", "libopus.a")
	}
	cfg.Libs = append([]string{archive}, cfg.Libs...)
	return libopustest.BuildCHelper(cfg)
}

var customMDCTTablesHelper libopustest.HelperCache

func TestCustomModeTransformsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := customMDCTTablesHelper.Path(func() (string, error) {
		return buildCustomFloatKernelHelper(libopustest.CHelperConfig{
			Label: "custom mode transforms", OutputBase: "gopus_custom_mdct_tables", SourceFile: "libopus_custom_mdct_tables.c",
			CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt"},
			Libs: []string{"-lm"}, DeadStrip: true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom mode transforms", err)
		return
	}
	for _, tc := range []struct{ fs, frame, maxLM int }{{16000, 60, 1}, {24000, 40, 0}, {12000, 240, 3}, {44100, 360, 2}, {48000, 640, 3}, {48000, 720, 3}, {48000, 1024, 3}} {
		t.Run(fmt.Sprintf("%d_%d", tc.fs, tc.frame), func(t *testing.T) {
			overlap := ((tc.frame >> tc.maxLM) >> 2) << 2
			window := GetWindowBufferF32(overlap)
			plan := NewCustomMDCTTables(tc.frame, tc.maxLM, window)
			if plan == nil {
				t.Fatal("dynamic transform allocation failed")
			}
			request := libopustest.NewOraclePayload("GMTI", uint32(tc.fs), uint32(tc.frame))
			var inputs, histories, spectra [4][]float32
			for shift := 0; shift <= tc.maxLM; shift++ {
				block := tc.frame >> shift
				inputs[shift] = make([]float32, block+overlap)
				histories[shift] = make([]float32, overlap)
				spectra[shift] = make([]float32, block)
				for kind, values := range [][]float32{inputs[shift], histories[shift], spectra[shift]} {
					for i := range values {
						values[i] = float32(((i+3)*(kind+7)+shift*11)%97-48) * (1.0 / 16)
						request.Float32(values[i])
					}
				}
			}
			reader, err := libopustest.RunOracle(helper, request.Bytes(), "custom mode transforms", "GMTO")
			if err != nil {
				t.Fatal(err)
			}
			gotOverlap, gotLM := reader.U32(), reader.U32()
			if gotOverlap != uint32(overlap) || gotLM != uint32(tc.maxLM) {
				t.Fatalf("C geometry overlap/LM=%d/%d want=%d/%d", gotOverlap, gotLM, overlap, tc.maxLM)
			}
			compare := func(label string, got []float32) {
				t.Helper()
				for i, v := range got {
					want := reader.U32()
					if math.Float32bits(v) != want {
						t.Errorf("%s[%d]=%08x want=%08x", label, i, math.Float32bits(v), want)
					}
				}
			}
			compare("window", window)
			for shift := 0; shift <= tc.maxLM; shift++ {
				block := tc.frame >> shift
				tables := &plan.blocks[shift]
				fft := tables.fft
				if n := reader.U32(); n != uint32(fft.nfft) {
					t.Fatalf("shift%d C FFT size=%d want=%d", shift, n, fft.nfft)
				}
				for i := range fft.nfft {
					v := fft.w[i<<max(0, fft.shift)]
					compare(fmt.Sprintf("shift%d/twiddle%d", shift, i), []float32{v.r, v.i})
				}
				compare(fmt.Sprintf("shift%d/trig", shift), tables.trig)
				var forward encoderScratch
				forward.customTransforms = plan
				var inverse imdctScratchF32
				inverse.customTransforms = plan
				coeffs := make([]float32, block)
				runForward := func() {
					mdctForwardOverlapF32Scratch(inputs[shift], overlap, coeffs, forward.mdctF, forward.mdctFFTIn, forward.mdctFFTOut, forward.mdctFFTTmp, forward.mdctLookup(2*block))
				}
				forward.mdctFFTTmp = make([]float32, block)
				runForward()
				compare(fmt.Sprintf("shift%d/forward", shift), coeffs)
				decoded := imdctOverlapWithPrevScratchF32Output32(spectra[shift], histories[shift], overlap, &inverse)
				compare(fmt.Sprintf("shift%d/inverse", shift), decoded)
				if allocs := testing.AllocsPerRun(50, func() {
					runForward()
					imdctOverlapWithPrevScratchF32Output32(spectra[shift], histories[shift], overlap, &inverse)
				}); allocs != 0 {
					t.Errorf("shift%d warm transform allocations=%g want=0", shift, allocs)
				}
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var customPreemphasisHelper libopustest.HelperCache

func TestCustomPreemphasisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := customPreemphasisHelper.Path(func() (string, error) {
		return buildCustomFloatKernelHelper(libopustest.CHelperConfig{
			Label: "custom preemphasis", OutputBase: "gopus_custom_preemphasis", SourceFile: "libopus_celt_preemphasis_info.c",
			CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt", "silk", "src"},
			Libs: []string{"-lm"}, DeadStrip: true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom preemphasis", err)
		return
	}
	// celt/modes.c tables for 16 kHz and 96 kHz exercise both signs of coef1.
	for _, coef := range [][4]float32{{0.6000061035, -0.1799926758, 0.4424998650, 2.2598876953}, {0.9230041504, 0.2200012207, 1.5128347184, 0.6610107422}} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("coef%g/ch%d", coef[0], channels), func(t *testing.T) {
				enc := NewEncoder(channels)
				enc.hd96kPreemph = coef
				mem := make([]float32, channels)
				for c := range mem {
					mem[c] = float32(c+1) * 0.0123456
				}
				copy(enc.preemphState, mem)
				for frame := range 3 {
					const n = 61
					pcm := preemphOraclePCM(channels, n, float64(frame)*0.23)
					if frame == 1 {
						for i := range pcm {
							pcm[i] *= 1e-30
						}
					}
					if frame == 2 {
						clear(pcm)
					}
					req := libopustest.NewOraclePayloadVersion("GCPI", 2, 1, uint32(channels), n)
					req.Float32s(coef[:]...)
					req.Float32s(mem...)
					req.Float32s(pcm...)
					rd, err := libopustest.RunOracleVersion(helper, req.Bytes(), "custom preemphasis", "GCPO", 2)
					if err != nil {
						t.Fatal(err)
					}
					if count := rd.U32(); count != 1 {
						t.Fatalf("C case count=%d", count)
					}
					want := make([]float32, len(pcm))
					for i := range want {
						want[i] = rd.Float32()
					}
					for c := range mem {
						mem[c] = rd.Float32()
					}
					if err := rd.ExpectConsumed(); err != nil {
						t.Fatal(err)
					}
					// The encoder writes each channel to its planar in buffer; the
					// oracle returns channel-interleaved output.
					perChannel := len(pcm) / channels
					planar := make([]float32, len(pcm))
					run := func() {
						var right []float32
						if channels == 2 {
							right = planar[perChannel:]
						}
						enc.applyPreemphasis2Tap(pcm, planar[:perChannel], right)
					}
					run()
					out := make([]float32, len(pcm))
					for i := range out {
						out[i] = planar[(i%channels)*perChannel+i/channels]
					}
					assertCELTFilterFloat32Bits(t, fmt.Sprintf("frame%d output", frame), out, want)
					assertCELTFilterFloat32Bits(t, fmt.Sprintf("frame%d memory", frame), enc.preemphState, mem)
					if allocs := testing.AllocsPerRun(20, run); allocs != 0 {
						t.Fatalf("warm allocations=%g", allocs)
					}
					copy(enc.preemphState, mem)
				}
			})
		}
	}
}
