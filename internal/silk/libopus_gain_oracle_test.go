package silk

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKGainInputMagic  = "GSGI"
	libopusSILKGainOutputMagic = "GSGO"

	libopusSILKGainModeQuant           = uint32(0)
	libopusSILKGainModeDequant         = uint32(1)
	libopusSILKGainModeID              = uint32(2)
	libopusSILKGainModeShapeStateSizes = uint32(3)
)

var libopusSILKGainHelper libopustest.HelperCache

type libopusSILKGainRecord struct {
	first int32
	ind   [maxNbSubfr]int8
	gains [maxNbSubfr]int32
}

func buildLibopusSILKGainHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk gain",
		OutputBase:   "gopus_libopus_silk_gain",
		SourceFile:   "libopus_silk_gain_info.c",
		ProbeRelPath: "silk/main.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources:   []string{"silk/gain_quant.c", "silk/lin2log.c", "silk/log2lin.c"},
	})
}

func getLibopusSILKGainHelperPath() (string, error) {
	return libopusSILKGainHelper.Path(buildLibopusSILKGainHelper)
}

func LibopusGainBoolWord(v bool) int32 {
	if v {
		return 1
	}
	return 0
}

func probeLibopusSILKGain(mode uint32, records [][]int32) ([]libopusSILKGainRecord, error) {
	binPath, err := getLibopusSILKGainHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKGainInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.I32(word)
		}
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk gain helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk gain", libopusSILKGainOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * 36)
	out := make([]libopusSILKGainRecord, count)
	for i := range out {
		out[i].first = reader.I32()
		for j := range maxNbSubfr {
			out[i].ind[j] = int8(reader.I32())
		}
		for j := range maxNbSubfr {
			out[i].gains[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
