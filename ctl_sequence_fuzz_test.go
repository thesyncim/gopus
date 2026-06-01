// ctl_sequence_fuzz_test.go drives a seeded random program of CTL SET/GET
// requests (with boundary / out-of-range arguments) interleaved with
// encodes/decodes and OPUS_RESET_STATE through both gopus and a libopus oracle,
// asserting behavioral parity:
//
//   - SET return-code parity (OPUS_OK vs OPUS_BAD_ARG) per step.
//   - GET value + return-code parity after every step.
//   - post-encode lookahead / final-range and post-decode last-packet-duration
//     parity (these GETs are interleaved in the program).
//
// The encode/decode byte-stream is already covered elsewhere; this gate targets
// the CTL get/set SEMANTICS (clamping, get-after-set, reset behaviour, and
// interaction with encode/decode state) where subtle divergences hide.
//
// Reference: libopus src/opus_encoder.c opus_encoder_ctl,
//            src/opus_decoder.c opus_decoder_ctl.

package gopus

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// libopus CTL request codes (opus_defines.h). Listed here so the fuzz program
// speaks the same domain as the C oracle.
const (
	reqSetApplication            = 4000
	reqGetApplication            = 4001
	reqSetBitrate                = 4002
	reqGetBitrate                = 4003
	reqSetMaxBandwidth           = 4004
	reqGetMaxBandwidth           = 4005
	reqSetVBR                    = 4006
	reqGetVBR                    = 4007
	reqSetBandwidth              = 4008
	reqGetBandwidth              = 4009
	reqSetComplexity             = 4010
	reqGetComplexity             = 4011
	reqSetInbandFEC              = 4012
	reqGetInbandFEC              = 4013
	reqSetPacketLossPerc         = 4014
	reqGetPacketLossPerc         = 4015
	reqSetDTX                    = 4016
	reqGetDTX                    = 4017
	reqSetVBRConstraint          = 4020
	reqGetVBRConstraint          = 4021
	reqSetForceChannels          = 4022
	reqGetForceChannels          = 4023
	reqSetSignal                 = 4024
	reqGetSignal                 = 4025
	reqGetLookahead              = 4027
	reqGetSampleRate             = 4029
	reqGetFinalRange             = 4031
	reqGetPitch                  = 4033
	reqSetGain                   = 4034
	reqGetGain                   = 4045
	reqSetLSBDepth               = 4036
	reqGetLSBDepth               = 4037
	reqGetLastPacketDuration     = 4039
	reqSetExpertFrameDuration    = 4040
	reqGetExpertFrameDuration    = 4041
	reqSetPredictionDisabled     = 4042
	reqGetPredictionDisabled     = 4043
	reqSetPhaseInversionDisabled = 4046
	reqGetPhaseInversionDisabled = 4047
	reqGetInDTX                  = 4049
	reqSetIgnoreExtensions       = 4058
	reqGetIgnoreExtensions       = 4059
)

// libopus argument-domain constants used by the generator.
const (
	cOpusAuto       = -1000
	cOpusBitrateMax = -1
	cBandwidthNB    = 1101
	cBandwidthMB    = 1102
	cBandwidthWB    = 1103
	cBandwidthSWB   = 1104
	cBandwidthFB    = 1105
	cSignalVoice    = 3001
	cSignalMusic    = 3002
	cAppVoIP        = 2048
	cAppAudio       = 2049
	cAppLowDelay    = 2051
	cFrameSizeArg   = 5000
	cFrameSize120Ms = 5009
)

const (
	cOpusOK     = 0
	cOpusBadArg = -1
)

// ctlFuzzFrameSize is the per-channel OP_PROCESS frame size, in samples at the
// API rate. It matches the gopus encoder's default FrameSize() (960 samples,
// the 48 kHz 20 ms count) so the encode input is identical at every API rate;
// 960 is a valid libopus frame size at 8/12/16/24/48 kHz (60/20 ms etc.).
const ctlFuzzFrameSize = 960

