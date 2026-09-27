//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

// CELTFixedQEXTPVQParams contains one fixed-point QEXT alg_quant call. X is
// the exact Q24 celt_norm vector entering exp_rotation; Gain is the Q31 CELT
// band gain. Main and extension range coders have independent capacities.
type CELTFixedQEXTPVQParams struct {
	N, K, Spread, Blocks, ExtraBits int
	Resynth                         bool
	Gain                            int32
	MainStorage, ExtStorage         int
	X                               []int32
}

// CELTFixedQEXTPVQRecord contains the main and refinement streams plus the
// resynthesized normalized vector returned by the combined reference.
type CELTFixedQEXTPVQRecord struct {
	CollapseMask  uint32
	MainRange     uint32
	ExtRange      uint32
	MainPacket    []byte
	ExtPacket     []byte
	Resynthesized []int32
}

var fixedQEXTPVQHelper HelperCache

func buildCELTFixedQEXTPVQHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT PVQ oracle",
		OutputBase:   "gopus_libopus_celt_alg_quant_fixed_qext",
		SourceFile:   "libopus_celt_alg_quant_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTPVQ compares a QEXT-refined PVQ vector against the
// independently built FIXED_POINT + ENABLE_QEXT libopus archive.
func ProbeCELTFixedQEXTPVQ(p CELTFixedQEXTPVQParams) (CELTFixedQEXTPVQRecord, error) {
	var out CELTFixedQEXTPVQRecord
	if p.N < 2 || p.N > 512 || p.K < 1 || p.K > 512 || p.Spread < 0 || p.Spread > 3 ||
		p.Blocks < 1 || p.Blocks > p.N || p.ExtraBits < 2 || p.ExtraBits > 12 ||
		p.MainStorage < 1 || p.MainStorage > 4096 || p.ExtStorage < 1 || p.ExtStorage > 4096 ||
		len(p.X) != p.N {
		return out, fmt.Errorf("invalid fixed-QEXT PVQ controls")
	}
	boolWord := uint32(0)
	if p.Resynth {
		boolWord = 1
	}
	payload := NewOraclePayloadVersion("GQVP", 1, 1, uint32(p.N), uint32(p.K), uint32(p.Spread),
		uint32(p.Blocks), uint32(p.ExtraBits), boolWord, uint32(p.Gain),
		uint32(p.MainStorage), uint32(p.ExtStorage))
	payload.I32s(p.X...)
	bin, err := fixedQEXTPVQHelper.Path(buildCELTFixedQEXTPVQHelper)
	if err != nil {
		return out, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT PVQ", "GQVO")
	if err != nil {
		return out, err
	}
	reader.Count(1)
	out.CollapseMask = reader.U32()
	out.MainRange = reader.U32()
	out.ExtRange = reader.U32()
	mainLen := int(reader.U32())
	if mainLen < 0 || mainLen > p.MainStorage {
		return CELTFixedQEXTPVQRecord{}, fmt.Errorf("fixed-QEXT main packet length %d exceeds capacity %d", mainLen, p.MainStorage)
	}
	out.MainPacket = append([]byte(nil), reader.Bytes(mainLen)...)
	extLen := int(reader.U32())
	if extLen < 0 || extLen > p.ExtStorage {
		return CELTFixedQEXTPVQRecord{}, fmt.Errorf("fixed-QEXT side packet length %d exceeds capacity %d", extLen, p.ExtStorage)
	}
	out.ExtPacket = append([]byte(nil), reader.Bytes(extLen)...)
	reader.Count(p.N)
	out.Resynthesized = make([]int32, p.N)
	for i := range out.Resynthesized {
		out.Resynthesized[i] = int32(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return CELTFixedQEXTPVQRecord{}, err
	}
	return out, nil
}
