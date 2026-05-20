package multistream

import (
	"fmt"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	libopusRefdecodeOnce sync.Once
	libopusRefdecodePath string
	libopusRefdecodeErr  error
)

func getLibopusRefdecodePath() (string, error) {
	libopusRefdecodeOnce.Do(func() {
		if _, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); !ok {
			libopusRefdecodeErr = fmt.Errorf("libopus reference tree not found")
			return
		}
		libopusRefdecodePath, libopusRefdecodeErr = libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "multistream reference decode",
			OutputBase: "gopus_libopus_refdecode",
			SourceFile: "libopus_refdecode_multistream.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		})
	})
	if libopusRefdecodeErr != nil {
		return "", libopusRefdecodeErr
	}
	return libopusRefdecodePath, nil
}

func decodeWithLibopusReferencePackets(
	mappingFamily, channels, streams, coupled, frameSize int,
	mapping []byte,
	demixingMatrix []byte,
	packets [][]byte,
) ([]float32, error) {
	binPath, err := getLibopusRefdecodePath()
	if err != nil {
		return nil, err
	}

	if mappingFamily < 1 || mappingFamily > 3 {
		return nil, fmt.Errorf("unsupported mapping family: %d", mappingFamily)
	}

	payload := libopustest.NewOraclePayload(
		"GMSI",
		uint32(mappingFamily),
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		uint32(len(demixingMatrix)),
	)
	payload.Raw(mapping)
	payload.Raw(demixingMatrix)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream reference decode", "GMSO")
	if err != nil {
		return nil, err
	}

	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}