// ctlReqName maps a request code to a readable name for failure messages.
func ctlReqName(req int32) string {
	switch int(req) {
	case reqSetApplication:
		return "SET_APPLICATION"
	case reqGetApplication:
		return "GET_APPLICATION"
	case reqSetBitrate:
		return "SET_BITRATE"
	case reqGetBitrate:
		return "GET_BITRATE"
	case reqSetMaxBandwidth:
		return "SET_MAX_BANDWIDTH"
	case reqGetMaxBandwidth:
		return "GET_MAX_BANDWIDTH"
	case reqSetVBR:
		return "SET_VBR"
	case reqGetVBR:
		return "GET_VBR"
	case reqSetBandwidth:
		return "SET_BANDWIDTH"
	case reqGetBandwidth:
		return "GET_BANDWIDTH"
	case reqSetComplexity:
		return "SET_COMPLEXITY"
	case reqGetComplexity:
		return "GET_COMPLEXITY"
	case reqSetInbandFEC:
		return "SET_INBAND_FEC"
	case reqGetInbandFEC:
		return "GET_INBAND_FEC"
	case reqSetPacketLossPerc:
		return "SET_PACKET_LOSS_PERC"
	case reqGetPacketLossPerc:
		return "GET_PACKET_LOSS_PERC"
	case reqSetDTX:
		return "SET_DTX"
	case reqGetDTX:
		return "GET_DTX"
	case reqSetVBRConstraint:
		return "SET_VBR_CONSTRAINT"
	case reqGetVBRConstraint:
		return "GET_VBR_CONSTRAINT"
	case reqSetForceChannels:
		return "SET_FORCE_CHANNELS"
	case reqGetForceChannels:
		return "GET_FORCE_CHANNELS"
	case reqSetSignal:
		return "SET_SIGNAL"
	case reqGetSignal:
		return "GET_SIGNAL"
	case reqGetLookahead:
		return "GET_LOOKAHEAD"
	case reqGetSampleRate:
		return "GET_SAMPLE_RATE"
	case reqGetFinalRange:
		return "GET_FINAL_RANGE"
	case reqGetPitch:
		return "GET_PITCH"
	case reqSetGain:
		return "SET_GAIN"
	case reqGetGain:
		return "GET_GAIN"
	case reqSetLSBDepth:
		return "SET_LSB_DEPTH"
	case reqGetLSBDepth:
		return "GET_LSB_DEPTH"
	case reqGetLastPacketDuration:
		return "GET_LAST_PACKET_DURATION"
	case reqSetExpertFrameDuration:
		return "SET_EXPERT_FRAME_DURATION"
	case reqGetExpertFrameDuration:
		return "GET_EXPERT_FRAME_DURATION"
	case reqSetPredictionDisabled:
		return "SET_PREDICTION_DISABLED"
	case reqGetPredictionDisabled:
		return "GET_PREDICTION_DISABLED"
	case reqSetPhaseInversionDisabled:
		return "SET_PHASE_INVERSION_DISABLED"
	case reqGetPhaseInversionDisabled:
		return "GET_PHASE_INVERSION_DISABLED"
	case reqGetInDTX:
		return "GET_IN_DTX"
	case reqSetIgnoreExtensions:
		return "SET_IGNORE_EXTENSIONS"
	case reqGetIgnoreExtensions:
		return "GET_IGNORE_EXTENSIONS"
	}
	return fmt.Sprintf("REQ(%d)", req)
}

func opName(op int) string {
	switch op {
	case libopustest.CTLOpSet:
		return "SET"
	case libopustest.CTLOpGet:
		return "GET"
	case libopustest.CTLOpProcess:
		return "PROCESS"
	case libopustest.CTLOpReset:
		return "RESET"
	}
	return "?"
}

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// gopusErrToCode maps a gopus typed-setter error to the libopus CTL return code.
// Any validation error becomes OPUS_BAD_ARG; nil becomes OPUS_OK.
func gopusErrToCode(err error) int32 {
	if err == nil {
		return cOpusOK
	}
	return cOpusBadArg
}

// ---------------------------------------------------------------------------
// gopus interpreter: apply one op in the libopus request/arg domain through the
// public typed setters/getters, returning (ret, value, haveValue).
// ---------------------------------------------------------------------------

