package celt

// handleChannelTransition detects and handles mono-to-stereo channel transitions.
// When transitioning from mono to stereo, the right channel overlap buffer must be
// initialized from the left channel to match libopus behavior.
// This ensures smooth crossfade during the transition.
//
// Additionally, energy history arrays (prevEnergy, prevEnergy2, prevLogE, prevLogE2)
// must be copied/initialized for the right channel. In libopus, mono frames always
// copy their energy to the right channel position after decoding:
//
//	if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands);
//
// This means when transitioning to stereo, the right channel already has valid energy.
// We replicate this behavior here for transitions.
//
// Reference: libopus celt/celt_decoder.c - mono-to-stereo handling
//
// Returns true if a mono-to-stereo transition occurred.
func (d *Decoder) handleChannelTransition(streamChannels int) bool {
	prevChannels := d.prevStreamChannels
	d.prevStreamChannels = streamChannels

	// Detect mono-to-stereo transition: previous was mono (1), current is stereo (2)
	if prevChannels == 1 && streamChannels == 2 && d.channels == 2 {
		// Copy left channel overlap buffer to right channel for smooth transition
		// Overlap buffer layout: [Left: 0..Overlap-1] [Right: Overlap..2*Overlap-1]
		// This matches libopus which copies decode_mem[0] to decode_mem[1] on transition
		if len(d.overlapBuffer) >= Overlap*2 {
			for i := 0; i < Overlap; i++ {
				d.overlapBuffer[Overlap+i] = d.overlapBuffer[i]
			}
		}

		// Copy left channel energy state to right channel.
		// This matches libopus behavior where mono frames always update both channels'
		// energy history (via OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)).
		// Energy arrays layout: [Left: 0..MaxBands-1] [Right: MaxBands..2*MaxBands-1]
		if len(d.prevEnergy) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				d.prevEnergy[MaxBands+i] = d.prevEnergy[i]
			}
		}
		if len(d.prevEnergy2) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				d.prevEnergy2[MaxBands+i] = d.prevEnergy2[i]
			}
		}
		if len(d.prevLogE) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				d.prevLogE[MaxBands+i] = d.prevLogE[i]
			}
		}
		if len(d.prevLogE2) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				d.prevLogE2[MaxBands+i] = d.prevLogE2[i]
			}
		}
		if len(d.backgroundEnergy) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				d.backgroundEnergy[MaxBands+i] = d.backgroundEnergy[i]
			}
		}

		// NOTE: preemphState is NOT copied during transition.
		// In libopus, each channel maintains its own independent de-emphasis filter state.
		// During mono packets on a stereo decoder, both states are updated independently
		// (with the same input but different state histories). At transition to stereo,
		// each channel continues with its own state - no copying is done.

		return true
	}

	// Detect stereo-to-mono transition: previous was stereo (2), current is mono (1)
	// For stereo-to-mono, libopus uses max of L/R for mono prediction, but doesn't
	// need to copy state since mono decoding will overwrite. However, we should ensure
	// the mono channel has reasonable values by taking max of L/R energies.
	if prevChannels == 2 && streamChannels == 1 && d.channels == 2 {
		// For stereo->mono transition, take max of L/R for energy state
		// This matches libopus prepareMonoEnergyFromStereo behavior
		if len(d.prevEnergy) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				right := d.prevEnergy[MaxBands+i]
				if right > d.prevEnergy[i] {
					d.prevEnergy[i] = right
				}
			}
		}
		if len(d.prevLogE) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				right := d.prevLogE[MaxBands+i]
				if right > d.prevLogE[i] {
					d.prevLogE[i] = right
				}
			}
		}
		if len(d.prevLogE2) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				right := d.prevLogE2[MaxBands+i]
				if right > d.prevLogE2[i] {
					d.prevLogE2[i] = right
				}
			}
		}
		if len(d.backgroundEnergy) >= MaxBands*2 {
			for i := 0; i < MaxBands; i++ {
				right := d.backgroundEnergy[MaxBands+i]
				if right > d.backgroundEnergy[i] {
					d.backgroundEnergy[i] = right
				}
			}
		}
		return true
	}

	return false
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
	needed := MaxBands * channels
	if len(d.prevEnergy) < needed {
		prev := make([]float64, needed)
		copy(prev, d.prevEnergy)
		d.prevEnergy = prev
	}
	if len(d.prevEnergy2) < needed {
		prev := make([]float64, needed)
		copy(prev, d.prevEnergy2)
		d.prevEnergy2 = prev
	}
	if len(d.prevLogE) < needed {
		prev := make([]float64, needed)
		copy(prev, d.prevLogE)
		for i := len(d.prevLogE); i < needed; i++ {
			prev[i] = -28.0
		}
		d.prevLogE = prev
	}
	if len(d.prevLogE2) < needed {
		prev := make([]float64, needed)
		copy(prev, d.prevLogE2)
		for i := len(d.prevLogE2); i < needed; i++ {
			prev[i] = -28.0
		}
		d.prevLogE2 = prev
	}
	if len(d.backgroundEnergy) < needed {
		prev := make([]float64, needed)
		copy(prev, d.backgroundEnergy)
		for i := len(d.backgroundEnergy); i < needed; i++ {
			prev[i] = 0
		}
		d.backgroundEnergy = prev
	}
	if len(d.qextOldBandE) < needed {
		prev := make([]float64, needed)
		copy(prev, d.qextOldBandE)
		d.qextOldBandE = prev
	}
}

