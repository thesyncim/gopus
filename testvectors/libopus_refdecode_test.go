package testvectors

import (
	"fmt"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	libopusRefdecodeSingleOnce sync.Once
	libopusRefdecodeSinglePath string
	libopusRefdecodeSingleErr  error
)

func getLibopusRefdecodeSinglePath() (string, error) {
	libopusRefdecodeSingleOnce.Do(func() {
		if _, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); !ok {
			libopusRefdecodeSingleErr = fmt.Errorf("libopus reference tree not found")
			return
		}
		libopusRefdecodeSinglePath, libopusRefdecodeSingleErr = libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "single reference decode",
			OutputBase: "gopus_libopus_refdecode_single",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		})
	})
	if libopusRefdecodeSingleErr != nil {
		return "", libopusRefdecodeSingleErr
	}
	return libopusRefdecodeSinglePath, nil
}

func decodeWithLibopusReferencePacketsSingle(channels, frameSize int, packets [][]byte) ([]float32, error) {
	binPath, err := getLibopusRefdecodeSinglePath()
	if err != nil {
		return nil, err
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("unsupported single-stream channel count: %d", channels)
	}

	payload := libopustest.NewOraclePayload("GOSI", uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "single reference decode", "GOSO")
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