func applyEncoderOp(enc *Encoder, op libopustest.CTLOp) (ret int32, value int32, haveValue bool) {
	switch op.Op {
	case libopustest.CTLOpProcess:
		// Mirror the C oracle's OP_PROCESS frame exactly: a FrameSize()-sample
		// 440 Hz sine duplicated across channels (same double-precision sin ->
		// float32 cast) so the encode input is byte-identical to libopus.
		frame := enc.FrameSize()
		pcm := generateSineWaveFloat32(enc.SampleRate(), 440, frame, enc.Channels())
		buf := make([]byte, 4000)
		n, err := enc.Encode(pcm, buf)
		if err != nil {
			return -1, 0, false
		}
		return int32(n), 0, false
	case libopustest.CTLOpReset:
		enc.Reset()
		return cOpusOK, 0, false
	case libopustest.CTLOpSet:
		return applyEncoderSet(enc, op.Request, op.Arg), 0, false
	case libopustest.CTLOpGet:
		v, ok := applyEncoderGet(enc, op.Request)
		return cOpusOK, v, ok
	}
	return cOpusOK, 0, false
}

func applyEncoderSet(enc *Encoder, req, arg int32) int32 {
	switch int(req) {
	case reqSetApplication:
		var app Application
		switch int(arg) {
		case cAppVoIP:
			app = ApplicationVoIP
		case cAppAudio:
			app = ApplicationAudio
		case cAppLowDelay:
			app = ApplicationLowDelay
		default:
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetApplication(app))
	case reqSetBitrate:
		return gopusErrToCode(enc.SetBitrate(int(arg)))
	case reqSetMaxBandwidth:
		bw, ok := bandwidthFromLibopus(arg)
		if !ok {
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetMaxBandwidth(bw))
	case reqSetVBR:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetVBR(arg == 1)
		return cOpusOK
	case reqSetBandwidth:
		if arg == cOpusAuto {
			return gopusErrToCode(enc.SetBandwidthAuto())
		}
		bw, ok := bandwidthFromLibopus(arg)
		if !ok {
			return cOpusBadArg
		}
		return gopusErrToCode(enc.SetBandwidth(bw))
	case reqSetComplexity:
		return gopusErrToCode(enc.SetComplexity(int(arg)))
	case reqSetInbandFEC:
		return gopusErrToCode(enc.SetInBandFEC(int(arg)))
	case reqSetPacketLossPerc:
		return gopusErrToCode(enc.SetPacketLoss(int(arg)))
	case reqSetDTX:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetDTX(arg == 1)
		return cOpusOK
	case reqSetVBRConstraint:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetVBRConstraint(arg == 1)
		return cOpusOK
	case reqSetForceChannels:
		// gopus uses -1 as the public OPUS_AUTO sentinel for ForceChannels
		// (documented: encoder_ctl_equivalence_test force_channels=OPUS_AUTO ->
		// gopus -1). Map the libopus OPUS_AUTO arg onto the gopus sentinel so
		// the SET behaviour (auto channel selection) is compared, not the
		// cosmetic sentinel value.
		v := int(arg)
		if v == cOpusAuto {
			v = -1
		}
		return gopusErrToCode(enc.SetForceChannels(v))
	case reqSetSignal:
		return gopusErrToCode(enc.SetSignal(Signal(arg)))
	case reqSetLSBDepth:
		return gopusErrToCode(enc.SetLSBDepth(int(arg)))
	case reqSetExpertFrameDuration:
		return gopusErrToCode(enc.SetExpertFrameDuration(ExpertFrameDuration(arg)))
	case reqSetPredictionDisabled:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetPredictionDisabled(arg == 1)
		return cOpusOK
	case reqSetPhaseInversionDisabled:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		enc.SetPhaseInversionDisabled(arg == 1)
		return cOpusOK
	}
	return cOpusBadArg
}

