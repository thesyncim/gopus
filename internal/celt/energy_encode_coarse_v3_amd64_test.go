//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext

package celt

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	celtQuantCoarseOracleStorage  = 512
	celtQuantCoarseOracleMaxDecay = float32(16)
)

var celtQuantCoarseOracleHelper libopustest.HelperCache
var celtQuantCoarseAllocSink float32

type celtQuantCoarseOracleCase struct {
	name     string
	channels int
	bands    int
	start    int
	end      int
	lm       int
	intra    bool
	maxDecay float32
	energies []float32
	initial  []float32
}

type celtQuantCoarseOracleResult struct {
	packet  []byte
	badness int
	tell    int
	err     int32
	old     []float32
	error   []float32
}

func celtQuantCoarseOracleCases(t *testing.T) []celtQuantCoarseOracleCase {
	t.Helper()
	cases := make([]celtQuantCoarseOracleCase, 0, 11)
	for lm := range 4 {
		for intra := range 2 {
			cases = append(cases, makeCELTQuantCoarseOracleCase(
				t, fmt.Sprintf("full_lm%d_intra%d", lm, intra), 2, MaxBands, 0, MaxBands, lm, intra != 0,
			))
		}
	}
	cases = append(cases,
		makeCELTQuantCoarseOracleCase(t, "mono_lm0_inter", 1, MaxBands, 0, MaxBands, 0, false),
		makeCELTQuantCoarseOracleCase(t, "partial_inter", 2, 17, 4, 17, 1, false),
		makeCELTQuantCoarseOracleCase(t, "partial_intra", 2, 17, 4, 17, 3, true),
	)
	return cases
}

func makeCELTQuantCoarseOracleCase(t *testing.T, name string, channels, bands, start, end, lm int, intra bool) celtQuantCoarseOracleCase {
	t.Helper()
	c := celtQuantCoarseOracleCase{
		name: name, channels: channels, bands: bands, start: start, end: end,
		lm: lm, intra: intra, maxDecay: celtQuantCoarseOracleMaxDecay,
		energies: make([]float32, channels*bands), initial: make([]float32, channels*bands),
	}
	for i := range c.initial {
		mantissa := uint32((i*0x1d35 + lm*0x4a9 + start*0x137 + end*0x8d1) & 0x007fffff)
		c.initial[i] = math.Float32frombits(0xc0000000 | mantissa)
	}

	coef := float32(AlphaCoef[lm])
	beta := float32(BetaCoefInter[lm])
	if intra {
		coef = 0
		beta = float32(BetaIntra)
	}
	var prev [2]float32
	for band := start; band < end; band++ {
		for ch := range channels {
			idx := ch*bands + band
			old := c.initial[idx]
			if old < -9 {
				old = -9
			}
			// Keep symbols in the ordinary Laplace range. The deterministic
			// non-grid energy values distinguish separate from fused reconstructed
			// energy without exercising ec_laplace_encode's extreme-symbol clipping.
			qi := (band*7+ch*11+lm*5+start*3+end)%19 - 9
			product := celtQuantCoarseSeparateMul32(coef, old)
			prediction := product + prev[ch]
			c.energies[idx] = prediction + float32(qi) + 0.25

			f := c.energies[idx] - product - prev[ch]
			gotQI := floor32ToInt(f + 0.5)
			if gotQI != qi {
				t.Fatalf("%s generated qi=%d, want %d at channel %d band %d", name, gotQI, qi, ch, band)
			}
			q := float32(gotQI)
			prevPlusQ := prev[ch] + q
			prev[ch] = opusmath.FMA32(-beta, q, prevPlusQ)
		}
	}
	for ch := range channels {
		for band := range start {
			idx := ch*bands + band
			c.energies[idx] = c.initial[idx]
		}
	}
	return c
}

func buildCELTV3QuantCoarseOracleHelper() (string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	if variant != libopustooling.LibopusReferenceScalar && variant != libopustooling.LibopusReferenceSIMD {
		return "", fmt.Errorf("coarse-energy encoder oracle selected unsupported libopus variant %s", variant)
	}
	cfg := libopustest.CHelperConfig{
		Label:       "CELT v3 coarse-energy encoder",
		OutputBase:  "gopus_libopus_celt_quant_coarse_energy_f32",
		SourceFile:  "libopus_celt_quant_coarse_energy_f32_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		SIMDRef:     variant == libopustooling.LibopusReferenceSIMD,
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return celtQuantCoarseOracleHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(cfg)
	})
}

