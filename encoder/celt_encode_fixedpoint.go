//go:build gopus_fixedpoint

package encoder

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/types"
)

// encoderFixedCELTFields carries the integer (FIXED_POINT) CELT encoder state
// added under the gopus_fixedpoint build. It is empty in the default build.
type encoderFixedCELTFields struct {
	fixedCELT       *fixedCELTState
	fixedCELTOut    []byte
	fixedFinalRange uint32
	fixedCELTUsed   bool
}

// fixedCELTFinalRange returns the integer CELT encoder's final range coder state
// when the last frame was produced by the integer path. currentFinalRange uses
// it so the encoder's reported final range matches the integer packet.
func (e *Encoder) fixedCELTFinalRange() (uint32, bool) {
	if e.fixedCELTUsed {
		return e.fixedFinalRange, true
	}
	return 0, false
}

// fixedCELTState holds the integer (FIXED_POINT) CELT encoder used under the
// gopus_fixedpoint build to produce byte-exact CELT-mode packets. It is created
// lazily and carries all CELT cross-frame state, so once a CELT-mode packet is
// routed to it every subsequent CELT frame must continue through it.
type fixedCELTState struct {
	enc      *fixedpoint.CELTEncoder
	channels int
	pcm16    []int16
	rng      *rangecoding.Encoder
}

// celtFixedEncodeInScope reports whether the integer CELT encoder can produce a
// byte-exact packet for this frame. Its scope matches
// fixedpoint.CELTEncoder.EncodeWithEC: the static 48 kHz mode, full-band
// (start==0, end==21), single 2.5/5/10/20 ms frame (frameSize<=960), 1 or 2
// channels, no hybrid (SILK present), no LFE, no QEXT, no surround energy mask.
// Anything else falls back to the float CELT encoder.
//
// It also requires a pure-CELT stream (restricted-low-delay or forced ModeCELT):
// the integer encoder carries no SILK->CELT transition-prefill state, so a stream
// that can switch modes stays on the float path to avoid a cross-frame state
// mismatch.
func (e *Encoder) celtFixedEncodeInScope(frameSize int) bool {
	if !e.lowDelay && e.mode != ModeCELT {
		return false
	}
	if e.lfe {
		return false
	}
	if frameSize <= 0 || frameSize > 960 {
		return false
	}
	const shortMdctSize = 120
	if frameSize%shortMdctSize != 0 {
		return false
	}
	c := int(e.channels)
	if c != 1 && c != 2 {
		return false
	}
	if e.sampleRate != 48000 {
		return false
	}
	if e.effectiveBandwidth() != types.BandwidthFullband {
		return false
	}
	if extsupport.QEXT && e.qextActive() {
		return false
	}
	if len(e.celtEnergyMask) > 0 {
		return false
	}
	return true
}

// encodeCELTFrameFixed runs the integer CELT encoder for one in-scope frame and
// returns the CELT payload bytes (TOC-excluded), matching the float
// celt.Encoder.EncodeFrame contract. ok is false when the frame is out of the
// integer encoder's scope, in which case the caller must use the float path.
func (e *Encoder) encodeCELTFrameFixed(pcm []opusRes, frameSize, bitrate, maxPayloadBytes int) (out []byte, ok bool, err error) {
	e.fixedCELTUsed = false
	if !e.celtFixedEncodeInScope(frameSize) {
		return nil, false, nil
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels {
		return nil, false, ErrInvalidFrameSize
	}

	st := e.ensureFixedCELT(channels)
	st.enc.SetBandRange(0, 21)
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	switch e.bitrateMode {
	case ModeCBR:
		st.enc.SetVBR(false)
		st.enc.SetConstrainedVBR(false)
	case ModeCVBR:
		st.enc.SetVBR(true)
		st.enc.SetConstrainedVBR(true)
	case ModeVBR:
		st.enc.SetVBR(true)
		st.enc.SetConstrainedVBR(false)
	}

	// Convert the delay-compensated float frame to int16 with the libopus
	// FLOAT2INT16 quantization, matching opus_encode_float -> celt_encode_with_ec.
	if cap(st.pcm16) < len(pcm) {
		st.pcm16 = make([]int16, len(pcm))
	}
	st.pcm16 = st.pcm16[:len(pcm)]
	for i, v := range pcm {
		st.pcm16[i] = opusmath.Float32ToInt16(float32(v))
	}

	// nbCompressedBytes is the output buffer cap; EncodeWithEC self-computes the
	// CBR byte count and clamps below it, and uses it directly as the VBR ceiling.
	nbCompressedBytes := celtPacketSizeCap - 1
	if maxPayloadBytes > 0 && maxPayloadBytes < nbCompressedBytes {
		nbCompressedBytes = maxPayloadBytes
	}

	if cap(st.rng.Buffer()) < nbCompressedBytes {
		buf := make([]byte, nbCompressedBytes)
		st.rng.Init(buf)
	} else {
		buf := st.rng.Buffer()[:nbCompressedBytes]
		for i := range buf {
			buf[i] = 0
		}
		st.rng.Init(buf)
	}

	n := st.enc.EncodeWithEC(st.pcm16, frameSize, st.rng, nbCompressedBytes)
	e.fixedFinalRange = st.rng.Range()
	out = append(e.fixedCELTOut[:0], st.rng.Buffer()[:n]...)
	e.fixedCELTOut = out
	e.fixedCELTUsed = true
	return out, true, nil
}

// LastFixedCELTInput16 returns the int16 PCM frame the integer CELT encoder
// consumed for the most recent frame, for parity tests that drive the same
// samples through the bare FIXED_POINT celt_encode_with_ec oracle.
func (e *Encoder) LastFixedCELTInput16() []int16 {
	if e.fixedCELT == nil {
		return nil
	}
	return e.fixedCELT.pcm16
}

func (e *Encoder) ensureFixedCELT(channels int) *fixedCELTState {
	if e.fixedCELT == nil || e.fixedCELT.channels != channels {
		e.fixedCELT = &fixedCELTState{
			enc:      fixedpoint.NewCELTEncoder(channels),
			channels: channels,
			rng:      &rangecoding.Encoder{},
		}
	}
	return e.fixedCELT
}

// resetFixedCELT clears the integer CELT cross-frame state, mirroring the float
// celtEncoder.Reset() done on a CELT mode transition.
func (e *Encoder) resetFixedCELT() {
	if e.fixedCELT != nil {
		e.fixedCELT.enc = fixedpoint.NewCELTEncoder(e.fixedCELT.channels)
	}
}

const celtPacketSizeCap = 1275
