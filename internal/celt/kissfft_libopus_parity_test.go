package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func probeLibopusCELTFFT(t *testing.T, nfft int, input []complex64) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCII", libopusCELTIMDCTModeFFT, uint32(nfft), 0, 0)
	for _, v := range input {
		payload.Float32(real(v))
		payload.Float32(imag(v))
	}

	binPath, err := libopusCELTIMDCTHelper.Path(buildLibopusCELTIMDCTHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT FFT", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT FFT", "GCIO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT FFT", err)
	}
	if gotMode := reader.U32(); gotMode != libopusCELTIMDCTModeFFT {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTIMDCTModeFFT)
	}
	count := int(reader.U32())
	out := make([]float32, 2*count)
	reader.ExpectRemaining(count * 8)
	for i := range count {
		out[2*i] = reader.Float32()
		out[2*i+1] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKissFFT32ToScratchMatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, nfft := range []int{60, 120, 240, 480} {
		for seed := 1; seed <= 3; seed++ {
			t.Run(fmt.Sprintf("nfft=%d/seed=%d", nfft, seed), func(t *testing.T) {
				input := make([]complex64, nfft)
				for i := range input {
					r := float32(math.Sin(float64(i+seed*7)*0.079)*0.9 + math.Cos(float64(i+3)*0.031)*0.2)
					im := float32(math.Cos(float64(i+seed*5)*0.053)*0.7 - math.Sin(float64(i+11)*0.017)*0.3)
					input[i] = complex(r, im)
				}

				got := kissFFT32ToScratch(input, make([]float32, 2*nfft), nil)
				want := probeLibopusCELTFFT(t, nfft, input)
				assertKissInterleavedBits(t, "fft", got, want)
			})
		}
	}
}

func assertKissInterleavedBits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := 0; i < len(want); i += 2 {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) ||
			math.Float32bits(got[i+1]) != math.Float32bits(want[i+1]) {
			t.Fatalf("%s[%d]=(%08x %.9g,%08x %.9g) want (%08x %.9g,%08x %.9g)",
				label, i/2,
				math.Float32bits(got[i]), got[i],
				math.Float32bits(got[i+1]), got[i+1],
				math.Float32bits(want[i]), want[i],
				math.Float32bits(want[i+1]), want[i+1])
		}
	}
}
