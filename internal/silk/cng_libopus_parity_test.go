//go:build gopus_libopus_oracle

package silk

import (
	"fmt"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKCNGInputMagic  = "GCNI"
	libopusSILKCNGOutputMagic = "GCNO"
)

var libopusSILKCNGHelper libopustest.HelperCache

func buildLibopusSILKCNGHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk cng",
		OutputBase:   "gopus_libopus_silk_cng",
		SourceFile:   "libopus_silk_cng_state_info.c",
		ProbeRelPath: "silk/main.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
	})
}

func probeLibopusSILKCNGStateSizes(t *testing.T) [8]int32 {
	t.Helper()
	binPath, err := libopusSILKCNGHelper.Path(buildLibopusSILKCNGHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk cng", err)
	}
	payload := libopustest.NewOraclePayload(libopusSILKCNGInputMagic, 0, 1)
	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		t.Fatalf("run silk cng helper: %v", err)
	}
	reader, err := libopustest.NewOracleReader("silk cng", libopusSILKCNGOutputMagic, data)
	if err != nil {
		t.Fatal(err)
	}
	count := reader.Count(1)
	reader.ExpectRemaining(count * 8 * 4)
	var out [8]int32
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSILKCNGStateStorageMatchesLibopusTypes(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes := probeLibopusSILKCNGStateSizes(t)
	var st cngState
	checks := []struct {
		name string
		got  uintptr
		want int32
	}{
		{"excBufQ14", unsafe.Sizeof(st.excBufQ14[0]), sizes[0]},
		{"smthNLSFQ15", unsafe.Sizeof(st.smthNLSFQ15[0]), sizes[1]},
		{"synthStateQ14", unsafe.Sizeof(st.synthStateQ14[0]), sizes[2]},
		{"smthGainQ16", unsafe.Sizeof(st.smthGainQ16), sizes[3]},
		{"randSeed", unsafe.Sizeof(st.randSeed), sizes[4]},
		{"fsKHz", unsafe.Sizeof(st.fsKHz), sizes[5]},
	}
	for _, check := range checks {
		if int32(check.got) != check.want {
			t.Fatalf("%s sizeof = %d, want libopus %d", check.name, check.got, check.want)
		}
	}
	if sizes[7] != maxLPCOrder {
		t.Fatalf("MAX_LPC_ORDER = %d, want libopus %d", maxLPCOrder, sizes[7])
	}
}

func TestSILKCNGResetKeepsSmoothedNLSFInLibopusWidth(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes := probeLibopusSILKCNGStateSizes(t)
	if sizes[1] != int32(unsafe.Sizeof(int16(0))) {
		t.Fatalf("libopus CNG_smth_NLSF_Q15 sizeof = %d, want int16 width", sizes[1])
	}
	var st decoderState
	st.lpcOrder = maxLPCOrder
	silkCNGReset(&st)
	stepQ15 := silkDiv32_16(32767, maxLPCOrder+1)
	accQ15 := int32(0)
	for i, got := range st.cng.smthNLSFQ15 {
		accQ15 += stepQ15
		if got != int16(accQ15) {
			t.Fatalf("smthNLSFQ15[%d] = %d, want %d", i, got, int16(accQ15))
		}
	}
}

func TestSILKCNGStateSizeHelperBuilds(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopusSILKCNGHelper.Path(buildLibopusSILKCNGHelper); err != nil {
		t.Fatal(fmt.Errorf("build silk cng helper: %w", err))
	}
}
