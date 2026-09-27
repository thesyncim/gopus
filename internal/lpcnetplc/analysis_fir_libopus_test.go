//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusLPCNetFIRHelper libopustest.HelperCache

func TestLPCNetFIRMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := libopusLPCNetFIRHelper.Path(func() (string, error) {
		return buildLibopusPLCHelper("libopus_lpcnet_celt_fir_info.c", "gopus_libopus_lpcnet_celt_fir")
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "selected LPCNet celt_fir", err)
	}

	var input [analysisLPCOrder + FrameSize]float32
	var coeffs [analysisLPCOrder]float32
	for i := range input {
		input[i] = float32((i*37)%101-50)/997 + float32(i%7)*0.00017
	}
	for i := range coeffs {
		coeffs[i] = float32((i*29)%43-21) / 6553
	}

	payload := libopustest.NewOraclePayload("GLRI")
	payload.Float32s(input[:]...)
	payload.Float32s(coeffs[:]...)
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "selected LPCNet celt_fir", "GLRO")
	if err != nil {
		t.Fatal(err)
	}
	arch := reader.U32()
	if err := libopustest.ValidateDNNDispatchArch(arch); err != nil {
		t.Fatalf("selected LPCNet celt_fir dispatch: %v", err)
	}
	var want [FrameSize]float32
	for i := range want {
		want[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	var got [FrameSize]float32
	celtFIRFloat(input[:], coeffs[:], got[:])
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("arch=%d celt_fir[%d] Go=%08x C=%08x", arch, i,
				math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		celtFIRFloat(input[:], coeffs[:], got[:])
	}); allocs != 0 {
		t.Fatalf("warm celt_fir allocation=%g want 0", allocs)
	}
}
