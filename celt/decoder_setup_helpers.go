package celt

import "github.com/thesyncim/gopus/rangecoding"

type preparedDecodeFrame struct {
	rd          *rangecoding.Decoder
	mode        ModeConfig
	lm          int
	end         int
	prev1Energy []float64
	prev1LogE   []float64
	prev2LogE   []float64
}

func (d *Decoder) prepareDecodeFrame(data []byte, frameSize int) (preparedDecodeFrame, error) {
	if !ValidFrameSize(frameSize) {
		return preparedDecodeFrame{}, ErrInvalidFrameSize
	}

	currentFrame := d.decodeFrameIndex
	d.decodeFrameIndex++
	if tmpPVQCallDebugEnabled {
		d.bandDebug.qDbgDecodeFrame = currentFrame
		d.bandDebug.pvqCallSeq = 0
	}

	d.prepareMonoEnergyFromStereo()

	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}

	prev1Energy, prev1LogE, prev2LogE := d.snapshotDecodeHistory()

	return preparedDecodeFrame{
		rd:          rd,
		mode:        mode,
		lm:          lm,
		end:         end,
		prev1Energy: prev1Energy,
		prev1LogE:   prev1LogE,
		prev2LogE:   prev2LogE,
	}, nil
}
