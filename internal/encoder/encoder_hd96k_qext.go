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
	if n, handled, err := e.encodeNativeHD96kFixed(pcm, frameSize, dst); handled || err != nil {
		return n, err
	}

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
	packetCap := libopusMaxDataBytesCap
	if e.qextActive() {
		packetCap = hd96kQEXTPacketSizeCap
	}
	maxDataBytes := min(len(dst), packetCap*6)
	if !useVBR {
		cbrBytes := min((bitrateToBitsFs(int(e.bitrate), 96000, frameSize)+4)/8, maxDataBytes)
		maxDataBytes = max(1, cbrBytes)
	}
	maxPayloadBytes := maxDataBytes - 1 // opus_encode_frame_native reserves the TOC byte.
	if maxPayloadBytes < 2 {
		return 0, ErrInvalidConfig
	}
	ce.SetMaxPayloadBytes(maxPayloadBytes)

	mainPayload, err := ce.EncodeFrame(pcm, frameSize)
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

	return assembleHD96kPacket(dst, frameSize, mainPayload, qextPayload, channels == 2)
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