func applyEncoderGet(enc *Encoder, req int32) (int32, bool) {
	switch int(req) {
	case reqGetApplication:
		return applicationToLibopus(enc.Application()), true
	case reqGetBitrate:
		return int32(enc.Bitrate()), true
	case reqGetMaxBandwidth:
		return bandwidthToLibopus(enc.MaxBandwidth()), true
	case reqGetVBR:
		return boolToI32(enc.VBR()), true
	case reqGetBandwidth:
		return encoderBandwidthToLibopus(enc), true
	case reqGetComplexity:
		return int32(enc.Complexity()), true
	case reqGetInbandFEC:
		return int32(enc.InBandFEC()), true
	case reqGetPacketLossPerc:
		return int32(enc.PacketLoss()), true
	case reqGetDTX:
		return boolToI32(enc.DTXEnabled()), true
	case reqGetVBRConstraint:
		return boolToI32(enc.VBRConstraint()), true
	case reqGetForceChannels:
		// Normalize gopus's -1 OPUS_AUTO sentinel to the libopus OPUS_AUTO code.
		fc := int32(enc.ForceChannels())
		if fc == -1 {
			fc = cOpusAuto
		}
		return fc, true
	case reqGetSignal:
		return int32(enc.Signal()), true
	case reqGetLookahead:
		return int32(enc.Lookahead()), true
	case reqGetSampleRate:
		return int32(enc.SampleRate()), true
	case reqGetFinalRange:
		return int32(enc.FinalRange()), true
	case reqGetLSBDepth:
		return int32(enc.LSBDepth()), true
	case reqGetExpertFrameDuration:
		return int32(enc.ExpertFrameDuration()), true
	case reqGetPredictionDisabled:
		return boolToI32(enc.PredictionDisabled()), true
	case reqGetPhaseInversionDisabled:
		return boolToI32(enc.PhaseInversionDisabled()), true
	case reqGetInDTX:
		return boolToI32(enc.InDTX()), true
	}
	return 0, false
}

func applyDecoderOp(dec *Decoder, op libopustest.CTLOp, feedPkt []byte) (ret int32, value int32, haveValue bool) {
	switch op.Op {
	case libopustest.CTLOpProcess:
		pcm := make([]float32, dec.maxPacketSamples*dec.Channels())
		n, err := dec.Decode(feedPkt, pcm)
		if err != nil {
			return -1, 0, false
		}
		return int32(n), 0, false
	case libopustest.CTLOpReset:
		dec.Reset()
		return cOpusOK, 0, false
	case libopustest.CTLOpSet:
		return applyDecoderSet(dec, op.Request, op.Arg), 0, false
	case libopustest.CTLOpGet:
		v, ok := applyDecoderGet(dec, op.Request)
		return cOpusOK, v, ok
	}
	return cOpusOK, 0, false
}

func applyDecoderSet(dec *Decoder, req, arg int32) int32 {
	switch int(req) {
	case reqSetComplexity:
		return gopusErrToCode(dec.SetComplexity(int(arg)))
	case reqSetGain:
		return gopusErrToCode(dec.SetGain(int(arg)))
	case reqSetPhaseInversionDisabled:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		dec.SetPhaseInversionDisabled(arg == 1)
		return cOpusOK
	case reqSetIgnoreExtensions:
		if arg < 0 || arg > 1 {
			return cOpusBadArg
		}
		dec.SetIgnoreExtensions(arg == 1)
		return cOpusOK
	}
	return cOpusBadArg
}

