//go:build gopus_custom_modes

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var customFFTTwiddlesHelper libopustest.HelperCache

func TestCustomFFTTwiddlesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := customFFTTwiddlesHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:       "custom FFT twiddles",
			OutputBase:  "gopus_custom_fft_twiddles",
			SourceFile:  "libopus_custom_fft_twiddles.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt"},
			CustomRef:   true,
			Libs:        []string{libopustest.CustomRefPath(".libs", "libopus.a"), "-lm"},
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
