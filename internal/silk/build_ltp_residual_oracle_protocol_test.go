package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	ltpResidualOracleInputMagic  = "GSLT"
	ltpResidualOracleOutputMagic = "GSLU"
	ltpResidualOracleVersion     = uint32(1)
	ltpResidualOracleMaxCases    = 8
	ltpResidualOracleMaxInput    = 4096
	ltpResidualOracleMaxSubframe = 384
	ltpResidualOracleMaxPre      = 16
	ltpResidualOracleMaxOutput   = 2048
)

var ltpResidualOracleHelper libopustest.HelperCache

func getLTPResidualOracleHelperPath() (string, error) {
	return ltpResidualOracleHelper.Path(buildLTPResidualOracleHelper)
}

func buildLTPResidualOracleHelper() (string, error) {
	useAVX2 := silkLPCOracleUsesAVX2()
	archive := libopustest.RefPath(".libs", "libopus.a")
	cflags := []string{"-DHAVE_CONFIG_H", "-O2"}
	if useAVX2 {
		archive = libopustest.SIMDRefPath(".libs", "libopus.a")
	}
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "SILK LTP residual filter",
		OutputBase:   "gopus_libopus_silk_ltp_residual",
		SourceFile:   "libopus_silk_ltp_residual_info.c",
		ProbeRelPath: "silk/float/main_FLP.h",
		CFlags:       cflags,
		RefIncludes:  []string{"celt", "silk", "silk/float", "src"},
		SIMDRef:      useAVX2,
		Libs:         []string{archive, "-lm"},
	})
}

type ltpResidualOracleInput struct {
	frameStart     int
	subframeLength int
	numSubframes   int
	preLength      int
	pitchBuffer    []float32 // C-domain, after buildLTPResidual's exact power-of-two scale.
	pitchLags      []int32
	invGains       []float32
	taps           []float32 // subframe-major, five float32 taps per subframe.
}

type ltpResidualOracleSubframe struct {
	pitchLag int32
	invGain  float32
	taps     [ltpOrderConst]float32
}

type ltpResidualOracleSample struct {
	x        float32
	lags     [ltpOrderConst]float32
	residual float32
}

type ltpResidualOracleResult struct {
	frameStart     int
	subframeLength int
	numSubframes   int
	preLength      int
	inputCount     int
	outputCount    int
	subframes      []ltpResidualOracleSubframe
	samples        []ltpResidualOracleSample
}

func encodeLTPResidualOracleInput(cases []ltpResidualOracleInput) ([]byte, error) {
	if len(cases) == 0 || len(cases) > ltpResidualOracleMaxCases {
		return nil, fmtLTPResidualOracle("input case count %d is outside 1..%d", len(cases), ltpResidualOracleMaxCases)
	}
	payload := libopustest.NewOraclePayload(ltpResidualOracleInputMagic, uint32(len(cases)))
	for index, record := range cases {
		if err := validateLTPResidualOracleInput(record); err != nil {
			return nil, fmtLTPResidualOracle("input case %d: %v", index, err)
		}
		payload.U32(uint32(record.frameStart))
		payload.U32(uint32(record.subframeLength))
		payload.U32(uint32(record.numSubframes))
		payload.U32(uint32(record.preLength))
		payload.U32(uint32(len(record.pitchBuffer)))
		payload.Float32s(record.pitchBuffer...)
		for subframe := range record.numSubframes {
			payload.I32(record.pitchLags[subframe])
			payload.Float32(record.invGains[subframe])
			start := subframe * ltpOrderConst
			payload.Float32s(record.taps[start : start+ltpOrderConst]...)
		}
	}
	return payload.Bytes(), nil
}

