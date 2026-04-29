package celt

import "github.com/thesyncim/gopus/rangecoding"

type preparedQEXTDecode struct {
	dec         *rangecoding.Decoder
	totalBitsQ3 int
	cfg         qextModeConfig
	end         int
	intensity   int
	dualStereo  int
	extraPulses []int
	extraQuant  []int
	energies    []float64
	coeffsL     []float64
	coeffsR     []float64
}

func (d *Decoder) decodeCoarseEnergyIntoWithPrevState(dst []float64, nbBands int, intra bool, lm int, prevState []float64, prevStride int, rd *rangecoding.Decoder) []float64 {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	needed := nbBands * d.channels
	if len(dst) < needed {
		dst = make([]float64, needed)
	} else {
		dst = dst[:needed]
	}
	if rd == nil {
		return dst
	}
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() {
		d.rangeDecoder = oldRD
	}()

	var alpha, beta float64
	if intra {
		alpha = 0.0
		beta = BetaIntra
	} else {
		alpha = AlphaCoef[lm]
		beta = BetaCoefInter[lm]
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()
	prevBandEnergy := ensureFloat64Slice(&d.scratchPrevBandEnergy, d.channels)
	for i := range prevBandEnergy {
		prevBandEnergy[i] = 0
	}

	for band := 0; band < nbBands; band++ {
		for c := 0; c < d.channels; c++ {
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = d.decodeLaplace(fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			prevFrameEnergy := 0.0
			idx := c*prevStride + band
			if idx >= 0 && idx < len(prevState) {
				prevFrameEnergy = prevState[idx]
			}
			minEnergy := -9.0 * DB6
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}
			pred := alpha*prevFrameEnergy + prevBandEnergy[c]
			q := float64(qi) * DB6
			energy := pred + q

			dst[c*nbBands+band] = energy
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	return dst
}

func (d *Decoder) storeQEXTEnergyState(energies []float64, nbBands int) {
	if nbBands <= 0 || len(energies) < nbBands*d.channels {
		return
	}
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		src := energies[c*nbBands : c*nbBands+nbBands]
		copy(d.qextOldBandE[base:base+nbBands], src)
		for band := nbBands; band < MaxBands; band++ {
			d.qextOldBandE[base+band] = 0
		}
	}
}

func (d *Decoder) prepareQEXTDecode(payload []byte, mainRD *rangecoding.Decoder, end, lm, frameSize int) *preparedQEXTDecode {
	if len(payload) == 0 || mainRD == nil || end <= 0 {
		return nil
	}

	extDec := &d.qextRangeDecoderScratch
	extDec.Init(payload)
	hdr := decodeQEXTHeader(extDec, d.channels, len(payload))

	qext := &d.scratchQEXTDecode
	*qext = preparedQEXTDecode{
		dec:         extDec,
		totalBitsQ3: len(payload) * (8 << bitRes),
		extraPulses: ensureIntSlice(&d.scratchQEXTPulses, MaxBands+nbQEXTBands),
		extraQuant:  ensureIntSlice(&d.scratchQEXTFineQuant, MaxBands+nbQEXTBands),
	}

	var qextMode *qextModeConfig
	if end == MaxBands {
		if cfg, ok := computeQEXTModeConfig(d.sampleRate, qextShortMDCTSize(frameSize)); ok {
			qextEnd := hdr.EndBands
			if qextEnd > cfg.EffBands {
				qextEnd = cfg.EffBands
			}
			if qextEnd > 0 {
				qext.cfg = cfg
				qext.end = qextEnd
				qext.intensity = hdr.Intensity
				if qext.intensity > qext.end {
					qext.intensity = qext.end
				}
				if d.channels == 2 && hdr.DualStereo && qext.intensity != 0 {
					qext.dualStereo = 1
				}

				qext.energies = ensureFloat64Slice(&d.scratchQEXTEnergies, qext.end*d.channels)
				qext.energies = qext.energies[:qext.end*d.channels]
				intra := extDec.Tell()+3 <= extDec.StorageBits() && extDec.DecodeBit(3) == 1
				qext.energies = d.decodeCoarseEnergyIntoWithPrevState(qext.energies, qext.end, intra, lm, d.qextOldBandE, MaxBands, extDec)
				qextMode = &qext.cfg
			}
		}
	}

	budgetQ3 := qext.totalBitsQ3 - mainRD.TellFrac() - 1
	if budgetQ3 < 0 {
		budgetQ3 = 0
	}
	tellBeforeAlloc := extDec.TellFrac()
	computeQEXTExtraAllocationDecodeWithMode(0, end, qext.end, budgetQ3, d.channels, lm, extDec, qext.extraPulses, qext.extraQuant, qextMode)
	_ = tellBeforeAlloc
	return qext
}

func (d *Decoder) decodeQEXTBands(frameSize, lm, shortBlocks, spread int, disableInv bool, qext *preparedQEXTDecode) {
	if qext == nil || qext.dec == nil || qext.end <= 0 {
		return
	}
	extBalance := qext.totalBitsQ3 - qext.dec.TellFrac()
	fineQ3 := 0
	if qext.end > 1 {
		fineQ3 = d.channels * (qext.extraQuant[MaxBands+1] << bitRes)
	}
	for i := 0; i < qext.end; i++ {
		idx := MaxBands + i
		extBalance -= qext.extraPulses[idx]
		extBalance -= fineQ3
	}
	if extBalance < 0 {
		extBalance = 0
	}

	d.DecodeFineEnergyWithDecoder(qext.dec, qext.energies, qext.end, qext.extraQuant[MaxBands:MaxBands+qext.end])
	zeros := ensureIntSlice(&d.scratchTFRes, qext.end)
	for i := 0; i < qext.end; i++ {
		zeros[i] = 0
	}
	var dummyDec rangecoding.Decoder
	dummyDec.Init(nil)

	qext.coeffsL, qext.coeffsR, _ = quantAllBandsDecodeWithScratchWithMode(
		qext.dec,
		d.channels,
		frameSize,
		lm,
		0,
		qext.end,
		qext.extraPulses[MaxBands:MaxBands+qext.end],
		shortBlocks,
		spread,
		qext.dualStereo,
		qext.intensity,
		zeros[:qext.end],
		qext.totalBitsQ3,
		extBalance,
		qext.end,
		disableInv,
		&d.rng,
		&d.scratchQEXTBands,
		&dummyDec,
		zeros[:qext.end],
		0,
		qext.cfg.EBands,
		qext.cfg.LogN,
		qext.cfg.CacheIndex,
		qext.cfg.CacheBits,
	)
	d.storeQEXTEnergyState(qext.energies, qext.end)
}
