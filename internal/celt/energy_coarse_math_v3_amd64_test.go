//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const celtCoarseEnergyOracleStorage = 512

var celtCoarseEnergyOracleHelper libopustest.HelperCache
var celtCoarseEnergyAllocSink float32

type celtCoarseEnergyOracleCase struct {
	channels int
	bands    int
	lm       int
	intra    bool
	initial  []float32
	qi       []int32
}

func celtCoarseEnergyOracleCases() []celtCoarseEnergyOracleCase {
	const bands = MaxBands
	cases := make([]celtCoarseEnergyOracleCase, 0, 16)
	for lm := range 4 {
		for intra := range 2 {
			for channels := 1; channels <= 2; channels++ {
				c := celtCoarseEnergyOracleCase{
					channels: channels,
					bands:    bands,
					lm:       lm,
					intra:    intra != 0,
					initial:  make([]float32, channels*bands),
					qi:       make([]int32, channels*bands),
				}
				for ch := range channels {
					for band := range bands {
						index := ch*bands + band
						mantissa := uint32((band*0x1d35 + ch*0x4a9 + lm*0x137 + intra*0x8d1) & 0x007fffff)
						magnitude := uint32(0x40000000)
						if band%3 == 1 {
							magnitude = 0x40800000
						} else if band%3 == 2 {
							magnitude = 0x41000000
						}
						energy := -math.Float32frombits(magnitude | mantissa)
						if band == 0 {
							energy = -10
						} else if band == 1 {
							energy = -9
						}
						c.initial[index] = energy
						c.qi[index] = int32((band*7+ch*11+lm*5+intra*13)%19 - 9)
					}
				}
				cases = append(cases, c)
			}
		}
	}
	return cases
}

func buildCELTCoarseEnergyV3Helper() (string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	if variant != libopustooling.LibopusReferenceScalar && variant != libopustooling.LibopusReferenceSIMD {
		return "", fmt.Errorf("coarse-energy float oracle selected unsupported libopus variant %s", variant)
	}
	cfg := libopustest.CHelperConfig{
		Label:       "CELT v3 coarse-energy decoder",
		OutputBase:  "gopus_libopus_celt_coarse_energy_f32",
		SourceFile:  "libopus_celt_coarse_energy_f32_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		SIMDRef:     variant == libopustooling.LibopusReferenceSIMD,
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return celtCoarseEnergyOracleHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(cfg)
	})
}

