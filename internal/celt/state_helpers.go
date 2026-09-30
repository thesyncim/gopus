package celt

import "github.com/thesyncim/gopus/internal/extsupport"

// handleChannelTransition mirrors CELT_SET_CHANNELS in celt_decoder.c.
// Received mono frames update both energy histories during decoding; changing
// the coded channel count preserves each output channel's overlap and PLC state.
func (d *Decoder) handleChannelTransition(streamChannels int) bool {
	previous := d.prevStreamChannels
	d.prevStreamChannels = int32(streamChannels)
	return d.channels == 2 && previous != 0 && previous != int32(streamChannels)
}

// ensureEnergyState ensures the decoder has room for the requested channel count
// in its energy/history arrays. This is needed for stereo packets when output is mono.
func (d *Decoder) ensureEnergyState(channels int) {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	needed := d.predStride() * channels
	if len(d.prevEnergy) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevEnergy)
		d.prevEnergy = prev
	}
	if len(d.prevEnergy2) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevEnergy2)
		d.prevEnergy2 = prev
	}
	if len(d.prevLogE) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevLogE)
		for i := len(d.prevLogE); i < needed; i++ {
			prev[i] = -28.0
		}
		d.prevLogE = prev
	}
	if len(d.prevLogE2) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevLogE2)
		for i := len(d.prevLogE2); i < needed; i++ {
			prev[i] = -28.0
		}
		d.prevLogE2 = prev
	}
	if len(d.backgroundEnergy) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.backgroundEnergy)
		for i := len(d.backgroundEnergy); i < needed; i++ {
			prev[i] = 0
		}
		d.backgroundEnergy = prev
	}
	if extsupport.QEXT {
		d.growQEXTOldBandE(needed)
	}
}

func (d *Decoder) ensureQEXTOldBandE() []celtGLog {
	needed := nbQEXTBands * int(d.channels)
	qextState := d.ensureQEXTState()
	if len(qextState.oldBandE) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, qextState.oldBandE)
		qextState.oldBandE = prev
	}
	return qextState.oldBandE
}

func (d *Decoder) allocationScratch() []int32 {
	return ensureInt32Slice(&d.scratchAllocWork, d.predStride()*5)
}

func (d *Decoder) snapshotDecodeHistory() ([]celtGLog, []celtGLog, []celtGLog) {
	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	return prev1Energy, d.prevLogE, d.prevLogE2
}

