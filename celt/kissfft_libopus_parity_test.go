package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func probeLibopusCELTFFT(t *testing.T, nfft int, input []complex64) []kissCpx {
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
	out := make([]kissCpx, count)
	reader.ExpectRemaining(count * 8)
	for i := range out {
		out[i].r = reader.Float32()
		out[i].i = reader.Float32()
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

				got := kissFFT32ToScratch(input, make([]kissCpx, nfft))
				want := probeLibopusCELTFFT(t, nfft, input)
				assertKissCpxBits(t, "fft", got, want)
			})
		}
	}
}

func assertKissCpxBits(t *testing.T, label string, got, want []kissCpx) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i].r) != math.Float32bits(want[i].r) ||
			math.Float32bits(got[i].i) != math.Float32bits(want[i].i) {
			t.Fatalf("%s[%d]=(%08x %.9g,%08x %.9g) want (%08x %.9g,%08x %.9g)",
				label, i,
				math.Float32bits(got[i].r), got[i].r,
				math.Float32bits(got[i].i), got[i].i,
				math.Float32bits(want[i].r), want[i].r,
				math.Float32bits(want[i].i), want[i].i)
		}
	}
}
