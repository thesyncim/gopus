//go:build gopus_fixed_point

package encoder

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
)

// fixedPointBuild reports whether the gopus_fixed_point integer codec paths are
// compiled in. Golden-fixture tests calibrated against the float SILK encode
// path gate on it (the FIXED_POINT SILK encode produces byte-exact-to-libopus
// payloads that differ from the float golden bytes).
const fixedPointBuild = true

// encoderFixedCELTFields carries the integer (FIXED_POINT) CELT encoder state
// added under the gopus_fixed_point build. It is empty in the default build.
type encoderFixedCELTFields struct {
	fixedCELT        *fixedCELTState
	fixedCELTOut     []byte
	fixedFinalRange  uint32
	fixedCELTUsed    bool
	fixedRawRes      []int32
	fixedFiltered    []int32
	fixedFrameSource []int32
	fixedDelayed     []int32
	fixedDelayBuffer []int32
	fixedHPMem       [4]int32
	fixedInputActive bool
	fixedFrameReady  bool
	fixedFrameCursor int
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

// clearFixedCELTUsed resets the integer-CELT-used flag at the start of each
// packet so a stale value from a previous CELT frame cannot mis-gate the TOC
// frame-size conversion for a subsequent SILK/Hybrid frame.
func (e *Encoder) clearFixedCELTUsed() { e.fixedCELTUsed = false }

// fixedCELTState holds the integer (FIXED_POINT) CELT encoder used under the
// gopus_fixed_point build to produce byte-exact CELT-mode packets. It is created
// lazily and carries all CELT cross-frame state, so once a CELT-mode packet is
// routed to it every subsequent CELT frame must continue through it.
type fixedCELTState struct {
	enc          *fixedpoint.CELTEncoder
	channels     int
	pcm16        []int16
	rng          *rangecoding.Encoder
	lastQ8       []int32
	lastAnalysis AnalysisInfo
	lastMaxBytes int32
	lastBitrate  int32
	lastLSBDepth int32
}

// celtFixedUpsample mirrors celt_encoder_init's st->upsample =
// resampling_factor(API sample rate): 1 at 48 kHz and 2/3/4/6 at 24/16/12/8 kHz.
// 0 means an unsupported API rate.
func (e *Encoder) celtFixedUpsample() int {
	switch e.sampleRate {
	case 48000:
		return 1
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	}
	return 0
}

// celtFixedEndBand maps the (already-clamped) effective bandwidth to the CELT
// end band, matching the endband switch in opus_encoder.c celt_encode_with_ec
// setup: NB=13, MB/WB=17, SWB=19, FB=21.
func celtFixedEndBand(bw types.Bandwidth) int {
	switch bw {
	case types.BandwidthNarrowband:
		return 13
	case types.BandwidthMediumband, types.BandwidthWideband:
		return 17
	case types.BandwidthSuperwideband:
		return 19
	case types.BandwidthFullband:
		return 21
	}
	return 21
}

// celtFixedFrameSizeInScope reports whether the integer CELT encoder supports
// the frame's static 48 kHz mode and API-rate upsampling layout.
func (e *Encoder) celtFixedFrameSizeInScope(frameSize int) bool {
	if e.lfe {
		return false
	}
	upsample := e.celtFixedUpsample()
	if upsample == 0 {
		return false
	}
	// The API-rate frameSize must upsample to a valid 48 kHz core block
	// (shortMdctSize<<LM for LM 0..3, i.e. 120/240/480/960).
	const shortMdctSize = 120
	core := frameSize * upsample
	if core <= 0 || core > 960 || core%shortMdctSize != 0 {
		return false
	}
	c := int(e.channels)
	if c != 1 && c != 2 {
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

// celtFixedEncodeInScope reports whether the integer CELT encoder can produce a
// byte-exact pure-CELT frame. A stream that can switch to or from SILK stays on
// the float path until its transition prefill runs through the integer encoder.
func (e *Encoder) celtFixedEncodeInScope(frameSize int) bool {
	if !e.lowDelay && e.mode != ModeCELT {
		return false
	}
	return e.celtFixedFrameSizeInScope(frameSize)
}

// celtFixedHybridEncodeInScope reports whether the integer CELT encoder can
// continue a shared range coder for one Hybrid frame.
func (e *Encoder) celtFixedHybridEncodeInScope(frameSize int) bool {
	if e.restrictedSilkApp {
		return false
	}
	return e.celtFixedFrameSizeInScope(frameSize)
}

// encodeCELTFrameFixed runs the integer CELT encoder for one in-scope frame and
// returns the CELT payload bytes (TOC-excluded), matching the float
// celt.Encoder.EncodeFrame contract. ok is false when the frame is out of the
// integer encoder's scope, in which case the caller must use the float path.
func (e *Encoder) encodeCELTFrameFixed(pcm []opusRes, frameSize, bitrate, maxPayloadBytes int, prefilled bool) (out []byte, ok bool, err error) {
	e.fixedCELTUsed = false
	if !e.celtFixedEncodeInScope(frameSize) {
		return nil, false, nil
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels {
		return nil, false, ErrInvalidFrameSize
	}

	st := e.ensureFixedCELT(channels)
	st.enc.SetBandRange(0, celtFixedEndBand(e.effectiveBandwidth()))
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtEncoder.Prediction()))
	st.enc.SetSilkInfo(0, 0)
	if e.lastAnalysisValid && !prefilled {
		st.lastAnalysis = e.lastAnalysisInfo
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid:         true,
			Bandwidth:     e.lastAnalysisInfo.BandwidthIndex,
			LeakBoost:     e.lastAnalysisInfo.LeakBoost,
			Activity:      e.lastAnalysisInfo.Activity,
			Tonality:      e.lastAnalysisInfo.Tonality,
			TonalitySlope: e.lastAnalysisInfo.TonalitySlope,
			MaxPitchRatio: e.lastAnalysisInfo.MaxPitchRatio,
		})
	} else {
		st.lastAnalysis = AnalysisInfo{}
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
	}
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

	// All supported public input APIs carry opus_res Q8 through DC rejection and
	// the delay buffer. The int16 seam serves callers outside that source path.
	var pcmRes []int32
	if e.fixedFrameReady && len(e.fixedDelayed) == len(pcm) {
		pcmRes = e.fixedDelayed
		st.lastQ8 = pcmRes
		st.pcm16 = st.pcm16[:0]
	} else {
		st.lastQ8 = nil
		if cap(st.pcm16) < len(pcm) {
			st.pcm16 = make([]int16, len(pcm))
		}
		st.pcm16 = st.pcm16[:len(pcm)]
		for i, v := range pcm {
			st.pcm16[i] = opusmath.Float32ToInt16(float32(v))
		}
	}

	// nbCompressedBytes is the output buffer cap; EncodeWithEC self-computes the
	// CBR byte count and clamps below it, and uses it directly as the VBR ceiling.
	nbCompressedBytes := celtPacketSizeCap - 1
	if maxPayloadBytes > 0 && maxPayloadBytes < nbCompressedBytes {
		nbCompressedBytes = maxPayloadBytes
	}
	st.lastMaxBytes = int32(nbCompressedBytes)
	st.lastBitrate = int32(bitrate)
	st.lastLSBDepth = e.lsbDepth

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

	var n int
	if pcmRes != nil {
		n = st.enc.EncodeWithECRes(pcmRes, frameSize, st.rng, nbCompressedBytes)
	} else {
		n = st.enc.EncodeWithEC(st.pcm16, frameSize, st.rng, nbCompressedBytes)
	}
	e.fixedFinalRange = st.rng.Range()
	out = append(e.fixedCELTOut[:0], st.rng.Buffer()[:n]...)
	e.fixedCELTOut = out
	e.fixedCELTUsed = true
	return out, true, nil
}