func validateLTPResidualOracleInput(record ltpResidualOracleInput) error {
	if record.frameStart < record.preLength || record.frameStart > len(record.pitchBuffer) {
		return fmtLTPResidualOracle("frameStart=%d preLength=%d inputCount=%d", record.frameStart, record.preLength, len(record.pitchBuffer))
	}
	if record.subframeLength <= 0 || record.subframeLength > ltpResidualOracleMaxSubframe ||
		record.numSubframes <= 0 || record.numSubframes > maxNbSubfr ||
		record.preLength < 0 || record.preLength > ltpResidualOracleMaxPre ||
		len(record.pitchBuffer) == 0 || len(record.pitchBuffer) > ltpResidualOracleMaxInput {
		return fmtLTPResidualOracle("geometry frameStart=%d subframeLength=%d numSubframes=%d preLength=%d inputCount=%d",
			record.frameStart, record.subframeLength, record.numSubframes, record.preLength, len(record.pitchBuffer))
	}
	if len(record.pitchLags) != record.numSubframes || len(record.invGains) != record.numSubframes ||
		len(record.taps) != record.numSubframes*ltpOrderConst {
		return fmtLTPResidualOracle("operand counts lags=%d inverseGains=%d taps=%d for %d subframes",
			len(record.pitchLags), len(record.invGains), len(record.taps), record.numSubframes)
	}
	outputCount := record.numSubframes * (record.subframeLength + record.preLength)
	if outputCount <= 0 || outputCount > ltpResidualOracleMaxOutput {
		return fmtLTPResidualOracle("output count %d is outside 1..%d", outputCount, ltpResidualOracleMaxOutput)
	}
	for index, value := range record.pitchBuffer {
		if !finiteLTPResidual(value) {
			return fmtLTPResidualOracle("pitchBuffer[%d]=%08x is not finite", index, math.Float32bits(value))
		}
	}
	for subframe := range record.numSubframes {
		lag := int(record.pitchLags[subframe])
		if lag < 0 || lag > ltpResidualOracleMaxInput {
			return fmtLTPResidualOracle("pitchLags[%d]=%d is outside 0..%d", subframe, lag, ltpResidualOracleMaxInput)
		}
		gain := record.invGains[subframe]
		if !finiteLTPResidual(gain) || gain <= 0 {
			return fmtLTPResidualOracle("invGains[%d]=%08x is not finite and positive", subframe, math.Float32bits(gain))
		}
		for tap := range ltpOrderConst {
			value := record.taps[subframe*ltpOrderConst+tap]
			if !finiteLTPResidual(value) {
				return fmtLTPResidualOracle("taps[%d][%d]=%08x is not finite", subframe, tap, math.Float32bits(value))
			}
		}
	}
	start := record.frameStart - record.preLength
	for subframe := range record.numSubframes {
		for sample := range record.subframeLength + record.preLength {
			xIndex := start + subframe*record.subframeLength + sample
			if xIndex < 0 || xIndex >= len(record.pitchBuffer) {
				return fmtLTPResidualOracle("subframe %d sample %d input index %d outside buffer %d", subframe, sample, xIndex, len(record.pitchBuffer))
			}
			for tap := range ltpOrderConst {
				lagIndex := xIndex - int(record.pitchLags[subframe]) + ltpOrderConst/2 - tap
				if lagIndex < 0 || lagIndex >= len(record.pitchBuffer) {
					return fmtLTPResidualOracle("subframe %d sample %d tap %d lag index %d outside buffer %d",
						subframe, sample, tap, lagIndex, len(record.pitchBuffer))
				}
			}
		}
	}
	return nil
}

