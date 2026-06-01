package multistream

import (
	"github.com/thesyncim/gopus/celt"
)

// celtSilenceFrame2B is the all-ones 2-byte CELT frame libopus decodes to let
// the CELT MDCT fade out on a Hybrid->SILK transition (opus_decode_frame).
var celtSilenceFrame2B = [...]byte{0xFF, 0xFF}

// transitionState captures the per-stream mode-transition decision for one
// frame, mirroring the pcm_transition handling in libopus opus_decode_frame
// (src/opus_decoder.c). A transition is a 5 ms crossfade applied whenever the
// per-stream coding mode crosses the CELT_ONLY boundary across consecutive
// frames, smoothing the discontinuity between the SILK/Hybrid and CELT MDCT
// reconstructions.
type transitionState struct {
	active     bool
	pcm        []float32
	prevMode   int
	prevBW     int
	prevStereo bool
	// pendingTransSize is the 5 ms transition span for a non-CELT target whose
	// transition PLC frame is decoded later (after the redundancy flags are read).
	pendingTransSize int
}

// beginModeTransition decides whether this frame is a CELT-boundary mode
// transition and, for a CELT-only target, decodes the 5 ms PLC transition frame
// in the previous mode up front (mirrors opus_decode_frame lines ~374-390:
// pcm_transition_celt decoded before the main CELT decode). For a non-CELT
// target the transition PLC frame is decoded later, after the redundancy flags
// are known, since libopus zeroes the transition when redundancy is present.
func (d *streamState) beginModeTransition(toc streamTOC, transSize int) (transitionState, error) {
	var ts transitionState
	if !d.haveDecoded {
		return ts, nil
	}
	prevMode := int(d.lastMode)
	mode := toc.mode
	celtBoundary := (mode == streamModeCELT && prevMode != streamModeCELT && !d.prevRedundancy) ||
		(mode != streamModeCELT && prevMode == streamModeCELT)
	if !celtBoundary {
		return ts, nil
	}
	ts.active = true
	ts.prevMode = prevMode
	ts.prevBW = int(d.lastBandwidth)
	ts.prevStereo = d.lastPacketStereo
	if mode == streamModeCELT {
		pcm, err := d.transitionPLCToFloat32(transSize, ts.prevMode, ts.prevBW, ts.prevStereo)
		if err != nil {
			return ts, err
		}
		ts.pcm = pcm
	}
	return ts, nil
}

// applyModeTransition crossfades the freshly decoded frame onto the transition
// PLC frame, mirroring opus_decode_frame lines ~660-676. With at least 5 ms of
// audio it copies the first 2.5 ms verbatim and smooth_fades the next 2.5 ms;
// otherwise it fades the whole available window.
func (d *streamState) applyModeTransition(ts *transitionState, out []float32, frameSize int) {
	if !ts.active || len(ts.pcm) == 0 {
		return
	}
	channels := int(d.channels)
	fs := int(d.sampleRate)
	f10 := fs / 50 / 2
	f5 := f10 >> 1
	f2_5 := f5 >> 1
	if frameSize >= f5 {
		copy(out[:f2_5*channels], ts.pcm[:f2_5*channels])
		streamSmoothFade(ts.pcm[f2_5*channels:], out[f2_5*channels:], out[f2_5*channels:], f2_5, channels, fs)
	} else {
		streamSmoothFade(ts.pcm, out, out, f2_5, channels, fs)
	}
}

