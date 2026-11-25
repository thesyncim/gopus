package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

// encodeOpusResWithAnalysisMaxBytes is the core single-frame encode pipeline,
// the Go counterpart of libopus opus_encode_native (src/opus_encoder.c). All
// public Encode* entry points funnel here after converting their input to the
// internal opusRes representation.
//
// inputPCM is one frame of interleaved samples (frameSize per channel);
// maxDataBytes is the caller's output budget after packet-size clamping; and
// refreshAnalysis, if non-nil, runs the tonality analyzer on the untouched input
// before any high-pass/DC processing, matching libopus run_analysis ordering.
//
// The function applies the input high-pass/DC filters and SILK cutoff update in
// libopus order, handles the low-space TOC-only path, selects mode and bandwidth,
// performs delay compensation and transition prefill, runs SILK/CELT/Hybrid, and
// assembles the packet. It returns nil if the buffered input does not yet contain
// a complete frame. Invalid frame sizes, insufficient output budgets, and
// encoding failures return ErrInvalidFrameSize, ErrBufferTooSmall, or
// ErrEncodingFailed, respectively.
func (e *Encoder) encodeOpusResWithAnalysisMaxBytes(inputPCM []opusRes, frameSize int, maxDataBytes int, refreshAnalysis func()) ([]byte, error) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// opus_encode_native clears rangeFinal at entry, including calls that emit
	// a TOC-only packet through the low-space return path.
	e.finalRange = 0
	// A non-positive frame size is never a valid Opus duration; reject it before
	// any sampleRate/frameSize division (libopus opus_encode_native returns
	// OPUS_BAD_ARG). Without this guard frameSize==0 passes the length check below
	// (expectedLen==0) and divides by zero in the frame-rate computation.
	if frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}
	expectedLen := frameSize * channels
	if len(inputPCM) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if maxDataBytes <= 0 {
		return nil, ErrEncodingFailed
	}
	// Just avoid insane packet sizes here; the per-frame caps apply later.
	packetCapBytes := e.maxOutputPacketBytes()
	if maxDataBytes > packetCapBytes {
		maxDataBytes = packetCapBytes
	}
	// opus_encode_native rejects a 100 ms frame when the caller provides only a
	// TOC byte. It clears rangeFinal above, then returns before analysis and
	// encoder-state updates (src/opus_encoder.c:1232-1238).
	if maxDataBytes == 1 && sampleRate == frameSize*10 {
		return nil, ErrBufferTooSmall
	}
	// e.bitrate carries st->bitrate_bps for this frame: the user bitrate bounded
	// by the output budget (user_bitrate_to_bitrate) and, in CBR, rounded to the
	// whole-byte frame size below.
	userBitrate := e.bitrate
	defer func() {
		e.bitrate = userBitrate
	}()
	e.bitrate = int32(e.resolvedBitrateForFrame(frameSize, maxDataBytes))
	isSilence := isDigitalSilenceRes(inputPCM, e.lsbDepth)
	e.hasCELTPrefill = false
	e.silkPrefillPending = false
	e.clearFixedCELTUsed()
	defer func() {
		e.analysisReadBakSet = false
	}()
	// Run Opus analysis on the original input frame (before top-level dc_reject)
	// to match libopus run_analysis ordering.
	if refreshAnalysis != nil {
		refreshAnalysis()
	}
	// opus_encode_native refreshes detected_bandwidth for every call before
	// either the automatic or user-forced mode branch consumes it.
	e.updateDetectedBandwidth()
	lookaheadSamples := 0
	vadPCM := inputPCM
	frameEnd := frameSize * channels
	samplesNeeded := frameEnd + lookaheadSamples
	directFrameInput := lookaheadSamples == 0 && len(e.inputBuffer) == 0
	// rawFramePCM is the caller frame before the Opus-level high-pass: libopus
	// reads it for compute_stereo_width() and the mode decisions, and only
	// opus_encode_frame_native() applies dc_reject()/hp_cutoff() into pcm_buf.
	var rawFramePCM []opusRes
	if directFrameInput {
		rawFramePCM = inputPCM[:frameEnd]
	} else {
		e.inputBuffer = append(e.inputBuffer, inputPCM...)
		if len(e.inputBuffer) < samplesNeeded {
			return nil, nil
		}
		rawFramePCM = e.inputBuffer[:frameEnd]
	}

	stereoWidth := e.frameStereoWidth(rawFramePCM, frameSize)

	// libopus "too little space" fast path (opus_encoder.c:1340). The resolved
	// bitrate is already in e.bitrate; derive the CBR-clamped budget and effective
	// bitrate, then emit a minimal TOC-only packet when neither the byte budget
	// nor the bitrate can support a real encode. This mirrors the per-stream
	// curr_max squeeze the multistream encoder applies to high-channel layouts.
	frameRate := sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 1
	}
	cbrMaxDataBytes := maxDataBytes
	if e.bitrateMode == ModeCBR {
		// src/opus_encoder.c:1327-1334: CBR codes whole bytes, so the frame
		// bitrate is the one the rounded byte count carries.
		cbrBytes := min((bitrateToBitsFs(int(e.bitrate), sampleRate, frameSize)+4)/8, maxDataBytes)
		e.bitrate = int32(bitsToBitrateFs(cbrBytes*8, sampleRate, frameSize))
		cbrMaxDataBytes = max(1, cbrBytes)
	}
	primaryBitrate := e.bitrate
	encodingBitrate := primaryBitrate
	dredBitrate := 0
	if e.dredEncodingActive() {
		if plan, ok := e.computeDREDEmissionPlan(frameSize); ok {
			dredBitrate = int(plan.bitrate)
			encodingBitrate -= plan.bitrate
			if encodingBitrate < 1 {
				encodingBitrate = 1
			}
		}
	}
	// libopus reserves DRED bitrate before its channel and bandwidth decisions
	// (src/opus_encoder.c:1337-1338). Keep the pre-reservation budget separately
	// for packet repacketization, while the selected primary mode uses the reduced
	// per-frame bitrate through primary frame encoding.
	e.bitrate = encodingBitrate
	effBitrate := int(encodingBitrate)
	if cbrMaxDataBytes < 3 || effBitrate < 3*frameRate*8 ||
		(frameRate < 50 && (cbrMaxDataBytes*frameRate < 300 || effBitrate < 2400)) {
		pkt, err := e.emitLowSpacePacket(sampleRate, frameSize, maxDataBytes, cbrMaxDataBytes, effBitrate)
		if err != nil {
			return nil, err
		}
		if !directFrameInput {
			remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
			e.inputBuffer = e.inputBuffer[:remaining]
		}
		return pkt, nil
	}
	// Keep the source frame unfiltered through mode selection. libopus applies
	// hp_cutoff()/dc_reject() inside each opus_encode_frame_native call, after
	// opus_encode_native has split a multi-frame packet.
	// Allow SILK DTX when DTX is on but the generalized DTX cannot be used,
	// e.g. because of the complexity setting or the sample rate
	// (src/opus_encoder.c:1458-1464).
	e.silkMode.UseDTX = e.dtxEnabled && !e.lastAnalysisValid && !isSilence

	var actualMode, prevModeNext Mode
	if e.mode == ModeAuto || e.lowDelay {
		// Full libopus mode and bandwidth decision chain: voice_ratio,
		// stereo_width, stream_channels, auto-bandwidth, bandwidth clamping,
		// decide_fec, and mode fixup. Low-delay applications pin CELT at
		// opus_encoder.c:1470 and still run bandwidth selection.
		actualMode, prevModeNext = e.autoModeAndBandwidthDecision(stereoWidth, frameSize, cbrMaxDataBytes, isSilence)
	} else {
		signalHint := e.signalType
		if signalHint == types.SignalAuto {
			signalHint = e.autoSignalFromPCM(rawFramePCM, frameSize)
		}
		e.updateStreamChannelsForFrame(frameSize)
		requestedMode := e.selectMode(frameSize, signalHint)
		if e.lfe {
			requestedMode = ModeCELT
		}
		if requestedMode != ModeCELT && frameSize < sampleRate/100 {
			requestedMode = ModeCELT
		}
		// The switch into CELT-only precedes the bandwidth clamp and the mode
		// fixup, as in the auto path (src/opus_encoder.c:1533-1557).
		requestedMode, prevModeNext = e.applyCELTTransitionDelay(frameSize, requestedMode)
		// libopus applies the stereo-to-mono delay after choosing the mode and
		// before recomputing the rate used for FEC and bandwidth decisions.
		e.applyStereoToMonoTransition(requestedMode)
		// Run decide_fec for non-auto modes too. In libopus, decide_fec()
		// runs unconditionally at line 1675 (not just in auto mode).
		// This controls whether LBRR is actually coded based on bitrate,
		// bandwidth, packet loss, and hysteresis.
		frameRate := sampleRate / frameSize
		if frameRate <= 0 {
			frameRate = 50
		}
		useVBR := e.bitrateMode != ModeCBR
		equivRate := e.computeEquivRate(e.bitrate, e.streamChannels, int32(frameRate),
			useVBR, requestedMode, e.complexity, e.packetLoss)
		// libopus keeps updating voice_ratio from valid analysis in forced modes
		// because forced CELT still uses it for automatic bandwidth selection.
		e.autoVoiceRatioFromAnalysis()
		e.selectAutoBandwidth(requestedMode, e.autoVoiceEst(), equivRate)
		e.bandwidth = e.autoClampBandwidth(e.bandwidth, requestedMode, equivRate, e.maxRateForFrame(frameSize, cbrMaxDataBytes))
		bw := e.bandwidth
		e.lbrrCoded = decideFEC(e.fecEnabled, e.packetLoss, e.lbrrCoded,
			requestedMode, &bw, equivRate)
		e.bandwidth = bw
		// libopus opus_encoder.c:1688-1695: only the restricted-SILK
		// application pins the bandwidth to WB; a plain forced-SILK request
		// with a wider bandwidth is promoted to Hybrid (and forced Hybrid at
		// <=WB drops to SILK), exactly like the auto path.
		if e.restrictedSilkApp && e.bandwidth > types.BandwidthWideband {
			e.bandwidth = types.BandwidthWideband
		}
		actualMode = autoModeFixup(requestedMode, e.bandwidth)
		if prevModeNext != ModeCELT {
			prevModeNext = actualMode
		}
	}
	transitionToCELT := prevModeNext == ModeCELT && actualMode != ModeCELT
	multiFrame := e.isMultiFramePacket(actualMode, frameSize)
	framePCM := rawFramePCM
	if !multiFrame {
		// A single native frame applies the input filter after mode selection.
		floatOffset := -1
		if directFrameInput {
			floatOffset = 0
		}
		framePCM = e.preprocessInputHPFrame(rawFramePCM, frameSize, actualMode, floatOffset)
		e.preprocessFixedInputRes(frameSize)
	}

	dredExtraDelay := 0
	if !e.lowDelay {
		dredExtraDelay = sampleRate / 250
	}
	f20 := e.frame20ms()
	dredInSubframes := (actualMode == ModeCELT && frameSize > f20 && frameSize%f20 == 0) ||
		(actualMode == ModeHybrid && frameSize > f20 && frameSize%f20 == 0) ||
		(actualMode == ModeSILK && frameSize > 3*f20)
	if e.dredEncodingActive() && !dredInSubframes {
		e.processDREDLatentsForPacket(framePCM, frameSize, dredExtraDelay, actualMode)
	} else if !e.dredEncodingActive() {
		e.clearInactiveDREDHistory()
	}

	// The peak signal energy is tracked once per packet on the unfiltered
	// input; each frame then decides its Opus-level activity
	// (src/opus_encoder.c:1310-1318 and 1911-1930). A multi-frame packet
	// decides it per frame. decide_dtx_mode() runs once the frame is coded, so
	// the encoder state advances before a DTX frame drops the payload
	// (src/opus_encoder.c:2564-2572).
	e.trackPeakSignalEnergy(vadPCM, isSilence)
	if !multiFrame {
		e.updateFrameActivity(vadPCM, isSilence, actualMode)
	}

	var frameData []byte
	var packet []byte
	var err error
	// packetBW is the TOC bandwidth of a single-frame packet: st->bandwidth, or
	// the SILK internal bandwidth for a SILK-only frame (curr_bandwidth).
	packetBW := e.effectiveBandwidth()
	silkDTX := false
	e.multiFrameDTXCount = 0
	e.multiFrameLastSubframeDTX = false
	// A switch between CELT and SILK or Hybrid carries a redundant CELT frame:
	// at the start of the first frame after CELT, or at the end of the last
	// frame before CELT (src/opus_encoder.c:1541-1558).
	celtToSILK := actualMode != ModeCELT && e.prevMode == ModeCELT
	redundancy := celtToSILK || transitionToCELT
	// The packet's equivalent rate after the mode decision
	// (src/opus_encoder.c:1573).
	equivRate := e.computeEquivRate(encodingBitrate, e.streamChannels, int32(sampleRate/frameSize),
		e.bitrateMode != ModeCBR, actualMode, e.complexity, e.packetLoss)
	if actualMode != ModeCELT {
		// The SILK prefill of a switch from CELT, which re-initializes SILK
		// on the first frame of the packet.
		e.maybePrefillSILKOnModeTransition(actualMode, true, true)
	}
	if multiFrame {
		packet, err = e.encodeMultiFramePacket(framePCM, vadPCM, multiFramePacket{
			mode:             actualMode,
			frameSize:        frameSize,
			originalBitrate:  int(primaryBitrate),
			encodingBitrate:  int(encodingBitrate),
			dredBitrate:      dredBitrate,
			dredExtraDelay:   dredExtraDelay,
			outDataBytes:     maxDataBytes,
			equivRate:        equivRate,
			redundancy:       redundancy,
			celtToSILK:       celtToSILK,
			toCELT:           transitionToCELT,
			floatInputDirect: directFrameInput,
		})
	} else {
		dredNoDecision := actualMode != ModeCELT && e.dredEncodingActive() && !e.lastOpusVADValid
		var frame codedFrame
		frame, err = e.encodeFrameNative(framePCM, frameRequest{
			mode:         actualMode,
			frameSize:    frameSize,
			maxDataBytes: cbrMaxDataBytes,
			dredBitrate:  dredBitrate,
			equivRate:    equivRate,
			prevMode:     e.prevMode,
			redundancy:   redundancy,
			celtToSILK:   celtToSILK,
		})
		if err == nil && dredNoDecision {
			e.backfillDREDActivityForFrame(frameSize, e.silkMode.SignalType != 0)
		}
		frameData, packetBW, silkDTX = frame.data, frame.bw, frame.dtx
	}
	// Primary encoding uses the reserved rate; packet assembly, DRED sizing,
	// and CBR/CVBR padding use the original per-frame budget.
	e.bitrate = primaryBitrate
	if err != nil {
		return nil, err
	}
	if !directFrameInput {
		remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
		e.inputBuffer = e.inputBuffer[:remaining]
	}
	// st->mode and st->bandwidth are set before the frame is coded.
	e.intMode = actualMode
	e.intBandwidth = e.bandwidth
	if silkDTX {
		// silk_Encode returned no payload: the packet is the TOC byte alone and
		// the frame ends before the previous mode, the channel history and
		// st->first advance (src/opus_encoder.c:2242-2248).
		e.finalRange = 0
		n, err := BuildPacketInto(e.scratchPacket, nil, modeToTypes(actualMode), packetBW, e.packetTOCFrameSize(frameSize), e.packetStereoForMode(actualMode))
		if err != nil {
			return nil, err
		}
		return e.scratchPacket[:n], nil
	}
	if multiFrame && actualMode != ModeCELT {
		if !e.multiFrameCommitted {
			// Every sub-frame was a SILK DTX frame, so none reached the
			// end-of-frame bookkeeping; the unpadded packet carries only TOCs.
			e.finalRange = 0
			return packet, nil
		}
		if !e.multiFrameLastCommitted {
			// The last sub-frame, which completes a switch to CELT, was a SILK
			// DTX frame: the previous mode comes from an earlier sub-frame.
			prevModeNext = actualMode
		}
	}

	// DTX decision (libopus opus_encoder.c:2564-2572): runs decide_dtx_mode AFTER
	// the frame is fully encoded so the encoder state (SILK NSQ/LPC history, CELT
	// energy memory) is advanced exactly as libopus does. When DTX fires the
	// already-encoded payload is discarded and only the 1-byte TOC is emitted; the
	// decoder runs its own comfort-noise generation when it sees a TOC with no
	// frame data.
	//
	// For multi-frame packets the DTX decision was already made per sub-frame
	// inside the encode*MultiFramePacket loop (mirroring libopus, which runs
	// decide_dtx_mode once per sub-frame). When EVERY sub-frame was suppressed
	// libopus' repacketizer emits an unpadded TOC-only packet — pad is
	// !use_vbr && (dtx_count != nb_frames), so all-DTX => no padding
	// (opus_encoder.c:1831). gopus' multi-frame builder already produced exactly
	// that all-empty packet, so it is returned here before the CBR padding step.
	// A partial DTX (some sub-frames carry payload) keeps its mixed packet and
	// flows through the normal CBR-padding path below. The per-sub-frame path is
	// only taken when DRED is not active; DRED multi-frame packets keep the
	// whole-frame DTX decision (their own packet builder owns the DTX-refresh
	// interaction) and so fall through to the else branch.
	perSubframeDTX := multiFrame && !e.dredEncodingActive()
	if e.dtxEnabled && e.dtx != nil && perSubframeDTX {
		subframeCount := e.multiFrameSubframeCount(actualMode, frameSize)
		if subframeCount > 0 && e.multiFrameDTXCount == subframeCount {
			if isConcreteMode(actualMode) {
				e.prevPacketMode = actualMode
			}
			if isConcreteMode(prevModeNext) {
				e.prevMode = prevModeNext
				if e.mode == ModeAuto {
					e.prevAutoMode = prevModeNext
				}
			}
			e.first = false
			e.prevChannels = e.streamChannels
			e.finalRange = 0
			return packet, nil
		}
	} else if !perSubframeDTX {
		activity := e.resolveDTXActivity()
		if e.decideDTXSuppress(activity, frameSize) {
			if isConcreteMode(actualMode) {
				e.prevPacketMode = actualMode
			}
			if isConcreteMode(prevModeNext) {
				e.prevMode = prevModeNext
				if e.mode == ModeAuto {
					e.prevAutoMode = prevModeNext
				}
			}
			e.first = false
			e.prevChannels = e.streamChannels
			e.finalRange = 0
			return e.buildDTXPacketForMode(frameSize, actualMode, packetBW)
		}
	}

	qextPayload := []byte(nil)
	if extsupport.QEXT && actualMode == ModeCELT && e.celtEncoder != nil {
		qextPayload = e.lastQEXTPayload()
	}
	var qextExtensionBuf [1]packetExtension
	qextExtensions := []packetExtension(nil)
	if len(qextPayload) > 0 {
		qextExtensionBuf[0] = packetExtension{ID: qextExtensionID, Data: qextPayload}
		qextExtensions = qextExtensionBuf[:]
	}
	dredPacketBuilt := false
	if packet == nil {
		stereo := e.packetStereoForMode(actualMode)
		// The TOC config table indexes by the 48 kHz-equivalent frame size
		// (libopus gen_toc derives the period from Fs/frame_size, which is the
		// same duration). For a sub-48 kHz API rate scale the API-rate frameSize
		// up to its 48 kHz core count.
		tocFrameSize := e.packetTOCFrameSize(frameSize)
		if e.dredEncodingActive() {
			if dredPacket, ok, dredErr := e.maybeBuildSingleFrameDREDPacket(frameData, actualMode, packetBW, frameSize, stereo, qextExtensions); dredErr != nil {
				return nil, dredErr
			} else if ok {
				packet = dredPacket
				dredPacketBuilt = true
			}
		}
		var (
			packetLen int
			pktErr    error
		)
		if packet == nil && len(qextPayload) > 0 {
			packetLen, pktErr = buildPacketWithSingleExtensionInto(
				e.scratchPacket,
				frameData,
				modeToTypes(actualMode),
				packetBW,
				tocFrameSize,
				stereo,
				qextExtensionID,
				qextPayload,
				0,
				false,
			)
		} else if packet == nil {
			targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
			if e.bitrateMode == ModeCBR && targetSize >= 2+len(frameData) {
				// A CBR packet is padded to cbr_bytes, which exceeds the
				// single-frame 1276 bytes above 510 kb/s at 20 ms
				// (opus_encode_frame_native pads to max_data_bytes).
				e.ensurePacketScratch(targetSize)
				if targetSize == 2+len(frameData) {
					config := configFromParams(modeToTypes(actualMode), packetBW, tocFrameSize)
					if config < 0 || len(e.scratchPacket) < targetSize {
						pktErr = ErrInvalidConfig
					} else {
						e.scratchPacket[0] = generateTOC(uint8(config), stereo, 3)
						e.scratchPacket[1] = 0x01
						copy(e.scratchPacket[2:], frameData)
						packetLen = targetSize
					}
				} else {
					packetLen, pktErr = buildPacketWithExtensionsInto(
						e.scratchPacket,
						frameData,
						modeToTypes(actualMode),
						packetBW,
						tocFrameSize,
						stereo,
						nil,
						targetSize,
						true,
					)
				}
			} else {
				packetLen, pktErr = BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), packetBW, tocFrameSize, stereo)
			}
		}
		if packet == nil && pktErr != nil {
			return nil, pktErr
		}
		if packet == nil {
			packet = e.scratchPacket[:packetLen]
		}
	}
	if isConcreteMode(actualMode) {
		e.prevPacketMode = actualMode
	}
	if isConcreteMode(prevModeNext) {
		e.prevMode = prevModeNext
		if e.mode == ModeAuto {
			e.prevAutoMode = prevModeNext
		}
	}
	switch e.bitrateMode {
	case ModeCBR:
		if dredPacketBuilt {
			break
		}
		targetSize := e.targetBytesForBitrate(int(e.bitrate), frameSize)
		if len(qextPayload) > 0 && len(packet) < targetSize {
			stereo := e.packetStereoForMode(actualMode)
			packetLen, pktErr := buildPacketWithSingleExtensionInto(
				e.scratchPacket,
				frameData,
				modeToTypes(actualMode),
				packetBW,
				e.packetTOCFrameSize(frameSize),
				stereo,
				qextExtensionID,
				qextPayload,
				targetSize,
				true,
			)
			if pktErr == nil {
				packet = e.scratchPacket[:packetLen]
			}
		} else {
			packet = padToSizeInto(e.scratchPacket, packet, targetSize)
		}
	case ModeCVBR:
		if !dredPacketBuilt && len(qextPayload) == 0 {
			packet = constrainSize(packet, e.targetBytesForBitrate(int(e.bitrate), frameSize), CVBRTolerance)
		}
	}
	e.prevChannels = e.streamChannels
	// C ref: opus_encode_native sets st->first = 0 here (line 2562), after a
	// frame is committed for every mode (auto, forced SILK/Hybrid/CELT). The
	// low-space and SILK nBytes==0 early returns happen earlier and leave
	// st->first = 1; gopus mirrors that by returning before this point for
	// those cases (emitLowSpacePacket / DTX-suppress set first explicitly).
	e.first = false
	switch {
	case multiFrame && e.multiFrameLastSubframeDTX:
		// libopus reports st->rangeFinal from the last opus_encode_frame_native
		// call in the repacketizer loop. When that final sub-frame DTXes it sets
		// rangeFinal = 0 (opus_encoder.c:2569), so a multi-frame packet whose last
		// internal sub-frame is suppressed has a final range of 0 even though its
		// earlier sub-frames carry payload.
		e.finalRange = 0
	default:
		e.finalRange = e.frameFinalRange
	}
	return packet, nil
}

