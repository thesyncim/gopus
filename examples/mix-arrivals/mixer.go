package main

import "fmt"

// TimedTrack represents one audio track placed at a sample offset
// in a shared timeline.
type TimedTrack struct {
	Name        string
	StartSample int
	Gain        float32
	PCM         []float32
}

// MixTimedTracks mixes all tracks into one interleaved PCM buffer.
func MixTimedTracks(tracks []TimedTrack, channels int) ([]float32, error) {
	if channels < 1 {
		return nil, fmt.Errorf("channels must be >= 1")
	}
	if len(tracks) == 0 {
		return make([]float32, 0), nil
	}

	maxSamples := 0
	for i := range tracks {
		track := tracks[i]
		if track.StartSample < 0 {
			return nil, fmt.Errorf("track %q has negative start sample", track.Name)
		}
		if len(track.PCM)%channels != 0 {
			return nil, fmt.Errorf("track %q PCM length (%d) is not aligned to %d channels", track.Name, len(track.PCM), channels)
		}
		trackSamples := len(track.PCM) / channels
		if end := track.StartSample + trackSamples; end > maxSamples {
			maxSamples = end
		}
	}

	mixed := make([]float32, maxSamples*channels)
	for i := range tracks {
		track := tracks[i]
		if len(track.PCM) == 0 || track.Gain == 0 {
			continue
		}
		start := track.StartSample * channels
		for j := range track.PCM {
			mixed[start+j] += track.PCM[j] * track.Gain
		}
	}

	return mixed, nil
}

// NormalizePeakInPlace scales the whole buffer if it exceeds targetPeak.
func NormalizePeakInPlace(samples []float32, targetPeak float32) (peakBefore float32, appliedGain float32) {
	appliedGain = 1
	if len(samples) == 0 || targetPeak <= 0 {
		return 0, appliedGain
	}

	for i := range samples {
		v := abs32(samples[i])
		if v > peakBefore {
			peakBefore = v
		}
	}

	if peakBefore == 0 || peakBefore <= targetPeak {
		return peakBefore, appliedGain
	}

	appliedGain = targetPeak / peakBefore
	for i := range samples {
		samples[i] *= appliedGain
	}

	return peakBefore, appliedGain
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
