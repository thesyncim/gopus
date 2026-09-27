//go:build gopus_qext && (arm64 || amd64)

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

const (
	qextBandEnergyScalar = uint32(0)
	qextBandEnergyNEON   = uint32(1)
	qextBandEnergySSE    = uint32(2)
)

var qextBandEnergyHelper libopustest.HelperCache

type qextBandEnergyCase struct {
	name      string
	frameSize int
	channels  int
	coeffs    []float32
}

func qextBandEnergyHelperPath() (string, error) {
	return qextBandEnergyHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "CELT QEXT band energy",
		OutputBase:   "gopus_libopus_celt_qext_band_energy",
		SourceFile:   "libopus_celt_qext_band_energy_info.c",
		ProbeRelPath: "celt/bands.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes:  []string{"celt", "silk", "src", "include"},
		QEXTRef:      true,
		Libs:         []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func qextBandEnergyCases() []qextBandEnergyCase {
	var cases []qextBandEnergyCase
	for _, frameSize := range []int{120, 240, 480, 960} {
		for _, channels := range []int{1, 2} {
			for pattern := range 4 {
				coeffs := make([]float32, frameSize*channels)
				for i := range coeffs {
					if pattern == 0 {
						coeffs[i] = float32((i*37)%127-63) * (1.0 / 64.0)
					} else if pattern == 1 && i%13 == 0 {
						coeffs[i] = float32((i%17)-8) * (1.0 / 16.0)
					} else if pattern == 2 {
						// Varied exponents and mantissas exercise reduction-order
						// rounding with float32 values like live MDCT coefficients.
						state := uint32(i+1) ^ uint32(frameSize*channels*17)
						state = state*1664525 + 1013904223
						exponent := uint32(122 + (state>>24)%10)
						bits := exponent<<23 | (state & 0x7fffff)
						if state&0x80000000 != 0 {
							bits |= 0x80000000
						}
						coeffs[i] = math.Float32frombits(bits)
					}
				}
				if pattern == 1 {
					for i := 0; i < frameSize*channels; i++ {
						if i%13 != 0 {
							coeffs[i] = 0
						}
					}
				}
				if pattern == 3 {
					lm := 0
					for 120<<lm < frameSize {
						lm++
					}
					start, stop := 100<<lm, 110<<lm
					for channel := range channels {
						base := channel * frameSize
						coeffs[base+start] = 1
						for i := start + 1; i < stop; i++ {
							coeffs[base+i] = 1.0 / 4096.0
						}
					}
				}
				cases = append(cases, qextBandEnergyCase{
					name:      fmt.Sprintf("pattern%d_%d_%d", pattern, frameSize, channels),
					frameSize: frameSize,
					channels:  channels,
					coeffs:    coeffs,
				})
			}
		}
	}
	return cases
}

func qextGoBandEnergyDispatch() uint32 {
	if celtUseFusedFloatMath {
		return qextBandEnergyNEON
	}
	if celtUseSSEFloatMath {
		return qextBandEnergySSE
	}
	return qextBandEnergyScalar
}

func TestQEXTBandEnergyMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := qextBandEnergyHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT QEXT band energy", err)
	}
	cases := qextBandEnergyCases()
	payload := libopustest.NewOraclePayload("GQBE", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(tc.channels))
		payload.Float32s(tc.coeffs...)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT QEXT band energy", "GQBO")
	if err != nil {
		t.Fatal(err)
	}
	dispatch := reader.U32()
	arch := int32(reader.U32())
	flags := reader.U32()
	if dispatch == 255 {
		t.Fatalf("libopus QEXT inner product dispatch is unknown (arch=%d flags=%#x)", arch, flags)
	}
	if want := qextGoBandEnergyDispatch(); dispatch != want {
		t.Fatalf("libopus QEXT inner product dispatch=%d (arch=%d flags=%#x), Go dispatch=%d; oracle lanes must match", dispatch, arch, flags, want)
	}
	if count := reader.Count(len(cases)); count != len(cases) {
		t.Fatalf("oracle case count=%d want %d", count, len(cases))
	}
	sequentialMismatches := 0
	sequentialCompared := 0
	for _, tc := range cases {
		if got := int(reader.U32()); got != tc.frameSize {
			t.Fatalf("case %s frameSize=%d want %d", tc.name, got, tc.frameSize)
		}
		if got := int(reader.U32()); got != tc.channels {
			t.Fatalf("case %s channels=%d want %d", tc.name, got, tc.channels)
		}
		shortSize := int(reader.U32())
		bands := int(reader.U32())
		if shortSize != 120 || bands != 2 {
			t.Fatalf("case %s mode shape short=%d bands=%d", tc.name, shortSize, bands)
		}
		wantEnergy := make([]float32, tc.channels*bands)
		wantLog := make([]float32, tc.channels*bands)
		for i := range wantEnergy {
			wantEnergy[i] = reader.Float32()
		}
		for i := range wantLog {
			wantLog[i] = reader.Float32()
		}
		lm := 0
		for 120<<lm < tc.frameSize {
			lm++
		}
		cfg, ok := computeQEXTModeConfig(48000, qextShortMDCTSize(tc.frameSize))
		if !ok {
			t.Fatalf("case %s missing QEXT mode", tc.name)
		}
		if cfg.EffBands != bands {
			t.Fatalf("case %s active bands=%d want %d", tc.name, cfg.EffBands, bands)
		}
		gotEnergy := make([]celtEner, bands)
		gotLog := make([]celtGLog, bands)
		for channel := range tc.channels {
			channelCoeffs := tc.coeffs[channel*tc.frameSize : (channel+1)*tc.frameSize]
			computeQEXTBandLogEF32Into(channelCoeffs, &cfg, bands, lm, gotEnergy, gotLog)
			for band := range bands {
				idx := channel*bands + band
				if got, want := math.Float32bits(float32(gotEnergy[band])), math.Float32bits(wantEnergy[idx]); got != want {
					t.Fatalf("case %s band %d amplitude bits=%08x want %08x (dispatch=%d arch=%d flags=%#x)", tc.name, band, got, want, dispatch, arch, flags)
				}
				if got, want := math.Float32bits(float32(gotLog[band])), math.Float32bits(wantLog[idx]); got != want {
					t.Fatalf("case %s band %d log-energy bits=%08x want %08x (dispatch=%d arch=%d flags=%#x)", tc.name, band, got, want, dispatch, arch, flags)
				}
				start := cfg.EBands[band] << lm
				stop := cfg.EBands[band+1] << lm
				oldAmplitude := qextSequentialBandAmplitude(channelCoeffs[start:stop])
				sequentialCompared++
				if math.Float32bits(oldAmplitude) != math.Float32bits(wantEnergy[idx]) {
					sequentialMismatches++
				}
			}
		}
	}
	if dispatch == qextBandEnergyScalar && sequentialMismatches != 0 {
		t.Fatalf("old sequential accumulation differs from selected scalar C in %d/%d band energies", sequentialMismatches, sequentialCompared)
	}
	if dispatch != qextBandEnergyScalar && sequentialMismatches == 0 {
		t.Fatalf("old sequential accumulation matched selected SIMD C for all %d band energies; negative control did not exercise reduction-order rounding", sequentialCompared)
	}
	t.Logf("old sequential accumulation differs from selected C in %d/%d band energies (dispatch=%d)", sequentialMismatches, sequentialCompared, dispatch)
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func qextSequentialBandAmplitude(coeffs []float32) float32 {
	sum := float32(1e-27)
	for _, coeff := range coeffs {
		sum += coeff * coeff
	}
	return float32(opusmath.SqrtF32(sum))
}