// emitLowSpacePacket reproduces the libopus opus_encoder.c "too little space to
// do something useful" fast path (lines 1340-1406). When the per-frame byte
// budget or bitrate is too small to run a real encode, libopus emits a minimal
// TOC-only "PLC" packet (1 or 2 bytes), padding it to the CBR budget. The
// multistream encoder squeezes the trailing streams of a high-channel-count
// layout (e.g. third-order ambisonics) down to a 1-2 byte curr_max, which is
// exactly this path; without it gopus errored ("max_data_bytes <= 0") on streams
// that libopus emits as 1-byte minimal packets.
//
// effBitrate is st->bitrate_bps after the CBR cbr_bytes clamp and the DRED
// reservation; cbrMaxDataBytes is the CBR-clamped max_data_bytes (== outDataBytes
// for VBR). outDataBytes is the original caller budget (curr_max).
func (e *Encoder) emitLowSpacePacket(sampleRate, frameSize, outDataBytes, cbrMaxDataBytes, effBitrate int) ([]byte, error) {
	frameRate := sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 1
	}

	// tocmode = st->mode: the internal selected mode carried between frames, seeded
	// to MODE_HYBRID at init (opus_encoder.c:319). libopus maps an unset st->mode
	// (==0) to MODE_SILK_ONLY (opus_encoder.c:1349); intMode is always concrete here,
	// so that fallback is only defensive.
	tocmode := e.intMode
	if !isConcreteMode(tocmode) {
		tocmode = ModeSILK
	}
	if frameRate > 100 {
		tocmode = ModeCELT
	}

	// bw = st->bandwidth==0 ? NB : st->bandwidth (opus_encoder.c:1345). intBandwidth
	// mirrors st->bandwidth: seeded to FULLBAND at init (opus_encoder.c:320) and
	// updated only after a full encode, so the early-exit reads the same stale value
	// libopus would (OPUS_SET_BANDWIDTH writes user_bandwidth, not st->bandwidth).
	bw := max(e.intBandwidth, types.BandwidthNarrowband)

	packetCode := 0
	numMultiframes := 0

	// 40 ms -> 2 x 20 ms if in CELT_ONLY or HYBRID mode.
	if frameRate == 25 && tocmode != ModeSILK {
		frameRate = 50
		packetCode = 1
	}
	// >= 60 ms frames.
	if frameRate <= 16 {
		if outDataBytes == 1 || (tocmode == ModeSILK && frameRate != 10) {
			tocmode = ModeSILK
			if frameRate <= 12 {
				packetCode = 1
			} else {
				packetCode = 0
			}
			if frameRate == 12 {
				frameRate = 25
			} else {
				frameRate = 16
			}
		} else {
			numMultiframes = 50 / frameRate
			frameRate = 50
			packetCode = 3
		}
	}

	// Per-mode bandwidth clamps (libopus lines 1379-1384).
	switch {
	case tocmode == ModeSILK && bw > types.BandwidthWideband:
		bw = types.BandwidthWideband
	case tocmode == ModeCELT && bw == types.BandwidthMediumband:
		bw = types.BandwidthNarrowband
	case tocmode == ModeHybrid && bw <= types.BandwidthSuperwideband:
		bw = types.BandwidthSuperwideband
	}

	stereo := e.packetStereoForMode(tocmode)
	tocByte := lowSpaceTOC(tocmode, frameRate, bw, stereo)
	tocByte |= byte(packetCode)

	ret := 1
	if packetCode > 1 {
		ret = 2
	}
	// libopus pads to IMAX(cbr_max_data_bytes, ret) for CBR.
	padTarget := max(cbrMaxDataBytes, ret)

	e.ensurePacketScratch(padTarget)
	pkt := e.scratchPacket[:ret]
	pkt[0] = tocByte
	if packetCode == 3 {
		pkt[1] = byte(numMultiframes)
	}

	if e.bitrateMode != ModeCBR {
		return pkt, nil
	}
	// CBR: pad to the budget (opus_packet_pad).
	if padTarget <= ret {
		return pkt, nil
	}
	padded := padToSizeInto(e.scratchPacket, pkt, padTarget)
	return padded, nil
}