func applyDecoderGet(dec *Decoder, req int32) (int32, bool) {
	switch int(req) {
	case reqGetBandwidth:
		return bandwidthGetToLibopus(dec.Bandwidth(), dec.bandwidthKnown), true
	case reqGetComplexity:
		return int32(dec.Complexity()), true
	case reqGetFinalRange:
		return int32(dec.FinalRange()), true
	case reqGetSampleRate:
		return int32(dec.SampleRate()), true
	case reqGetPitch:
		return int32(dec.Pitch()), true
	case reqGetGain:
		return int32(dec.Gain()), true
	case reqGetLastPacketDuration:
		return int32(dec.LastPacketDuration()), true
	case reqGetPhaseInversionDisabled:
		return boolToI32(dec.PhaseInversionDisabled()), true
	case reqGetIgnoreExtensions:
		return boolToI32(dec.IgnoreExtensions()), true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// libopus <-> gopus enum translation.
// ---------------------------------------------------------------------------

func bandwidthFromLibopus(arg int32) (Bandwidth, bool) {
	switch int(arg) {
	case cBandwidthNB:
		return BandwidthNarrowband, true
	case cBandwidthMB:
		return BandwidthMediumband, true
	case cBandwidthWB:
		return BandwidthWideband, true
	case cBandwidthSWB:
		return BandwidthSuperwideband, true
	case cBandwidthFB:
		return BandwidthFullband, true
	}
	return 0, false
}

func bandwidthToLibopus(bw Bandwidth) int32 {
	switch bw {
	case BandwidthNarrowband:
		return cBandwidthNB
	case BandwidthMediumband:
		return cBandwidthMB
	case BandwidthWideband:
		return cBandwidthWB
	case BandwidthSuperwideband:
		return cBandwidthSWB
	case BandwidthFullband:
		return cBandwidthFB
	}
	return cOpusAuto
}

// bandwidthGetToLibopus mirrors libopus decoder OPUS_GET_BANDWIDTH which
// returns st->bandwidth: 0 until a real packet decided it.
func bandwidthGetToLibopus(bw Bandwidth, known bool) int32 {
	if !known {
		return 0
	}
	return bandwidthToLibopus(bw)
}

// encoderBandwidthToLibopus reports the value the gopus encoder Bandwidth()
// getter returns, in the libopus argument domain. libopus OPUS_GET_BANDWIDTH
// returns st->bandwidth (the DECIDED bandwidth: FULLBAND at init, updated only
// during encode), not the user request.
func encoderBandwidthToLibopus(enc *Encoder) int32 {
	return bandwidthToLibopus(enc.Bandwidth())
}

func applicationToLibopus(app Application) int32 {
	switch app {
	case ApplicationVoIP:
		return cAppVoIP
	case ApplicationAudio:
		return cAppAudio
	case ApplicationLowDelay:
		return cAppLowDelay
	}
	return int32(app)
}

func applicationFromConfig(app Application) int {
	return int(applicationToLibopus(app))
}

// ---------------------------------------------------------------------------
// Random program generation.
// ---------------------------------------------------------------------------

type ctlArgGen func(r *rand.Rand) int32

// encoderSetReqs is the set of encoder SET requests fuzzed, each paired with an
// argument generator that emits a mix of valid + boundary + out-of-range values.
var encoderSetReqs = []struct {
	req int32
	gen ctlArgGen
}{
	{reqSetBitrate, gen(0, -1, -1000, 1, 500, 499, 501, 6000, 64000, 510000, 750000, 1500000, 99999999, -2, -999999)},
	{reqSetComplexity, gen(-2, -1, 0, 1, 5, 9, 10, 11, 100)},
	{reqSetVBR, gen(-1, 0, 1, 2)},
	{reqSetVBRConstraint, gen(-1, 0, 1, 2)},
	{reqSetInbandFEC, gen(-1, 0, 1, 2, 3, 10)},
	{reqSetPacketLossPerc, gen(-1, 0, 1, 50, 100, 101, 200)},
	{reqSetDTX, gen(-1, 0, 1, 2)},
	// Force-channels auto is spelled OPUS_AUTO (-1000) in the libopus CTL
	// domain; gopus maps that to its public -1 sentinel (see applyEncoderSet).
	// Raw -1 is intentionally omitted because it is an invalid value in the
	// libopus domain yet the auto sentinel in the gopus public domain, so it
	// has no single shared meaning to compare.
	{reqSetForceChannels, gen(-1000, -2, 0, 1, 2, 3)},
	{reqSetSignal, gen(-1000, -1, 0, 3001, 3002, 3003, 9999)},
	{reqSetBandwidth, gen(-1000, 1100, 1101, 1102, 1103, 1104, 1105, 1106, 9999)},
	{reqSetMaxBandwidth, gen(-1000, 1100, 1101, 1102, 1103, 1104, 1105, 1106, 9999)},
	{reqSetLSBDepth, gen(7, 8, 16, 24, 25, 0, 32)},
	{reqSetExpertFrameDuration, gen(4999, 5000, 5001, 5004, 5009, 5010, 0)},
	{reqSetPredictionDisabled, gen(-1, 0, 1, 2)},
	{reqSetPhaseInversionDisabled, gen(-1, 0, 1, 2)},
	{reqSetApplication, gen(2048, 2049, 2051, 2050, 0, -1)},
}

// encoderGetReqs is the set of encoder GET requests fuzzed for value parity.
//
// OPUS_GET_BITRATE is deliberately excluded: libopus resolves OPUS_AUTO /
// OPUS_BITRATE_MAX (and clamps the stored user bitrate against the 1276-byte
// max) at GET time via user_bitrate_to_bitrate(st, prev_framesize, 1276),
// whereas gopus defers that resolution to Encode and Bitrate() returns the
// stored clamped user bitrate. That is a documented intentional design choice
// (see TestEncoderCTL_BitrateGetResidual); SET_BITRATE clamping is still
// exercised for return-code parity.
//
// OPUS_GET_FINAL_RANGE / OPUS_GET_IN_DTX value parity is NOT asserted here:
// both reflect the encoder's entropy-coded OUTPUT, which is exercised
// byte-for-byte by the encode differential fuzz (encode_differential_fuzz_test);
// asserting them on arbitrary CTL programs (which may drive OPUS_AUTO-bitrate
// corners outside the encode harness's explicit-bitrate matrix) would duplicate
// that gate and conflate CTL semantics with encode parity. Their CTL return
// code is still verified (ctlGetComparesValue).
var encoderGetReqs = []int32{
	reqGetApplication, reqGetMaxBandwidth, reqGetVBR,
	reqGetBandwidth, reqGetComplexity, reqGetInbandFEC, reqGetPacketLossPerc,
	reqGetDTX, reqGetVBRConstraint, reqGetForceChannels, reqGetSignal,
	reqGetLookahead, reqGetSampleRate, reqGetFinalRange, reqGetLSBDepth,
	reqGetExpertFrameDuration, reqGetPredictionDisabled,
	reqGetPhaseInversionDisabled, reqGetInDTX,
}

// ctlGetComparesValue reports whether a GET request's VALUE (not just its
// return code) is asserted for parity. Output-derived encoder GETs whose value
// is a function of the entropy-coded packet stream are compared only for return
// code, since byte-stream parity is covered by the dedicated encode/decode
// differential harnesses.
func ctlGetComparesValue(isDecoder bool, req int32) bool {
	if !isDecoder {
		switch int(req) {
		case reqGetFinalRange, reqGetInDTX:
			return false
		}
	}
	return true
}

var decoderSetReqs = []struct {
	req int32
	gen ctlArgGen
}{
	{reqSetComplexity, gen(-2, -1, 0, 1, 5, 10, 11, 100)},
	{reqSetGain, gen(-32769, -32768, -256, 0, 256, 32767, 32768, 100000)},
	{reqSetPhaseInversionDisabled, gen(-1, 0, 1, 2)},
	{reqSetIgnoreExtensions, gen(-1, 0, 1, 2)},
}

var decoderGetReqs = []int32{
	reqGetBandwidth, reqGetComplexity, reqGetFinalRange, reqGetSampleRate,
	reqGetPitch, reqGetGain, reqGetLastPacketDuration,
	reqGetPhaseInversionDisabled, reqGetIgnoreExtensions,
}

// gen returns an argument generator that uniformly picks from the supplied pool.
func gen(values ...int32) ctlArgGen {
	pool := append([]int32(nil), values...)
	return func(r *rand.Rand) int32 {
		return pool[r.Intn(len(pool))]
	}
}

func genEncoderProgram(r *rand.Rand, n int, withProcess bool) []libopustest.CTLOp {
	ops := make([]libopustest.CTLOp, 0, n)
	for i := 0; i < n; i++ {
		roll := r.Intn(10)
		if !withProcess && roll == 0 {
			// Replace the PROCESS slot with a GET so the op mix stays dense.
			roll = 2
		}
		switch roll {
		case 0:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpProcess})
		case 1:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpReset})
		case 2, 3, 4:
			g := encoderGetReqs[r.Intn(len(encoderGetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpGet, Request: g})
		default:
			s := encoderSetReqs[r.Intn(len(encoderSetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpSet, Request: s.req, Arg: s.gen(r)})
		}
	}
	return ops
}

