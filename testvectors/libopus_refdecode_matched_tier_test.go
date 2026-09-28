package testvectors

// libopus_refdecode_matched_tier_test.go — build-matched quality reference decode.
//
// This helper links the libopus tree selected by the build-aware reference
// resolver and invokes the matching Go decode path. The transport payload and
// reader contract match the single-stream helper.

import (
	"fmt"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusRefdecodeMatchedTierHelper libopustest.HelperCache

// getLibopusRefdecodeMatchedTierPath builds (once) the single-stream refdecode C
// binary linked against the libopus tier that matches the gopus build under test.
func getLibopusRefdecodeMatchedTierPath() (string, error) {
	return libopusRefdecodeMatchedTierHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "matched-tier reference decode",
			OutputBase: "gopus_libopus_refdecode_matched_tier",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
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

// decodeLossPatternWithMatchedTierReference applies packet loss and FEC calls
// through one libopus decoder selected for the current Go feature and ISA lane.
func decodeLossPatternWithMatchedTierReference(sampleRate, channels int, packets [][]byte, loss []bool) ([]float32, error) {
	type decodeStep struct {
		packet    []byte
		fec       bool
		frameSize int
	}

	steps := make([]decodeStep, 0, len(packets)+len(loss))
	lastPacketDuration := sampleRate / 50
	lostCount := 0
	appendStep := func(packet []byte, fec bool) error {
		frameSize := 5760
		if packet == nil || fec {
			frameSize = lastPacketDuration
		}
		steps = append(steps, decodeStep{packet: packet, fec: fec, frameSize: frameSize})
		if packet != nil {
			info, err := gopus.ParsePacket(packet)
			if err != nil {
				return fmt.Errorf("parse reference packet duration: %w", err)
			}
			lastPacketDuration = info.TOC.FrameSize * info.FrameCount * sampleRate / 48000
		}
		return nil
	}
	for i, packet := range packets {
		if i < len(loss) && loss[i] {
			lostCount++
			continue
		}
		if lostCount == 0 {
			if err := appendStep(packet, false); err != nil {
				return nil, err
			}
			continue
		}

		decodeCount := lostCount + 1
		hasLBRR := gopus.PacketHasLBRR(packet)
		for frame := 0; frame < decodeCount; frame++ {
			switch {
			case frame == lostCount-1 && hasLBRR:
				if err := appendStep(packet, true); err != nil {
					return nil, err
				}
			case frame < lostCount:
				if err := appendStep(nil, false); err != nil {
					return nil, err
				}
			default:
				if err := appendStep(packet, false); err != nil {
					return nil, err
				}
			}
		}
		lostCount = 0
	}

	binPath, err := getLibopusRefdecodeMatchedTierPath()
	if err != nil {
		return nil, err
	}
	const frameSize = 5760
	payload := libopustest.NewOraclePayloadVersion("GOSI", 7,
		libopusRefdecodeSingleFormatFloat32,
		uint32(sampleRate),
		0,
		uint32(channels),
		uint32(frameSize),
		uint32(len(steps)),
	)
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(step.frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "matched-tier loss reference decode", "GOSO", 1)
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
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return decoded, nil
}
