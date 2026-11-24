package dred

import "math/bits"

// RequestedFeatureFrames mirrors opus_dred_parse()'s min_feature_frames bound
// for a caller-provided max_dred_samples request. The result is capped to the
// libopus decoder's fixed DRED feature window.
func RequestedFeatureFrames(maxDredSamples, sampleRate int) int {
	if sampleRate <= 0 || maxDredSamples < 0 {
		return 0
	}
	const maxFeatureFrames = 2 * NumRedundancyFrames
	hi, lo := bits.Mul64(100, uint64(maxDredSamples))
	if hi >= uint64(sampleRate) {
		return maxFeatureFrames
	}
	offset, _ := bits.Div64(hi, lo, uint64(sampleRate))
	if offset >= maxFeatureFrames-2 {
		return maxFeatureFrames
	}
	return 2 + int(offset)
}

// MaxLatentsForRequest reports the maximum number of latent chunks libopus can
// decode for a single opus_dred_parse() request before payload-size limits are
// considered.
func MaxLatentsForRequest(maxDredSamples, sampleRate int) int {
	featureFrames := RequestedFeatureFrames(maxDredSamples, sampleRate)
	if featureFrames == 0 {
		return 0
	}
	maxLatents := (featureFrames + 3) / 4
	if maxLatents > MaxLatents {
		return MaxLatents
	}
	return maxLatents
}

// LatentSpanSamples converts one DRED latent chunk (40 ms) to samples at the
// caller's sample rate, matching opus_dred_parse()'s nb_latents return math.
func LatentSpanSamples(sampleRate int) int {
	if sampleRate <= 0 {
		return 0
	}
	return sampleRate / 25
}

// FillQuantizerLevels writes the request-bounded libopus quantizer schedule
// into dst and returns the number of entries written.
func (h Header) FillQuantizerLevels(dst []int32, maxDredSamples, sampleRate int) int {
	n := min(h.Availability(maxDredSamples, sampleRate).MaxLatents, len(dst))
	for i := range n {
		dst[i] = h.QuantizerLevel(i)
	}
	return n
}

// MaxAvailableSamples mirrors opus_dred_parse()'s positive sample-count result
// using the request-bounded latent ceiling derived from max_dred_samples.
func (h Header) MaxAvailableSamples(maxDredSamples, sampleRate int) int {
	return h.Availability(maxDredSamples, sampleRate).AvailableSamples
}
