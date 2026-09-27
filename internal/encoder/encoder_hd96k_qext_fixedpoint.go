//go:build gopus_fixed_point && gopus_qext

package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
)

func (e *Encoder) encodeNativeHD96kFixed(pcm []float32, frameSize int, dst []byte) (int, bool, error) {
	if !validHD96kFrameSize(frameSize) || len(pcm) != frameSize*int(e.channels) {
		return 0, true, ErrInvalidFrameSize
	}
	if e.voipApp {
		return 0, false, nil
	}
	e.fixedCELTUsed = false
	channels := int(e.channels)
	if len(dst) < 3 {
		return 0, true, ErrInvalidConfig
	}

	// The native 96 kHz route receives un-delayed API samples. FIXED_POINT uses
	// the same Q8 conversion as opus_encode_native before CELT pre-emphasis.
	e.prepareFixedInputRes(pcm)
	defer e.clearFixedInputRes()
	if cap(e.fixedFiltered) < len(e.fixedRawRes) {
		e.fixedFiltered = make([]int32, len(e.fixedRawRes))
	}
	e.fixedFiltered = e.fixedFiltered[:len(e.fixedRawRes)]
	if e.qextActive() {
		copy(e.fixedFiltered, e.fixedRawRes)
	} else {
		fixedDCRejectRes(e.fixedRawRes, e.fixedFiltered, &e.fixedHPMem, 96000, channels, 3)
	}
	e.fixedFrameSource = e.fixedFiltered

	// Keep the public CELT controls synchronized with the float encoder state;
	// the fixed encoder uses its native 96 kHz mode and consumes the same values.
	e.ensureCELTEncoder()
	ce := e.celtEncoder
	if !ce.HD96kEncodeEnabled() {
		ce.EnableHD96kMode()
	}
	ce.SetQEXTEnabled(e.qextActive())
	ce.SetStreamChannels(channels)
	ce.SetBandwidth(celt.CELTFullband)
	ce.SetHybrid(false)
	ce.SetTopLevelDelayCompensatedInput(true)
	ce.SetDCRejectEnabled(false)
	ce.SetLSBQuantizationEnabled(false)
	ce.SetDelayCompensationEnabled(false)
	ce.SetLSBDepth(int(e.lsbDepth))
	ce.SetPrediction(e.celtPredictionMode())
	ce.SetComplexity(int(e.complexity))
	useVBR := e.bitrateMode != ModeCBR
	if useVBR {
		ce.SetBitrate(int(e.bitrate))
	}
	ce.SetVBR(useVBR)
	if useVBR {
		ce.SetConstrainedVBR(e.bitrateMode == ModeCVBR)
	}

	st := e.ensureFixedCELTRate(channels, 96000)
	frameSamples := frameSize * channels
	if cap(e.fixedDelayed) < frameSamples {
		e.fixedDelayed = make([]int32, frameSamples)
	}
	e.fixedDelayed = e.fixedDelayed[:frameSamples]
	if e.lowDelay {
		copy(e.fixedDelayed, e.fixedFiltered)
	} else {
		const encoderBuffer = 960
		const delayCompensation = 384
		bufferSamples := encoderBuffer * channels
		if cap(st.hd96Delay) < bufferSamples {
			st.hd96Delay = make([]int32, bufferSamples)
		} else {
			st.hd96Delay = st.hd96Delay[:bufferSamples]
		}
		inputSamples := (frameSize + delayCompensation) * channels
		if cap(st.hd96Frame) < inputSamples {
			st.hd96Frame = make([]int32, inputSamples)
		} else {
			st.hd96Frame = st.hd96Frame[:inputSamples]
		}
		copy(st.hd96Frame[:delayCompensation*channels], st.hd96Delay[(encoderBuffer-delayCompensation)*channels:])
		copy(st.hd96Frame[delayCompensation*channels:], e.fixedFiltered)
		copy(e.fixedDelayed, st.hd96Frame[:frameSamples])
		if encoderBuffer-frameSize-delayCompensation > 0 {
			keep := (encoderBuffer - frameSize - delayCompensation) * channels
			copy(st.hd96Delay[:keep], st.hd96Delay[frameSize*channels:frameSize*channels+keep])
			copy(st.hd96Delay[keep:], st.hd96Frame)
		} else {
			start := (frameSize + delayCompensation - encoderBuffer) * channels
			copy(st.hd96Delay, st.hd96Frame[start:start+bufferSamples])
		}
	}
	e.fixedFrameReady = true
	e.fixedFrameCursor = 0
	st.enc.SetQEXTEnabled(e.qextActive())
	st.enc.SetLFE(e.lfe)
	st.enc.SetBandRange(0, 21)
	st.enc.SetStreamChannels(int32(channels))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(int(celt.BitrateMax))
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtPredictionMode()))
	st.enc.SetSilkInfo(0, 0)
	if e.lastAnalysisValid {
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
	st.enc.SetVBR(useVBR)
	if useVBR {
		st.enc.SetBitrate(int(e.bitrate))
		st.enc.SetConstrainedVBR(e.bitrateMode == ModeCVBR)
	}
	if e.fixedMaskPending || e.fixedMaskActive {
		e.setFixedCELTEnergyMask(st.enc)
		e.fixedMaskPending = false
	}

	packetCap := 1276
	if e.qextActive() {
		packetCap = hd96kQEXTPacketSizeCap
	}
	maxDataBytes := min(len(dst), packetCap*6)
	if e.bitrateMode == ModeCBR {
		cbrBytes := min((bitrateToBitsFs(int(e.bitrate), 96000, frameSize)+4)/8, maxDataBytes)
		maxDataBytes = max(1, cbrBytes)
	}
	maxCompressedBytes := maxDataBytes - 1 // opus_encode_frame_native reserves the TOC byte.
	if e.qextActive() {
		maxCompressedBytes = min(maxCompressedBytes, hd96kQEXTPacketSizeCap)
	} else {
		maxCompressedBytes = min(maxCompressedBytes, 1275)
	}
	if maxCompressedBytes < 2 {
		return 0, true, ErrInvalidConfig
	}
	if cap(st.rng.Buffer()) < maxCompressedBytes {
		st.rng.Init(make([]byte, maxCompressedBytes))
	} else {
		buf := st.rng.Buffer()[:maxCompressedBytes]
		clear(buf)
		st.rng.Init(buf)
	}
	st.lastQ8 = e.fixedDelayed
	st.pcm16 = st.pcm16[:0]
	st.lastMaxBytes = int32(maxCompressedBytes)
	innerBitrate := int32(st.enc.Bitrate())
	st.lastBitrate = innerBitrate
	st.lastLSBDepth = e.lsbDepth
	n := st.enc.EncodeWithECRes(e.fixedDelayed, frameSize, st.rng, maxCompressedBytes)
	e.fixedFinalRange = st.enc.FinalRange()
	e.fixedCELTUsed = true
	mainPayload := st.rng.Buffer()[:n]
	if cap(e.fixedCELTOut) < n {
		e.fixedCELTOut = make([]byte, n)
	} else {
		e.fixedCELTOut = e.fixedCELTOut[:n]
	}
	copy(e.fixedCELTOut, mainPayload)
	e.frameFinalRange = e.fixedFinalRange
	e.finalRange = e.frameFinalRange
	e.first = false
	n, err := assembleHD96kPacket(dst, frameSize, e.fixedCELTOut, st.enc.LastQEXTPayload(), channels == 2)
	return n, true, err
}
