package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	if e.restrictedSilkApp {
		return ModeSILK
	}
	if e.lowDelay {
		return ModeCELT
	}
	if frameSize > e.frame20ms() {
		if e.mode != ModeAuto {
			// Hybrid long packets are encoded as 20ms multi-frame packets.
			if e.mode == ModeHybrid {
				return ModeHybrid
			}
			// CELT 40/60ms is encoded as multi-frame (2/3 x 20ms) packets.
			return e.mode
		}
		bw := e.effectiveBandwidth()

		// Fullband long frames in auto mode follow CELT-only path in libopus audio app.
		if bw == types.BandwidthFullband {
			return ModeCELT
		}
		if bw == types.BandwidthSuperwideband {
			return e.selectLongSWBAutoMode(frameSize, signalHint)
		}
		// Respect explicit or analyzed signal hints.
		switch signalHint {
		case types.SignalVoice:
			// In SWB long-frame auto mode, libopus only uses Hybrid or CELT.
			// Avoid raw SILK packets in this lane.
			if bw == types.BandwidthSuperwideband {
				return ModeHybrid
			}
			return ModeSILK
		case types.SignalMusic:
			return ModeCELT
		}
		// In auto-signal mode for long frames, bias by bandwidth instead of the
		// per-frame classifier to avoid unstable SILK/CELT switching.
		if bw == types.BandwidthSuperwideband {
			return ModeCELT
		}
		return ModeSILK
	}
	if e.mode != ModeAuto {
		return e.mode
	}
	return e.selectShortAutoMode(frameSize, signalHint)
}

func isConcreteMode(mode Mode) bool {
	return mode == ModeSILK || mode == ModeHybrid || mode == ModeCELT
}

// applyCELTTransitionDelay mirrors libopus to_celt handling:
// when switching from SILK/Hybrid to CELT on >=10 ms frames, hold one frame in
// the previous non-CELT mode but advance prev-mode state to CELT for next frame.
func (e *Encoder) applyCELTTransitionDelay(frameSize int, requested Mode) (actual Mode, prevNext Mode) {
	actual = requested
	prevNext = requested

	prev := e.prevMode
	if !isConcreteMode(prev) || !isConcreteMode(requested) {
		return actual, prevNext
	}

	switchingAcrossCELT := (requested == ModeCELT && prev != ModeCELT) ||
		(requested != ModeCELT && prev == ModeCELT)
	if !switchingAcrossCELT {
		return actual, prevNext
	}

	// libopus delays SILK/Hybrid->CELT transition for 10ms+ frames.
	if requested == ModeCELT {
		minDelayFrame := int(e.sampleRate) / 100
		if minDelayFrame <= 0 {
			minDelayFrame = 480
		}
		if frameSize >= minDelayFrame {
			actual = prev
			prevNext = ModeCELT
		}
	}
	return actual, prevNext
}

// selectShortAutoMode ports libopus auto mode-threshold control for 10/20 ms
// frames (SILK/hybrid vs CELT), including previous-mode hysteresis.
func (e *Encoder) selectShortAutoMode(frameSize int, signalHint types.Signal) Mode {
	_ = signalHint
	bw := e.effectiveBandwidth()

	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, int32(frameRate), useVBR, ModeAuto, e.complexity, e.packetLoss)

	prev := e.prevAutoMode
	if prev != ModeSILK && prev != ModeHybrid && prev != ModeCELT {
		prev = ModeAuto
	}

	voiceEst := e.autoVoiceEstimate(prev)
	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384
	if e.voipApp {
		threshold += 8000
	}
	switch prev {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
		threshold += 4000
	}

	mode := ModeSILK
	if equivRate >= int32(threshold) {
		mode = ModeCELT
	}
	// Match libopus behavior: with in-band FEC and sufficient expected loss,
	// force SILK unless music-safe FEC is confident the signal is music.
	if e.fecEnabled && e.packetLoss > int32((128-voiceEst)>>4) &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeSILK
	}
	// Match libopus behavior: when DTX is enabled for voiced content, favor SILK.
	if e.dtxEnabled && voiceEst > 100 {
		mode = ModeSILK
	}
	// For SWB/FB lanes, SILK-only maps to hybrid in libopus.
	if mode == ModeSILK && bw > types.BandwidthWideband {
		mode = ModeHybrid
	}
	if mode == ModeHybrid && bw <= types.BandwidthWideband {
		mode = ModeSILK
	}

	if !ValidFrameSize(frameSize, mode) {
		if ValidFrameSize(frameSize, ModeCELT) {
			return ModeCELT
		}
		if ValidFrameSize(frameSize, ModeSILK) {
			return ModeSILK
		}
		return ModeCELT
	}
	return mode
}

