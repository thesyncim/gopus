//go:build gopus_qext

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusQEXTThetaGainHelper libopustest.HelperCache

func buildLibopusQEXTThetaGainHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext theta gains",
		OutputBase:  "gopus_libopus_celt_qext_theta_gain",
		SourceFile:  "libopus_celt_qext_theta_gain_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTThetaGainsMatchLibopusWithoutExtensionCoder(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := libopusQEXTThetaGainHelper.Path(buildLibopusQEXTThetaGainHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext theta gains", err)
		return
	}
	if !celtQEXTFloatMath {
		t.Fatal("QEXT build must select Q30 stereo gains")
	}
	// The middle angles are the two theta RDO trials from a stereo 5 ms
	// packet's N=2 band. The context has no secondary coder at that band.
	thetaQ30 := [...]int32{0, 134217728, 317849600, 324927488, 536870912, 805306368, 1 << 30}
	payload := libopustest.NewOraclePayload("GQTG", uint32(len(thetaQ30)))
	for _, theta := range thetaQ30 {
		payload.U32(uint32(theta))
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext theta gains", "GQTO")
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.Count(len(thetaQ30)); got != len(thetaQ30) {
		t.Fatalf("theta gain count %d, want %d", got, len(thetaQ30))
	}
	for _, theta := range thetaQ30 {
		wantMid, wantSide := reader.Float32(), reader.Float32()
		gotMid, gotSide := thetaSplitGains(&splitCtx{ithetaQ30: int(theta)}, celtQEXTFloatMath)
		if math.Float32bits(gotMid) != math.Float32bits(wantMid) || math.Float32bits(gotSide) != math.Float32bits(wantSide) {
			t.Errorf("theta_q30=%d: Go mid/side %08x/%08x, C %08x/%08x", theta,
				math.Float32bits(gotMid), math.Float32bits(gotSide), math.Float32bits(wantMid), math.Float32bits(wantSide))
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
