package testvectors

// libopus_refdecode_matched_tier_test.go — build-matched quality reference decode.
//
// This helper links the libopus tree selected by the build-aware reference
// resolver and invokes the matching Go decode path. The transport payload and
// reader contract match the scalar helper, so quality tests can use either path.

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

// runMatchedTierReferencePacketsSingle mirrors runLibopusReferencePacketsSingle
// but dispatches to the tier-matched binary.
func runMatchedTierReferencePacketsSingle(channels, frameSize int, packets [][]byte, sampleFormat uint32) (*libopustest.OracleReader, error) {
	binPath, err := getLibopusRefdecodeMatchedTierPath()
	if err != nil {
		return nil, err
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("unsupported single-stream channel count: %d", channels)
	}

	payload := libopustest.NewOraclePayloadVersion("GOSI", 2, sampleFormat, uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "matched-tier reference decode", "GOSO")
}

// decodeWithMatchedTierReferencePacketsSingle decodes packets with the libopus
// reference whose SIMD tier matches the gopus build under test, returning float32
// PCM. Use this for QUALITY (opus_compare Q) parity so the comparison is
// like-with-like; keep the scalar helper for bit-exact oracles.
func decodeWithMatchedTierReferencePacketsSingle(channels, frameSize int, packets [][]byte) ([]float32, error) {
	reader, err := runMatchedTierReferencePacketsSingle(channels, frameSize, packets, libopusRefdecodeSingleFormatFloat32)
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