// selectLongSWBAutoMode mirrors libopus mode-threshold control for long-frame SWB
// auto mode (Celt-only vs Silk/Hybrid lane), using analysis-derived voice estimate
// and previous-mode hysteresis.
func (e *Encoder) selectLongSWBAutoMode(frameSize int, signalHint types.Signal) Mode {
	_ = signalHint
	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, int32(frameRate), useVBR, ModeAuto, e.complexity, e.packetLoss)

	prev := e.prevAutoMode
	if prev != ModeCELT && prev != ModeSILK && prev != ModeHybrid {
		prev = ModeAuto
	}
	voiceEst := e.autoVoiceEstimate(prev)

	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384

	// Match libopus auto-mode threshold bias for VoIP.
	if e.voipApp {
		threshold += 8000
	}

	// libopus hysteresis: bias against rapid CELT<->SILK/HYBRID switching.
	switch prev {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
		threshold += 4000
	}

	mode := ModeHybrid
	if equivRate >= int32(threshold) {
		mode = ModeCELT
	}
	// Match libopus behavior: with in-band FEC and sufficient expected loss,
	// force SILK unless music-safe FEC is confident the signal is music.
	if e.fecEnabled && e.packetLoss > int32((128-voiceEst)>>4) &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeHybrid
	}
	// Match libopus behavior: when DTX is enabled for voiced content, favor SILK lane.
	if e.dtxEnabled && voiceEst > 100 {
		mode = ModeHybrid
	}
	return mode
}

// autoSignalFromPCM runs analysis when needed and returns a voice or music hint
// only when a valid long-frame result crosses a confidence threshold. Otherwise
// it returns SignalAuto.
func (e *Encoder) autoSignalFromPCM(pcm []opusRes, frameSize int) types.Signal {
	if len(pcm) == 0 || frameSize <= 0 {
		return types.SignalAuto
	}
	if !e.analysisEnabled() {
		return types.SignalAuto
	}
	if !e.lastAnalysisFresh {
		pcm32 := []float32(pcm)
		f20 := e.frame20ms()
		runAnalyzer := frameSize > f20
		if !runAnalyzer && e.mode == ModeAuto && frameSize == f20 && e.effectiveBandwidth() == types.BandwidthSuperwideband {
			runAnalyzer = true
		}
		if runAnalyzer && e.analyzer != nil {
			info := e.analyzer.RunAnalysis(pcm32, frameSize, int(e.channels))
			if info.Valid {
				e.lastAnalysisInfo = info
				e.lastAnalysisValid = true
				e.lastAnalysisFresh = true
			}
		}
	}

	// Only trust clear decisions from analysis probabilities on long frames.
	if frameSize > e.frame20ms() && e.lastAnalysisValid {
		if e.lastAnalysisInfo.MusicProb >= 0.65 {
			return types.SignalMusic
		}
		if e.lastAnalysisInfo.MusicProb <= 0.60 {
			return types.SignalVoice
		}
		return types.SignalAuto
	}
	// libopus mode-auto fallback when analysis is unavailable/invalid is to keep
	// OPUS_SIGNAL_AUTO and rely on threshold control with default voice estimate.
	return types.SignalAuto
}

func (e *Encoder) autoVoiceEstimate(prev Mode) int {
	voiceEst := 48 // OPUS_APPLICATION_AUDIO fallback.
	if e.voipApp {
		voiceEst = 115
	}
	if e.signalType == types.SignalVoice {
		return 127
	}
	if e.signalType == types.SignalMusic {
		return 0
	}
	if !e.lastAnalysisValid {
		return voiceEst
	}
	prob := e.lastAnalysisInfo.MusicProb
	switch prev {
	case ModeCELT:
		prob = e.lastAnalysisInfo.MusicProbMax
	case ModeSILK, ModeHybrid:
		prob = e.lastAnalysisInfo.MusicProbMin
	}
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}
	voiceRatio := int(opusmath.FloorHalfPlusF32ToInt32(float32(100) * (float32(1) - prob)))
	voiceEst = min(
		// OPUS_APPLICATION_AUDIO clamp.
		(voiceRatio*327)>>8, 115)
	return voiceEst
}

