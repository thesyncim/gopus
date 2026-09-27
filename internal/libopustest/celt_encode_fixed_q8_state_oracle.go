package libopustest

import (
	"fmt"
)

// CELTFixedQ8EncoderState is the explicit logical state read from the selected
// C CELT encoder after one raw Q8 frame. Scalar fields precede the arrays in the
// trace stream; the helper never hashes struct padding or pointers.
type CELTFixedQ8EncoderState struct {
	RNG                       uint32
	SpreadDecision            int32
	DelayedIntra              int32
	TonalAverage              int32
	LastCodedBands            int32
	HFAverage                 int32
	TapsetDecision            int32
	PrefilterPeriod           int32
	PrefilterGain             int32
	PrefilterTapset           int32
	ConsecTransient           int32
	VBRReservoir              int32
	VBRDrift                  int32
	VBROffset                 int32
	VBRCount                  int32
	OverlapMax                int32
	StereoSaving              int32
	Intensity                 int32
	SpecAvg                   int32
	ForceIntra                int32
	DisablePrefilter          int32
	SilkSignalType            int32
	SilkOffset                int32
	AnalysisValid             int32
	AnalysisBandwidth         int32
	AnalysisActivityBits      uint32
	AnalysisTonalityBits      uint32
	AnalysisSlopeBits         uint32
	AnalysisMaxPitchRatioBits uint32
	AnalysisLeakBoost         [19]uint8
	EnergyMask                []int32
	PreemphMemE               []int32
	InMem                     []int32
	PrefilterMem              []int32
	OldBandE                  []int32
	OldLogE                   []int32
	OldLogE2                  []int32
	EnergyError               []int32
}

// CELTFixedQ8StateRecord combines a raw CELT packet with the exact logical C
// encoder state after that frame.
type CELTFixedQ8StateRecord struct {
	CELTFixedQ8Record
	State CELTFixedQ8EncoderState
}

var celtFixedQ8QEXTStateHelper HelperCache

func buildCELTFixedQ8QEXTStateHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "selected fixed-QEXT CELT raw Q8 state trace",
		OutputBase:   "gopus_libopus_celt_encode_fixed_q8_qext_state",
		SourceFile:   "libopus_celt_encode_fixed_q8_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DGOPUS_REQUIRE_QEXT=1", "-DGOPUS_STATE_TRACE=1", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTQ8State encodes the same raw Q8 frames as ProbeCELTFixedQEXTQ8
// and also returns explicit C encoder state after each frame.
func ProbeCELTFixedQEXTQ8State(p CELTFixedQ8Params) ([]CELTFixedQ8StateRecord, error) {
	payload, err := fixedCELTQ8Payload(p)
	if err != nil {
		return nil, err
	}
	bin, err := celtFixedQ8QEXTStateHelper.Path(buildCELTFixedQ8QEXTStateHelper)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "selected fixed-QEXT CELT raw Q8 state trace", "GQSO")
	if err != nil {
		return nil, err
	}
	reader.Count(len(p.Frames))
	out := make([]CELTFixedQ8StateRecord, len(p.Frames))
	for i := range out {
		n := int(reader.U32())
		out[i].FinalRange = reader.U32()
		if n < 0 || n > p.Frames[i].MaxBytes {
			return nil, fmt.Errorf("fixed CELT Q8 state frame %d packet size %d", i, n)
		}
		out[i].Packet = append([]byte(nil), reader.Bytes(n)...)
		state, err := readCELTFixedQ8EncoderState(reader)
		if err != nil {
			return nil, fmt.Errorf("fixed CELT Q8 state frame %d: %w", i, err)
		}
		out[i].State = state
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func readCELTFixedQ8EncoderState(reader *OracleReader) (CELTFixedQ8EncoderState, error) {
	var state CELTFixedQ8EncoderState
	state.RNG = reader.U32()
	if reader.Count(24) == 0 {
		return state, reader.Err()
	}
	scalars := []*int32{
		&state.SpreadDecision,
		&state.DelayedIntra,
		&state.TonalAverage,
		&state.LastCodedBands,
		&state.HFAverage,
		&state.TapsetDecision,
		&state.PrefilterPeriod,
		&state.PrefilterGain,
		&state.PrefilterTapset,
		&state.ConsecTransient,
		&state.VBRReservoir,
		&state.VBRDrift,
		&state.VBROffset,
		&state.VBRCount,
		&state.OverlapMax,
		&state.StereoSaving,
		&state.Intensity,
		&state.SpecAvg,
		&state.ForceIntra,
		&state.DisablePrefilter,
		&state.SilkSignalType,
		&state.SilkOffset,
		&state.AnalysisValid,
		&state.AnalysisBandwidth,
	}
	for _, scalar := range scalars {
		*scalar = reader.I32()
	}
	state.AnalysisActivityBits = reader.U32()
	state.AnalysisTonalityBits = reader.U32()
	state.AnalysisSlopeBits = reader.U32()
	state.AnalysisMaxPitchRatioBits = reader.U32()
	copy(state.AnalysisLeakBoost[:], reader.Bytes(len(state.AnalysisLeakBoost)))
	var err error
	if state.EnergyMask, err = readI32Array(reader, 42); err != nil {
		return state, err
	}
	if state.PreemphMemE, err = readI32Array(reader, 2); err != nil {
		return state, err
	}
	if state.InMem, err = readI32Array(reader, 240); err != nil {
		return state, err
	}
	if state.PrefilterMem, err = readI32Array(reader, 2048); err != nil {
		return state, err
	}
	if state.OldBandE, err = readI32Array(reader, 42); err != nil {
		return state, err
	}
	if state.OldLogE, err = readI32Array(reader, 42); err != nil {
		return state, err
	}
	if state.OldLogE2, err = readI32Array(reader, 42); err != nil {
		return state, err
	}
	if state.EnergyError, err = readI32Array(reader, 42); err != nil {
		return state, err
	}
	return state, reader.Err()
}

func readI32Array(reader *OracleReader, maxLen int) ([]int32, error) {
	n := int(reader.U32())
	if n < 0 || n > maxLen {
		return nil, fmt.Errorf("array length %d outside [0,%d]", n, maxLen)
	}
	values := make([]int32, n)
	for i := range values {
		values[i] = reader.I32()
	}
	return values, reader.Err()
}