func probeCELTV3QuantCoarseOracle(cases []celtQuantCoarseOracleCase) ([]celtQuantCoarseOracleResult, error) {
	binPath, err := buildCELTV3QuantCoarseOracleHelper()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GCQI", uint32(len(cases)))
	for _, c := range cases {
		payload.U32(uint32(c.channels))
		payload.U32(uint32(c.bands))
		payload.U32(uint32(c.start))
		payload.U32(uint32(c.end))
		payload.U32(uint32(c.lm))
		if c.intra {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(celtQuantCoarseOracleStorage)
		payload.Float32(c.maxDecay)
		payload.Float32s(c.energies...)
		payload.Float32s(c.initial...)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT v3 coarse-energy encoder", "GCQO")
	if err != nil {
		return nil, err
	}
	if count := reader.Count(len(cases)); count != len(cases) {
		return nil, fmt.Errorf("C case count=%d want %d", count, len(cases))
	}
	results := make([]celtQuantCoarseOracleResult, len(cases))
	for i, c := range cases {
		for _, want := range []uint32{uint32(c.channels), uint32(c.bands), uint32(c.start), uint32(c.end), uint32(c.lm), boolUint32(c.intra), celtQuantCoarseOracleStorage} {
			if got := reader.U32(); got != want {
				return nil, fmt.Errorf("case %s C metadata=%d want %d", c.name, got, want)
			}
		}
		packetLen := int(reader.U32())
		if packetLen < 0 || packetLen > celtQuantCoarseOracleStorage {
			return nil, fmt.Errorf("case %s C packet length=%d", c.name, packetLen)
		}
		results[i].badness = int(reader.I32())
		results[i].tell = int(reader.U32())
		results[i].err = reader.I32()
		results[i].packet = append([]byte(nil), reader.Bytes(packetLen)...)
		total := c.channels * c.bands
		results[i].old = make([]float32, total)
		results[i].error = make([]float32, total)
		for j := range total {
			results[i].old[j] = reader.Float32()
		}
		for j := range total {
			results[i].error[j] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return nil, fmt.Errorf("case %s C result: %w", c.name, err)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return results, nil
}

func boolUint32(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

func TestCELTV3QuantCoarseEnergyEncoderMatchesLibopusKernel(t *testing.T) {
	requireCELTV3OracleTarget(t)
	if got := requirePairedCELTOracleMode(t); got != libopustooling.LibopusReferenceScalar && got != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("paired CELT oracle variant=%s, want scalar or SIMD", got)
	}
	libopustest.RequireOracle(t)
	cases := celtQuantCoarseOracleCases(t)
	results, err := probeCELTV3QuantCoarseOracle(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT v3 coarse-energy encoder", err)
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.start == 0 {
				compareCELTQuantCoarsePass(t, c, results[i])
				compareCELTQuantCoarseTrial(t, c, results[i])
				compareCELTQuantCoarseFullRange(t, c, results[i])
			} else {
				compareCELTQuantCoarsePartialRange(t, c, results[i])
			}
		})
	}
	assertCELTQuantCoarseWitnesses(t, cases)
	assertCELTQuantCoarseZeroAllocs(t, cases[0])
}

func compareCELTQuantCoarsePass(t *testing.T, c celtQuantCoarseOracleCase, want celtQuantCoarseOracleResult) {
	t.Helper()
	enc := NewEncoder(c.channels)
	copyQuantCoarseInitial(enc, c)
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	re.Init(buffer)
	enc.SetRangeEncoder(re)
	got, badness := enc.encodeCoarseEnergyPass(
		float32sToGLogs(c.energies), c.start, c.end, c.intra, c.lm,
		celtQuantCoarseOracleStorage*8, c.maxDecay, false,
	)
	tell := re.Tell()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "encodeCoarseEnergyPass", got, enc.scratch.coarseError, packet, badness, true, tell, int32(re.Error()), want)
}

func compareCELTQuantCoarseTrial(t *testing.T, c celtQuantCoarseOracleCase, want celtQuantCoarseOracleResult) {
	t.Helper()
	old := float32sToGLogs(c.initial)
	errorValues := make([]celtGLog, len(old))
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	re.Init(buffer)
	prob := eProbModel[c.lm][boolInt(c.intra)][:]
	badness := quantCoarseEnergyImpl(
		re, c.start, c.end, float32sToGLogs(c.energies), old,
		celtQuantCoarseOracleStorage*8, re.Tell(), prob, errorValues,
		c.channels, c.lm, c.intra, c.maxDecay, false, c.bands,
	)
	tell := re.Tell()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "quantCoarseEnergyImpl", old, errorValues, packet, badness, true, tell, int32(re.Error()), want)
}

func compareCELTQuantCoarseFullRange(t *testing.T, c celtQuantCoarseOracleCase, want celtQuantCoarseOracleResult) {
	t.Helper()
	enc := NewEncoder(c.channels)
	copyQuantCoarseInitial(enc, c)
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	re.Init(buffer)
	enc.SetRangeEncoder(re)
	got := enc.EncodeCoarseEnergyRange(float32sToGLogs(c.energies), c.start, c.end, c.intra, c.lm)
	tell := re.Tell()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "EncodeCoarseEnergyRange(full)", got, enc.scratch.coarseError, packet, 0, false, tell, int32(re.Error()), want)
}