// effectiveBandwidth returns the resolved bandwidth for encoder submodules.
func (e *Encoder) effectiveBandwidth() types.Bandwidth {
	if e.lfe {
		return types.BandwidthNarrowband
	}
	return e.bandwidth
}

// packetTOCFrameSize maps the native-Fs per-frame size to the 48 kHz-equivalent
// count used to index the TOC config table. libopus gen_toc derives the TOC
// period from Fs/frame_size, so the duration (and therefore the period) is
// identical to the 48 kHz frame size frameSize*48000/Fs. At 48 kHz it is the
// identity; at sub-48 kHz native rates it scales the native frame size up to its
// 48 kHz-equivalent duration so the on-wire TOC config is unchanged.
func (e *Encoder) packetTOCFrameSize(frameSize int) int {
	fs := int(e.sampleRate)
	if fs <= 0 || fs == 48000 {
		return frameSize
	}
	return frameSize * 48000 / fs
}

func (e *Encoder) celtPredictionMode() int {
	if e.predictionDisabled {
		return 0
	}
	return 2
}

// silkInternalBandwidth is the TOC bandwidth of a SILK-only frame coded at
// the SILK internal sampling rate (src/opus_encoder.c:2215-2227).
func silkInternalBandwidth(internalSampleRate int32) types.Bandwidth {
	switch internalSampleRate {
	case 8000:
		return types.BandwidthNarrowband
	case 12000:
		return types.BandwidthMediumband
	default:
		return types.BandwidthWideband
	}
}

// configureSILKMode sets the silk_mode controls opus_encode_frame_native hands
// silk_Encode for a frame of mode (src/opus_encoder.c:2050-2189): the SILK
// rate, the packet duration and channel layout, the internal sampling rate
// request and limits, the bit cap and CBR flag, and the FEC, complexity, loss
// and dependency settings. maxDataBytes is the frame's max_data_bytes after
// the 1276-byte cap: a SILK-only frame whose budget cannot carry wideband caps
// the internal rate.
func (e *Encoder) configureSILKMode(mode Mode, frameSize, maxDataBytes, bitRate, maxBits int, useCBR bool) {
	m := &e.silkMode
	sampleRate := int(e.sampleRate)
	m.NChannelsAPI = e.channels
	m.NChannelsInternal = int32(e.silkInternalChannels())
	m.APISampleRate = e.sampleRate
	m.PayloadSizeMs = int32(1000 * frameSize / sampleRate)
	m.BitRate = int32(bitRate)
	switch e.effectiveBandwidth() {
	case types.BandwidthNarrowband:
		m.DesiredInternalSampleRate = 8000
	case types.BandwidthMediumband:
		m.DesiredInternalSampleRate = 12000
	default:
		m.DesiredInternalSampleRate = 16000
	}
	// Hybrid frames do not allow a bandwidth reduction at the lowest rates.
	m.MinInternalSampleRate = 8000
	if mode == ModeHybrid {
		m.MinInternalSampleRate = 16000
	}
	m.MaxInternalSampleRate = 16000
	if mode == ModeSILK {
		effectiveMaxRate := int32(bitsToBitrateFs(maxDataBytes*8, sampleRate, frameSize))
		if sampleRate/frameSize > 50 {
			effectiveMaxRate = effectiveMaxRate * 2 / 3
		}
		if effectiveMaxRate < 8000 {
			m.MaxInternalSampleRate = 12000
			m.DesiredInternalSampleRate = min(12000, m.DesiredInternalSampleRate)
		}
		if effectiveMaxRate < 7000 {
			m.MaxInternalSampleRate = 8000
			m.DesiredInternalSampleRate = min(8000, m.DesiredInternalSampleRate)
		}
		// At 96 kHz no input resampler serves 8 or 12 kHz.
		if sampleRate == 96000 {
			m.MaxInternalSampleRate = 16000
			m.DesiredInternalSampleRate = 16000
		}
	}
	m.PacketLossPercentage = e.packetLoss
	m.Complexity = e.complexity
	m.LBRRCoded = e.lbrrCoded
	m.UseCBR = useCBR
	m.MaxBits = int32(maxBits)
	m.ToMono = e.toMono != 0
	m.ReducedDependency = e.predictionDisabled
}

