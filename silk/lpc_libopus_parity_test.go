package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKLPCInputMagic  = "GSLI"
	libopusSILKLPCOutputMagic = "GSLO"

	libopusSILKLPCModeBurgModified      = uint32(0)
	libopusSILKLPCModeLPCAnalysisFilter = uint32(1)
	libopusSILKLPCModeInnerProduct      = uint32(2)
	libopusSILKLPCModeEnergy            = uint32(3)
	libopusSILKLPCModeFindLPC           = uint32(4)
)

var libopusSILKLPCHelper libopustest.HelperCache

func getLibopusSILKLPCHelperPath() (string, error) {
	return libopusSILKLPCHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "silk lpc",
		OutputBase:  "gopus_libopus_silk_lpc",
		SourceFile:  "libopus_silk_lpc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

type libopusSILKBurgCase struct {
	name       string
	subfrLen   int
	nbSubfr    int
	order      int
	minInvGain float32
	x          []float32
}

type libopusSILKBurgResult struct {
	resNrg float32
	a      []float32
}

type libopusSILKLPCFilterCase struct {
	name  string
	order int
	pred  []float32
	x     []float32
}

type libopusSILKInnerProductCase struct {
	name string
	a    []float32
	b    []float32
}

type libopusSILKEnergyCase struct {
	name string
	x    []float32
}

type libopusSILKFindLPCCase struct {
	name                 string
	subfrLength          int
	nbSubfr              int
	order                int
	useInterpolatedNLSFs bool
	firstFrameAfterReset bool
	minInvGain           float32
	prevNLSF             []int16
	x                    []float32
}

type libopusSILKFindLPCResult struct {
	interpIdx int
	nlsf      []int16
}

func probeLibopusSILKBurgModified(cases []libopusSILKBurgCase) ([]libopusSILKBurgResult, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeBurgModified, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLen))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.order))
		payload.Float32(tc.minInvGain)
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk lpc burg", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]libopusSILKBurgResult, count)
	for i := range out {
		out[i].resNrg = reader.Float32()
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].a = make([]float32, order)
		for j := 0; j < 16; j++ {
			v := reader.Float32()
			if j < order {
				out[i].a[j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKLPCAnalysisFilter(cases []libopusSILKLPCFilterCase) ([][]float32, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeLPCAnalysisFilter, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.order))
		payload.Float32s(tc.pred...)
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk lpc analysis filter", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		length := int(reader.U32())
		if length <= 0 || length > 512 {
			return nil, fmt.Errorf("helper length=%d", length)
		}
		out[i] = make([]float32, length)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKInnerProductFLP(cases []libopusSILKInnerProductCase) ([]float64, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeInnerProduct, uint32(len(cases)))
	for _, tc := range cases {
		if len(tc.a) != len(tc.b) {
			return nil, fmt.Errorf("%s: a len=%d b len=%d", tc.name, len(tc.a), len(tc.b))
		}
		payload.U32(uint32(len(tc.a)))
		payload.Float32s(tc.a...)
		payload.Float32s(tc.b...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk inner product flp", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]float64, count)
	for i := range out {
		out[i] = reader.Float64()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKEnergyFLP(cases []libopusSILKEnergyCase) ([]float64, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeEnergy, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk energy flp", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]float64, count)
	for i := range out {
		out[i] = reader.Float64()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKFindLPCFLP(cases []libopusSILKFindLPCCase) ([]libopusSILKFindLPCResult, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeFindLPC, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.order))
		if tc.useInterpolatedNLSFs {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		if tc.firstFrameAfterReset {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.Float32(tc.minInvGain)
		for i := 0; i < 16; i++ {
			var v int16
			if i < len(tc.prevNLSF) {
				v = tc.prevNLSF[i]
			}
			payload.I32(int32(v))
		}
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk find lpc flp", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]libopusSILKFindLPCResult, count)
	for i := range out {
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].interpIdx = int(reader.U32())
		out[i].nlsf = make([]int16, order)
		for j := 0; j < 16; j++ {
			v := int16(reader.I32())
			if j < order {
				out[i].nlsf[j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKBurgModifiedFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKBurgCase{
		{name: "order10_four_subframes", subfrLen: 90, nbSubfr: 4, order: 10, minInvGain: 1e-4, x: silkBurgOracleSignal(360, 0x12345678)},
		{name: "order16_four_subframes", subfrLen: 96, nbSubfr: 4, order: 16, minInvGain: 1e-4, x: silkBurgOracleSignal(384, 0x9abcdef0)},
		{name: "order16_two_subframes", subfrLen: 96, nbSubfr: 2, order: 16, minInvGain: 5e-5, x: silkBurgOracleSignal(192, 0x31415926)},
		{name: "order10_tight_gain", subfrLen: 80, nbSubfr: 2, order: 10, minInvGain: 0.08, x: silkBurgOracleSignal(160, 0x27182818)},
	}
	want, err := probeLibopusSILKBurgModified(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lpc burg", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := &Encoder{}
			gotA, gotRes := enc.burgModifiedFLPZeroAllocF32(tc.x, tc.minInvGain, tc.subfrLen, tc.nbSubfr, tc.order)
			gotRes32 := float32(gotRes)
			if math.Float32bits(gotRes32) != math.Float32bits(want[i].resNrg) {
				t.Fatalf("resNrg=%08x %.10g want %08x %.10g",
					math.Float32bits(gotRes32), gotRes32,
					math.Float32bits(want[i].resNrg), want[i].resNrg)
			}
			if len(gotA) != len(want[i].a) {
				t.Fatalf("A len=%d want %d", len(gotA), len(want[i].a))
			}
			for j := range gotA {
				got := float32(gotA[j])
				if math.Float32bits(got) != math.Float32bits(want[i].a[j]) {
					t.Fatalf("A[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got), got,
						math.Float32bits(want[i].a[j]), want[i].a[j])
				}
			}
		})
	}
}

func TestSILKLPCAnalysisFilterFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKLPCFilterCase{
		{name: "order10", order: 10, pred: silkLPCOraclePred(10, 0.44), x: silkBurgOracleSignal(160, 0x10203040)},
		{name: "order16", order: 16, pred: silkLPCOraclePred(16, 0.38), x: silkBurgOracleSignal(224, 0x50607080)},
		{name: "order16_short", order: 16, pred: silkLPCOraclePred(16, 0.25), x: silkBurgOracleSignal(64, 0xa0b0c0d0)},
	}
	want, err := probeLibopusSILKLPCAnalysisFilter(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lpc analysis filter", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, len(tc.x))
			lpcAnalysisFilterF32(got, tc.pred, tc.x, len(tc.x), tc.order)
			if len(got) != len(want[i]) {
				t.Fatalf("residual len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("residual[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got[j]), got[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

func TestSILKInnerProductFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKInnerProductCase{
		{name: "one", a: []float32{0.25}, b: []float32{-16}},
		{name: "four", a: silkFLPOracleSignal(4, 0xabcdef01, 1.0), b: silkFLPOracleSignal(4, 0x01020304, 0.75)},
		{name: "five", a: silkFLPOracleSignal(5, 0xabcdef02, 1.5), b: silkFLPOracleSignal(5, 0x10203040, 0.5)},
		{name: "pitch_len_31", a: silkFLPOracleSignal(31, 0xabcdef03, 42.0), b: silkFLPOracleSignal(31, 0x20304050, 17.0)},
		{name: "pitch_len_80", a: silkFLPOracleSignal(80, 0xabcdef04, 32768.0), b: silkFLPOracleSignal(80, 0x30405060, 32768.0)},
		{name: "pitch_len_120", a: silkFLPOracleSignal(120, 0xabcdef05, 4096.0), b: silkFLPOracleSignal(120, 0x40506070, 8192.0)},
		{name: "order_sensitive", a: []float32{1e20, 1, -1e20, 1, -1e20, 1, 1e20, 1}, b: []float32{1, 1, 1, 1, 1, 1, 1, 1}},
	}
	want, err := probeLibopusSILKInnerProductFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk inner product flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := innerProductFLP(tc.a, tc.b, len(tc.a))
			if math.Float64bits(got) != math.Float64bits(want[i]) {
				t.Fatalf("innerProduct=%016x %.17g want %016x %.17g",
					math.Float64bits(got), got,
					math.Float64bits(want[i]), want[i])
			}
		})
	}
}

func TestSILKEnergyFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKEnergyCase{
		{name: "one", x: []float32{-16}},
		{name: "four", x: silkFLPOracleSignal(4, 0x55667788, 1.0)},
		{name: "five", x: silkFLPOracleSignal(5, 0x66778899, 1.5)},
		{name: "pitch_len_31", x: silkFLPOracleSignal(31, 0x778899aa, 42.0)},
		{name: "pitch_len_80", x: silkFLPOracleSignal(80, 0x8899aabb, 32768.0)},
		{name: "pitch_len_120", x: silkFLPOracleSignal(120, 0x99aabbcc, 4096.0)},
		{name: "wide_dynamic_range", x: []float32{1e10, 1, -1e10, 1, 1, -1e10, 1, 1e10}},
	}
	want, err := probeLibopusSILKEnergyFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk energy flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := energyFLP(tc.x)
			if math.Float64bits(got) != math.Float64bits(want[i]) {
				t.Fatalf("energy=%016x %.17g want %016x %.17g",
					math.Float64bits(got), got,
					math.Float64bits(want[i]), want[i])
			}
		})
	}
}

func TestSILKFindLPCFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKFindLPCCase{
		{
			name:                 "order10_first_frame",
			subfrLength:          60,
			nbSubfr:              4,
			order:                10,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: true,
			minInvGain:           1e-4,
			prevNLSF:             silkFindLPCPrevNLSF(10, 0x12345678),
			x:                    silkBurgOracleSignal(4*(60+10), 0x10203040),
		},
		{
			name:                 "order10_interpolation_enabled",
			subfrLength:          60,
			nbSubfr:              4,
			order:                10,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: false,
			minInvGain:           2e-4,
			prevNLSF:             silkFindLPCPrevNLSF(10, 0x22334455),
			x:                    silkBurgOracleSignal(4*(60+10), 0x31415926),
		},
		{
			name:                 "order16_low_complexity",
			subfrLength:          80,
			nbSubfr:              4,
			order:                16,
			useInterpolatedNLSFs: false,
			firstFrameAfterReset: false,
			minInvGain:           1e-4,
			prevNLSF:             silkFindLPCPrevNLSF(16, 0xabcdef01),
			x:                    silkBurgOracleSignal(4*(80+16), 0x50607080),
		},
		{
			name:                 "order16_interpolation_enabled",
			subfrLength:          80,
			nbSubfr:              4,
			order:                16,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: false,
			minInvGain:           3e-4,
			prevNLSF:             silkFindLPCPrevNLSF(16, 0x0badf00d),
			x:                    silkBurgOracleSignal(4*(80+16), 0x90abcdef),
		},
		{
			name:                 "order16_two_subframes",
			subfrLength:          80,
			nbSubfr:              2,
			order:                16,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: false,
			minInvGain:           5e-5,
			prevNLSF:             silkFindLPCPrevNLSF(16, 0x13579bdf),
			x:                    silkBurgOracleSignal(2*(80+16), 0x2468ace0),
		},
	}
	want, err := probeLibopusSILKFindLPCFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk find lpc flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bw := BandwidthWideband
			if tc.order == 10 {
				bw = BandwidthMediumband
			}
			enc := NewEncoder(bw)
			if tc.useInterpolatedNLSFs {
				enc.SetComplexity(10)
			} else {
				enc.SetComplexity(0)
			}
			if !tc.firstFrameAfterReset {
				enc.MarkEncoded()
			}
			copy(enc.prevLSFQ15, tc.prevNLSF)

			_, gotNLSF, gotInterp := enc.computeLPCAndNLSFWithInterp(tc.x, tc.nbSubfr, tc.subfrLength, float64(tc.minInvGain))
			if gotInterp != want[i].interpIdx {
				t.Fatalf("interpIdx=%d want %d", gotInterp, want[i].interpIdx)
			}
			if len(gotNLSF) != len(want[i].nlsf) {
				t.Fatalf("NLSF len=%d want %d", len(gotNLSF), len(want[i].nlsf))
			}
			for j := range gotNLSF {
				if gotNLSF[j] != want[i].nlsf[j] {
					t.Fatalf("NLSF[%d]=%d want %d", j, gotNLSF[j], want[i].nlsf[j])
				}
			}
		})
	}
}

func silkBurgOracleSignal(n int, seed uint32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1664525 + 1013904223
		noise := float32(int32(state>>8)&0xffff-32768) / 32768.0
		ramp := float32((i%31)-15) * (1.0 / 256.0)
		x[i] = 0.35*noise + ramp
	}
	return x
}

func silkFindLPCPrevNLSF(order int, seed uint32) []int16 {
	out := make([]int16, order)
	state := seed
	prev := 900
	for i := range out {
		state = state*1664525 + 1013904223
		step := 1300 + int((state>>24)&0x3ff)
		prev += step
		limit := 32000 - (order-i-1)*700
		if prev > limit {
			prev = limit
		}
		out[i] = int16(prev)
	}
	return out
}

func silkFLPOracleSignal(n int, seed uint32, scale float32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1664525 + 1013904223
		mant := int32(state>>8)&0xffff - 32768
		x[i] = scale * float32(mant) / 32768.0
	}
	return x
}

func silkLPCOraclePred(order int, gain float32) []float32 {
	pred := make([]float32, order)
	for i := range pred {
		sign := float32(1)
		if i&1 != 0 {
			sign = -1
		}
		pred[i] = sign * gain / float32(i+2)
	}
	return pred
}