// lowSpaceTOC reproduces libopus gen_toc(mode, framerate, bandwidth, channels).
func lowSpaceTOC(mode Mode, framerate int, bw types.Bandwidth, stereo bool) byte {
	period := 0
	for framerate < 400 {
		framerate <<= 1
		period++
	}
	var toc byte
	switch mode {
	case ModeSILK:
		toc = byte((int(bw)-int(types.BandwidthNarrowband))<<5) | byte((period-2)<<3)
	case ModeCELT:
		tmp := max(int(bw)-int(types.BandwidthMediumband), 0)
		toc = 0x80 | byte(tmp<<5) | byte(period<<3)
	default: // Hybrid
		toc = 0x60 | byte((int(bw)-int(types.BandwidthSuperwideband))<<4) | byte((period-2)<<3)
	}
	if stereo {
		toc |= 1 << 2
	}
	return toc
}

// buildDTXPacket builds the TOC-only DTX packet of a frame in the mode
// selectMode picks, at the current bandwidth.
func (e *Encoder) buildDTXPacket(frameSize int) ([]byte, error) {
	actualMode := e.selectMode(frameSize, e.signalType)
	packetBW := e.effectiveBandwidth()
	if actualMode == ModeSILK {
		packetBW = min(packetBW, types.BandwidthWideband)
	}
	return e.buildDTXPacketForMode(frameSize, actualMode, packetBW)
}