// silkActivity returns the Opus-level voice activity decision handed to
// silk_Encode (src/opus_encoder.c:1888-1930): VAD_NO_DECISION when the analysis
// made no decision, otherwise whether the frame is active.
func (e *Encoder) silkActivity() int {
	switch {
	case !e.lastOpusVADValid:
		return silk.VADNoDecision
	case e.lastOpusVADActive:
		return 1
	default:
		return silk.VADNoActivity
	}
}

// maxLongPacketFrameBytes bounds the combined size of all subframe payloads
// kept by keepFrame within a single long packet. Each of the <=6 internal
// subframes is independently capped by at most a full Opus packet, so this
// covers any realisable sum while letting resetPacketFrameScratch pre-grow the
// backing buffer once, keeping earlier keepFrame subslices stable.
const maxLongPacketFrameBytes = 6 * maxSilkPacketBytes

// resetPacketFrameScratch prepares the reusable long-packet assembly scratch at
// the top of each long-packet encode. It pre-grows scratchFrameBytes so that the
// per-frame keepFrame appends never reallocate (which would invalidate earlier
// returned subslices).
func (e *Encoder) resetPacketFrameScratch() {
	if cap(e.scratchFrameBytes) < maxLongPacketFrameBytes {
		e.scratchFrameBytes = make([]byte, 0, maxLongPacketFrameBytes)
	}
	e.scratchFrameBytes = e.scratchFrameBytes[:0]
	if cap(e.scratchQEXTPayloadBytes) < maxLongPacketFrameBytes {
		e.scratchQEXTPayloadBytes = make([]byte, 0, maxLongPacketFrameBytes)
	}
	e.scratchQEXTPayloadBytes = e.scratchQEXTPayloadBytes[:0]
}

// ensurePacketScratch reserves n bytes for an assembled packet. The default
// buffer holds a TOC byte and a 1275-byte frame. Longer packets use the caller's
// larger byte budget, matching libopus's assembly into the caller's buffer.
func (e *Encoder) ensurePacketScratch(n int) {
	if cap(e.scratchPacket) >= n {
		e.scratchPacket = e.scratchPacket[:cap(e.scratchPacket)]
		return
	}
	e.scratchPacket = make([]byte, n)
}

// keepFrame copies frame into the reusable scratchFrameBytes backing buffer and
// returns a length-capped subslice. The range coder output buffer is reused
// across subframes, so the copy gives each kept frame stable storage. The
// returned subslice is 3-index sliced so a stray append cannot reach into the
// next frame's bytes.
func (e *Encoder) keepFrame(frame []byte) []byte {
	start := len(e.scratchFrameBytes)
	e.scratchFrameBytes = append(e.scratchFrameBytes, frame...)
	end := len(e.scratchFrameBytes)
	return e.scratchFrameBytes[start:end:end]
}

// keepQEXTPayload copies a QEXT extension payload into the reusable
// scratchQEXTPayloadBytes backing buffer, giving it stable storage for the
// duration of packet assembly without a per-frame heap allocation.
func (e *Encoder) keepQEXTPayload(payload []byte) []byte {
	start := len(e.scratchQEXTPayloadBytes)
	e.scratchQEXTPayloadBytes = append(e.scratchQEXTPayloadBytes, payload...)
	end := len(e.scratchQEXTPayloadBytes)
	return e.scratchQEXTPayloadBytes[start:end:end]
}

// multiFramePacket carries the packet-level arguments of the multi-frame
// branch of opus_encode_native (src/opus_encoder.c:1697-1838).
type multiFramePacket struct {
	mode             Mode
	frameSize        int
	originalBitrate  int // st->bitrate_bps before the DRED reservation
	encodingBitrate  int // st->bitrate_bps after the DRED reservation
	dredBitrate      int
	dredExtraDelay   int
	outDataBytes     int
	equivRate        int32
	redundancy       bool
	celtToSILK       bool
	toCELT           bool
	floatInputDirect bool
}

