package testvectors

// libopus_refdecode_matched_tier_test.go — TIER-MATCHED quality reference decode.
//
// The scalar reference decode (decodeWithLibopusReferencePacketsSingle in
// libopus_refdecode_test.go) always links the bit-reproducible scalar libopus
// (opus-1.6.1). That is correct for the bit-exact int16-PLC oracles, but it makes
// a default (asm/SIMD) gopus build compare go-asm against pure-C scalar — an
// unmatched pairing that conflates gopus's asm 1-ULP envelope with libopus's own
// scalar-vs-SIMD envelope.
//
// This helper closes that gap WITHOUT touching the scalar helper: it links the
// libopus reference whose SIMD tier MATCHES the gopus build under test —
//
//   - asm gopus build (default, !purego): SIMD libopus (opus-1.6.1-simd, built by
//     `make ensure-libopus-simd`; NEON on arm64, SSE/AVX RTCD on amd64), so the
//     comparison is asm-vs-SIMD.
//   - pure-Go gopus build (-tags purego): scalar libopus (opus-1.6.1), so the
//     comparison is pure-Go-vs-scalar and is expected to be ~bit-exact.
//
// gopusBuildIsAsm (build_tier_asm.go / build_tier_purego.go) is the selector;
// CHelperConfig.SIMDRef drives the libopus tree choice. The transport payload and
// reader contract are identical to the scalar helper, so quality tests can swap in
// decodeWithMatchedTierReferencePacketsSingle with no other changes.

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
		if _, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); !ok {
			return "", fmt.Errorf("libopus reference tree not found")
		}
		libArchive := libopustest.RefPath(".libs", "libopus.a")
		if gopusBuildIsAsm {
			libArchive = libopustest.SIMDRefPath(".libs", "libopus.a")
		}
		return libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "matched-tier reference decode",
			OutputBase: "gopus_libopus_refdecode_matched_tier",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			SIMDRef:    gopusBuildIsAsm,
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
