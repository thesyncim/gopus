package opusmath

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var sinF32OracleHelper libopustest.HelperCache

func TestSinF32MatchesSamePlatformCLibm(t *testing.T) {
	libopustest.RequireOracle(t)
	inputs := []uint32{
		0x00000000, 0x80000000, // signed zero
		math.Float32bits(0.5), math.Float32bits(-0.5),
		math.Float32bits(1), math.Float32bits(-1),
		0x7f800000, 0xff800000, // signed infinity
		0x7fc00000, 0xffc00000, // quiet NaN payloads
		0x7f800001, 0xff800001, // signaling NaN payloads
	}
	bin, err := sinF32OracleHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "same-platform C sinf32 oracle",
			OutputBase: "gopus_sin_f32_oracle",
			SourceFile: "libopus_sin_f32_oracle.c",
			CFlags:     []string{"-O2"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "same-platform C sinf32 oracle", err)
	}
	payload := libopustest.NewOraclePayloadVersion("GSII", 1, uint32(len(inputs)))
	for _, bits := range inputs {
		payload.U32(bits)
	}
	wire, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		t.Fatalf("run C sine oracle: %v", err)
	}
	r, err := libopustest.NewOracleReader("same-platform C sinf32 oracle", "GSIO", wire)
	if err != nil {
		t.Fatal(err)
	}
	count := r.Count(len(inputs))
	for i := 0; i < count; i++ {
		want := r.U32()
		input := math.Float32frombits(inputs[i])
		got := math.Float32bits(SinF32(input))
		if got != want {
			t.Errorf("SinF32(%08x)=%08x want same-platform C sin=%08x", inputs[i], got, want)
		}
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
