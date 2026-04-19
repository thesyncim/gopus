package testvectors

import "fmt"

const (
	amd64FixtureWaveformMinQ         = 95.0
	amd64FixtureWaveformMaxDelay     = 32
	differentialFuzzPacketMinQ       = 90.0
	differentialFuzzPacketMaxDelay   = 8
	differentialFuzzMaxPCM16AbsDiff  = 32
	differentialFuzzMaxPCM16MeanDiff = 2.0
)

func computeOpusCompareQualityBetweenDecoded(reference, decoded []float32, sampleRate, channels, maxDelay int) (float64, int, error) {
	if len(reference) == 0 || len(decoded) == 0 {
		return 0, 0, fmt.Errorf("decoded waveform missing: reference=%d decoded=%d", len(reference), len(decoded))
	}

	compareLen := len(reference)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}
	if compareLen <= 0 {
		return 0, 0, fmt.Errorf("decoded waveform overlap missing")
	}

	return ComputeOpusCompareQualityFloat32WithDelay(
		decoded[:compareLen],
		reference[:compareLen],
		sampleRate,
		channels,
		maxDelay,
	)
}

func comparePacketWaveformsWithLibopusReference(referencePackets, decodedPackets [][]byte, channels, frameSize int) (float64, int, error) {
	refDecoded, err := decodeCompliancePacketsWithLibopusReferenceOnly(referencePackets, channels, frameSize)
	if err != nil {
		return 0, 0, fmt.Errorf("decode reference packets with libopus: %w", err)
	}
	decoded, err := decodeCompliancePacketsWithLibopusReferenceOnly(decodedPackets, channels, frameSize)
	if err != nil {
		return 0, 0, fmt.Errorf("decode candidate packets with libopus: %w", err)
	}

	return computeOpusCompareQualityBetweenDecoded(
		refDecoded,
		decoded,
		48000,
		channels,
		amd64FixtureWaveformMaxDelay,
	)
}