// buildDTXPacketForMode assembles the TOC-only packet emitted when DTX fires,
// matching libopus opus_encoder.c, where DTX returns
//
//	data[0] = gen_toc(mode, Fs/frame_size, curr_bandwidth, channels);
//	return 1;
//
// packetBW is the frame's TOC bandwidth (curr_bandwidth). For SILK it is a
// single code-0 frame; for CELT/Hybrid frames longer than 20ms it builds N
// zero-length 20ms sub-frames so the repacketizer collapses them exactly as
// libopus does (code 1 for two sub-frames, code 3 for three). The decoder's
// comfort noise generation runs when it receives a TOC-only packet.
func (e *Encoder) buildDTXPacketForMode(frameSize int, actualMode Mode, packetBW types.Bandwidth) ([]byte, error) {
	stereo := e.packetStereoForMode(actualMode)
	mode := modeToTypes(actualMode)

	// CELT and Hybrid have no single-frame TOC config beyond 20 ms, so a 40/60 ms
	// (>20 ms) packet in those modes is assembled as N=frameSize/960 internal 20 ms
	// frames (encodeCELTMultiFramePacket / encodeHybridMultiFramePacket). When DTX
	// fires for such a frame, libopus' per-sub-frame encode returns tmp_len==1 for
	// every sub-frame and the repacketizer collapses them to a TOC-only packet:
	// code 1 for 2 sub-frames (1 byte) or code 3 with a frame-count byte for 3
	// sub-frames (2 bytes). Mirror that with a multi-frame TOC-only packet built
	// from N zero-length sub-frames at the 20 ms sub-frame config. SILK keeps the
	// single-frame path below because it has native 40/60 ms configs (code 0).
	f20 := e.frame20ms()
	if (mode == types.ModeCELT || mode == types.ModeHybrid) && frameSize > f20 && frameSize%f20 == 0 {
		frameCount := frameSize / f20
		e.resetPacketFrameScratch()
		frames := e.scratchFrameSlots[:0]
		for range frameCount {
			frames = append(frames, e.keepFrame(nil))
		}
		n, err := buildMultiFramePacketInto(e.scratchPacket, frames, mode, packetBW, 960, stereo, false)
		if err != nil {
			return nil, err
		}
		return e.scratchPacket[:n], nil
	}

	// Build TOC-only packet (no frame data) into scratch buffer.
	n, err := BuildPacketInto(e.scratchPacket, nil, mode, packetBW, e.packetTOCFrameSize(frameSize), stereo)
	if err != nil {
		return nil, err
	}
	return e.scratchPacket[:n], nil
}

