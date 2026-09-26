package testvectors

// libopus_refdecode_matched_tier_test.go — build-matched quality reference decode.
//
// This helper links the libopus tree selected by the build-aware reference
// resolver and invokes the matching Go decode path. The transport payload and
// reader contract match the single-stream helper.

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var libopusRefdecodeMatchedTierHelper libopustest.HelperCache

// getLibopusRefdecodeMatchedTierPath builds (once) the single-stream refdecode C
// binary linked against the libopus tier that matches the gopus build under test.
func getLibopusRefdecodeMatchedTierPath() (string, error) {
	return libopusRefdecodeMatchedTierHelper.Path(func() (string, error) {
		if _, err := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); err != nil {
			return "", err
		}
		libArchive := libopustest.RefPath(".libs", "libopus.a")
		return libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "matched-tier reference decode",
			OutputBase: "gopus_libopus_refdecode_matched_tier",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			SIMDRef:    gopusBuildIsSIMD,
			Libs:       []string{libArchive, "-lm"},
		})
	})
}

// runMatchedTierReferencePacketsSingle decodes through the tier-matched binary
// at the same API sample rate as the Go decoder.
func runMatchedTierReferencePacketsSingle(sampleRate, channels, frameSize int, packets [][]byte, sampleFormat uint32) (*libopustest.OracleReader, error) {
	binPath, err := getLibopusRefdecodeMatchedTierPath()
	if err != nil {
		return nil, err
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("unsupported single-stream channel count: %d", channels)
	}

	payload := libopustest.NewOraclePayloadVersion("GOSI", 3, sampleFormat, uint32(sampleRate), uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "matched-tier reference decode", "GOSO")
}

// decodeWithMatchedTierReferencePacketsSingle returns float32 PCM from the
// libopus build with the same CPU features as the Go decoder under test.
func decodeWithMatchedTierReferencePacketsSingle(sampleRate, channels, frameSize int, packets [][]byte) ([]float32, error) {
	reader, err := runMatchedTierReferencePacketsSingle(sampleRate, channels, frameSize, packets, libopusRefdecodeSingleFormatFloat32)
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
