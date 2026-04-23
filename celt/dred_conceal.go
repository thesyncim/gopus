package celt

import "github.com/thesyncim/gopus/plc"

const (
	dred48kSincOrder = 48
)

// Matches libopus celt_decoder.c deep PLC/DRED sinc_filter.
var dred48kSincFilter = [...]float32{
	4.2931e-05, -0.000190293, -0.000816132, -0.000637162, 0.00141662, 0.00354764, 0.00184368, -0.00428274,
	-0.00856105, -0.0034003, 0.00930201, 0.0159616, 0.00489785, -0.0169649, -0.0259484, -0.00596856,
	0.0286551, 0.0405872, 0.00649994, -0.0509284, -0.0716655, -0.00665212, 0.134336, 0.278927,
	0.339995, 0.278927, 0.134336, -0.00665212, -0.0716655, -0.0509284, 0.00649994, 0.0405872,
	0.0286551, -0.00596856, -0.0259484, -0.0169649, 0.00489785, 0.0159616, 0.00930201, -0.0034003,
	-0.00856105, -0.00428274, 0.00184368, 0.00354764, 0.00141662, -0.000637162, -0.000816132, -0.000190293,
	4.2931e-05,
}

// ConcealDRED48kMonoToFloat32 mirrors the libopus FRAME_DRED lost-frame path
// for the current mono 48 kHz seam. The caller owns the queued 16 kHz neural
// history and provides a frame generator that fills one 10 ms concealed frame
// in normalized float32 units whenever more queued samples are needed.
func (d *Decoder) ConcealDRED48kMonoToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || len(out) < frameSize || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	if d.chooseLostFrameType(0, true, true) != frameDRED {
		return false
	}
	return d.concealDRED48kMono(out[:frameSize], frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true)
}

// ConcealDRED48kMonoStateOnly updates retained CELT 48 kHz mono DRED state
// without producing caller-visible PCM or recording another loss event.
// This is used by the hybrid decoder, which already emitted the audible lost
// frame via its SILK+CELT PLC base but still needs libopus-shaped DRED CELT
// waveform state for the next good packet.
func (d *Decoder) ConcealDRED48kMonoStateOnly(
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	return d.concealDRED48kMono(nil, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, false)
}

func (d *Decoder) concealDRED48kMono(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
	recordLoss bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	lossCount := d.plcState.LostCount()
	if recordLoss {
		_ = d.plcState.RecordLoss()
		lossCount = d.plcState.LostCount()
	}
	*lastNeural = d.lastPLCFrameWasNeural()

	totalSamples := frameSize + Overlap
	d.scratchPLC = ensureFloat64Slice(&d.scratchPLC, totalSamples)
	if !d.concealPeriodicPLC(d.scratchPLC[:totalSamples], frameSize, lossCount, d.lastPLCFrameWasPeriodic() || *lastNeural, false) {
		return false
	}

	baseline := ensureFloat64Slice(&d.scratchPLCDREDBase, Overlap)
	copy(baseline[:Overlap], d.scratchPLC[:Overlap])

	samplesNeeded16k := (frameSize + dred48kSincOrder + Overlap) / 3
	if !*lastNeural {
		*plcFill = 0
	}
	for *plcFill < samplesNeeded16k {
		if *plcFill+160 > len(plcPCM) {
			return false
		}
		frame := plcPCM[*plcFill : *plcFill+160]
		if !generate(frame) {
			return false
		}
		for i := range frame {
			frame[i] = quantizedPCM16GridSample(frame[i])
		}
		*plcFill += 160
	}

	neural := ensureFloat32Slice(&d.scratchPLCDREDNeural, totalSamples)
	for i := 0; i < totalSamples/3; i++ {
		var sum float32
		for j := 0; j < 17; j++ {
			sum += 3 * (plcPCM[i+j] * 32768) * dred48kSincFilter[3*j]
		}
		neural[3*i] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * (plcPCM[i+j+1] * 32768) * dred48kSincFilter[3*j+2]
		}
		neural[3*i+1] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * (plcPCM[i+j+1] * 32768) * dred48kSincFilter[3*j+1]
		}
		neural[3*i+2] = sum
	}

	consumed16k := frameSize / 3
	copy(plcPCM[:], plcPCM[consumed16k:*plcFill])
	*plcFill -= consumed16k

	preemph := float32(PreemphCoef)
	preemphMem := *plcPreemphMem * 32768
	for i := 0; i < frameSize; i++ {
		tmp := neural[i]
		d.scratchPLC[i] = float64(tmp - preemph*preemphMem)
		preemphMem = tmp
	}
	// Match libopus celt_decode_lost(FRAME_DRED): retain plc_preemphasis_mem at
	// the frame boundary and keep the overlap tail in a local only.
	*plcPreemphMem = preemphMem * (1.0 / 32768.0)
	overlapMem := preemphMem
	for i := 0; i < Overlap; i++ {
		idx := frameSize + i
		tmp := neural[idx]
		d.scratchPLC[idx] = float64(tmp - preemph*overlapMem)
		overlapMem = tmp
	}

	if !*lastNeural {
		window := GetWindowBufferF32(Overlap)
		blend := min(Overlap, frameSize)
		for i := 0; i < blend; i++ {
			d.scratchPLC[i] = float64((1-window[i])*float32(baseline[i]) + window[i]*float32(d.scratchPLC[i]))
		}
	}

	d.updatePostfilterHistory(d.scratchPLC[:frameSize], frameSize, combFilterHistory)
	d.updatePLCDecodeHistory(d.scratchPLC[:frameSize], frameSize, plcDecodeBufferSize)
	d.updatePLCOverlapBuffer(d.scratchPLC[:totalSamples], frameSize)
	if out != nil {
		if len(out) < frameSize {
			return false
		}
		d.applyDeemphasisAndScaleToFloat32(out[:frameSize], d.scratchPLC[:frameSize], 1.0/32768.0)
	} else {
		d.advanceDeemphasisStateMono(d.scratchPLC[:frameSize])
	}

	if recordLoss {
		d.finishLostFrame(frameDRED, frameSize)
	} else {
		d.plcDuration = 0
		d.plcSkip = false
		d.plcLastFrameType = frameDRED
		d.plcPrevLossWasPeriodic = false
	}
	d.plcPrefilterAndFoldPending = true
	*lastNeural = true
	return true
}