// modeToTypes converts internal encoder Mode to types.Mode.
func modeToTypes(m Mode) types.Mode {
	switch m {
	case ModeSILK:
		return types.ModeSILK
	case ModeHybrid:
		return types.ModeHybrid
	case ModeCELT:
		return types.ModeCELT
	default:
		return types.ModeCELT
	}
}

func (e *Encoder) silkInternalChannels() int {
	if e.channels != 2 {
		return 1
	}
	streamChannels := e.streamChannels
	if streamChannels <= 0 {
		streamChannels = int32(e.channels)
	}
	if streamChannels <= 1 {
		return 1
	}
	return 2
}

func (e *Encoder) packetStereoForMode(mode Mode) bool {
	if e.channels != 2 {
		return false
	}
	switch mode {
	case ModeSILK:
		return e.silkInternalChannels() == 2
	case ModeHybrid, ModeCELT:
		return e.celtInternalChannelsForMode(mode) == 2
	}
	return true
}

func (e *Encoder) celtInternalChannelsForMode(mode Mode) int {
	if e.channels != 2 {
		return 1
	}
	streamChannels := e.streamChannels
	if streamChannels <= 0 {
		streamChannels = int32(e.channels)
	}
	if (mode == ModeCELT || mode == ModeHybrid) && streamChannels <= 1 {
		return 1
	}
	return 2
}

// preprocessInputHP applies the input high-pass stage that precedes SILK/CELT,
// matching src/opus_encoder.c: VoIP uses the adaptive hp_cutoff() biquad;
// other applications use the fixed 3 Hz dc_reject(), except that an enabled
// QEXT path copies the input directly. The hp_cutoff frequency follows the
// SILK variable-HP-cutoff smoother.
func (e *Encoder) preprocessInputHP(in []opusRes, frameSize int) []opusRes {
	return e.preprocessInputHPFrame(in, frameSize, e.mode, 0)
}

func (e *Encoder) preprocessInputHPFrame(in []opusRes, frameSize int, mode Mode, floatOffset int) []opusRes {
	cutoffHz := e.updateVariableHPCutoff(mode)
	if !e.voipApp {
		if extsupport.QEXT && e.qextActive() {
			return in
		}
		return e.dcRejectFrame(in, frameSize, floatOffset)
	}
	return e.hpCutoffFrameAtCutoff(in, frameSize, floatOffset, cutoffHz)
}

// hpCutoff applies the adaptive second-order high-pass filter used for VoIP
// input, ported from hp_cutoff() + silk_biquad_res() (float path) in
// src/opus_encoder.c. The cutoff frequency adapts from the SILK encoder's
// variable_HP_smth1_Q15 estimate, smoothed at the Opus level into
// variable_HP_smth2_Q15.
func (e *Encoder) hpCutoff(in []opusRes, frameSize int) []opusRes {
	cutoffHz := e.updateVariableHPCutoff(e.mode)
	return e.hpCutoffFrameAtCutoff(in, frameSize, 0, cutoffHz)
}

func (e *Encoder) updateVariableHPCutoff(mode Mode) int32 {
	// opus_encoder.c advances this smoother for every native frame, including
	// non-VoIP frames where the selected filter is dc_reject or QEXT bypass.
	var hpFreqSmth1 int32
	if e.silk != nil && mode != ModeCELT {
		hpFreqSmth1 = e.silk.VariableHPSmth1Q15()
	} else {
		hpFreqSmth1 = silk.MinCutoffLogSmth2Q15()
	}
	if !e.variableHPSmth2Inited {
		e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15()
		e.variableHPSmth2Inited = true
	}
	e.variableHPSmth2Q15 = silk.SmoothVariableHPSmth2Q15(e.variableHPSmth2Q15, hpFreqSmth1)
	return silk.VariableHPCutoffHz(e.variableHPSmth2Q15)
}