// prepareMonoEnergyFromStereo mirrors libopus behavior for mono streams by
// using the max of L/R energies for prediction when stereo history exists.
func (d *Decoder) prepareMonoEnergyFromStereo() {
	stride := d.predStride()
	if d.channels != 1 || len(d.prevEnergy) < stride*2 {
		return
	}
	left := d.prevEnergy[:stride]
	right := d.prevEnergy[stride : 2*stride]
	right = right[:len(left)]
	for i, r := range right {
		if r > left[i] {
			left[i] = r
		}
	}
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy decoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (d *Decoder) PrevEnergy() []float32 {
	out := make([]float32, len(d.prevEnergy))
	copy(out, d.prevEnergy)
	return out
}

// PrevEnergy2 returns the band energies from two frames ago.
// Used for anti-collapse detection.
func (d *Decoder) PrevEnergy2() []float32 {
	out := make([]float32, len(d.prevEnergy2))
	copy(out, d.prevEnergy2)
	return out
}

// SetPrevEnergy copies the given energies to the previous energy buffer.
// Also shifts current prev to prev2.
func (d *Decoder) SetPrevEnergy(energies []float32) {
	// Shift: current prev becomes prev2
	copy(d.prevEnergy2, d.prevEnergy)
	// Copy new energies to prev
	copy(d.prevEnergy, energies)
}

// SetPrevEnergyWithPrev updates prevEnergy using the provided previous state.
// This avoids losing the prior frame when prevEnergy is updated during decoding.
// The energies array uses compact layout [L0..L(n-1), R0..R(n-1)] where n = nbBands.
// The prevEnergy array uses full layout [L0..L20, R0..R20] where 21 = MaxBands.
func (d *Decoder) SetPrevEnergyWithPrev(prev, energies []float32) {
	if len(prev) == len(d.prevEnergy2) {
		copy(d.prevEnergy2, prev)
	} else {
		copy(d.prevEnergy2, d.prevEnergy)
	}

	// Determine nbBands from the energies array length
	channels := int(d.channels)
	nbBands := min(len(energies)/channels, d.predStride())

	// Copy with layout conversion: compact [c*nbBands+band] -> prediction-stride
	// [c*predStride+band] (predStride == MaxBands for the static codec, the mode's
	// nbEBands for a per-mode custom layout).
	stride := d.predStride()
	for c := range channels {
		copy(d.prevEnergy[c*stride:c*stride+nbBands], energies[c*nbBands:(c+1)*nbBands])
	}
}

func (d *Decoder) setPrevEnergyGLog(energies []celtGLog) {
	d.setPrevEnergyGLogWithPrev(nil, energies)
}

func (d *Decoder) setPrevEnergyGLogWithPrev(prev []celtGLog, energies []celtGLog) {
	if len(prev) == len(d.prevEnergy2) {
		copy(d.prevEnergy2, prev)
	} else {
		copy(d.prevEnergy2, d.prevEnergy)
	}

	// Determine nbBands from the energies array length
	channels := int(d.channels)
	nbBands := min(len(energies)/channels, d.predStride())

	// Copy with layout conversion: compact [c*nbBands+band] -> prediction-stride.
	stride := d.predStride()
	for c := range channels {
		copy(d.prevEnergy[c*stride:c*stride+nbBands], energies[c*nbBands:(c+1)*nbBands])
	}
}

func (d *Decoder) updateLogEGLog(energies []celtGLog, nbBands int, transient bool) {
	if nbBands > d.predStride() {
		nbBands = d.predStride()
	}
	if nbBands <= 0 {
		return
	}
	channels := int(d.channels)
	if len(energies) < nbBands*channels {
		nbBands = len(energies) / channels
	}
	if nbBands <= 0 {
		return
	}

	if !transient {
		copy(d.prevLogE2, d.prevLogE)
	}
	stride := d.predStride()
	for c := range channels {
		src := energies[c*nbBands : (c+1)*nbBands]
		dst := d.prevLogE[c*stride : c*stride+nbBands]
		if !transient {
			copy(dst, src)
			continue
		}
		for band, e := range src {
			if e < dst[band] {
				dst[band] = e
			}
		}
	}
}

func (d *Decoder) ensureBackgroundEnergyState() {
	if len(d.backgroundEnergy) == len(d.prevEnergy) {
		return
	}
	if len(d.backgroundEnergy) < len(d.prevEnergy) {
		prev := make([]celtGLog, len(d.prevEnergy))
		copy(prev, d.backgroundEnergy)
		for i := len(d.backgroundEnergy); i < len(prev); i++ {
			prev[i] = 0
		}
		d.backgroundEnergy = prev
		return
	}
	d.backgroundEnergy = d.backgroundEnergy[:len(d.prevEnergy)]
}

func (d *Decoder) updateBackgroundEnergy(lm int) {
	d.ensureBackgroundEnergyState()
	if lm < 0 {
		lm = 0
	}
	if lm > 30 {
		lm = 30
	}
	m := 1 << uint(lm)
	maxIncUnits := min(int(d.plcLossDuration)+m, 160)
	maxBackgroundIncrease := celtGLog(float32(maxIncUnits) * 0.001)
	background := d.backgroundEnergy
	prev := d.prevEnergy[:len(background)]
	for i, bg := range background {
		bg += maxBackgroundIncrease
		if e := prev[i]; bg > e {
			bg = e
		}
		background[i] = bg
	}
}
