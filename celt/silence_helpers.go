package celt

// DecodeStereoParams decodes stereo parameters (intensity and dual stereo).
// Reference: RFC 6716 Section 4.3.4, libopus celt/celt_decoder.c
func (d *Decoder) DecodeStereoParams(nbBands int) (intensity, dualStereo int) {
	if d.rangeDecoder == nil {
		return -1, 0
	}

	// IntensityDecay = 16384 (Q15)
	const decay = 16384

	// Compute fs0 exactly as encoder does
	// fs0 = laplaceNMin + (laplaceFS - laplaceNMin)*decay >> 15
	fs0 := laplaceNMin + ((laplaceFS-laplaceNMin)*decay)>>15

	// Decode intensity band index using Laplace distribution
	intensity = d.decodeLaplace(fs0, decay)

	// Decode dual stereo flag
	dualStereo = d.rangeDecoder.DecodeBit(1)

	return intensity, dualStereo
}

func (d *Decoder) synthesizeSilenceMono(frameSize int) []float64 {
	if frameSize <= 0 {
		return nil
	}
	out := ensureFloat64Slice(&d.scratchSynth, frameSize)
	clear(out)

	if Overlap <= 0 {
		return out
	}
	if len(d.overlapBuffer) < Overlap {
		buf := make([]float64, Overlap)
		copy(buf, d.overlapBuffer)
		d.overlapBuffer = buf
	}
	prev := d.overlapBuffer[:Overlap]
	window := GetWindowBufferF32(Overlap)
	half := Overlap >> 1
	if half > frameSize {
		half = frameSize
	}
	for i := 0; i < half; i++ {
		x2 := float32(prev[i])
		out[i] = float64(x2 * window[Overlap-1-i])
		j := Overlap - 1 - i
		if j < frameSize {
			out[j] = float64(x2 * window[i])
		}
	}
	clear(prev)
	return out
}

func (d *Decoder) synthesizeSilenceStereo(frameSize int) []float64 {
	if frameSize <= 0 {
		return nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		buf := make([]float64, Overlap*2)
		copy(buf, d.overlapBuffer)
		d.overlapBuffer = buf
	}

	out := ensureFloat64Slice(&d.scratchStereo, frameSize*2)
	clear(out)
	if Overlap <= 0 {
		return out
	}

	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]
	window := GetWindowBufferF32(Overlap)
	half := Overlap >> 1
	if half > frameSize {
		half = frameSize
	}
	for i := 0; i < half; i++ {
		j := Overlap - 1 - i

		x2L := float32(overlapL[i])
		out[2*i] = float64(x2L * window[Overlap-1-i])
		if j < frameSize {
			out[2*j] = float64(x2L * window[i])
		}

		x2R := float32(overlapR[i])
		out[2*i+1] = float64(x2R * window[Overlap-1-i])
		if j < frameSize {
			out[2*j+1] = float64(x2R * window[i])
		}
	}
	clear(overlapL)
	clear(overlapR)
	return out
}

// decodeSilenceFrame synthesizes a CELT silence frame from overlap state.
func (d *Decoder) decodeSilenceFrame(frameSize int, newPeriod int, newGain float64, newTapset int) []float64 {
	mode := GetModeConfig(frameSize)
	d.applyPendingPLCPrefilterAndFold()
	var samples []float64
	if d.channels == 2 {
		samples = d.synthesizeSilenceStereo(frameSize)
	} else {
		samples = d.synthesizeSilenceMono(frameSize)
	}
	if len(samples) == 0 {
		return nil
	}

	d.applyPostfilter(samples, frameSize, mode.LM, newPeriod, newGain, newTapset)
	if len(d.directOutPCM) >= len(samples) {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
	} else {
		d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	}

	return samples
}