func TestCELTV3CoarseEnergyMatchesLibopusKernel(t *testing.T) {
	if got := requirePairedCELTOracleMode(t); got != libopustooling.LibopusReferenceScalar && got != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("paired CELT oracle variant=%s, want scalar or SIMD", got)
	}
	libopustest.RequireOracle(t)
	cases := celtCoarseEnergyOracleCases()
	fusedPredictionWitnesses := 0
	for _, c := range cases {
		if c.intra {
			continue
		}
		alpha := float32(AlphaCoef[c.lm])
		beta := float32(BetaCoefInter[c.lm])
		var prev [2]float32
		for band := range c.bands {
			for ch := range c.channels {
				old := c.initial[ch*c.bands+band]
				if old < -9 {
					old = -9
				}
				q := float32(c.qi[ch*c.bands+band]) * float32(DB6)
				separateProduct := celtCoarseEnergySeparateMul32(alpha, old)
				separate := separateProduct + prev[ch] + q
				fused := opusmath.FMA32(alpha, old, prev[ch]) + q
				if math.Float32bits(separate) != math.Float32bits(fused) {
					fusedPredictionWitnesses++
				}
				prev[ch] = opusmath.FMA32(-beta, q, prev[ch]+q)
			}
		}
	}
	if fusedPredictionWitnesses == 0 {
		t.Fatal("oracle inputs do not distinguish the v3 fused prediction from separate MUL+ADD")
	}
	payload := libopustest.NewOraclePayload("GCEI", uint32(len(cases)))
	for _, c := range cases {
		payload.U32(uint32(c.channels))
		payload.U32(uint32(c.bands))
		payload.U32(uint32(c.lm))
		if c.intra {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(celtCoarseEnergyOracleStorage)
		payload.Float32s(c.initial...)
		payload.I32s(c.qi...)
	}

	binPath, err := buildCELTCoarseEnergyV3Helper()
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT v3 coarse-energy decoder", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT v3 coarse-energy decoder", "GCEO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT v3 coarse-energy decoder", err)
	}
	if count := reader.Count(len(cases)); count != len(cases) {
		t.Fatalf("C case count=%d want %d", count, len(cases))
	}

	var allocDecoder *Decoder
	var allocRangeDecoder *rangecoding.Decoder
	var allocPacket []byte
	var allocCase celtCoarseEnergyOracleCase
	for caseIndex, c := range cases {
		if got := int(reader.U32()); got != c.channels {
			t.Fatalf("case %d C channels=%d want %d", caseIndex, got, c.channels)
		}
		if got := int(reader.U32()); got != c.bands {
			t.Fatalf("case %d C bands=%d want %d", caseIndex, got, c.bands)
		}
		if got := int(reader.U32()); got != c.lm {
			t.Fatalf("case %d C LM=%d want %d", caseIndex, got, c.lm)
		}
		wantIntra := 0
		if c.intra {
			wantIntra = 1
		}
		if got := int(reader.U32()); got != wantIntra {
			t.Fatalf("case %d C intra=%d want %d", caseIndex, got, wantIntra)
		}
		if got := int(reader.U32()); got != celtCoarseEnergyOracleStorage {
			t.Fatalf("case %d C storage=%d want %d", caseIndex, got, celtCoarseEnergyOracleStorage)
		}
		packet := reader.Bytes(celtCoarseEnergyOracleStorage)
		if len(packet) != celtCoarseEnergyOracleStorage {
			t.Fatalf("case %d C packet length=%d", caseIndex, len(packet))
		}
		want := make([]float32, c.channels*c.bands)
		for i := range want {
			want[i] = reader.Float32()
		}
		wantTell := int(reader.U32())
		if err := reader.Err(); err != nil {
			t.Fatalf("case %d C result: %v", caseIndex, err)
		}

		dec := NewDecoder(c.channels)
		stride := dec.predStride()
		for ch := range c.channels {
			for band := range c.bands {
				dec.prevEnergy[ch*stride+band] = celtGLog(c.initial[ch*c.bands+band])
			}
		}
		rd := &rangecoding.Decoder{}
		rd.Init(packet)
		dec.rangeDecoder = rd
		got := dec.decodeCoarseEnergyGLogInto(make([]celtGLog, len(want)), c.bands, c.intra, c.lm)
		if gotTell := rd.Tell(); gotTell != wantTell {
			t.Fatalf("case %d range tell=%d want C=%d", caseIndex, gotTell, wantTell)
		}
		for i := range want {
			if gotBits, wantBits := math.Float32bits(float32(got[i])), math.Float32bits(want[i]); gotBits != wantBits {
				t.Fatalf("case %d channels=%d bands=%d LM=%d intra=%t energy[%d]=%08x want C %08x",
					caseIndex, c.channels, c.bands, c.lm, c.intra, i, gotBits, wantBits)
			}
		}
		if err := reader.Err(); err != nil {
			t.Fatalf("case %d output parse: %v", caseIndex, err)
		}
		if caseIndex == 0 {
			allocDecoder = dec
			allocRangeDecoder = rd
			allocPacket = packet
			allocCase = c
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	if allocDecoder == nil || allocRangeDecoder == nil || len(allocPacket) == 0 {
		t.Fatal("allocation case was not initialized")
	}

	allocDst := make([]celtGLog, allocCase.channels*allocCase.bands)
	allocRangeDecoder.Init(allocPacket)
	allocDecoder.decodeCoarseEnergyGLogInto(allocDst, allocCase.bands, allocCase.intra, allocCase.lm)
	allocs := testing.AllocsPerRun(100, func() {
		allocRangeDecoder.Init(allocPacket)
		got := allocDecoder.decodeCoarseEnergyGLogInto(allocDst, allocCase.bands, allocCase.intra, allocCase.lm)
		celtCoarseEnergyAllocSink = float32(got[len(got)-1])
	})
	if allocs != 0 {
		t.Fatalf("steady-state coarse-energy decode allocations=%g want 0", allocs)
	}
}

//go:noinline
func celtCoarseEnergySeparateMul32(a, b float32) float32 {
	return a * b
}