// encodeMultiFramePacket codes a packet longer than one Opus frame the way
// opus_encode_native does (src/opus_encoder.c:1697-1838): CELT-only and hybrid
// packets split into 20 ms frames, SILK-only packets into 40 ms (80 ms), 60 ms
// (120 ms) or 20 ms (100 ms) frames. Each frame runs opus_encode_frame_native
// with the packet's equiv_rate, redundancy and prefill and a budget curr_max,
// and the repacketizer joins them.
func (e *Encoder) encodeMultiFramePacket(pcm, vadPCM []opusRes, p multiFramePacket) ([]byte, error) {
	mode := p.mode
	frameSize := p.frameSize
	f20 := e.frame20ms()
	encFrameSize := f20
	if mode == ModeSILK {
		switch frameSize {
		case 4 * f20: // 80 ms -> 2x40 ms
			encFrameSize = 2 * f20
		case 6 * f20: // 120 ms -> 2x60 ms
			encFrameSize = 3 * f20
		}
	}
	if frameSize <= encFrameSize || frameSize%encFrameSize != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / encFrameSize
	if frameCount > 6 {
		return nil, ErrInvalidFrameSize
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels || len(vadPCM) != frameSize*channels {
		return nil, ErrInvalidFrameSize
	}
	// The TOC codes the 48 kHz-equivalent frame duration.
	tocFrameSize := encFrameSize * 48000 / int(e.sampleRate)
	if e.analysisReadBakSet && e.analyzer != nil {
		e.analyzer.ReadPos = e.analysisReadPosBak
		e.analyzer.ReadSubframe = e.analysisSubframeBak
	}

	e.resetPacketFrameScratch()
	frames := e.scratchFrameSlots[:frameCount]
	sameSize := true
	prevSize := -1
	// The repacketizer takes the whole output buffer in VBR and the CBR
	// packet size in CBR.
	repacketizeLen := p.outDataBytes
	if e.bitrateMode == ModeCBR {
		repacketizeLen = min(e.targetBytesForBitrate(p.originalBitrate, frameSize), p.outDataBytes)
	}
	repacketizeLen = max(repacketizeLen, 1)
	// Worst cases: code 2 with different sizes for 2 frames, code 3 VBR for
	// more.
	maxHeaderBytes := 3
	if frameCount > 2 {
		maxHeaderBytes = 2 + (frameCount-1)*2
	}
	qext := extsupport.QEXT && mode == ModeCELT && e.qextActive()
	if qext {
		// The separators and the padding length byte of the QEXT extensions.
		maxHeaderBytes += frameCount
	}
	maxLenSum := max(frameCount+repacketizeLen-maxHeaderBytes, frameCount)
	// The assembled packet is bounded by maxLenSum+maxHeaderBytes.
	e.ensurePacketScratch(maxLenSum + maxHeaderBytes)
	dredBytes := 0
	if p.dredBitrate > 0 {
		dredBytes = bitrateToBitsFs(p.dredBitrate, int(e.sampleRate), frameSize) / 8
	}
	dredActive := e.dredEncodingActive()
	if dredActive {
		e.clearDREDPacketSnapshot()
	}
	totSize := 0
	firstFrameMaxBytes := 0
	var qextExtensions [6]packetExtension
	qextExtensionCount := 0
	savedBitrate := e.bitrate
	e.bitrate = int32(p.encodingBitrate)
	defer func() { e.bitrate = savedBitrate }()
	bakToMono := e.beginMultiFramePacket()
	defer func() { e.toMono = bakToMono }()
	// st->prev_mode as each frame sees it: the frames of the packet advance
	// it once they are coded.
	prevMode := e.prevMode
	var packetBW types.Bandwidth
	frameStride := encFrameSize * channels
	for i := range frameCount {
		e.primeSubframeAnalysis(encFrameSize)
		start := i * frameStride
		rawSubPCM := pcm[start : start+frameStride]
		floatOffset := -1
		if p.floatInputDirect {
			floatOffset = start
		}
		subPCM := e.preprocessInputHPFrame(rawSubPCM, encFrameSize, mode, floatOffset)
		e.preprocessFixedInputRes(encFrameSize)
		subVADPCM := vadPCM[start : start+frameStride]
		e.nonfinalFrame = i < frameCount-1
		if mode != ModeCELT && i > 0 {
			// The packet's SILK prefill reruns in every frame from the delay
			// history the previous frame left (src/opus_encoder.c:2191-2209).
			e.maybePrefillSILKOnModeTransition(mode, false, false)
		}
		e.updateFrameActivity(subVADPCM, isDigitalSilenceRes(subVADPCM, e.lsbDepth), mode)
		dredNoDecision := !e.lastOpusVADValid
		if dredActive {
			e.processDREDLatentsWithActivity(subPCM, p.dredExtraDelay, e.lastOpusVADActive)
			if mode == ModeCELT && i == 0 {
				e.snapshotDREDPacketState()
			}
		}

		currMax := min(bitrateToBitsFs(p.encodingBitrate, int(e.sampleRate), encFrameSize)/8, maxLenSum/frameCount)
		if dredBytes > 0 {
			currMax = min(currMax, (maxLenSum-dredBytes)/frameCount)
			if i == 0 {
				currMax += dredBytes
			}
		}
		// Each QEXT subframe reserves its extension-length signal before coding.
		// src/opus_encoder.c reduces curr_max by curr_max/254 in this loop.
		if e.qextActive() {
			currMax -= currMax / 254
		}
		currMax = min(maxLenSum-totSize, currMax)
		if i == 0 {
			firstFrameMaxBytes = currMax
		}
		// A switch to CELT is signalled in the last frame, a switch from CELT
		// in the first one.
		frameToCELT := p.toCELT && i == frameCount-1
		frameRedundancy := p.redundancy && (frameToCELT || (!p.toCELT && i == 0))
		frame, err := e.encodeFrameNative(subPCM, frameRequest{
			mode:         mode,
			frameSize:    encFrameSize,
			maxDataBytes: currMax,
			dredBitrate:  p.dredBitrate,
			equivRate:    p.equivRate,
			prevMode:     prevMode,
			redundancy:   frameRedundancy,
			celtToSILK:   p.celtToSILK,
		})
		if err != nil {
			return nil, err
		}
		if frame.dtx {
			// SILK DTX still consumes and high-pass filters this child input,
			// but libopus returns before advancing the delay buffer.
			e.advanceFixedInputCursor(encFrameSize)
		}
		// The repacketizer only joins frames with the same TOC.
		if i == 0 {
			packetBW = frame.bw
		} else if frame.bw != packetBW {
			return nil, ErrEncodingFailed
		}
		if mode != ModeCELT {
			if dredActive && dredNoDecision {
				e.backfillDREDActivityForFrame(encFrameSize, e.silkMode.SignalType != 0)
			}
			if dredActive && i == 0 {
				e.snapshotDREDPacketState()
			}
		}
		// Keep a stable copy: the frame scratch is reused.
		frameCopy := e.keepFrame(frame.data)
		// A SILK DTX frame is TOC-only and ends before the Opus-level DTX
		// decision; any other frame runs decide_dtx_mode, and a suppressed
		// frame becomes a length-0 frame of the packet.
		suppressed := frame.dtx
		if !frame.dtx {
			prevMode = mode
			if frameToCELT {
				prevMode = ModeCELT
			}
			if mode != ModeCELT {
				e.commitMultiFrameSubframe(i == frameCount-1)
			}
			suppressed = !dredActive && e.subframeDTXSuppress(encFrameSize)
		}
		e.multiFrameLastSubframeDTX = suppressed
		if suppressed {
			frameCopy = frameCopy[:0]
			e.multiFrameDTXCount++
			totSize++ // tot_size += tmp_len (1) for a DTX frame
		} else {
			totSize += len(frameCopy) + 1
		}
		frames[i] = frameCopy
		if !suppressed && qext {
			if qextPayload := e.lastQEXTPayload(); len(qextPayload) > 0 {
				qextExtensions[qextExtensionCount] = packetExtension{
					ID:    qextExtensionID,
					Data:  e.keepQEXTPayload(qextPayload),
					Frame: i,
				}
				qextExtensionCount++
			}
		}
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}
	e.analysisReadBakSet = false

	stereo := e.packetStereoForMode(mode)
	if dredActive {
		// DRED plan and extension sizing use the original packet budget. The
		// primary frames above use p.encodingBitrate after reservation.
		e.bitrate = int32(p.originalBitrate)
		if dredPacket, ok, err := e.maybeBuildMultiFrameDREDPacket(frames, mode, packetBW, frameSize, tocFrameSize, firstFrameMaxBytes, stereo, !sameSize, qextExtensions[:qextExtensionCount]); err != nil {
			return nil, err
		} else if ok {
			return dredPacket, nil
		}
	}
	var packetLen int
	var err error
	if qextExtensionCount > 0 {
		// opus_repacketizer_out_range_impl pads CBR multi-frame packets to
		// repacketize_len while retaining each frame's QEXT extension.
		withPadding := e.bitrateMode == ModeCBR && e.multiFrameDTXCount != frameCount
		targetLen := 0
		if withPadding {
			targetLen = repacketizeLen
		}
		packetLen, err = buildMultiFramePacketWithExtensionsInto(e.scratchPacket, frames, modeToTypes(mode), packetBW, tocFrameSize, stereo, !sameSize, qextExtensions[:qextExtensionCount], targetLen, withPadding)
	} else {
		packetLen, err = buildMultiFramePacketInto(e.scratchPacket, frames, modeToTypes(mode), packetBW, tocFrameSize, stereo, !sameSize)
	}
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:packetLen], nil
}

