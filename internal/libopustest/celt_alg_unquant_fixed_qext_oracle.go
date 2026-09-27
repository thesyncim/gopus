//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type CELTFixedQEXTPVQDecodeParams struct {
	N, K, Spread, Blocks, ExtraBits int
	Gain                            int32
	MainPacket, ExtPacket           []byte
}

type CELTFixedQEXTPVQDecodeRecord struct {
	CollapseMask           uint32
	MainRange, MainVal     uint32
	MainTell, MainTellFrac uint32
	MainError              uint32
	ExtRange, ExtVal       uint32
	ExtTell, ExtTellFrac   uint32
	ExtError               uint32
	Samples                []int32
}

var fixedQEXTPVQDecodeHelper HelperCache

func buildCELTFixedQEXTPVQDecodeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT PVQ decoder oracle",
		OutputBase:   "gopus_libopus_celt_alg_unquant_fixed_qext",
		SourceFile:   "libopus_celt_alg_unquant_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTPVQDecode runs linked fixed+QEXT libopus alg_unquant on
// independently encoded main and extension packets.
func ProbeCELTFixedQEXTPVQDecode(p CELTFixedQEXTPVQDecodeParams) (CELTFixedQEXTPVQDecodeRecord, error) {
	var out CELTFixedQEXTPVQDecodeRecord
	if p.N < 2 || p.N > 512 || p.K < 1 || p.K > 512 || p.Spread < 0 || p.Spread > 3 ||
		p.Blocks < 1 || p.N%p.Blocks != 0 || p.ExtraBits < 2 || p.ExtraBits > 12 ||
		len(p.MainPacket) == 0 || len(p.MainPacket) > 4096 || len(p.ExtPacket) > 4096 {
		return out, fmt.Errorf("invalid fixed-QEXT PVQ decode controls")
	}
	payload := NewOraclePayloadVersion("GQDI", 1,
		uint32(p.N), uint32(p.K), uint32(p.Spread), uint32(p.Blocks), uint32(p.ExtraBits),
		uint32(p.Gain), uint32(len(p.MainPacket)), uint32(len(p.ExtPacket)))
	payload.Raw(p.MainPacket)
	payload.Raw(p.ExtPacket)
	bin, err := fixedQEXTPVQDecodeHelper.Path(buildCELTFixedQEXTPVQDecodeHelper)
	if err != nil {
		return out, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT PVQ decoder", "GQDO")
	if err != nil {
		return out, err
	}
	reader.Count(1)
	out.CollapseMask = reader.U32()
	out.MainRange, out.MainVal = reader.U32(), reader.U32()
	out.MainTell, out.MainTellFrac, out.MainError = reader.U32(), reader.U32(), reader.U32()
	out.ExtRange, out.ExtVal = reader.U32(), reader.U32()
	out.ExtTell, out.ExtTellFrac, out.ExtError = reader.U32(), reader.U32(), reader.U32()
	reader.Count(p.N)
	out.Samples = make([]int32, p.N)
	for i := range out.Samples {
		out.Samples[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return CELTFixedQEXTPVQDecodeRecord{}, err
	}
	return out, nil
}