// encodeHybridCELTFrameFixed codes the Hybrid CELT bands into the range coder
// already seeded by the SILK and redundancy-signalling layers. pcmQ8 is the
// ENABLE_RES24 opus_res frame after top-level high-pass, delay, high-band and
// stereo-width processing. The caller owns that frame and the shared coder.
func (e *Encoder) encodeHybridCELTFrameFixed(pcmQ8 []int32, frameSize, bitrate, maxPayloadBytes int, re *rangecoding.Encoder, prefilled bool) (out []byte, ok bool, err error) {
	e.fixedCELTUsed = false
	if !e.celtFixedHybridEncodeInScope(frameSize) {
		return nil, false, nil
	}
	if re == nil || len(pcmQ8) != frameSize*int(e.channels) {
		return nil, false, ErrInvalidFrameSize
	}

	channels := int(e.channels)
	st := e.ensureFixedCELT(channels)
	st.enc.SetBandRange(17, celtFixedEndBand(e.effectiveBandwidth()))
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtEncoder.Prediction()))
	if !prefilled {
		st.enc.SetSilkInfo(e.silkMode.SignalType, e.silkMode.Offset)
	}
	if e.lastAnalysisValid && !prefilled {
		st.lastAnalysis = e.lastAnalysisInfo
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid:         true,
			Bandwidth:     e.lastAnalysisInfo.BandwidthIndex,
			LeakBoost:     e.lastAnalysisInfo.LeakBoost,
			Activity:      e.lastAnalysisInfo.Activity,
			Tonality:      e.lastAnalysisInfo.Tonality,
			TonalitySlope: e.lastAnalysisInfo.TonalitySlope,
			MaxPitchRatio: e.lastAnalysisInfo.MaxPitchRatio,
		})
	} else {
		st.lastAnalysis = AnalysisInfo{}
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
	}
	st.enc.SetVBR(e.celtEncoder.VBR())
	// libopus sets constrained VBR only for CELT-only frames. Hybrid VBR uses
	// the rate left after SILK without constraining CELT to it.
	st.enc.SetConstrainedVBR(false)

	st.lastQ8 = pcmQ8
	st.pcm16 = st.pcm16[:0]
	st.lastMaxBytes = int32(maxPayloadBytes)
	st.lastBitrate = int32(bitrate)
	st.lastLSBDepth = e.lsbDepth

	n := st.enc.EncodeWithECRes(pcmQ8, frameSize, re, maxPayloadBytes)
	e.fixedFinalRange = re.Range()
	e.fixedCELTUsed = true
	return re.Buffer()[:n], true, nil
}