func genDecoderProgram(r *rand.Rand, n int) []libopustest.CTLOp {
	ops := make([]libopustest.CTLOp, 0, n)
	for i := 0; i < n; i++ {
		switch r.Intn(10) {
		case 0, 1:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpProcess})
		case 2:
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpReset})
		case 3, 4, 5:
			g := decoderGetReqs[r.Intn(len(decoderGetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpGet, Request: g})
		default:
			s := decoderSetReqs[r.Intn(len(decoderSetReqs))]
			ops = append(ops, libopustest.CTLOp{Op: libopustest.CTLOpSet, Request: s.req, Arg: s.gen(r)})
		}
	}
	return ops
}

// ---------------------------------------------------------------------------
// Parity comparison.
// ---------------------------------------------------------------------------

// compareCTLResults asserts gopus vs oracle parity for a single program. It
// reports the first divergence with a minimized prefix so failures pinpoint the
// exact op (and its predecessors) that produced the mismatch.
func compareCTLResults(t *testing.T, label string, isDecoder bool, ops []libopustest.CTLOp, gopus, oracle []libopustest.CTLResult) {
	t.Helper()
	if len(gopus) != len(oracle) {
		t.Fatalf("%s: result count gopus=%d oracle=%d", label, len(gopus), len(oracle))
	}
	for i := range ops {
		g, o := gopus[i], oracle[i]
		op := ops[i]
		mismatch := ""
		if op.Op == libopustest.CTLOpSet {
			if g.Ret != o.Ret {
				mismatch = fmt.Sprintf("SET %s(%d) ret gopus=%d oracle=%d",
					ctlReqName(op.Request), op.Arg, g.Ret, o.Ret)
			}
		} else if op.Op == libopustest.CTLOpGet {
			if g.Ret != o.Ret {
				mismatch = fmt.Sprintf("GET %s ret gopus=%d oracle=%d",
					ctlReqName(op.Request), g.Ret, o.Ret)
			} else if o.Ret == cOpusOK && ctlGetComparesValue(isDecoder, op.Request) && g.Value != o.Value {
				mismatch = fmt.Sprintf("GET %s value gopus=%d oracle=%d",
					ctlReqName(op.Request), g.Value, o.Value)
			}
		}
		if mismatch != "" {
			t.Errorf("%s: op %d (%s): %s\nminimized prefix:\n%s",
				label, i, opName(op.Op), mismatch, formatProgram(ops[:i+1]))
			return
		}
	}
}

