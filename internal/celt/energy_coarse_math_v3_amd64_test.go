//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext

package celt

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"testing"
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
	requireCELTV3OracleTarget(t)
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
	payload := libopustest.NewOraclePayloadVersion("GCEI", 2, uint32(len(cases)))
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
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "CELT v3 coarse-energy decoder", "GCEO", 2)
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
		wantRange := reader.U32()
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
		if gotRange := rd.Range(); gotRange != wantRange {
			t.Fatalf("case %d range=%08x want C=%08x", caseIndex, gotRange, wantRange)
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
	rng     uint32
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
	payload := libopustest.NewOraclePayloadVersion("GCQI", 2, uint32(len(cases)))
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
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "CELT v3 coarse-energy encoder", "GCQO", 2)
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
		results[i].rng = reader.U32()
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
				t.Run("encodeCoarseEnergyPass", func(t *testing.T) {
					compareCELTQuantCoarsePass(t, c, results[i])
				})
				t.Run("quantCoarseEnergyImpl", func(t *testing.T) {
					compareCELTQuantCoarseTrial(t, c, results[i])
				})
				t.Run("EncodeCoarseEnergyRange/full", func(t *testing.T) {
					compareCELTQuantCoarseFullRange(t, c, results[i])
				})
			} else {
				t.Run("EncodeCoarseEnergyRange/partial", func(t *testing.T) {
					compareCELTQuantCoarsePartialRange(t, c, results[i])
				})
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
		celtQuantCoarseOracleStorage*8, c.maxDecay, true,
	)
	tell, rng := re.Tell(), re.Range()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "encodeCoarseEnergyPass", got, enc.scratch.coarseError, packet, badness, true, tell, rng, int32(re.Error()), want)
}

func compareCELTQuantCoarseTrial(t *testing.T, c celtQuantCoarseOracleCase, want celtQuantCoarseOracleResult) {
	t.Helper()
	old := float32sToGLogs(c.initial)
	errorValues := make([]celtGLog, len(old))
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	re.Init(buffer)
	prob := eProbModel[c.lm][boolInt(c.intra)][:]
	tell := re.Tell()
	seedCELTQuantCoarseOracleIntraFlag(re, c)
	badness := quantCoarseEnergyImpl(
		re, c.start, c.end, float32sToGLogs(c.energies), old,
		celtQuantCoarseOracleStorage*8, tell, prob, errorValues,
		c.channels, c.lm, c.intra, c.maxDecay, false, c.bands,
	)
	tell, rng := re.Tell(), re.Range()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "quantCoarseEnergyImpl", old, errorValues, packet, badness, true, tell, rng, int32(re.Error()), want)
}

func compareCELTQuantCoarseFullRange(t *testing.T, c celtQuantCoarseOracleCase, want celtQuantCoarseOracleResult) {
	t.Helper()
	enc := NewEncoder(c.channels)
	copyQuantCoarseInitial(enc, c)
	buffer := make([]byte, celtQuantCoarseOracleStorage)
	re := &rangecoding.Encoder{}
	re.Init(buffer)
	enc.SetRangeEncoder(re)
	seedCELTQuantCoarseOracleIntraFlag(re, c)
	got := enc.EncodeCoarseEnergyRange(float32sToGLogs(c.energies), c.start, c.end, c.intra, c.lm)
	tell, rng := re.Tell(), re.Range()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "EncodeCoarseEnergyRange(full)", got, enc.scratch.coarseError, packet, 0, false, tell, rng, int32(re.Error()), want)
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
	seedCELTQuantCoarseOracleIntraFlag(re, c)
	got := enc.EncodeCoarseEnergyRange(float32sToGLogs(c.energies), c.start, c.end, c.intra, c.lm)
	if gotLen, wantLen := len(got), c.channels*c.end; gotLen != wantLen {
		t.Fatalf("EncodeCoarseEnergyRange(partial) compact result length=%d want %d (channels %d x end band %d)", gotLen, wantLen, c.channels, c.end)
	}
	tell, rng := re.Tell(), re.Range()
	packet := re.Done()
	assertCELTQuantCoarseResult(t, c, "EncodeCoarseEnergyRange(partial)", got, enc.scratch.coarseError, packet, 0, false, tell, rng, int32(re.Error()), want)
}

// quant_coarse_energy_impl owns this bit in C. The Go frame caller writes the
// same bit before entering EncodeCoarseEnergyRange, while these direct-kernel
// tests seed it at that caller boundary.
func seedCELTQuantCoarseOracleIntraFlag(re *rangecoding.Encoder, c celtQuantCoarseOracleCase) {
	if re.Tell()+3 <= re.StorageBits() {
		re.EncodeBit(boolInt(c.intra), 3)
	}
}

func copyQuantCoarseInitial(enc *Encoder, c celtQuantCoarseOracleCase) {
	stride := enc.predStride()
	for ch := range c.channels {
		for band := range c.bands {
			enc.prevEnergy[ch*stride+band] = celtGLog(c.initial[ch*c.bands+band])
		}
	}
}

func assertCELTQuantCoarseResult(t *testing.T, c celtQuantCoarseOracleCase, path string, gotOld, gotError []celtGLog, packet []byte, badness int, checkBadness bool, tell int, rng uint32, encError int32, want celtQuantCoarseOracleResult) {
	t.Helper()
	if checkBadness && badness != want.badness {
		t.Fatalf("%s badness=%d want C %d", path, badness, want.badness)
	}
	if tell != want.tell {
		t.Fatalf("%s range tell=%d want C %d", path, tell, want.tell)
	}
	if rng != want.rng {
		t.Fatalf("%s range=%08x want C %08x", path, rng, want.rng)
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