// encodeRedundantCELTFrameFixed codes one selected fixed-point 5 ms redundancy
// frame. It continues the fixed CELT history while using the independent range
// coder that carries the redundant packet section.
func (e *Encoder) encodeRedundantCELTFrameFixed(pcmQ8 []int32, frameSize, bitrate, maxPayloadBytes int, hybrid, analysis bool) (out []byte, finalRange uint32, ok bool, err error) {
	if !e.celtFixedFrameSizeInScope(frameSize) || len(pcmQ8) != frameSize*int(e.channels) || maxPayloadBytes <= 0 {
		return nil, 0, false, nil
	}
	st := e.ensureFixedCELT(int(e.channels))
	st.enc.SetBandRange(0, celtFixedEndBand(e.effectiveBandwidth()))
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtEncoder.Prediction()))
	if hybrid {
		st.enc.SetSilkInfo(e.silkMode.SignalType, e.silkMode.Offset)
	}
	if analysis && e.lastAnalysisValid {
		st.lastAnalysis = e.lastAnalysisInfo
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid:         true,
			Bandwidth:     e.lastAnalysisInfo.BandwidthIndex,
			LeakBoost:     e.lastAnalysisInfo.LeakBoost,
			Activity:      e.lastAnalysisInfo.Activity,
			Tonality:      e.lastAnalysisInfo.Tonality,
			TonalitySlope: e.lastAnalysisInfo.TonalitySlope,
			MaxPitchRatio: e.lastAnalysisInfo.MaxPitchRatio,
		})
	} else {
		st.lastAnalysis = AnalysisInfo{}
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
	}
	st.enc.SetVBR(false)
	st.enc.SetConstrainedVBR(false)
	st.lastQ8 = pcmQ8
	st.lastMaxBytes = int32(maxPayloadBytes)
	st.lastBitrate = int32(bitrate)
	st.lastLSBDepth = e.lsbDepth

	if cap(st.rng.Buffer()) < maxPayloadBytes {
		st.rng.Init(make([]byte, maxPayloadBytes))
	} else {
		buf := st.rng.Buffer()[:maxPayloadBytes]
		clear(buf)
		st.rng.Init(buf)
	}
	n := st.enc.EncodeWithECRes(pcmQ8, frameSize, st.rng, maxPayloadBytes)
	return st.rng.Buffer()[:n], st.rng.Range(), true, nil
}