func parseLTPResidualOracleOutput(data []byte, wantCases int) ([]ltpResidualOracleResult, error) {
	reader, version, err := libopustest.NewOracleReaderVersion("SILK LTP residual", ltpResidualOracleOutputMagic, data)
	if err != nil {
		return nil, err
	}
	if version != ltpResidualOracleVersion {
		return nil, fmtLTPResidualOracle("output version=%d want %d", version, ltpResidualOracleVersion)
	}
	count := reader.Count(wantCases)
	if err := reader.Err(); err != nil {
		return nil, err
	}
	if count <= 0 || count > ltpResidualOracleMaxCases {
		return nil, fmtLTPResidualOracle("output case count %d outside 1..%d", count, ltpResidualOracleMaxCases)
	}
	results := make([]ltpResidualOracleResult, count)
	for caseIndex := range count {
		result := &results[caseIndex]
		result.frameStart = int(reader.U32())
		result.subframeLength = int(reader.U32())
		result.numSubframes = int(reader.U32())
		result.preLength = int(reader.U32())
		result.inputCount = int(reader.U32())
		result.outputCount = int(reader.U32())
		if err := reader.Err(); err != nil {
			return nil, err
		}
		if result.frameStart < result.preLength || result.frameStart > result.inputCount ||
			result.subframeLength <= 0 || result.subframeLength > ltpResidualOracleMaxSubframe ||
			result.numSubframes <= 0 || result.numSubframes > maxNbSubfr ||
			result.preLength < 0 || result.preLength > ltpResidualOracleMaxPre ||
			result.inputCount <= 0 || result.inputCount > ltpResidualOracleMaxInput {
			return nil, fmtLTPResidualOracle("case %d has invalid dimensions frameStart=%d subframeLength=%d subframes=%d preLength=%d inputCount=%d",
				caseIndex, result.frameStart, result.subframeLength, result.numSubframes, result.preLength, result.inputCount)
		}
		wantOutput := result.numSubframes * (result.subframeLength + result.preLength)
		if wantOutput <= 0 || wantOutput > ltpResidualOracleMaxOutput || result.outputCount != wantOutput {
			return nil, fmtLTPResidualOracle("case %d outputCount=%d want %d", caseIndex, result.outputCount, wantOutput)
		}
		requiredBytes := result.numSubframes*(4+4+ltpOrderConst*4) +
			result.outputCount*(1+ltpOrderConst+1)*4
		if reader.Remaining() < requiredBytes {
			return nil, fmtLTPResidualOracle("case %d needs %d bytes for declared operands/results, only %d remain",
				caseIndex, requiredBytes, reader.Remaining())
		}
		result.subframes = make([]ltpResidualOracleSubframe, result.numSubframes)
		for subframe := range result.subframes {
			record := &result.subframes[subframe]
			record.pitchLag = reader.I32()
			record.invGain = reader.Float32()
			if record.pitchLag < 0 || record.pitchLag > ltpResidualOracleMaxInput ||
				!finiteLTPResidual(record.invGain) || record.invGain <= 0 {
				return nil, fmtLTPResidualOracle("case %d subframe %d has invalid lag=%d invGain=%08x",
					caseIndex, subframe, record.pitchLag, math.Float32bits(record.invGain))
			}
			for tap := range ltpOrderConst {
				record.taps[tap] = reader.Float32()
				if !finiteLTPResidual(record.taps[tap]) {
					return nil, fmtLTPResidualOracle("case %d subframe %d tap %d is nonfinite", caseIndex, subframe, tap)
				}
			}
		}
		result.samples = make([]ltpResidualOracleSample, result.outputCount)
		for sample := range result.samples {
			record := &result.samples[sample]
			record.x = reader.Float32()
			if !finiteLTPResidual(record.x) {
				return nil, fmtLTPResidualOracle("case %d sample %d x is nonfinite", caseIndex, sample)
			}
			for tap := range ltpOrderConst {
				record.lags[tap] = reader.Float32()
				if !finiteLTPResidual(record.lags[tap]) {
					return nil, fmtLTPResidualOracle("case %d sample %d lag %d is nonfinite", caseIndex, sample, tap)
				}
			}
			record.residual = reader.Float32()
			if !finiteLTPResidual(record.residual) {
				return nil, fmtLTPResidualOracle("case %d sample %d residual is nonfinite", caseIndex, sample)
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return results, nil
}

func finiteLTPResidual(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func fmtLTPResidualOracle(format string, args ...any) error {
	return fmt.Errorf("SILK LTP residual oracle: "+format, args...)
}

func TestLTPResidualOracleProtocolRejectsMalformedOutput(t *testing.T) {
	valid := makeLTPResidualOracleOutputFixture()
	parsed, err := parseLTPResidualOracleOutput(valid, 1)
	if err != nil {
		t.Fatalf("parse valid oracle protocol: %v", err)
	}
	if len(parsed) != 1 || parsed[0].outputCount != 4 || len(parsed[0].samples) != 4 {
		t.Fatalf("valid protocol parsed unexpected result: %+v", parsed)
	}

	tests := []struct {
		name string
		data []byte
		want int
	}{
		{name: "wrong version", data: mutateLTPResidualFixture(valid, 4, 2), want: 1},
		{name: "wrong count", data: valid, want: 2},
		{name: "truncated sample", data: valid[:len(valid)-1], want: 1},
		{name: "wrong output length", data: mutateLTPResidualFixture(valid, 32, 3), want: 1},
		{name: "oversized subframe", data: mutateLTPResidualFixture(valid, 16, ltpResidualOracleMaxSubframe+1), want: 1},
		{name: "trailing bytes", data: append(append([]byte(nil), valid...), 0), want: 1},
		{name: "nonfinite residual", data: mutateLTPResidualFixture(valid, len(valid)-4, 0x7fc00000), want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseLTPResidualOracleOutput(test.data, test.want); err == nil {
				t.Fatal("malformed protocol accepted")
			}
		})
	}
}

func makeLTPResidualOracleOutputFixture() []byte {
	payload := libopustest.NewOraclePayload(ltpResidualOracleOutputMagic,
		1,
	)
	for _, value := range []uint32{30, 2, 1, 2, 64, 4} {
		payload.U32(value)
	}
	payload.I32(5)
	payload.Float32(1)
	payload.Float32s(0, 0, 0, 0, 0)
	for range 4 {
		payload.Float32s(1, 1, 1, 1, 1, 1, 1)
	}
	return payload.Bytes()
}

func mutateLTPResidualFixture(src []byte, offset int, value uint32) []byte {
	copyData := append([]byte(nil), src...)
	copyData[offset] = byte(value)
	copyData[offset+1] = byte(value >> 8)
	copyData[offset+2] = byte(value >> 16)
	copyData[offset+3] = byte(value >> 24)
	return copyData
}
