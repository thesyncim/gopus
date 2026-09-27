//go:build gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/celt"

// Native 96 kHz (Opus HD / QEXT) top-level packet framing.
//
// At Fs=96000 libopus runs CELT-only fullband frames (configs 28–31, 2.5–20 ms /
// 240–1920 samples) and, when QEXT is enabled at runtime, carries the >20 kHz extension-band data in a reserved QEXT
// extension that rides inside the Opus padding region. The packet layout is
// produced by celt_encode_with_ec() itself (celt/celt_encoder.c lines
// 2562-2581 under ENABLE_QEXT):
//
//	TOC byte                 config<<3 | stereo<<2 | 0x03  (code 3)
//	frame-count byte         0x41 (padding flag 0x40 | 1 frame)
//	padding-length bytes     (qext_bytes+253)/254 bytes; first n-1 are 255,
//	                         last is qext_bytes%254 (or 254 when divisible)
//	main CELT payload        new_compressedBytes
//	extension-ID byte        QEXT_EXTENSION_ID<<1 = 124<<1 = 0xF8
//	QEXT payload             qext_bytes-1 bytes
//
// where qext_bytes is the byte count of the whole extension region (the 0xF8
// ID byte plus the qext payload). gopus produces the main CELT payload and the
// QEXT payload (the bytes after the 0xF8 ID) at the CELT layer
// (celt.Encoder.EnableHD96kMode + EncodeFrame at frameSize=1920); this layer
// assembles them into the final Opus packet byte-for-byte.

const hd96kFrameSize = 1920
const hd96kQEXTPacketSizeCap = 3825

// hd96kQEXTExtIDByte is QEXT_EXTENSION_ID<<1 (124<<1), the extension-ID byte
// that precedes the QEXT payload in the padding region.
const hd96kQEXTExtIDByte = byte(qextExtensionID << 1)

