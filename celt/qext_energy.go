package celt

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

func computeQEXTBandAmplitudesInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []float64) {
	if cfg == nil || end <= 0 {
		return
	}
	if end > len(cfg.EBands)-1 {
		end = len(cfg.EBands) - 1
	}
	if end > len(bandE) {
		end = len(bandE)
	}
	for i := 0; i < end; i++ {
		start := cfg.EBands[i] << lm
		stop := cfg.EBands[i+1] << lm
		if start < 0 {
			start = 0
		}
		if stop > len(mdctCoeffs) {
			stop = len(mdctCoeffs)
		}
		if stop <= start {
			bandE[i] = 1e-27
			continue
		}
		sum := float32(1e-27) + float32(sumOfSquaresF64toF32(mdctCoeffs[start:stop], stop-start))
		bandE[i] = float64(float32(math.Sqrt(float64(sum))))
	}
}

func computeQEXTBandLogEInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE, bandLogE []float64) {
	computeQEXTBandAmplitudesInto(mdctCoeffs, cfg, end, lm, bandE)
	if end > len(bandLogE) {
		end = len(bandLogE)
	}
	for i := 0; i < end; i++ {
		amp := bandE[i]
		if amp < 1e-27 {
			amp = 1e-27
		}
		bandLogE[i] = float64(celtLog2(float32(amp))) - eMeans[i]
	}
}

func normalizeQEXTBandsInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE, norm []float64) {
	if cfg == nil || end <= 0 || len(norm) == 0 {
		return
	}
	clear(norm)
	if end > len(cfg.EBands)-1 {
		end = len(cfg.EBands) - 1
	}
	if end > len(bandE) {
		end = len(bandE)
	}
	for i := 0; i < end; i++ {
		start := cfg.EBands[i] << lm
		stop := cfg.EBands[i+1] << lm
		if start < 0 {
			start = 0
		}
		if stop > len(mdctCoeffs) {
			stop = len(mdctCoeffs)
		}
		if stop > len(norm) {
			stop = len(norm)
		}
		if stop <= start {
			continue
		}
		amp := float32(bandE[i])
		if amp < float32(1e-27) {
			amp = float32(1e-27)
		}
		g := float32(1.0) / amp
		for j := start; j < stop; j++ {
			norm[j] = float64(float32(mdctCoeffs[j]) * g)
		}
	}
}

func (e *Encoder) encodeQEXTCoarseEnergyWithEncoder(re *rangecoding.Encoder, energies []float64, nbBands, lm, nbAvailableBytes int, oldBandEState, quantizedEnergies, errorVals []float64, delayedIntra *float64) bool {
	if re == nil || nbBands <= 0 {
		return false
	}
	needed := nbBands * e.channels
	if len(energies) < needed || len(quantizedEnergies) < needed || len(errorVals) < needed {
		return false
	}
	if len(oldBandEState) < MaxBands*e.channels {
		return false
	}

	savedRE := e.rangeEncoder
	savedPrev := e.prevEnergy
	savedDelayed := e.delayedIntra
	savedCoarseAvail := e.coarseAvailableBytes
	savedFrameBits := e.frameBits
	savedQuant := e.scratch.quantizedEnergies
	savedErr := e.scratch.coarseError
	defer func() {
		e.rangeEncoder = savedRE
		e.prevEnergy = savedPrev
		e.delayedIntra = savedDelayed
		e.coarseAvailableBytes = savedCoarseAvail
		e.frameBits = savedFrameBits
		e.scratch.quantizedEnergies = savedQuant
		e.scratch.coarseError = savedErr
	}()

	e.rangeEncoder = re
	e.prevEnergy = oldBandEState
	if delayedIntra != nil {
		e.delayedIntra = *delayedIntra
	} else {
		e.delayedIntra = 0
	}
	e.coarseAvailableBytes = nbAvailableBytes
	e.frameBits = re.StorageBits()
	e.scratch.quantizedEnergies = quantizedEnergies[:needed]
	e.scratch.coarseError = errorVals[:needed]

	clear(e.scratch.quantizedEnergies)
	clear(e.scratch.coarseError)

	intra := e.DecideIntraMode(energies, 0, nbBands, lm)
	if re.Tell()+3 <= e.frameBits {
		re.EncodeBit(boolToInt(intra), 3)
	}
	e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
	if delayedIntra != nil {
		*delayedIntra = e.delayedIntra
	}
	return intra
}
