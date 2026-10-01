package bwe

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

var featureFFTOracle libopustest.HelperCache

func TestBWEFeatureFFTMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "../../.."))
	binPath, err := featureFFTOracle.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper(root, "libopus_osce_bwe_fft_oracle.c", "gopus_libopus_osce_bwe_fft_oracle", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE FFT", err)
	}

	for _, tc := range []struct {
		name string
		in   func(int) float32
	}{
		{name: "impulse", in: func(i int) float32 {
			if i == 37 {
				return 0.75
			}
			return 0
		}},
		{name: "mixed", in: func(i int) float32 { return float32(math.Sin(float64(i)*0.079)*0.37 + math.Cos(float64(i)*0.031)*0.21) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input [bweWindowSize]float32
			var payload [bweWindowSize * 4]byte
			var fftIn [bweWindowSize]complex64
			for i := range input {
				input[i] = tc.in(i)
				binary.LittleEndian.PutUint32(payload[i*4:], math.Float32bits(input[i]))
				fftIn[i] = complex(input[i]*float32(1.0/bweWindowSize), 0)
			}

			out, err := libopustest.RunHelper(binPath, payload[:])
			if err != nil {
				t.Fatalf("OSCE BWE FFT oracle: %v", err)
			}
			reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE BWE FFT", "OSCEFFT\x00", out)
			if err != nil {
				t.Fatal(err)
			}
			if version != 2 {
				t.Fatalf("helper version=%d, want 2", version)
			}
			if n := reader.I32(); n != bweSpecNumFreqs {
				t.Fatalf("helper bins=%d, want %d", n, bweSpecNumFreqs)
			}
			if n := reader.I32(); n != bweSpecNumFreqs {
				t.Fatalf("helper magnitudes=%d, want %d", n, bweSpecNumFreqs)
			}
			if n := reader.I32(); n != bweNumBands {
				t.Fatalf("helper bands=%d, want %d", n, bweNumBands)
			}
			if n := reader.I32(); n != bweNumBands {
				t.Fatalf("helper logs=%d, want %d", n, bweNumBands)
			}
			reader.ExpectRemaining(bweSpecNumFreqs*8 + bweSpecNumFreqs*4 + bweNumBands*8)
			wantFFT := make([]complex64, bweSpecNumFreqs)
			for i := range wantFFT {
				wantFFT[i] = complex(reader.Float32(), reader.Float32())
			}
			wantMag := make([]float32, bweSpecNumFreqs)
			for i := range wantMag {
				wantMag[i] = reader.Float32()
			}
			wantBands := make([]float32, bweNumBands)
			for i := range wantBands {
				wantBands[i] = reader.Float32()
			}
			wantLogs := make([]float32, bweNumBands)
			for i := range wantLogs {
				wantLogs[i] = reader.Float32()
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			got := make([]complex64, bweWindowSize)
			fftScratch := make([]celt.KissCpx, bweWindowSize)
			celt.KissFFT32ToWithScratch(got, fftIn[:], fftScratch)
			for i := range wantFFT {
				if math.Float32bits(real(got[i])) != math.Float32bits(real(wantFFT[i])) ||
					math.Float32bits(imag(got[i])) != math.Float32bits(imag(wantFFT[i])) {
					t.Fatalf("fft[%d] Go=(%08x,%08x) C=(%08x,%08x)", i,
						math.Float32bits(real(got[i])), math.Float32bits(imag(got[i])),
						math.Float32bits(real(wantFFT[i])), math.Float32bits(imag(wantFFT[i])))
				}
			}

			var gotMag [bweSpecNumFreqs]float32
			for i := range gotMag {
				re, im := real(got[i]), imag(got[i])
				gotMag[i] = float32(bweWindowSize * opusmath.SqrtCReal(opusmath.CReal(mulAdd32(re, re, roundMul32(im, im)))))
				if math.Float32bits(gotMag[i]) != math.Float32bits(wantMag[i]) {
					t.Fatalf("mag[%d] Go=%08x C=%08x", i, math.Float32bits(gotMag[i]), math.Float32bits(wantMag[i]))
				}
			}
			var gotBands [bweNumBands]float32
			applyFilterbankBWE(gotBands[:], gotMag[:])
			for i := range gotBands {
				if math.Float32bits(gotBands[i]) != math.Float32bits(wantBands[i]) {
					t.Fatalf("band[%d] Go=%08x C=%08x", i, math.Float32bits(gotBands[i]), math.Float32bits(wantBands[i]))
				}
			}
			for i := range gotBands {
				gotBands[i] = float32(opusmath.LogCReal(opusmath.CReal(gotBands[i]) + 1e-9))
				if math.Float32bits(gotBands[i]) != math.Float32bits(wantLogs[i]) {
					t.Fatalf("log[%d] Go=%08x C=%08x", i, math.Float32bits(gotBands[i]), math.Float32bits(wantLogs[i]))
				}
			}
		})
	}
}