func compareCELTQuantCoarsePartialRange(t *testing.T, c celtQuantCoarseOracleCase, want celtQuantCoarseOracleResult) {
	t.Helper()
	enc := NewEncoder(c.channels)
	copyQuantCoarseInitial(enc, c)
	enc.coarseAvailableSet = true
	enc.coarseAvailableBytes = 128
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	re.Init(buffer)
	enc.SetRangeEncoder(re)
	got := enc.EncodeCoarseEnergyRange(float32sToGLogs(c.energies), c.start, c.end, c.intra, c.lm)
	if gotLen, wantLen := len(got), c.channels*c.end; gotLen != wantLen {
		t.Fatalf("EncodeCoarseEnergyRange(partial) compact result length=%d want %d (channels %d x end band %d)", gotLen, wantLen, c.channels, c.end)
	}
	tell := re.Tell()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "EncodeCoarseEnergyRange(partial)", got, enc.scratch.coarseError, packet, 0, false, tell, int32(re.Error()), want)
}

func copyQuantCoarseInitial(enc *Encoder, c celtQuantCoarseOracleCase) {
	stride := enc.predStride()
	for ch := range c.channels {
		for band := range c.bands {
			enc.prevEnergy[ch*stride+band] = celtGLog(c.initial[ch*c.bands+band])
		}
	}
}

func assertCELTQuantCoarseResult(t *testing.T, c celtQuantCoarseOracleCase, path string, gotOld, gotError []celtGLog, packet []byte, badness int, checkBadness bool, tell int, encError int32, want celtQuantCoarseOracleResult) {
	t.Helper()
	if checkBadness && badness != want.badness {
		t.Fatalf("%s badness=%d want C %d", path, badness, want.badness)
	}
	if tell != want.tell {
		t.Fatalf("%s range tell=%d want C %d", path, tell, want.tell)
	}
	if encError != want.err {
		t.Fatalf("%s encoder error=%d want C %d", path, encError, want.err)
	}
	if !bytes.Equal(packet, want.packet) {
		t.Fatalf("%s packet=%x want C %x", path, packet, want.packet)
	}
	if len(gotOld) != len(want.old) || len(gotError) != len(want.error) {
		t.Fatalf("%s result lengths energy=%d/%d error=%d/%d", path, len(gotOld), len(want.old), len(gotError), len(want.error))
	}
	for i := range gotOld {
		if gotBits, wantBits := math.Float32bits(float32(gotOld[i])), math.Float32bits(want.old[i]); gotBits != wantBits {
			t.Fatalf("%s %s old energy[%d]=%08x want C %08x", path, c.name, i, gotBits, wantBits)
		}
		if gotBits, wantBits := math.Float32bits(float32(gotError[i])), math.Float32bits(want.error[i]); gotBits != wantBits {
			t.Fatalf("%s %s error[%d]=%08x want C %08x", path, c.name, i, gotBits, wantBits)
		}
	}
}

func assertCELTQuantCoarseWitnesses(t *testing.T, cases []celtQuantCoarseOracleCase) {
	t.Helper()
	reconstructionWitnesses := 0
	for _, c := range cases {
		coef := float32(AlphaCoef[c.lm])
		beta := float32(BetaCoefInter[c.lm])
		if c.intra {
			coef = 0
			beta = float32(BetaIntra)
		}
		var prev [2]float32
		for band := c.start; band < c.end; band++ {
			for ch := range c.channels {
				idx := ch*c.bands + band
				old := c.initial[idx]
				if old < -9 {
					old = -9
				}
				product := celtQuantCoarseSeparateMul32(coef, old)
				f := c.energies[idx] - product - prev[ch]
				qi := floor32ToInt(f + 0.5)
				q := float32(qi)
				separate := product + prev[ch] + q
				fused := opusmath.FMA32(coef, old, prev[ch]) + q
				if math.Float32bits(separate) != math.Float32bits(fused) {
					reconstructionWitnesses++
				}
				prevPlusQ := prev[ch] + q
				fusedUpdate := opusmath.FMA32(-beta, q, prevPlusQ)
				prev[ch] = fusedUpdate
			}
		}
	}
	if reconstructionWitnesses == 0 {
		t.Fatal("oracle inputs do not distinguish separate reconstruction MUL/ADD from fused reconstruction")
	}
}

func assertCELTQuantCoarseZeroAllocs(t *testing.T, c celtQuantCoarseOracleCase) {
	t.Helper()
	enc := NewEncoder(c.channels)
	copyQuantCoarseInitial(enc, c)
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	enc.SetRangeEncoder(re)
	energies := float32sToGLogs(c.energies)
	encode := func() {
		re.Init(buffer)
		got := enc.EncodeCoarseEnergy(energies, c.end, c.intra, c.lm)
		celtQuantCoarseAllocSink = float32(got[len(got)-1])
	}
	encode()
	allocs := testing.AllocsPerRun(100, encode)
	if allocs != 0 {
		t.Fatalf("steady-state EncodeCoarseEnergy allocations=%g want 0", allocs)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

//go:noinline
func celtQuantCoarseSeparateMul32(a, b float32) float32 {
	return a * b
}
