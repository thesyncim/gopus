//go:build amd64.v3 && !gopus_fixed_point

package silk

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	silkGainTweakInputMagic  = "GSGI"
	silkGainTweakOutputMagic = "GSGO"
)

type silkGainTweakCase struct {
	gain, gainMult, gainAdd float32
}

var silkGainTweakOracleHelper libopustest.HelperCache

func getSILKGainTweakOraclePath() (string, error) {
	return silkGainTweakOracleHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "silk gain tweak",
		OutputBase:   "gopus_libopus_silk_gain_tweak",
		SourceFile:   "libopus_silk_gain_tweak_info.c",
		ProbeRelPath: "silk/float/main_FLP.h",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes:  []string{"celt", "silk", "silk/float"},
		SIMDRef:      silkLPCOracleUsesAVX2(),
	})
}

func probeSILKGainTweakOracle(cases []silkGainTweakCase) ([]float32, error) {
	path, err := getSILKGainTweakOraclePath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(silkGainTweakInputMagic, 0, uint32(len(cases)))
	for _, tc := range cases {
		payload.Float32(tc.gain)
		payload.Float32(tc.gainMult)
		payload.Float32(tc.gainAdd)
	}
	reader, err := libopustest.RunOracle(path, payload.Bytes(), "silk gain tweak", silkGainTweakOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKGainTweakFMA32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x53494c4b))
	cases := make([]silkGainTweakCase, 260)
	cases[0] = silkGainTweakCase{gain: 136.0, gainMult: 1.0 / 65536.0, gainAdd: 0.64}
	cases[1] = silkGainTweakCase{gain: 136.125, gainMult: 1.0 / 64.0, gainAdd: 0.64}
	cases[2] = silkGainTweakCase{gain: 32768.0, gainMult: 1.0 / 32768.0, gainAdd: 0.64}
	cases[3] = silkGainTweakCase{gain: 0.125, gainMult: 1.75, gainAdd: 0.75}
	normal := func(minExponent, maxExponent int) float32 {
		exponent := uint32(minExponent + rng.Intn(maxExponent-minExponent+1))
		return math.Float32frombits(exponent<<23 | rng.Uint32()&0x7fffff)
	}
	for i := 4; i < len(cases); i++ {
		// Cover positive finite source-domain values through int16-scale gains,
		// 2^-16..4 multipliers, and offsets through 1.0 with non-grid mantissas.
		cases[i] = silkGainTweakCase{
			gain:     normal(110, 142),
			gainMult: normal(111, 128),
			gainAdd:  normal(116, 126),
		}
	}
	want, err := probeSILKGainTweakOracle(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk gain tweak", err)
	}
	separateWitness := false
	for i, tc := range cases {
		got := silkGainTweak32(tc.gain, tc.gainMult, tc.gainAdd)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("case%d gain tweak=%08x C=%08x", i, math.Float32bits(got), math.Float32bits(want[i]))
		}
		if math.Float32bits(noFMA32(tc.gain, tc.gainMult)+tc.gainAdd) != math.Float32bits(want[i]) {
			separateWitness = true
		}
	}
	if !separateWitness {
		t.Fatal("oracle corpus did not distinguish the separate multiply/add from the contracted result")
	}
}

func TestSILKGainTweak32ZeroAlloc(t *testing.T) {
	gains := [maxNbSubfr]float32{0.27182818, 0.31415927, 0.5772157, 0.6931472}
	const gainMult, gainAdd float32 = 0.8123457, 0.6543211
	var sink float32
	for i := range gains {
		gains[i] = silkGainTweak32(gains[i], gainMult, gainAdd)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		for i := range gains {
			gains[i] = silkGainTweak32(gains[i], gainMult, gainAdd)
		}
		sink = gains[0]
	}); allocs != 0 {
		t.Fatalf("SILK gain tweak loop allocated %v times", allocs)
	}
	if math.Float32bits(sink) == 0 {
		t.Fatalf("unexpected zero gain sink: %v", sink)
	}
}