// EncodeNativeHD96k encodes one native 96 kHz CELT-only fullband frame and
// assembles the complete Opus packet (TOC + frame-count + padding-length +
// main CELT payload + QEXT extension). It mirrors the libopus --enable-qext
// Fs=96000 encode path. pcm holds frameSize*channels interleaved float samples
// at 96 kHz; frameSize must be 240, 480, 960, or 1920 samples per channel.
// dst receives the packet.
//
// The CELT main payload and QEXT payload are produced by the native HD96k CELT
// encode; this routine owns only the top-level Opus framing of those payloads.
func (e *Encoder) EncodeNativeHD96k(pcm []float32, frameSize int, dst []byte) (int, error) {
	if !validHD96kFrameSize(frameSize) {
		return 0, ErrInvalidFrameSize
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels {
		return 0, ErrInvalidFrameSize
	}
	if len(dst) < 3 {
		return 0, ErrInvalidConfig
	}
	packetCap := libopusMaxDataBytesCap
	if e.qextActive() {
		packetCap = hd96kQEXTPacketSizeCap
	}
	maxDataBytes := min(len(dst), packetCap*6)
	userBitrate := e.bitrate
	e.updateHD96kStreamChannels(frameSize, maxDataBytes)
	defer func() { e.bitrate = userBitrate }()
	if n, handled, err := e.encodeNativeHD96kFixed(pcm, frameSize, dst); handled || err != nil {
		return n, err
	}

	e.ensureCELTEncoder()
	ce := e.celtEncoder
	if !ce.HD96kEncodeEnabled() {
		ce.EnableHD96kMode()
	}
	ce.SetQEXTEnabled(e.qextActive())
	ce.SetStreamChannels(int(e.streamChannels))
	ce.SetBandwidth(celt.CELTFullband)
	ce.SetHybrid(false)
	ce.SetTopLevelDelayCompensatedInput(true)
	ce.SetDCRejectEnabled(false)
	ce.SetLSBQuantizationEnabled(false)
	ce.SetDelayCompensationEnabled(false)
	ce.SetLSBDepth(int(e.lsbDepth))
	// Every CELT frame sets the prediction (src/opus_encoder.c:2288-2295).
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
	if !useVBR {
		cbrBytes := min((bitrateToBitsFs(int(e.bitrate), 96000, frameSize)+4)/8, maxDataBytes)
		maxDataBytes = max(1, cbrBytes)
	}
	maxPayloadBytes := maxDataBytes - 1 // opus_encode_frame_native reserves the TOC byte.
	if !e.qextActive() {
		maxPayloadBytes = min(maxPayloadBytes, 1275)
	}
	if maxPayloadBytes < 2 {
		return 0, ErrInvalidConfig
	}
	ce.SetMaxPayloadBytes(maxPayloadBytes)

	inputPCM := pcm
	if !e.qextActive() {
		inputPCM = e.dcRejectHD96kFloat(pcm, frameSize)
	}
	framePCM := e.prepareHD96kFloatPCM(inputPCM, frameSize)
	equivRate := e.computeEquivRate(e.bitrate, e.streamChannels, int32(96000/frameSize),
		e.bitrateMode != ModeCBR, ModeCELT, e.complexity, e.packetLoss)
	e.applyHD96kFloatStereoWidth(framePCM, equivRate)
	mainPayload, err := ce.EncodeFrame(framePCM, frameSize)
	if err != nil {
		return 0, err
	}
	qextPayload := ce.LastQEXTPayload()
	// src/opus_encoder.c reads the CELT encoder's final range after the native
	// frame is encoded. QEXT's CELT range combines the main and side coders.
	e.frameFinalRange = ce.FinalRange()
	e.finalRange = e.frameFinalRange

	// C ref: opus_encode_native clears st->first once a frame is committed.
	// The native 96 kHz CELT path always produces a frame, so mark it coded.
	e.first = false

	return assembleHD96kPacket(dst, frameSize, mainPayload, qextPayload, e.streamChannels == 2)
}

// updateHD96kStreamChannels mirrors opus_encode_native's rate-dependent
// mono/stereo selection before the native CELT frame is encoded. The native
// 96 kHz route bypasses the regular frame driver, so it resolves the same
// caller-budgeted bitrate and CBR byte rounding locally before using the
// shared stream-channel decision.
func (e *Encoder) updateHD96kStreamChannels(frameSize, maxDataBytes int) {
	effectiveBitrate := resolveUserBitrate(int(e.bitrate), 96000, int(e.channels), frameSize, maxDataBytes)
	if e.bitrateMode == ModeCBR {
		cbrBytes := min((bitrateToBitsFs(effectiveBitrate, 96000, frameSize)+4)/8, maxDataBytes)
		cbrBytes = max(1, cbrBytes)
		effectiveBitrate = bitsToBitrateFs(cbrBytes*8, 96000, frameSize)
	}
	e.bitrate = int32(effectiveBitrate)
	e.updateStreamChannelsForFrame(frameSize)
}

// hd96kStereoWidthQ14 matches opus_encoder.c's CELT-only stereo-width target
// from the frame's equivalent rate. The high-rate native route bypasses the
// shared frame driver, so it applies this source control before CELT itself.
func hd96kStereoWidthQ14(equivRate int32) int16 {
	switch {
	case equivRate > 32000:
		return 1 << 14
	case equivRate < 16000:
		return 0
	default:
		return int16((1 << 14) - 2048*(32000-equivRate)/(equivRate-14000))
	}
}

// prepareHD96kFloatPCM builds opus_encode_frame_native's delayed pcm_buf for
// the native 96 kHz mode and advances its 10 ms history before any CELT fades.
func (e *Encoder) prepareHD96kFloatPCM(pcm []float32, frameSize int) []opusRes {
	channels := int(e.channels)
	frameSamples := frameSize * channels
	if e.lowDelay {
		out := e.ensureDelayedPCM(frameSamples)
		copy(out, pcm)
		return out
	}
	const encoderBuffer = 960
	const delayCompensation = 384
	bufferSamples := encoderBuffer * channels
	if len(e.delayBuffer) != bufferSamples {
		e.delayBuffer = make([]opusRes, bufferSamples)
	}
	pcmBuf := e.ensureInputPCM((frameSize + delayCompensation) * channels)
	copy(pcmBuf[:delayCompensation*channels], e.delayBuffer[(encoderBuffer-delayCompensation)*channels:])
	copy(pcmBuf[delayCompensation*channels:], pcm)
	if encoderBuffer-frameSize-delayCompensation > 0 {
		keep := (encoderBuffer - frameSize - delayCompensation) * channels
		copy(e.delayBuffer[:keep], e.delayBuffer[frameSize*channels:frameSize*channels+keep])
		copy(e.delayBuffer[keep:], pcmBuf)
	} else {
		start := (frameSize + delayCompensation - encoderBuffer) * channels
		copy(e.delayBuffer, pcmBuf[start:start+bufferSamples])
	}
	return pcmBuf[:frameSamples]
}

// dcRejectHD96kFloat ports the float opus_encoder.c:dc_reject branch at Fs=96
// kHz. QEXT bypasses this filter in the caller and leaves hp_mem unchanged.
func (e *Encoder) dcRejectHD96kFloat(in []float32, frameSize int) []float32 {
	channels := int(e.channels)
	out := e.ensureDCPCM(frameSize * channels)
	coef := round32(round32(float32(6.3)*float32(3)) / float32(96000))
	coef2 := round32(float32(1) - coef)
	const verySmall = float32(1e-30)
	if channels == 2 {
		m0, m2 := e.hpMem[0], e.hpMem[2]
		for i := range frameSize {
			x0, x1 := in[2*i], in[2*i+1]
			out[2*i], out[2*i+1] = x0-m0, x1-m2
			// opus_encoder.c's float expression contracts each multiply-add on
			// targets with FMA: coef*x + VERY_SMALL, then + coef2*mem.
			m0 = fma32(coef2, m0, fma32(coef, x0, verySmall))
			m2 = fma32(coef2, m2, fma32(coef, x1, verySmall))
		}
		e.hpMem[0], e.hpMem[2] = m0, m2
	} else {
		m0 := e.hpMem[0]
		for i := range frameSize {
			x := in[i]
			out[i] = x - m0
			m0 = fma32(coef2, m0, fma32(coef, x, verySmall))
		}
		e.hpMem[0] = m0
	}
	return out
}

// applyHD96kFloatStereoWidth applies the native mode's 240-sample window and
// the CELT-only width target from opus_encoder.c:2320-2348.
func (e *Encoder) applyHD96kFloatStereoWidth(pcm []opusRes, equivRate int32) {
	width := hd96kStereoWidthQ14(equivRate)
	e.silkMode.StereoWidthQ14 = int32(width)
	if e.channels != 2 || len(e.celtEnergyMask) != 0 ||
		(e.hybridStereoWidthQ14 >= 1<<14 && width >= 1<<14) {
		return
	}
	prev := e.hybridStereoWidthQ14
	if !e.restrictedSilkApp {
		window := celt.GetWindowBufferF32(240)
		g1 := 1 - opusVal16(prev)*(1.0/16384)
		g2 := 1 - opusVal16(width)*(1.0/16384)
		frameSize := len(pcm) / 2
		for i := 0; i < frameSize; i++ {
			g := g2
			if i < len(window) {
				w := round32(window[i] * window[i])
				g = fma32(w, g2, round32(round32(1-w)*g1))
			}
			diff := round32(0.5 * (pcm[i*2] - pcm[i*2+1]))
			diff = round32(g * diff)
			pcm[i*2] -= diff
			pcm[i*2+1] += diff
		}
	}
	e.hybridStereoWidthQ14 = width
}

func validHD96kFrameSize(frameSize int) bool {
	return frameSize == 240 || frameSize == 480 || frameSize == 960 || frameSize == hd96kFrameSize
}

// assembleHD96kPacket lays out the native 96 kHz CELT-only fullband Opus
// packet from the main CELT payload and the QEXT extension payload, matching
// the libopus celt_encode_with_ec() byte layout exactly.
//
// When qextPayload is empty (QEXT not reserved for this frame) the packet uses
// code 0 (single frame, no padding) so the framing degrades to a plain CELT FB
// packet, exactly as libopus does when qext_bytes <= 20.
func assembleHD96kPacket(dst []byte, frameSize int, mainPayload, qextPayload []byte, stereo bool) (int, error) {
	// CELT-only fullband configs map 2.5/5/10/20 ms to 28/29/30/31.
	config := 28
	for size := 240; size < frameSize; size <<= 1 {
		config++
	}
	toc := byte(config << 3)
	if stereo {
		toc |= 0x04
	}

	if len(qextPayload) == 0 {
		// Plain CELT FB packet, code 0.
		need := 1 + len(mainPayload)
		if len(dst) < need {
			return 0, ErrInvalidConfig
		}
		dst[0] = toc // code 0
		copy(dst[1:], mainPayload)
		return need, nil
	}

	// qext_bytes is the size of the whole extension region: the 0xF8 ID byte
	// plus the QEXT payload bytes.
	qextBytes := 1 + len(qextPayload)
	paddingLenBytes := (qextBytes + 253) / 254

	need := 1 + 1 + paddingLenBytes + len(mainPayload) + qextBytes
	if len(dst) < need {
		return 0, ErrInvalidConfig
	}

	pos := 0
	dst[pos] = toc | 0x03 // code 3
	pos++
	dst[pos] = 0x41 // padding flag (0x40) | 1 frame (0x01)
	pos++

	// Padding-length field: first paddingLenBytes-1 bytes are 255, last byte
	// is qext_bytes%254 (or 254 when an exact multiple of 254).
	for i := 0; i < paddingLenBytes-1; i++ {
		dst[pos] = 255
		pos++
	}
	rem := qextBytes % 254
	if rem == 0 {
		rem = 254
	}
	dst[pos] = byte(rem)
	pos++

	copy(dst[pos:], mainPayload)
	pos += len(mainPayload)

	dst[pos] = hd96kQEXTExtIDByte
	pos++
	copy(dst[pos:], qextPayload)
	pos += len(qextPayload)

	return pos, nil
}