// beginMultiFramePacket runs the packet start of the multi-frame branch of
// opus_encode_native (src/opus_encoder.c:1763-1767): a packet that carries a
// stereo->mono transition (silk_mode.toMono) forces mono coding from then on
// (force_channels = 1); otherwise prev_channels takes the packet's channels.
// Every sub-frame is coded with silk_mode.toMono = 0; the packet's value, which
// comes back afterwards, is returned.
func (e *Encoder) beginMultiFramePacket() (bakToMono int32) {
	bakToMono = e.toMono
	if bakToMono != 0 {
		e.forceChannels = 1
	} else {
		e.prevChannels = e.streamChannels
	}
	e.toMono = 0
	e.multiFrameCommitted = false
	e.multiFrameLastCommitted = false
	return bakToMono
}

// commitMultiFrameSubframe records that a SILK or Hybrid sub-frame reached the
// end-of-frame bookkeeping of opus_encode_frame_native (the previous mode,
// prev_channels and st->first), which a SILK DTX sub-frame skips.
func (e *Encoder) commitMultiFrameSubframe(last bool) {
	e.multiFrameCommitted = true
	e.multiFrameLastCommitted = last
}

// ensureSILKEncoder creates the SILK encoder on first use, in the state
// silk_InitEncoder leaves it (src/opus_encoder.c opus_encoder_init). The SILK
// encoder picks and changes its internal sampling rate itself from the
// silk_mode controls of each frame.
func (e *Encoder) ensureSILKEncoder() {
	if e.silk == nil {
		e.silk = silk.NewPacketEncoder(int(e.channels))
	}
}

