//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

// CELTFixedQEXTExtraAllocParams contains the Q24 log energies and Q-format
// controls for one fixed-point clt_compute_extra_allocation call.
type CELTFixedQEXTExtraAllocParams struct {
	SampleRate, FrameSize             int
	Channels, LM, Start, End, QEXTEnd int
	TotalQ3                           int32
	ToneFreqQ14                       int16
	ToneishnessQ29                    int32
	StorageBytes                      int
	MainBandLogE, QEXTBandLogE        []int32
}

// CELTFixedQEXTExtraAllocRecord contains independent encode-side allocation
// arrays and entropy output from FIXED_POINT + ENABLE_QEXT libopus.
type CELTFixedQEXTExtraAllocRecord struct {
	TotalBands   int
	ExtraPulses  []int32
	ExtraQuant   []int32
	DecodePulses []int32
	DecodeQuant  []int32
	Bytes        []byte
	TellFrac     uint32
	Range        uint32
}

var fixedQEXTExtraAllocHelper HelperCache

func buildCELTFixedQEXTExtraAllocHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT extra-allocation oracle",
		OutputBase:   "gopus_libopus_celt_qext_extra_alloc_fixed",
		SourceFile:   "libopus_celt_qext_extra_alloc_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTExtraAllocation runs the fixed-point QEXT allocation helper
// against the selected combined reference archive. The protocol transports CELT
// Q24 and Q29 values as int32 and tone frequency as int16, matching libopus.
func ProbeCELTFixedQEXTExtraAllocation(p CELTFixedQEXTExtraAllocParams) (CELTFixedQEXTExtraAllocRecord, error) {
	var out CELTFixedQEXTExtraAllocRecord
	if (p.SampleRate != 48000 && p.SampleRate != 96000) || p.FrameSize != p.SampleRate/50 ||
		p.Channels < 1 || p.Channels > 2 || p.LM < 0 || p.LM > 3 || p.Start < 0 || p.End <= p.Start || p.End > 21 ||
		p.QEXTEnd < 0 || p.QEXTEnd > 14 || p.TotalQ3 < 0 || p.StorageBytes < 0 || p.StorageBytes > 4096 ||
		len(p.MainBandLogE) != p.Channels*21 || len(p.QEXTBandLogE) != p.Channels*14 {
		return out, fmt.Errorf("invalid fixed-QEXT extra-allocation controls")
	}
	payload := NewOraclePayloadVersion("GQAI", 2, uint32(p.SampleRate), uint32(p.FrameSize),
		uint32(p.Channels), uint32(p.LM), uint32(p.Start),
		uint32(p.End), uint32(p.QEXTEnd), uint32(p.TotalQ3))
	payload.I16(p.ToneFreqQ14)
	payload.I32(p.ToneishnessQ29)
	payload.U32(uint32(p.StorageBytes))
	payload.U32(uint32(len(p.MainBandLogE)))
	payload.I32s(p.MainBandLogE...)
	payload.U32(uint32(len(p.QEXTBandLogE)))
	payload.I32s(p.QEXTBandLogE...)
	bin, err := fixedQEXTExtraAllocHelper.Path(buildCELTFixedQEXTExtraAllocHelper)
	if err != nil {
		return out, err
	}
	reader, err := RunOracleVersion(bin, payload.Bytes(), "fixed-QEXT CELT extra allocation", "GQAO", 2)
	if err != nil {
		return out, err
	}
	reader.Count(1)
	out.TotalBands = int(reader.U32())
	reader.Count(out.TotalBands)
	out.ExtraPulses = make([]int32, out.TotalBands)
	for i := range out.ExtraPulses {
		out.ExtraPulses[i] = reader.I32()
	}
	reader.Count(out.TotalBands)
	out.ExtraQuant = make([]int32, out.TotalBands)
	for i := range out.ExtraQuant {
		out.ExtraQuant[i] = reader.I32()
	}
	out.Bytes = reader.Bytes(int(reader.U32()))
	out.TellFrac = reader.U32()
	out.Range = reader.U32()
	reader.Count(out.TotalBands)
	out.DecodePulses = make([]int32, out.TotalBands)
	for i := range out.DecodePulses {
		out.DecodePulses[i] = reader.I32()
	}
	reader.Count(out.TotalBands)
	out.DecodeQuant = make([]int32, out.TotalBands)
	for i := range out.DecodeQuant {
		out.DecodeQuant[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return CELTFixedQEXTExtraAllocRecord{}, err
	}
	return out, nil
}