func (e *Encoder) hpCutoffFrameAtCutoff(in []opusRes, frameSize int, floatOffset int, cutoffHz int32) []opusRes {
	channels := int(e.channels)
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}

	bQ28, aQ28 := silk.HPCutoffCoefsQ28(cutoffHz, int32(fs))
	var b [3]float32
	var a [2]float32
	const inv28 = float32(1.0) / float32(int32(1)<<28)
	b[0] = float32(bQ28[0]) * inv28
	b[1] = float32(bQ28[1]) * inv28
	b[2] = float32(bQ28[2]) * inv28
	a[0] = float32(aQ28[0]) * inv28
	a[1] = float32(aQ28[1]) * inv28
	const verySmall = float32(1e-30)

	src32 := e.floatInputFrame
	if !e.floatInputExact || floatOffset < 0 || floatOffset+n > len(src32) {
		src32 = nil
	} else {
		src32 = src32[floatOffset : floatOffset+n]
	}

	// silk_biquad_res follows the selected target's contraction order. AMD64 v3
	// fuses vout and both S[0] terms, then rounds -vout*A[1] before fusing
	// B[2]*inval into S[1]. Keep that chain target-gated; older targets retain
	// their existing recurrence. VERY_SMALL is added to S[1] separately.
	// The src32 branch is
	// hoisted out of the inner loop, and stereo runs both channels' independent
	// recurrences in one interleaved pass so the OoO engine overlaps the two
	// latency-bound filter chains. Per-sample arithmetic is byte-identical to the
	// per-channel form (each channel's state depends only on its own history).
	if channels == 1 {
		s0 := e.hpMem[0]
		s1 := e.hpMem[1]
		if src32 != nil {
			for i := range frameSize {
				inval := src32[i]
				var vout float32
				if outerTargetV3FMA {
					vout = fma32(b[0], inval, s0)
					s0 = fma32(b[1], inval, fma32(-vout, a[0], s1))
					s1 = fma32(b[2], inval, round32(-vout*a[1])) + verySmall
				} else {
					vout = s0 + b[0]*inval
					s0 = s1 - vout*a[0] + b[1]*inval
					s1 = fma32(-vout, a[1], round32(b[2]*inval)) + verySmall
				}
				out[i] = opusRes(vout)
			}
		} else {
			for i := range frameSize {
				inval := float32(in[i])
				var vout float32
				if outerTargetV3FMA {
					vout = fma32(b[0], inval, s0)
					s0 = fma32(b[1], inval, fma32(-vout, a[0], s1))
					s1 = fma32(b[2], inval, round32(-vout*a[1])) + verySmall
				} else {
					vout = s0 + b[0]*inval
					s0 = s1 - vout*a[0] + b[1]*inval
					s1 = fma32(-vout, a[1], round32(b[2]*inval)) + verySmall
				}
				out[i] = opusRes(vout)
			}
		}
		e.hpMem[0] = s0
		e.hpMem[1] = s1
		return out
	}

	s0L := e.hpMem[0]
	s1L := e.hpMem[1]
	s0R := e.hpMem[2]
	s1R := e.hpMem[3]
	if src32 != nil {
		for i := range frameSize {
			l := src32[2*i]
			var voutL float32
			if outerTargetV3FMA {
				voutL = fma32(b[0], l, s0L)
				s0L = fma32(b[1], l, fma32(-voutL, a[0], s1L))
				s1L = fma32(b[2], l, round32(-voutL*a[1])) + verySmall
			} else {
				voutL = s0L + b[0]*l
				s0L = s1L - voutL*a[0] + b[1]*l
				s1L = fma32(-voutL, a[1], round32(b[2]*l)) + verySmall
			}
			out[2*i] = opusRes(voutL)
			r := src32[2*i+1]
			var voutR float32
			if outerTargetV3FMA {
				voutR = fma32(b[0], r, s0R)
				s0R = fma32(b[1], r, fma32(-voutR, a[0], s1R))
				s1R = fma32(b[2], r, round32(-voutR*a[1])) + verySmall
			} else {
				voutR = s0R + b[0]*r
				s0R = s1R - voutR*a[0] + b[1]*r
				s1R = fma32(-voutR, a[1], round32(b[2]*r)) + verySmall
			}
			out[2*i+1] = opusRes(voutR)
		}
	} else {
		for i := range frameSize {
			l := float32(in[2*i])
			var voutL float32
			if outerTargetV3FMA {
				voutL = fma32(b[0], l, s0L)
				s0L = fma32(b[1], l, fma32(-voutL, a[0], s1L))
				s1L = fma32(b[2], l, round32(-voutL*a[1])) + verySmall
			} else {
				voutL = s0L + b[0]*l
				s0L = s1L - voutL*a[0] + b[1]*l
				s1L = fma32(-voutL, a[1], round32(b[2]*l)) + verySmall
			}
			out[2*i] = opusRes(voutL)
			r := float32(in[2*i+1])
			var voutR float32
			if outerTargetV3FMA {
				voutR = fma32(b[0], r, s0R)
				s0R = fma32(b[1], r, fma32(-voutR, a[0], s1R))
				s1R = fma32(b[2], r, round32(-voutR*a[1])) + verySmall
			} else {
				voutR = s0R + b[0]*r
				s0R = s1R - voutR*a[0] + b[1]*r
				s1R = fma32(-voutR, a[1], round32(b[2]*r)) + verySmall
			}
			out[2*i+1] = opusRes(voutR)
		}
	}
	e.hpMem[0] = s0L
	e.hpMem[1] = s1L
	e.hpMem[2] = s0R
	e.hpMem[3] = s1R
	return out
}

// dcRejectFrame applies a first-order DC rejection filter at 3 Hz.
func (e *Encoder) dcRejectFrame(in []opusRes, frameSize int, floatOffset int) []opusRes {
	channels := int(e.channels)
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	coef := float32(6.3) * float32(3) / float32(fs)
	coef2 := float32(1.0) - coef
	src := in
	if e.floatInputExact && floatOffset >= 0 && floatOffset+n <= len(e.floatInputFrame) {
		src = e.floatInputFrame[floatOffset : floatOffset+n]
	} else {
		src = src[:n]
	}
	out = out[:len(src)]
	verySmall := dcRejectVerySmall[0]
	if channels == 2 {
		m0 := e.hpMem[0]
		m2 := e.hpMem[2]
		k := 0
		for ; k+3 < len(src); k += 4 {
			s := src[k : k+4 : k+4]
			o := out[k : k+4 : k+4]
			x0, x1, x2, x3 := s[0], s[1], s[2], s[3]
			// Precompute coef*x + verySmall in parallel so the two independent
			// stereo IIR chains depend on a single FMA per sample.
			c0 := coef*x0 + verySmall
			c1 := coef*x1 + verySmall
			c2 := coef*x2 + verySmall
			c3 := coef*x3 + verySmall
			o[0] = x0 - m0
			o[1] = x1 - m2
			m0 = c0 + coef2*m0
			m2 = c1 + coef2*m2
			o[2] = x2 - m0
			o[3] = x3 - m2
			m0 = c2 + coef2*m0
			m2 = c3 + coef2*m2
		}
		for ; k+1 < len(src); k += 2 {
			x0 := src[k]
			x1 := src[k+1]
			out[k] = x0 - m0
			out[k+1] = x1 - m2
			m0 = coef*x0 + verySmall + coef2*m0
			m2 = coef*x1 + verySmall + coef2*m2
		}
		e.hpMem[0] = m0
		e.hpMem[2] = m2
	} else {
		m0 := e.hpMem[0]
		i := 0
		for ; i+3 < len(src); i += 4 {
			s := src[i : i+4 : i+4]
			o := out[i : i+4 : i+4]
			x0, x1, x2, x3 := s[0], s[1], s[2], s[3]
			c0 := coef*x0 + verySmall
			c1 := coef*x1 + verySmall
			c2 := coef*x2 + verySmall
			c3 := coef*x3 + verySmall
			o[0] = x0 - m0
			m0 = c0 + coef2*m0
			o[1] = x1 - m0
			m0 = c1 + coef2*m0
			o[2] = x2 - m0
			m0 = c2 + coef2*m0
			o[3] = x3 - m0
			m0 = c3 + coef2*m0
		}
		for ; i < len(src); i++ {
			x := src[i]
			out[i] = x - m0
			m0 = coef*x + verySmall + coef2*m0
		}
		e.hpMem[0] = m0
	}
	return out
}