// trackPeakSignalEnergy ports the peak signal energy tracking of
// opus_encode_native (src/opus_encoder.c:1310-1318): once per packet, on the
// input of the whole packet, unless it is digital silence or the packet's
// analysis finds it inactive.
func (e *Encoder) trackPeakSignalEnergy(pcm []opusRes, isSilence bool) {
	if isSilence || e.dtx == nil {
		return
	}
	if !e.lastAnalysisValid || e.lastAnalysisInfo.VADProb > DTXActivityThreshold {
		e.dtx.peakSignalEnergy = maxf(0.999*e.dtx.peakSignalEnergy, computeFrameEnergyRes(pcm))
	}
}

// updateFrameActivity ports the Opus-level voice activity decision of
// opus_encode_frame_native (src/opus_encoder.c:1911-1930) for a frame whose
// input is pcm: inactive for digital silence; otherwise the frame's analysis
// decides, and a frame it finds inactive stays active when it is loud against
// the tracked peak; without analysis a CELT-only frame compares its energy
// with the peak, and any other frame makes no decision (VAD_NO_DECISION).
func (e *Encoder) updateFrameActivity(pcm []opusRes, isSilence bool, mode Mode) {
	e.lastOpusVADActivityObserved = true
	e.lastAnalysisFresh = false
	peak := opusVal32(0)
	if e.dtx != nil {
		peak = e.dtx.peakSignalEnergy
	}
	switch {
	case isSilence:
		e.lastOpusVADProb = 0
		e.lastOpusVADValid = true
		e.lastOpusVADActive = false
	case e.lastAnalysisValid:
		e.lastOpusVADProb = e.lastAnalysisInfo.VADProb
		e.lastOpusVADValid = true
		e.lastOpusVADActive = e.lastOpusVADProb >= DTXActivityThreshold ||
			peak < pseudoSNRThreshold*computeFrameEnergyRes(pcm)
	case mode == ModeCELT:
		// Peak energy is boosted a bit because not only the active frames
		// are averaged.
		e.lastOpusVADProb = 1
		e.lastOpusVADValid = true
		e.lastOpusVADActive = peak < pseudoSNRThreshold*(0.5*computeFrameEnergyRes(pcm))
	default:
		e.clearOpusVADDecision()
	}
}