func formatProgram(ops []libopustest.CTLOp) string {
	s := ""
	for i, op := range ops {
		switch op.Op {
		case libopustest.CTLOpSet:
			s += fmt.Sprintf("  [%d] SET %s arg=%d\n", i, ctlReqName(op.Request), op.Arg)
		case libopustest.CTLOpGet:
			s += fmt.Sprintf("  [%d] GET %s\n", i, ctlReqName(op.Request))
		case libopustest.CTLOpProcess:
			s += fmt.Sprintf("  [%d] PROCESS\n", i)
		case libopustest.CTLOpReset:
			s += fmt.Sprintf("  [%d] RESET\n", i)
		}
	}
	return s
}

func runEncoderCTLProgram(t *testing.T, sampleRate, channels int, app Application, ops []libopustest.CTLOp) []libopustest.CTLResult {
	enc := mustNewTestEncoder(t, sampleRate, channels, app)
	out := make([]libopustest.CTLResult, len(ops))
	for i, op := range ops {
		ret, val, have := applyEncoderOp(enc, op)
		out[i] = libopustest.CTLResult{Ret: ret, Value: val, HaveValue: have}
	}
	return out
}

func runDecoderCTLProgram(t *testing.T, sampleRate, channels int, ops []libopustest.CTLOp, feed []byte) []libopustest.CTLResult {
	dec := mustNewTestDecoder(t, sampleRate, channels)
	out := make([]libopustest.CTLResult, len(ops))
	for i, op := range ops {
		ret, val, have := applyDecoderOp(dec, op, feed)
		out[i] = libopustest.CTLResult{Ret: ret, Value: val, HaveValue: have}
	}
	return out
}

// encodeFeederPacket produces a real packet (gopus AUDIO encoder, 440 Hz sine).
// The same bytes are decoded by both gopus and the libopus oracle so the decoder
// OP_PROCESS input — and every decode-derived GET — is byte-identical.
func encodeFeederPacket(t *testing.T, sampleRate, channels int) []byte {
	t.Helper()
	enc := mustNewTestEncoder(t, sampleRate, channels, ApplicationAudio)
	pcm := generateSineWaveFloat32(sampleRate, 440, enc.FrameSize(), channels)
	buf := make([]byte, 4000)
	n, err := enc.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("feeder encode: %v", err)
	}
	return buf[:n]
}