func (d *Decoder) allocationScratch() []int {
	return ensureIntSlice(&d.scratchAllocWork, MaxBands*4)
}

// prepareMonoEnergyFromStereo mirrors libopus behavior for mono streams by
// using the max of L/R energies for prediction when stereo history exists.
func (d *Decoder) prepareMonoEnergyFromStereo() {
	if d.channels != 1 || len(d.prevEnergy) < MaxBands*2 {
		return
	}
	for i := 0; i < MaxBands; i++ {
		right := d.prevEnergy[MaxBands+i]
		if right > d.prevEnergy[i] {
			d.prevEnergy[i] = right
		}
	}
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy decoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (d *Decoder) PrevEnergy() []float64 {
	return d.prevEnergy
}

// PrevEnergy2 returns the band energies from two frames ago.
// Used for anti-collapse detection.
func (d *Decoder) PrevEnergy2() []float64 {
	return d.prevEnergy2
}

// SetPrevEnergy copies the given energies to the previous energy buffer.
// Also shifts current prev to prev2.
func (d *Decoder) SetPrevEnergy(energies []float64) {
	// Shift: current prev becomes prev2
	copy(d.prevEnergy2, d.prevEnergy)
	// Copy new energies to prev
	copy(d.prevEnergy, energies)
}

// SetPrevEnergyWithPrev updates prevEnergy using the provided previous state.
// This avoids losing the prior frame when prevEnergy is updated during decoding.
// The energies array uses compact layout [L0..L(n-1), R0..R(n-1)] where n = nbBands.
// The prevEnergy array uses full layout [L0..L20, R0..R20] where 21 = MaxBands.
func (d *Decoder) SetPrevEnergyWithPrev(prev, energies []float64) {
	if len(prev) == len(d.prevEnergy2) {
		copy(d.prevEnergy2, prev)
	} else {
		copy(d.prevEnergy2, d.prevEnergy)
	}

	// Determine nbBands from the energies array length
	nbBands := len(energies) / d.channels
	if nbBands > MaxBands {
		nbBands = MaxBands
	}

	// Copy with layout conversion: compact [c*nbBands+band] -> full [c*MaxBands+band]
	for c := 0; c < d.channels; c++ {
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := c*MaxBands + band
			if src < len(energies) {
				d.prevEnergy[dst] = energies[src]
			}
		}
	}
}

func (d *Decoder) updateLogE(energies []float64, nbBands int, transient bool) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if len(energies) < nbBands*d.channels {
		nbBands = len(energies) / d.channels
	}
	if nbBands <= 0 {
		return
	}

	if !transient {
		copy(d.prevLogE2, d.prevLogE)
	}
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := base + band
			e := energies[src]
			if transient {
				if e < d.prevLogE[dst] {
					d.prevLogE[dst] = e
				}
			} else {
				d.prevLogE[dst] = e
			}
		}
	}
}

func (d *Decoder) ensureBackgroundEnergyState() {
	if len(d.backgroundEnergy) == len(d.prevEnergy) {
		return
	}
	if len(d.backgroundEnergy) < len(d.prevEnergy) {
		prev := make([]float64, len(d.prevEnergy))
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
	maxIncUnits := d.plcLossDuration + m
	if maxIncUnits > 160 {
		maxIncUnits = 160
	}
	maxBackgroundIncrease := float64(maxIncUnits) * 0.001
	for i := range d.backgroundEnergy {
		bg := d.backgroundEnergy[i] + maxBackgroundIncrease
		e := d.prevEnergy[i]
		if bg > e {
			bg = e
		}
		d.backgroundEnergy[i] = bg
	}
}
