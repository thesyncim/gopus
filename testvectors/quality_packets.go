package testvectors

import "fmt"

func qualityDelaySearchWindow(frameSize int) int {
	if frameSize < 32 {
		return 32
	}
	if frameSize > 960 {
		return 960
	}
	return frameSize
}

func computeOpusCompareQualityFromPacketsWithMaxDelay(packets [][]byte, original []float32, channels, frameSize, maxDelay int) (float64, int, []float32, error) {
	decoded, err := decodeCompliancePackets(packets, channels, frameSize)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("decode compliance packets: %w", err)
	}
	if len(decoded) == 0 {
		return 0, 0, nil, fmt.Errorf("no decoded samples")
	}

	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	q, delay, err := ComputeOpusCompareQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, channels, maxDelay)
	if err != nil {
		return 0, 0, nil, err
	}
	return q, delay, decoded, nil
}

func computeOpusCompareQualityFromPackets(packets [][]byte, original []float32, channels, frameSize int) (float64, error) {
	// Search up to one packet duration of residual delay. This is wide enough to
	// avoid clipping true pre-skip/resampling offsets on short CELT frames, but
	// still local enough to avoid latching onto distant periodic aliases.
	q, _, _, err := computeOpusCompareQualityFromPacketsWithMaxDelay(packets, original, channels, frameSize, qualityDelaySearchWindow(frameSize))
	if err != nil {
		return 0, err
	}
	return q, nil
}