// prefillCELTFrameFixed mirrors the reset and 2-byte CELT tmp_prefill in
// opus_encode_frame_native. pcmQ8 is the exact ENABLE_RES24 prefill window from
// the fixed-point delay buffer. The current CELT controls survive Reset, while
// SILKInfo and AnalysisInfo clear with the CELT reset region.
func (e *Encoder) prefillCELTFrameFixed(pcmQ8 []int32, frameSize, startBand, bitrate, maxPayloadBytes int, prediction int32) bool {
	if !e.celtFixedFrameSizeInScope(frameSize) || len(pcmQ8) != frameSize*int(e.channels) || maxPayloadBytes < 2 {
		return false
	}
	st := e.ensureFixedCELT(int(e.channels))
	st.enc.SetBandRange(startBand, celtFixedEndBand(e.effectiveBandwidth()))
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(prediction)
	st.enc.SetVBR(e.celtEncoder.VBR())
	st.enc.SetConstrainedVBR(startBand == 0 && e.bitrateMode == ModeCVBR)
	st.enc.Reset()

	if cap(st.rng.Buffer()) < maxPayloadBytes {
		st.rng.Init(make([]byte, maxPayloadBytes))
	} else {
		buf := st.rng.Buffer()[:maxPayloadBytes]
		clear(buf)
		st.rng.Init(buf)
	}
	st.enc.EncodeWithECRes(pcmQ8, frameSize, st.rng, maxPayloadBytes)
	st.enc.SetPrediction(0)
	return true
}

// LastFixedCELTInputQ8 returns the exact opus_res frame consumed by integer
// CELT. The view is valid until the next encode call.
func (e *Encoder) LastFixedCELTInputQ8() []int32 {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return nil
	}
	return e.fixedCELT.lastQ8
}

// LastFixedCELTAnalysis returns the analysis snapshot consumed by the last
// integer CELT frame. The value contains no borrowed buffers.
func (e *Encoder) LastFixedCELTAnalysis() AnalysisInfo {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return AnalysisInfo{}
	}
	return e.fixedCELT.lastAnalysis
}

// LastFixedCELTControls reports the rate and caller capacity that the last
// integer CELT frame received, including the short API's effective LSB depth.
func (e *Encoder) LastFixedCELTControls() (bitrate, maxBytes, lsbDepth int) {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return 0, 0, 0
	}
	return int(e.fixedCELT.lastBitrate), int(e.fixedCELT.lastMaxBytes), int(e.fixedCELT.lastLSBDepth)
}

func (e *Encoder) ensureFixedCELT(channels int) *fixedCELTState {
	if e.fixedCELT == nil || e.fixedCELT.channels != channels {
		e.fixedCELT = &fixedCELTState{
			enc:      fixedpoint.NewCELTEncoderRate(channels, int(e.sampleRate)),
			channels: channels,
			rng:      &rangecoding.Encoder{},
		}
	}
	return e.fixedCELT
}

// resetFixedCELT clears the integer CELT cross-frame state, mirroring the float
// celtEncoder.Reset() done on a CELT mode transition. The API-rate upsample is
// preserved by recreating at the encoder's sample rate.
func (e *Encoder) resetFixedCELT() {
	e.fixedHPMem = [4]int32{}
	clear(e.fixedDelayBuffer)
	e.fixedInputActive = false
	e.fixedFrameReady = false
	e.fixedFrameCursor = 0
	if e.fixedCELT != nil {
		e.fixedCELT.enc = fixedpoint.NewCELTEncoderRate(e.fixedCELT.channels, int(e.sampleRate))
	}
}

func (e *Encoder) resetFixedCELTState() {
	if e.fixedCELT != nil {
		e.fixedCELT.enc.Reset()
		e.fixedCELT.lastAnalysis = AnalysisInfo{}
	}
}

const celtPacketSizeCap = 1275
