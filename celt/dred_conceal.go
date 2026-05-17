//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

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

// ConcealPLCNeural48kMonoToFloat32 mirrors the libopus FRAME_PLC_NEURAL
// lost-frame path for the current mono 48 kHz seam. The caller owns the
// queued 16 kHz neural history and provides a frame generator that fills one
// 10 ms concealed frame in normalized float32 units whenever more queued
// samples are needed.
func (d *Decoder) ConcealPLCNeural48kMonoToFloat32(
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
	if d.chooseLostFrameType(0, true, false) != framePLCNeural {
		return false
	}
	return d.concealNeural48kMono(out[:frameSize], frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, framePLCNeural)
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
	return d.concealNeural48kMono(out[:frameSize], frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, frameDRED)
}

// ConcealDRED48kToFloat32 mirrors ConcealDRED48kMonoToFloat32 for both mono
// and stereo decoders. libopus implements DRED concealment as fundamentally
// mono (a single LPCNetPLCState, see opus_decoder.c) and, for stereo output,
// runs the mono pipeline against channel-0 CELT state then duplicates that
// state to channel-1 (celt_decoder.c:1066-1067:
// `if (C==2) OPUS_COPY(decode_mem[1], decode_mem[0], ...)`). This wrapper
// follows the same shape: it runs concealNeural48kMono against channel-0
// slices of the CELT state buffers, mirrors channel-0 to channel-1 after
// the call, and writes mono PCM duplicated across both interleaved channels.
func (d *Decoder) ConcealDRED48kToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || frameSize <= 0 {
		return false
	}
	if d.channels == 1 {
		return d.ConcealDRED48kMonoToFloat32(out, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate)
	}
	if d.channels != 2 {
		return false
	}
	if len(out) < frameSize*d.channels {
		return false
	}
	return d.runStereoDREDConceal(out, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, frameDRED)
}

// ConcealPLCNeural48kToFloat32 mirrors ConcealPLCNeural48kMonoToFloat32 for
// both mono and stereo decoders. See ConcealDRED48kToFloat32 for the
// mono-downmix-in / mono-duplicate-out stereo rationale.
func (d *Decoder) ConcealPLCNeural48kToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || frameSize <= 0 {
		return false
	}
	if d.channels == 1 {
		return d.ConcealPLCNeural48kMonoToFloat32(out, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate)
	}
	if d.channels != 2 {
		return false
	}
	if len(out) < frameSize*d.channels {
		return false
	}
	return d.runStereoDREDConceal(out, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, framePLCNeural)
}

// runStereoDREDConceal mirrors the libopus stereo neural PLC path that
// computes mono PCM and mono CELT state and then duplicates the channel-0
// state and PCM to channel-1. The CELT mono helpers are exercised by
// temporarily aliasing the channel-0 portion of each per-channel state
// buffer to the decoder's mono slot.
func (d *Decoder) runStereoDREDConceal(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
	recordLoss bool,
	frameType int,
) bool {
	if d == nil || d.channels != 2 || frameSize <= 0 {
		return false
	}
	if lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	// Either a real output buffer of size >= frameSize*2 or nil for state-only.
	if out != nil && len(out) < frameSize*2 {
		return false
	}

	// Materialize any ringed PLC decode history first so both channels are
	// in contiguous libopus layout before we narrow our view to channel 0.
	d.materializePLCDecodeHistory()
	d.materializePostfilterHistoryFromPLC()

	// Snapshot stereo buffers and replace them with channel-0 aliases so the
	// mono helper sees a self-consistent mono CELT state without reallocating.
	origPostfilterMem := d.postfilterMem
	origPLCDecodeMem := d.plcDecodeMem
	origOverlapBuffer := d.overlapBuffer
	origPreemphState := d.preemphState
	origChannels := d.channels

	d.channels = 1
	if len(origPostfilterMem) >= combFilterHistory*2 {
		d.postfilterMem = origPostfilterMem[:combFilterHistory]
	}
	if len(origPLCDecodeMem) >= plcDecodeBufferSize*2 {
		d.plcDecodeMem = origPLCDecodeMem[:plcDecodeBufferSize]
	}
	if len(origOverlapBuffer) >= Overlap*2 {
		d.overlapBuffer = origOverlapBuffer[:Overlap]
	}
	if len(origPreemphState) >= 2 {
		d.preemphState = origPreemphState[:1]
	}

	// Run the mono neural concealment pipeline against the channel-0 slices.
	monoOut := out
	if out != nil {
		monoOut = out[:frameSize]
	}
	ok := d.concealNeural48kMono(monoOut, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, recordLoss, frameType)

	// Restore stereo slice headers. concealNeural48kMono may have grown its
	// channel-0 buffers in place when sizes already matched, so refresh the
	// headers from the originals before mirroring to channel 1.
	d.channels = origChannels
	d.postfilterMem = origPostfilterMem
	d.plcDecodeMem = origPLCDecodeMem
	d.overlapBuffer = origOverlapBuffer
	d.preemphState = origPreemphState

	if !ok {
		return false
	}

	// Mirror channel-0 CELT state to channel-1 (matches libopus C
	// `OPUS_COPY(decode_mem[1], decode_mem[0], ...)`).
	if len(d.postfilterMem) >= combFilterHistory*2 {
		copy(d.postfilterMem[combFilterHistory:combFilterHistory*2], d.postfilterMem[:combFilterHistory])
	}
	if len(d.plcDecodeMem) >= plcDecodeBufferSize*2 {
		copy(d.plcDecodeMem[plcDecodeBufferSize:plcDecodeBufferSize*2], d.plcDecodeMem[:plcDecodeBufferSize])
	}
	if len(d.overlapBuffer) >= Overlap*2 {
		copy(d.overlapBuffer[Overlap:Overlap*2], d.overlapBuffer[:Overlap])
	}
	if len(d.preemphState) >= 2 {
		d.preemphState[1] = d.preemphState[0]
	}

	// Duplicate mono PCM into interleaved stereo PCM for the caller.
	if out != nil {
		for i := frameSize - 1; i >= 0; i-- {
			v := out[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
	return true
}

// ConcealPLCNeural48kMonoStateOnly updates retained CELT 48 kHz mono neural
// PLC state without producing caller-visible PCM or recording another loss
// event.
func (d *Decoder) ConcealPLCNeural48kMonoStateOnly(
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
	return d.concealNeural48kMono(nil, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, false, framePLCNeural)
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
	return d.concealNeural48kMono(nil, frameSize, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, false, frameDRED)
}

func (d *Decoder) concealNeural48kMono(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []float32,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
	recordLoss bool,
	frameType int,
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

	preemph := deepPLCPreemphCoef
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
		d.finishLostFrame(frameType, frameSize)
	} else {
		switch frameType {
		case frameDRED:
			d.plcDuration = 0
		case framePLCNeural:
			d.accumulatePLCLossDuration(frameSize)
		}
		d.plcSkip = false
		d.plcLastFrameType = frameType
		d.plcPrevLossWasPeriodic = false
	}
	d.plcPrefilterAndFoldPending = true
	*lastNeural = true
	return true
}
