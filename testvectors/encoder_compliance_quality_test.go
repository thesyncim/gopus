package testvectors

import "fmt"

// qualityOfPackets is the shared packet-quality wrapper for the encoder
// compliance tests. It decodes Opus packets with the libopus reference decoder
// (via decodeCompliancePackets) and scores the result against the original
// input signal through the canonical comparator (CompareDecodedFloat32), so all
// compliance Q values flow through the same opus_compare core the decoder
// parity suite uses.
//
// Candidate (decoded) and reference (original) are truncated to their common
// prefix before scoring, matching the long-standing compliance behavior; this
// keeps measured Q identical to the previous ad-hoc helpers it replaces.
func qualityOfPackets(packets [][]byte, original []float32, channels, frameSize int) (QualityComparison, []float32, error) {
	decoded, err := decodeCompliancePackets(packets, channels, frameSize)
	if err != nil {
		return QualityComparison{}, nil, fmt.Errorf("decode compliance packets: %w", err)
	}
	if len(decoded) == 0 {
		return QualityComparison{}, nil, fmt.Errorf("no decoded samples")
	}

	compareLen := min(len(decoded), len(original))

	cmp, err := CompareDecodedFloat32(
		decoded[:compareLen],
		original[:compareLen],
		48000,
		channels,
		qualityDelaySearchWindow(frameSize),
	)
	if err != nil {
		return QualityComparison{}, nil, err
	}
	return cmp, decoded, nil
}