// decodeSILKModeWithTransition decodes a SILK-only frame with the libopus
// CELT->SILK SILK-state reset, the Hybrid->SILK CELT fade-out, and the
// pcm_transition crossfade from a previous CELT frame (opus_decode_frame).
func (d *streamState) decodeSILKModeWithTransition(frame []byte, frameSize, transSize int, toc streamTOC) ([]float32, error) {
	ts, err := d.beginModeTransition(toc, transSize)
	if err != nil {
		return nil, err
	}

	// libopus discards stale SILK state on a CELT->SILK transition (the SILK
	// decoder has no usable history after a CELT-only run); mirrors the
	// MODE_SILK_ONLY path in opus_decode_frame.
	if d.haveDecoded && int(d.lastMode) == streamModeCELT {
		d.silkDec.Reset()
	}

	out, err := d.decodeSILKToFloat32(frame, frameSize, toc.stereo, toc.bandwidth)
	if err != nil {
		return nil, err
	}

	// Hybrid->SILK fade-out: decode a 2.5 ms CELT silence frame and add it so
	// the CELT MDCT history rings down cleanly (opus_decode_frame MODE_SILK_ONLY
	// else branch).
	if err := d.addHybridToSilkFadeOut(out); err != nil {
		return nil, err
	}

	// pcm_transition for a CELT->SILK mode change: decode the 5 ms PLC frame in
	// the previous CELT mode and crossfade it onto the front of this SILK frame.
	if ts.active && len(ts.pcm) == 0 {
		pcm, perr := d.transitionPLCToFloat32(transSize, ts.prevMode, ts.prevBW, ts.prevStereo)
		if perr != nil {
			return nil, perr
		}
		ts.pcm = pcm
	}

	d.applyModeTransition(&ts, out, frameSize)
	d.prevRedundancy = false
	return out, nil
}

// transitionPLCToFloat32 decodes a packet-loss-concealment frame of transSize
// samples in the supplied previous mode/bandwidth/stereo without recording it as
// the stream's decode state. It mirrors the opus_decode_frame(NULL) transition
// decode, which advances the shared SILK/Hybrid/CELT concealment state in the
// previous mode before the main frame is decoded.
func (d *streamState) transitionPLCToFloat32(transSize, prevMode, prevBW int, prevStereo bool) ([]float32, error) {
	channels := int(d.channels)
	switch prevMode {
	case streamModeSILK:
		// SILK concealment cannot produce less than 10 ms; libopus decodes the
		// transition PLC frame at >=F10 and uses only the first transSize samples,
		// advancing the SILK PLC state by a full 10 ms (opus_decode_frame ->
		// silk_frame_size = IMAX(F10, ...)).
		f10 := int(d.sampleRate) / 100
		silkPLCSize := transSize
		if silkPLCSize < f10 {
			silkPLCSize = f10
		}
		pcm, err := d.decodeSILKToFloat32(nil, silkPLCSize, prevStereo, prevBW)
		if err != nil {
			return nil, err
		}
		if silkPLCSize > transSize {
			pcm = pcm[:transSize*channels]
		}
		return pcm, nil
	case streamModeHybrid:
		return d.hybridDec.DecodeToFloat32WithPacketStereo(nil, transSize, prevStereo)
	case streamModeCELT:
		d.celtDec.SetBandwidth(celt.BandwidthFromOpusConfig(prevBW))
		out := make([]float32, transSize*int(d.channels))
		if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(nil, transSize, prevStereo, out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return make([]float32, transSize*int(d.channels)), nil
	}
}

// addHybridToSilkFadeOut handles the libopus Hybrid->SILK fade-out: when the
// previous frame was Hybrid and this frame is SILK-only, libopus decodes a 2.5 ms
// CELT silence frame at start band 0 and adds it so the CELT MDCT history rings
// down cleanly (opus_decode_frame, the MODE_SILK_ONLY else branch). The shared
// CELT decoder matches the single decoder libopus uses, so decoding here advances
// the same state.
func (d *streamState) addHybridToSilkFadeOut(out []float32) error {
	if int(d.lastMode) != streamModeHybrid {
		return nil
	}
	channels := int(d.channels)
	f2_5 := d.sampleRateF2_5()
	scratch := make([]float32, f2_5*channels)
	if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(celtSilenceFrame2B[:], f2_5, d.lastPacketStereo, scratch); err != nil {
		return err
	}
	n := len(out)
	if len(scratch) < n {
		n = len(scratch)
	}
	for i := 0; i < n; i++ {
		out[i] += scratch[i]
	}
	return nil
}

func (d *streamState) sampleRateF2_5() int {
	return int(d.sampleRate) / 50 / 2 / 2 / 2
}
