//go:build arm64 || amd64

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	celtBandEnergyInputMagic  = "GBEI"
	celtBandEnergyOutputMagic = "GBEO"
	celtBandEnergyScalar      = uint32(0)
	celtBandEnergyNEON        = uint32(1)
	celtBandEnergySSE         = uint32(2)
)

var celtBandEnergyHelper libopustest.HelperCache

type celtBandEnergyCase struct {
	name      string
	frameSize int
	channels  int
	coeffs    []float32
}

func celtBandEnergyHelperPath() (string, error) {
	return celtBandEnergyHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "CELT band energy",
		OutputBase:   "gopus_libopus_celt_band_energy",
		SourceFile:   "libopus_celt_band_energy_info.c",
		ProbeRelPath: "celt/bands.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src", "include"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func celtBandEnergyCases() []celtBandEnergyCase {
	var cases []celtBandEnergyCase
	rng := rand.New(rand.NewSource(0x6b17e))
	for _, frameSize := range []int{120, 240, 480, 960} {
		for _, channels := range []int{1, 2} {
			coeffs := make([]float32, frameSize*channels)
			cases = append(cases, celtBandEnergyCase{
				name:      fmt.Sprintf("zero_%d_%d", frameSize, channels),
				frameSize: frameSize,
				channels:  channels,
				coeffs:    append([]float32(nil), coeffs...),
			})
			for i := range coeffs {
				coeffs[i] = float32((rng.Float64()*2 - 1) * 0.75)
			}
			cases = append(cases, celtBandEnergyCase{
				name:      fmt.Sprintf("random_%d_%d", frameSize, channels),
				frameSize: frameSize,
				channels:  channels,
				coeffs:    append([]float32(nil), coeffs...),
			})
		}
	}
	return cases
}

func celtGoBandEnergyDispatch() uint32 {
	if celtUseFusedFloatMath {
		return celtBandEnergyNEON
	}
	if celtUseSSEFloatMath {
		return celtBandEnergySSE
	}
	return celtBandEnergyScalar
}

func TestCELTBandEnergyMatchesLiveLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := celtBandEnergyHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT band energy", err)
	}
	cases := celtBandEnergyCases()
	payload := libopustest.NewOraclePayload(celtBandEnergyInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(tc.channels))
		payload.Float32s(tc.coeffs...)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT band energy", celtBandEnergyOutputMagic)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := reader.U32()
	arch := reader.I32()
	flags := reader.U32()
	if dispatch == 255 {
		t.Fatalf("libopus CELT inner product dispatch is unknown (arch=%d flags=%#x)", arch, flags)
	}
	if want := celtGoBandEnergyDispatch(); dispatch != want {
		t.Fatalf("libopus CELT inner product dispatch=%d (arch=%d flags=%#x), Go dispatch=%d; oracle lanes must match", dispatch, arch, flags, want)
	}
	count := reader.Count(len(cases))
	for i := 0; i < count; i++ {
		tc := cases[i]
		if got := reader.U32(); got != uint32(tc.frameSize) {
			t.Fatalf("case %s frame size=%d", tc.name, got)
		}
		if got := reader.U32(); got != uint32(tc.channels) {
			t.Fatalf("case %s channels=%d", tc.name, got)
		}
		effBands := int(reader.U32())
		nbBands := int(reader.U32())
		cSums := make([]float32, tc.channels*effBands)
		cBands := make([]float32, tc.channels*nbBands)
		cLogs := make([]float32, tc.channels*nbBands)
		for j := range cSums {
			cSums[j] = reader.Float32()
		}
		for j := range cBands {
			cBands[j] = reader.Float32()
		}
		for j := range cLogs {
			cLogs[j] = reader.Float32()
		}
		if nbBands != MaxBands || effBands != GetModeConfig(tc.frameSize).EffBands {
			t.Fatalf("case %s mode shape effBands=%d nbBands=%d", tc.name, effBands, nbBands)
		}
		for channel := 0; channel < tc.channels; channel++ {
			for band := 0; band < effBands; band++ {
				start := ScaledBandStart(band, tc.frameSize)
				end := ScaledBandEnd(band, tc.frameSize)
				coeffs := tc.coeffs[channel*tc.frameSize : (channel+1)*tc.frameSize]
				bandCoeffs := coeffs[start:end:end]
				idx := channel*nbBands + band
				gotSum := celtInnerProdF32LibopusOrder(bandCoeffs)
				if gotBits, wantBits := math.Float32bits(gotSum), math.Float32bits(cSums[channel*effBands+band]); gotBits != wantBits {
					var fmaSum float32
					var noFmaSum float32
					for _, x := range bandCoeffs {
						fmaSum = float32(math.FMA(float64(x), float64(x), float64(fmaSum)))
						noFmaSum = noFMA32Add(noFmaSum, noFMA32Mul(x, x))
					}
					t.Fatalf("case %s band %d inner product bits=%08x want %08x fma=%08x nofma=%08x (dispatch=%d arch=%d flags=%#x)", tc.name, band, gotBits, wantBits, math.Float32bits(fmaSum), math.Float32bits(noFmaSum), dispatch, arch, flags)
				}
				gotAmp := celtSqrt(float32(1e-27) + gotSum)
				if gotBits, wantBits := math.Float32bits(gotAmp), math.Float32bits(cBands[idx]); gotBits != wantBits {
					t.Fatalf("case %s band %d amplitude bits=%08x want %08x (dispatch=%d arch=%d flags=%#x)", tc.name, band, gotBits, wantBits, dispatch, arch, flags)
				}
				gotLog := computeBandRMSFloat32(coeffs, start, end) - float32(eMeans[band]*DB6)
				if gotBits, wantBits := math.Float32bits(gotLog), math.Float32bits(cLogs[idx]); gotBits != wantBits {
					t.Fatalf("case %s band %d log bits=%08x want %08x (dispatch=%d arch=%d flags=%#x)", tc.name, band, gotBits, wantBits, dispatch, arch, flags)
				}
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
