//go:build gopus_celt_trace && !gopus_fixed_point

package celt

const encodePitchTraceMaxCalls = 8

// EnablePitchAnalysisTraceForTesting enables the bounded pitch-analysis
// snapshots used by the late-frame CBR differential diagnostic.
func (e *Encoder) EnablePitchAnalysisTraceForTesting() {
	if e.encodeStageTrace.enabled {
		e.encodeStageTrace.pitchEnabled = true
	}
}

func (e *Encoder) runPrefilterPitchDownsample(input []celtSig, output []float32, length, channels, perChannelLength, factor int) {
	pitchDownsampleSig(input, output, length, channels, factor)
	e.encodeStageTrace.recordPitchDownsample(input, output, length, channels, perChannelLength, factor)
}

func (e *Encoder) recordPitchControls(frameSize, channels int, enabled bool, complexity int32, maxPeriod, minPeriod int,
	tfEstimate, toneFreq, toneishness, maxPitchRatio float32) {
	s := &e.encodeStageTrace
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.PitchControls) >= 1 || frameSize <= 0 || channels <= 0 || channels > 2 ||
		complexity < 0 || maxPeriod <= 0 || minPeriod <= 0 || frameSize > int(^uint32(0)>>1) ||
		maxPeriod > int(^uint32(0)>>1) || minPeriod > int(^uint32(0)>>1) {
		s.trace.StageOverflow = true
		return
	}
	enabledFlag := int32(0)
	if enabled {
		enabledFlag = 1
	}
	s.trace.PitchControls = append(s.trace.PitchControls, EncodePitchControlsTrace{
		FrameSize: int32(frameSize), Channels: int32(channels), Enabled: enabledFlag,
		Complexity: complexity, MaxPeriod: int32(maxPeriod), MinPeriod: int32(minPeriod),
		TFEstimate: tfEstimate, ToneFreq: toneFreq, Toneishness: toneishness, MaxPitchRatio: maxPitchRatio,
	})
}

func (e *Encoder) runPrefilterPitchSearch(buffer []float32, xOffset, length, maxPitch int) int {
	result := pitchSearch(buffer[xOffset:], buffer, length, maxPitch, &e.scratch)
	e.encodeStageTrace.recordPitchSearch(buffer, xOffset, length, maxPitch, result)
	return result
}

func (e *Encoder) runPrefilterRemoveDoubling(buffer []float32, maxPeriod, minPeriod, n int, t0 *int) float32 {
	t0Before := *t0
	prevPeriod, prevGain := e.prefilterPeriod, e.prefilterGain
	gain := removeDoubling(buffer, maxPeriod, minPeriod, n, t0, prevPeriod, prevGain, &e.scratch)
	e.encodeStageTrace.recordRemoveDoubling(buffer, maxPeriod, minPeriod, n, t0Before, *t0, prevPeriod, prevGain, gain)
	return gain
}

func (s *encodeStageTraceState) recordPitchDownsample(input []celtSig, output []float32, length, channels, perChannelLength, factor int) {
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.PitchDownsample) >= encodePitchTraceMaxCalls || length <= 0 || channels <= 0 || channels > 2 ||
		factor <= 0 || perChannelLength < length*factor || len(input) < channels*perChannelLength || len(output) < length {
		s.trace.StageOverflow = true
		return
	}
	inputPerChannel := length * factor
	inputCopy := make([]float32, inputPerChannel*channels)
	for channel := range channels {
		copySigToFloat32(inputCopy[channel*inputPerChannel:(channel+1)*inputPerChannel],
			input[channel*perChannelLength:channel*perChannelLength+inputPerChannel])
	}
	s.trace.PitchDownsample = append(s.trace.PitchDownsample, EncodePitchDownsampleTrace{
		Length: int32(length), Channels: int32(channels), Factor: int32(factor),
		Input: inputCopy, Output: copyStageFloat32(output[:length]),
	})
}

func (s *encodeStageTraceState) recordPitchSearch(buffer []float32, xOffset, length, maxPitch, result int) {
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.PitchSearch) >= encodePitchTraceMaxCalls || len(buffer) == 0 || xOffset < 0 || xOffset >= len(buffer) ||
		length <= 0 || maxPitch <= 0 || result < -1 || result > int(^uint32(0)>>1) {
		s.trace.StageOverflow = true
		return
	}
	s.trace.PitchSearch = append(s.trace.PitchSearch, EncodePitchSearchTrace{
		Length: int32(length), MaxPitch: int32(maxPitch), XOffset: int32(xOffset),
		Buffer: copyStageFloat32(buffer), Result: int32(result),
	})
}

func (s *encodeStageTraceState) recordRemoveDoubling(buffer []float32, maxPeriod, minPeriod, n int,
	t0Before, t0After, prevPeriod int, prevGain, gain float32) {
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.RemoveDoubling) >= encodePitchTraceMaxCalls || len(buffer) == 0 || maxPeriod <= 0 || minPeriod <= 0 || n <= 0 ||
		maxPeriod > int(^uint32(0)>>1) || minPeriod > int(^uint32(0)>>1) || n > int(^uint32(0)>>1) ||
		t0Before < -1 || t0Before > int(^uint32(0)>>1) || t0After < -1 || t0After > int(^uint32(0)>>1) ||
		prevPeriod < 0 || prevPeriod > int(^uint32(0)>>1) {
		s.trace.StageOverflow = true
		return
	}
	s.trace.RemoveDoubling = append(s.trace.RemoveDoubling, EncodeRemoveDoublingTrace{
		MaxPeriod: int32(maxPeriod), MinPeriod: int32(minPeriod), N: int32(n),
		T0Before: int32(t0Before), T0After: int32(t0After), PrevPeriod: int32(prevPeriod),
		PrevGain: prevGain, Gain: gain, Buffer: copyStageFloat32(buffer),
	})
}
