package celt

import (
	"fmt"

	"github.com/thesyncim/gopus/rangecoding"
)

type decodedFrameSpectrum struct {
	qext           *preparedQEXTDecode
	coeffsL        []float64
	coeffsR        []float64
	collapse       []byte
	antiCollapseOn bool
}

func (d *Decoder) decodeFrameSpectrum(qextPayload []byte, rd *rangecoding.Decoder, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv int, energies []float64, fineQuant, finePriority, pulses, tfRes []int, intensity, dualStereo, balance, codedBands int) decodedFrameSpectrum {
	spectrum := decodedFrameSpectrum{}

	d.DecodeFineEnergy(energies, end, fineQuant)
	spectrum.qext = d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	if spectrum.qext != nil {
		d.decodeFineEnergyWithDecoderPrev(spectrum.qext.dec, energies, end, fineQuant, spectrum.qext.extraQuant[:end])
		if tmpQEXTHeaderDumpEnabled {
			fmt.Printf("QEXT_MAIN_FINE_DEC channels=%d tell=%d\n", d.channels, spectrum.qext.dec.TellFrac())
		}
	}
	traceRange("fine", rd)

	spectrum.coeffsL, spectrum.coeffsR, spectrum.collapse = quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.channels == 1, &d.rng, &d.scratchBands, &d.bandDebug,
		func() *rangecoding.Decoder {
			if spectrum.qext == nil {
				return nil
			}
			return spectrum.qext.dec
		}(), func() []int {
			if spectrum.qext == nil {
				return nil
			}
			return spectrum.qext.extraPulses[:end]
		}(), func() int {
			if spectrum.qext == nil {
				return 0
			}
			return spectrum.qext.totalBitsQ3
		}())
	if spectrum.qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, d.channels == 1, spectrum.qext)
	}
	traceRange("pvq", rd)

	if antiCollapseRsv > 0 {
		spectrum.antiCollapseOn = rd.DecodeRawBits(1) == 1
	}
	traceFlag("anticollapse_on", boolToInt(spectrum.antiCollapseOn))
	traceRange("anticollapse", rd)

	bitsLeft := totalBits - rd.Tell()
	if len(qextPayload) != 0 {
		d.DecodeEnergyFinaliseRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)
	}
	traceRange("finalise", rd)

	return spectrum
}