// ---------------------------------------------------------------------------
// Tests.
// ---------------------------------------------------------------------------

// TestEncoderCTLSequenceFuzz drives seeded random encoder CTL programs through
// gopus and the libopus oracle and asserts SET/GET behavioral parity.
func TestEncoderCTLSequenceFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.CTLSequenceHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "ctl sequence", err)
	}

	type cfg struct {
		rate int
		ch   int
		app  Application
		// withProcess interleaves encodes. Enabled only at 48 kHz: a PROCESS op
		// makes the GET-after-encode state (decided bandwidth, first-frame lock)
		// depend on the encoder's byte output, which is covered byte-for-byte by
		// the 48 kHz encode differential fuzz. Non-48 kHz rates exercise the
		// rate-independent SET/GET/clamp/reset CTL semantics without encode.
		withProcess bool
	}
	configs := []cfg{
		{48000, 1, ApplicationAudio, true},
		{48000, 2, ApplicationAudio, true},
		{48000, 2, ApplicationVoIP, true},
		{48000, 1, ApplicationLowDelay, true},
		{24000, 1, ApplicationLowDelay, false},
		{16000, 1, ApplicationVoIP, false},
		{8000, 2, ApplicationAudio, false},
		{12000, 1, ApplicationAudio, false},
	}

	const seeds = 60
	const opsPerSeed = 40

	for _, c := range configs {
		c := c
		t.Run(fmt.Sprintf("%dHz_%dch_%v", c.rate, c.ch, c.app), func(t *testing.T) {
			for seed := 0; seed < seeds; seed++ {
				r := rand.New(rand.NewSource(int64(seed)*1000003 + int64(c.rate) + int64(c.ch)))
				ops := genEncoderProgram(r, opsPerSeed, c.withProcess)

				oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
					IsDecoder:   false,
					SampleRate:  c.rate,
					Channels:    c.ch,
					Application: applicationFromConfig(c.app),
					FrameSize:   ctlFuzzFrameSize,
					Ops:         ops,
				})
				if err != nil {
					t.Fatalf("oracle seed %d: %v", seed, err)
				}
				gopus := runEncoderCTLProgram(t, c.rate, c.ch, c.app, ops)
				compareCTLResults(t, fmt.Sprintf("enc seed=%d", seed), false, ops, gopus, oracle)
				if t.Failed() {
					return
				}
			}
		})
	}
}

// TestDecoderCTLSequenceFuzz drives seeded random decoder CTL programs through
// gopus and the libopus oracle and asserts SET/GET behavioral parity.
func TestDecoderCTLSequenceFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.CTLSequenceHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "ctl sequence", err)
	}

	type cfg struct {
		rate int
		ch   int
	}
	configs := []cfg{
		{48000, 1},
		{48000, 2},
		{24000, 2},
		{16000, 1},
		{8000, 2},
		{12000, 1},
	}

	const seeds = 60
	const opsPerSeed = 40

	for _, c := range configs {
		c := c
		t.Run(fmt.Sprintf("%dHz_%dch", c.rate, c.ch), func(t *testing.T) {
			feed := encodeFeederPacket(t, c.rate, c.ch)
			for seed := 0; seed < seeds; seed++ {
				r := rand.New(rand.NewSource(int64(seed)*2000003 + int64(c.rate) + int64(c.ch)))
				ops := genDecoderProgram(r, opsPerSeed)

				oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
					IsDecoder:  true,
					SampleRate: c.rate,
					Channels:   c.ch,
					FrameSize:  ctlFuzzFrameSize,
					FeedPacket: feed,
					Ops:        ops,
				})
				if err != nil {
					t.Fatalf("oracle seed %d: %v", seed, err)
				}
				gopus := runDecoderCTLProgram(t, c.rate, c.ch, ops, feed)
				compareCTLResults(t, fmt.Sprintf("dec seed=%d", seed), true, ops, gopus, oracle)
				if t.Failed() {
					return
				}
			}
		})
	}
}