// dcRejectVerySmall holds libopus VERY_SMALL as a variable, so the dc_reject
// loops keep it in a register instead of reloading the constant every sample.
var dcRejectVerySmall = [1]float32{1e-30}

func (e *Encoder) ensureDCPCM(size int) []opusRes {
	if cap(e.scratchDCPCM) < size {
		e.scratchDCPCM = make([]opusRes, size)
	}
	return e.scratchDCPCM[:size]
}

func trimSilkTrailingZeros(frameData []byte) []byte {
	for len(frameData) > 2 && frameData[len(frameData)-1] == 0 {
		frameData = frameData[:len(frameData)-1]
	}
	return frameData
}

func (e *Encoder) refreshFrameAnalysisF32(pcm32 []float32, frameSize int) {
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	e.analysisReadBakSet = false
	if e.analyzer == nil || frameSize <= 0 || len(pcm32) == 0 {
		return
	}
	if !e.analysisEnabled() {
		if e.analyzer.Initialized {
			e.analyzer.Reset()
		}
		return
	}
	// Mirror libopus opus_encoder.c: back up analysis read cursor before
	// run_analysis() so long packets can consume per-subframe info later.
	e.analysisReadPosBak = e.analyzer.ReadPos
	e.analysisSubframeBak = e.analyzer.ReadSubframe
	e.analysisReadBakSet = true
	// Keep analysis on float-domain samples to match opus_encode_float / opus_demo -f32.
	info := e.analyzer.RunAnalysis(pcm32, frameSize, int(e.channels))
	if !info.Valid {
		return
	}
	e.lastAnalysisInfo = info
	e.lastAnalysisValid = true
	e.lastAnalysisFresh = true
}

func (e *Encoder) analysisEnabled() bool {
	// Match src/opus_encoder.c opus_encode_native(): fixed-point builds require
	// complexity 10; float builds require complexity 7.
	return !e.restrictedSilkApp && e.complexity >= 7 && (!fixedPointBuild || e.complexity >= 10) && e.sampleRate >= 16000 && e.sampleRate <= 48000
}

// primeSubframeAnalysis advances tonality_get_info() for long packets and keeps
// a reusable per-subframe analysis snapshot for downstream VAD/CELT decisions.
func (e *Encoder) primeSubframeAnalysis(frameSize int) {
	if !e.analysisReadBakSet || e.analyzer == nil {
		return
	}
	info := e.analyzer.tonalityGetInfo(frameSize)
	if info.Valid {
		e.lastAnalysisInfo = info
		e.lastAnalysisValid = true
		e.lastAnalysisFresh = true
		return
	}
	// Keep the last valid snapshot to avoid forcing a fallback RunAnalysis()
	// mid-packet when tonality_get_info has insufficient lookahead.
	if e.lastAnalysisValid {
		e.lastAnalysisFresh = true
	}
}

func (e *Encoder) syncCELTAnalysisToCELT() {
	if e.celtEncoder == nil {
		return
	}
	if !e.lastAnalysisValid {
		e.celtEncoder.SetAnalysisInfoWithTonality(0, [19]uint8{}, 0, 0, 0, 0, false)
		return
	}
	e.celtEncoder.SetAnalysisInfoWithTonality(
		int(e.lastAnalysisInfo.BandwidthIndex),
		e.lastAnalysisInfo.LeakBoost,
		e.lastAnalysisInfo.Activity,
		e.lastAnalysisInfo.Tonality,
		e.lastAnalysisInfo.TonalitySlope,
		e.lastAnalysisInfo.MaxPitchRatio,
		true,
	)
}

func quantizeFloat32ToInt16LibopusInPlace(samples []float32) {
	const invScale = float32(1.0 / 32768.0)
	for i, v := range samples {
		samples[i] = float32(opusmath.Float32ToInt16(v)) * invScale
	}
}

func downmixStereoToSilkMonoLibopus(dst, interleaved []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := range samples {
		sum := float32ToInt16Libopus(interleaved[2*i] + interleaved[2*i+1])
		dst[i] = float32(silkRShiftRound1(sum)) * invScale
	}
}

func averageSilkResamplerOutputsLibopus(dst, right []float32, samples int) {
	const invScale = float32(1.0 / 32768.0)
	for i := range samples {
		leftQ0 := float32ToInt16Libopus(dst[i])
		rightQ0 := float32ToInt16Libopus(right[i])
		dst[i] = float32((leftQ0+rightQ0)>>1) * invScale
	}
}

func float32ToInt16Libopus(v float32) int32 {
	return int32(opusmath.Float32ToInt16(v))
}

func silkRShiftRound1(v int32) int32 {
	return (v >> 1) + (v & 1)
}

func (e *Encoder) ensureDelayedPCM(size int) []opusRes {
	if cap(e.scratchDelayedPCM) < size {
		e.scratchDelayedPCM = make([]opusRes, size)
	}
	return e.scratchDelayedPCM[:size]
}

func (e *Encoder) ensureTransitionPrefill(size int) []opusRes {
	if cap(e.scratchTransitionPrefill) < size {
		e.scratchTransitionPrefill = make([]opusRes, size)
	}
	return e.scratchTransitionPrefill[:size]
}

func (e *Encoder) ensureSilkPrefill(size int) []opusRes {
	if cap(e.scratchSilkPrefill) < size {
		e.scratchSilkPrefill = make([]opusRes, size)
	}
	return e.scratchSilkPrefill[:size]
}

func (e *Encoder) ensureCELTPrefill(size int) []opusRes {
	if cap(e.scratchCELTPrefill) < size {
		e.scratchCELTPrefill = make([]opusRes, size)
	}
	return e.scratchCELTPrefill[:size]
}

// delayCompensatedPCM returns pcm_buf of opus_encode_frame_native: the Fs/250
// samples of delay history followed by the start of the frame, the CELT input.
// It leaves the delay buffer as it is and records the CELT transition prefill
// window, libopus tmp_prefill.
func (e *Encoder) delayCompensatedPCM(pcm []opusRes, frameSize int) []opusRes {
	channels := max(int(e.channels), 1)
	frameSamples := min(len(pcm), frameSize*channels)
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm[:frameSamples])
		return out
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	if delaySamples <= 0 || frameSamples <= 0 {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm[:frameSamples])
		return out
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]opusRes, encoderBufferSamples)
	}

	tailStart := encoderBufferSamples - delaySamples

	// Preserve the libopus delay-history snapshot window used by CELT transition prefill:
	// delay_buffer[encoder_buffer-delay_comp-Fs/400 : +Fs/400].
	prefillFrameSize := sampleRate / 400
	prefillSamples := prefillFrameSize * channels
	prefillStart := encoderBufferSamples - delaySamples - prefillSamples
	if prefillSamples > 0 && prefillStart >= 0 && prefillStart+prefillSamples <= len(e.delayBuffer) {
		prefill := e.ensureTransitionPrefill(prefillSamples)
		copy(prefill, e.delayBuffer[prefillStart:prefillStart+prefillSamples])
	} else {
		e.scratchTransitionPrefill = e.scratchTransitionPrefill[:0]
	}

	out := e.ensureDelayedPCM(frameSize * channels)
	if frameSamples <= delaySamples {
		copy(out, e.delayBuffer[tailStart:tailStart+frameSamples])
		clear(out[frameSamples:])
	} else {
		copy(out, e.delayBuffer[tailStart:])
		copy(out[delaySamples:], pcm[:frameSamples-delaySamples])
		clear(out[frameSamples:])
	}
	return out
}