func (e *Encoder) clearOpusVADDecision() {
	e.lastOpusVADValid = false
	e.lastOpusVADActive = true
	e.lastOpusVADProb = 1.0
}

// resolveDTXActivity resolves the libopus opus_int activity for the just-encoded
// frame (opus_encoder.c:2235). When the Opus-level VAD made no decision
// (VAD_NO_DECISION, lastOpusVADValid==false) libopus resolves activity from the
// SILK signal type: activity = (signalType != TYPE_NO_VOICE_ACTIVITY).
func (e *Encoder) resolveDTXActivity() bool {
	if e.lastOpusVADValid {
		return e.lastOpusVADActive
	}
	if e.silk != nil {
		return e.silkMode.SignalType != 0
	}
	// VAD_NO_DECISION with no SILK result resolves to active (true), matching the
	// libopus default where activity stays VAD_NO_DECISION (-1, truthy) and
	// decide_dtx_mode treats !activity as false.
	return true
}

// celtUpsampleFactor mirrors libopus resampling_factor(Fs): the CELT input
// upsample factor for the native API rate (1 at 48 kHz, 2/3/4/6 at
// 24/16/12/8 kHz). The float CELT encoder consumes native-Fs frame sizes and
// upsamples to the 48 kHz core internally.
func (e *Encoder) celtUpsampleFactor() int {
	switch e.sampleRate {
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	}
	return 1
}

// ensureCELTEncoder creates the CELT encoder on first use and syncs the
// Opus-level CELT settings. The CELT prediction setting is frame state: the
// frame encode sets it for CELT-only and hybrid frames, after a mode-transition
// prefill and before a SILK->CELT redundant frame, and the CELT->SILK redundant
// frame of a SILK-only frame codes with the setting the last CELT use left
// (src/opus_encoder.c:2288-2295, 2478-2486, 2519-2526).
func (e *Encoder) ensureCELTEncoder() {
	if e.celtEncoder == nil {
		e.celtEncoder = celt.NewEncoder(int(e.channels))
		configureCELTEncoderForSampleRate(e.celtEncoder, e.sampleRate)
		e.celtEncoder.SetComplexity(int(e.complexity))
		// Opus encoder already rounds input to the configured LSB depth.
		e.celtEncoder.SetLSBQuantizationEnabled(false)
		// Opus encoder already applies dc_reject at the top level.
		e.celtEncoder.SetDCRejectEnabled(false)
		// Opus encoder already applies CELT delay compensation at the top level.
		e.celtEncoder.SetDelayCompensationEnabled(false)
		e.celtEncoder.SetPhaseInversionDisabled(e.phaseInversionDisabled)
		// celt_encoder_init leaves CBR at OPUS_BITRATE_MAX with the VBR
		// constraint on.
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(true)
		e.celtEncoder.SetBitrate(celt.BitrateMax)
	}
	e.syncQEXTToCELT()
	e.celtEncoder.SetLFE(e.lfe)
	e.syncCELTEnergyMask()
	e.celtEncoder.SetStreamChannels(int(e.streamChannels))
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetPacketLoss(int(e.packetLoss))
	// Default the CELT input upsample to the API rate's resampling_factor so the
	// CELT prefill (Fs/400 native) and CELT-only frames consume native-Fs sizes.
	// The redundancy CELT path overrides this to 1 (fixed 48 kHz block).
	e.celtEncoder.SetUpsample(e.celtUpsampleFactor())
}
