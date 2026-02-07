//go:build cgo_libopus

package testvectors

import (
	"fmt"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/silk"
)

type libopusNSQStateSnapshot = cgowrap.SilkNSQStateSnapshot

func compareNSQTraceWithLibopus(tr silk.NSQTrace) string {
	libPulses, libXQ, libSeed := cgowrap.SilkNSQDelDec(
		tr.FrameLength, tr.SubfrLength, tr.NbSubfr, tr.LTPMemLength,
		tr.PredLPCOrder, tr.ShapeLPCOrder, tr.WarpingQ16, tr.NStatesDelayedDecision,
		tr.SignalType, tr.QuantOffsetType, tr.NLSFInterpCoefQ2, tr.SeedIn,
		tr.InputQ0,
		tr.PredCoefQ12,
		tr.LTPCoefQ14,
		tr.ARShpQ13,
		tr.HarmShapeGainQ14,
		tr.TiltQ14,
		tr.LFShpQ14,
		tr.GainsQ16,
		tr.PitchL,
		tr.LambdaQ10, tr.LTPScaleQ14,
	)
	libPulsesHash := hashInt8Slice(libPulses)
	libXQHash := hashInt16Slice(libXQ)
	msg := fmt.Sprintf("seedIn=%d seedOut(go=%d lib=%d) pulsesHash(go=%x lib=%x) xqHash(go=%x lib=%x) frameLen=%d subfrLen=%d nbSubfr=%d ltpMem=%d",
		tr.SeedIn, tr.SeedOut, libSeed, tr.PulsesHash, libPulsesHash, tr.XqHash, libXQHash, tr.FrameLength, tr.SubfrLength, tr.NbSubfr, tr.LTPMemLength)

	if len(tr.NSQXQ) > 0 && len(tr.NSQSLTPShpQ14) > 0 && len(tr.NSQLPCQ14) > 0 && len(tr.NSQAR2Q14) > 0 {
		sPulses, sXQ, sSeed, _, _, _, sFinal := cgowrap.SilkNSQDelDecCaptureWithStateFinal(
			tr.FrameLength, tr.SubfrLength, tr.NbSubfr, tr.LTPMemLength,
			tr.PredLPCOrder, tr.ShapeLPCOrder, tr.WarpingQ16, tr.NStatesDelayedDecision,
			tr.SignalType, tr.QuantOffsetType, tr.NLSFInterpCoefQ2, tr.SeedIn,
			tr.InputQ0,
			tr.PredCoefQ12,
			tr.LTPCoefQ14,
			tr.ARShpQ13,
			tr.HarmShapeGainQ14,
			tr.TiltQ14,
			tr.LFShpQ14,
			tr.GainsQ16,
			tr.PitchL,
			tr.LambdaQ10, tr.LTPScaleQ14,
			tr.NSQXQ,
			tr.NSQSLTPShpQ14,
			tr.NSQLPCQ14,
			tr.NSQAR2Q14,
			tr.NSQLFARQ14,
			tr.NSQDiffQ14,
			tr.NSQLagPrev,
			tr.NSQSLTPBufIdx,
			tr.NSQSLTPShpBufIdx,
			tr.NSQRandSeed,
			tr.NSQPrevGainQ16,
			tr.NSQRewhiteFlag,
		)
		sPulsesHash := hashInt8Slice(sPulses)
		sXQHash := hashInt16Slice(sXQ)
		msg += fmt.Sprintf(" stateNSQ(seed=%d pulses=%x xq=%x)", sSeed, sPulsesHash, sXQHash)

		if len(tr.NSQPostXQ) > 0 {
			if tr.NSQPostXQHash != 0 && tr.NSQPostXQHash != hashInt16Slice(sFinal.XQ) {
				if idx, goVal, libVal, ok := firstInt16Diff(tr.NSQPostXQ, sFinal.XQ); ok {
					msg += fmt.Sprintf(" postXQ diff idx=%d go=%d lib=%d", idx, goVal, libVal)
				} else {
					msg += " postXQ diff"
				}
			}
			if tr.NSQPostSLTPShpHash != 0 && tr.NSQPostSLTPShpHash != hashInt32Slice(sFinal.SLTPShpQ14) {
				if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQPostSLTPShpQ14, sFinal.SLTPShpQ14); ok {
					msg += fmt.Sprintf(" postSLTPShp diff idx=%d go=%d lib=%d", idx, goVal, libVal)
				} else {
					msg += " postSLTPShp diff"
				}
			}
			if tr.NSQPostSLPCHash != 0 && tr.NSQPostSLPCHash != hashInt32Slice(sFinal.SLPCQ14) {
				if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQPostLPCQ14, sFinal.SLPCQ14); ok {
					msg += fmt.Sprintf(" postSLPC diff idx=%d go=%d lib=%d", idx, goVal, libVal)
				} else {
					msg += " postSLPC diff"
				}
			}
			if tr.NSQPostSAR2Hash != 0 && tr.NSQPostSAR2Hash != hashInt32Slice(sFinal.SAR2Q14) {
				if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQPostAR2Q14, sFinal.SAR2Q14); ok {
					msg += fmt.Sprintf(" postSAR2 diff idx=%d go=%d lib=%d", idx, goVal, libVal)
				} else {
					msg += " postSAR2 diff"
				}
			}
			if tr.NSQPostLFARQ14 != sFinal.LFARQ14 || tr.NSQPostDiffQ14 != sFinal.DiffQ14 {
				msg += fmt.Sprintf(" postScalars diff lfAR go=%d lib=%d diff go=%d lib=%d",
					tr.NSQPostLFARQ14, sFinal.LFARQ14, tr.NSQPostDiffQ14, sFinal.DiffQ14)
			}
			if tr.NSQPostLagPrev != sFinal.LagPrev || tr.NSQPostSLTPBufIdx != sFinal.SLTPBufIdx || tr.NSQPostSLTPShpBufIdx != sFinal.SLTPShpBufIdx {
				msg += fmt.Sprintf(" postIdx diff lagPrev go=%d lib=%d sLTPBufIdx go=%d lib=%d sLTPShpBufIdx go=%d lib=%d",
					tr.NSQPostLagPrev, sFinal.LagPrev, tr.NSQPostSLTPBufIdx, sFinal.SLTPBufIdx, tr.NSQPostSLTPShpBufIdx, sFinal.SLTPShpBufIdx)
			}
			if tr.NSQPostRandSeed != sFinal.RandSeed || tr.NSQPostPrevGainQ16 != sFinal.PrevGainQ16 || tr.NSQPostRewhiteFlag != sFinal.RewhiteFlag {
				msg += fmt.Sprintf(" postFlags diff randSeed go=%d lib=%d prevGain go=%d lib=%d rewhite go=%d lib=%d",
					tr.NSQPostRandSeed, sFinal.RandSeed, tr.NSQPostPrevGainQ16, sFinal.PrevGainQ16, tr.NSQPostRewhiteFlag, sFinal.RewhiteFlag)
			}
		}
	}

	var libSLTPQ15 []int32
	var libSLTPRaw []int16
	var libDelayedGain []int32
	if len(tr.NSQXQ) > 0 && len(tr.NSQSLTPShpQ14) > 0 && len(tr.NSQLPCQ14) > 0 && len(tr.NSQAR2Q14) > 0 {
		_, _, _, libSLTPQ15, libSLTPRaw, libDelayedGain = cgowrap.SilkNSQDelDecCaptureWithState(
			tr.FrameLength, tr.SubfrLength, tr.NbSubfr, tr.LTPMemLength,
			tr.PredLPCOrder, tr.ShapeLPCOrder, tr.WarpingQ16, tr.NStatesDelayedDecision,
			tr.SignalType, tr.QuantOffsetType, tr.NLSFInterpCoefQ2, tr.SeedIn,
			tr.InputQ0,
			tr.PredCoefQ12,
			tr.LTPCoefQ14,
			tr.ARShpQ13,
			tr.HarmShapeGainQ14,
			tr.TiltQ14,
			tr.LFShpQ14,
			tr.GainsQ16,
			tr.PitchL,
			tr.LambdaQ10, tr.LTPScaleQ14,
			tr.NSQXQ,
			tr.NSQSLTPShpQ14,
			tr.NSQLPCQ14,
			tr.NSQAR2Q14,
			tr.NSQLFARQ14,
			tr.NSQDiffQ14,
			tr.NSQLagPrev,
			tr.NSQSLTPBufIdx,
			tr.NSQSLTPShpBufIdx,
			tr.NSQRandSeed,
			tr.NSQPrevGainQ16,
			tr.NSQRewhiteFlag,
		)
	} else {
		_, _, _, libSLTPQ15, libSLTPRaw, libDelayedGain = cgowrap.SilkNSQDelDecCaptureSLTPQ15(
			tr.FrameLength, tr.SubfrLength, tr.NbSubfr, tr.LTPMemLength,
			tr.PredLPCOrder, tr.ShapeLPCOrder, tr.WarpingQ16, tr.NStatesDelayedDecision,
			tr.SignalType, tr.QuantOffsetType, tr.NLSFInterpCoefQ2, tr.SeedIn,
			tr.InputQ0,
			tr.PredCoefQ12,
			tr.LTPCoefQ14,
			tr.ARShpQ13,
			tr.HarmShapeGainQ14,
			tr.TiltQ14,
			tr.LFShpQ14,
			tr.GainsQ16,
			tr.PitchL,
			tr.LambdaQ10, tr.LTPScaleQ14,
		)
	}
	libSLTPHash := hashInt32Slice(libSLTPQ15)
	if tr.SLTPQ15Hash != 0 && tr.SLTPQ15Hash != libSLTPHash {
		if idx, goVal, libVal, ok := firstInt32Diff(tr.SLTPQ15, libSLTPQ15); ok {
			msg += fmt.Sprintf(" sLTPQ15 diff idx=%d go=%d lib=%d", idx, goVal, libVal)
		} else {
			msg += " sLTPQ15 diff"
		}
	}
	if len(tr.SLTPRaw) > 0 && len(libSLTPRaw) > 0 {
		if idx, goVal, libVal, ok := firstInt16Diff(tr.SLTPRaw, libSLTPRaw); ok {
			subfr, lag, start, bufIdx := locateSLTPIndex(tr, idx)
			if subfr >= 0 {
				msg += fmt.Sprintf(" sLTPrw diff idx=%d go=%d lib=%d subfr=%d lag=%d start=%d bufIdx=%d",
					idx, goVal, libVal, subfr, lag, start, bufIdx)
			} else {
				msg += fmt.Sprintf(" sLTPrw diff idx=%d go=%d lib=%d", idx, goVal, libVal)
			}
		}
	}
	if len(tr.DelayedGainQ10) > 0 && len(libDelayedGain) > 0 {
		if idx, goVal, libVal, ok := firstInt32Diff(tr.DelayedGainQ10, libDelayedGain); ok {
			msg += fmt.Sprintf(" dGain diff idx=%d go=%d lib=%d", idx, goVal, libVal)
		}
	}

	if tr.SubfrLength > 0 && tr.NbSubfr > 0 && len(tr.XScSubfrHash) >= tr.NbSubfr {
		for sf := 0; sf < tr.NbSubfr; sf++ {
			start := sf * tr.SubfrLength
			end := start + tr.SubfrLength
			if end > len(tr.InputQ0) {
				end = len(tr.InputQ0)
			}
			if start >= end {
				continue
			}
			libXSc := cgowrap.SilkNSQScaleXScQ10(tr.InputQ0[start:end], tr.GainsQ16[sf])
			libXScHash := hashInt32Slice(libXSc)
			if tr.XScSubfrHash[sf] != 0 && tr.XScSubfrHash[sf] != libXScHash {
				goStart := sf * tr.SubfrLength
				goEnd := goStart + tr.SubfrLength
				if goEnd > len(tr.XScQ10) {
					goEnd = len(tr.XScQ10)
				}
				if idx, goVal, libVal, ok := firstInt32Diff(tr.XScQ10[goStart:goEnd], libXSc); ok {
					msg += fmt.Sprintf(" xsc diff sf=%d idx=%d go=%d lib=%d", sf, idx, goVal, libVal)
				} else {
					msg += fmt.Sprintf(" xsc diff sf=%d", sf)
				}
				break
			}
		}
	}

	return msg
}

func captureLibopusNSQState(samples []float32, sampleRate, bitrate, frameSize, frameIndex int) (libopusNSQStateSnapshot, bool) {
	return cgowrap.SilkCaptureNSQStateAtFrame(samples, sampleRate, bitrate, frameSize, frameIndex)
}

// firstInt32Diff/firstInt16Diff are defined in libopus_trace_test.go

func locateSLTPIndex(tr silk.NSQTrace, idx int) (subfr int, lag int, start int, bufIdx int) {
	subfr = -1
	lag = 0
	start = 0
	bufIdx = 0
	if tr.SubfrLength <= 0 || tr.NbSubfr <= 0 {
		return
	}
	for sf := 0; sf < tr.NbSubfr; sf++ {
		bufIdx = tr.LTPMemLength + sf*tr.SubfrLength
		if sf < len(tr.PitchL) {
			lag = tr.PitchL[sf]
		} else {
			lag = 0
		}
		start = bufIdx - lag - 5/2
		if idx >= start && idx < bufIdx {
			subfr = sf
			return
		}
	}
	return
}