// celtTransitionPrefillSource returns libopus tmp_prefill: the Fs/400 samples of
// delay history that precede this frame's CELT input,
// delay_buffer[encoder_buffer-total_buffer-Fs/400 : +Fs/400], copied before the
// delay buffer shifts (src/opus_encoder.c:2297-2301). When this frame ran a SILK
// transition prefill, that prefill's onset ramp has rewritten the same window.
func (e *Encoder) celtTransitionPrefillSource(prefillSamples int) []opusRes {
	src := e.scratchTransitionPrefill
	if e.hasCELTPrefill {
		src = e.scratchCELTPrefill
	}
	if prefillSamples <= 0 || len(src) < prefillSamples {
		return nil
	}
	return src[:prefillSamples]
}

// maybePrefillSILKOnModeTransition stages the SILK prefill of a frame
// (src/opus_encoder.c:1576-1581 and 2191-2209): the prefill of a switch from
// CELT to SILK or Hybrid, where on the first frame of the packet (initSILK) the
// SILK encoder is re-initialized (silk_InitEncoder), and the prefill that
// starts the first frame at a new SILK internal bandwidth (silk_bw_switch). The
// 10 ms of delay history, with the onset ramp that avoids coding a
// discontinuity, is kept for the silk_Encode prefill call that runs once the
// frame's SILK controls are set; captureCELTPrefill also records the part of it
// the CELT transition prefill reads. It runs before the frame advances the
// delay buffer.
func (e *Encoder) maybePrefillSILKOnModeTransition(actualMode Mode, initSILK, captureCELTPrefill bool) {
	fromCELT := e.shouldPrefillSILKOnModeTransition(actualMode)
	bwSwitch := e.silkBWSwitch && actualMode != ModeCELT && !e.lowDelay && !e.restrictedSilkApp
	if !fromCELT && !bwSwitch {
		return
	}
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	// libopus prefill uses encoder_buffer = Fs/100 samples of delay history.
	prefillFrameSize := sampleRate / 100
	prefillSamples := prefillFrameSize * channels
	prefill := e.ensureSilkPrefill(prefillSamples)
	clear(prefill)
	if len(e.delayBuffer) >= prefillSamples {
		copy(prefill, e.delayBuffer[:prefillSamples])
	} else if len(e.delayBuffer) > 0 {
		copy(prefill[prefillSamples-len(e.delayBuffer):], e.delayBuffer)
	}
	e.applySilkTransitionPrefillRamp(prefill, prefillFrameSize)
	e.stageFixedSILKPrefill(captureCELTPrefill)

	if captureCELTPrefill {
		// CELT mode-transition prefill consumes this exact history slice in libopus:
		// delay_buffer[encoder_buffer-delay_comp-Fs/400 : +Fs/400].
		prefillLen := sampleRate / 400
		delayComp := sampleRate / 250
		prefillOffset := prefillFrameSize - delayComp - prefillLen
		celtPrefillSamples := prefillLen * channels
		if prefillLen > 0 && prefillOffset >= 0 && celtPrefillSamples > 0 {
			start := prefillOffset * channels
			end := start + celtPrefillSamples
			if start >= 0 && end <= len(prefill) {
				out := e.ensureCELTPrefill(celtPrefillSamples)
				copy(out, prefill[start:end])
				e.hasCELTPrefill = true
			}
		}
	}

	e.ensureSILKEncoder()
	if fromCELT && initSILK {
		e.silk.Init()
	}
	e.silkPrefillPending = true
}

func (e *Encoder) shouldPrefillSILKOnModeTransition(actualMode Mode) bool {
	if actualMode == ModeCELT || e.lowDelay {
		return false
	}
	prev := e.prevMode
	if !isConcreteMode(prev) || prev != ModeCELT {
		return false
	}
	if e.channels < 1 || e.sampleRate <= 0 {
		return false
	}
	return true
}

// runPendingSILKPrefill runs the staged prefill through silk_Encode with the
// frame's controls and no range coder: prefill 1 after CELT, or 2 at a new
// SILK bandwidth, which keeps the variable LP filter state. The real encode of
// the frame then cannot switch the bandwidth again (opusCanSwitch = 0).
func (e *Encoder) runPendingSILKPrefill(prefill, activity int) error {
	if prefill == 0 {
		return nil
	}
	e.silkPrefillPending = false
	err := e.encodeSILKPrefill(prefill, activity)
	e.silkMode.OpusCanSwitch = false
	return err
}

func (e *Encoder) applySilkTransitionPrefillRamp(prefill []opusRes, prefillFrameSize int) {
	if len(prefill) == 0 || prefillFrameSize <= 0 {
		return
	}
	channels := max(int(e.channels), 1)
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	prefillLen := sampleRate / 400
	start := min(max(prefillFrameSize-delayComp-prefillLen, 0), prefillFrameSize)

	prefix := min(start*channels, len(prefill))
	for i := range prefix {
		prefill[i] = 0
	}
	if prefillLen <= 0 {
		return
	}
	if start+prefillLen > prefillFrameSize {
		prefillLen = prefillFrameSize - start
	}
	if prefillLen <= 0 {
		return
	}

	inc := max(48000/sampleRate, 1)
	window := celt.GetWindowBufferF32(prefillLen * inc)
	if len(window) > 0 {
		if maxByWindow := len(window) / inc; maxByWindow < prefillLen {
			prefillLen = maxByWindow
		}
	}
	if prefillLen <= 0 {
		return
	}

	if len(window) == 0 {
		den := opusVal16(prefillLen)
		if den < 1 {
			den = 1
		}
		for i := 0; i < prefillLen; i++ {
			g := opusVal16(i) / den
			base := (start + i) * channels
			for c := 0; c < channels && base+c < len(prefill); c++ {
				prefill[base+c] *= g
			}
		}
		return
	}

	for i := 0; i < prefillLen; i++ {
		w := window[i*inc]
		g := w * w
		base := (start + i) * channels
		for c := 0; c < channels && base+c < len(prefill); c++ {
			prefill[base+c] *= g
		}
	}
}

// updateDelayBuffer advances the delay buffer by the frame, as
// opus_encode_frame_native does once the SILK layer has coded it.
func (e *Encoder) updateDelayBuffer(pcm []opusRes, frameSize int) {
	sampleRate := int(e.sampleRate)
	delayComp := sampleRate / 250
	if delayComp <= 0 {
		return
	}
	channels := max(int(e.channels), 1)
	delaySamples := delayComp * channels
	encoderBufferSamples := (sampleRate / 100) * channels
	frameSamples := min(len(pcm), frameSize*channels)
	if delaySamples <= 0 || frameSamples <= 0 {
		return
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]opusRes, encoderBufferSamples)
	}
	e.updateDelayBufferInternal(pcm, frameSamples, encoderBufferSamples)
}

func (e *Encoder) updateDelayBufferInternal(pcm []opusRes, frameSamples, encoderBufferSamples int) {
	if frameSamples <= 0 || encoderBufferSamples <= 0 {
		return
	}
	if frameSamples >= encoderBufferSamples {
		copy(e.delayBuffer, pcm[frameSamples-encoderBufferSamples:frameSamples])
		return
	}

	keep := encoderBufferSamples - frameSamples
	copy(e.delayBuffer[:keep], e.delayBuffer[frameSamples:frameSamples+keep])
	copy(e.delayBuffer[keep:], pcm[:frameSamples])
}
