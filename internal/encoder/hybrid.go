// Package encoder implements hybrid mode encoding for the unified Opus encoder.
// This file contains the hybrid mode encoding logic that coordinates SILK and CELT.
//
// Per RFC 6716 Section 3.2.1:
// - SILK encodes FIRST, CELT encodes SECOND (order matters!)
// - SILK operates at WB (16kHz) - downsample input from 48kHz
// - CELT encodes bands 17-21 only (8-20kHz) - use hybrid mode
// - CELT input is delay-compensated (Fs/250 = 192 samples at 48kHz) in the caller
//
// Key improvements implemented from libopus reference:
// - Proper SILK/CELT bit allocation using rate tables with TOC overhead correction
// - HB_gain for high-band attenuation when CELT is under-allocated
// - gain_fade for smooth transitions between frames (in-place, no extra delay)
// - Libopus-matching downsampler (AR2+FIR) for 48kHz to 16kHz
// - Energy matching between SILK and CELT at crossover
// - VBR constraint always disabled for CELT in hybrid mode (per libopus)
//
// Reference: RFC 6716 Section 3.2, libopus src/opus_encoder.c

package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
)

const (
	// maxHybridPacketSize is the maximum packet size for hybrid mode.
	maxHybridPacketSize = 1275

	// hybridOverlap is the overlap size for gain fading (matches CELT overlap).
	// 120 samples at 48kHz = 2.5ms.
	hybridOverlap = 120
)

// hybridRateTable contains SILK bitrate allocation for hybrid mode.
// This matches libopus compute_silk_rate_for_hybrid() rate_table.
// Format: [total bitrate, 10ms no FEC, 20ms no FEC, 10ms FEC, 20ms FEC]
// All values are per-channel bitrates.
var hybridRateTable = [][]int{
	{0, 0, 0, 0, 0},
	{12000, 10000, 10000, 11000, 11000},
	{16000, 13500, 13500, 15000, 15000},
	{20000, 16000, 16000, 18000, 18000},
	{24000, 18000, 18000, 21000, 21000},
	{32000, 22000, 22000, 28000, 28000},
	{64000, 38000, 38000, 50000, 50000},
}

// HybridState holds state for hybrid mode encoding.
// This is stored in the Encoder and persists across frames.
type HybridState struct {
	// prevHBGain is the high-band gain from the previous frame.
	// Used for smooth gain fading to prevent artifacts.
	prevHBGain opusVal16

	// stereoWidthQ14 is the stereo width in Q14 format.
	// Reduced at low bitrates to improve coding efficiency.
	stereoWidthQ14 int16

	// --- Scratch buffers for zero-allocation hybrid encoding ---

	// rangeEncoder is reused across frames to avoid heap allocation.
	rangeEncoder rangecoding.Encoder

	// scratchPacket is the shared range encoder output buffer.
	scratchPacket [maxHybridPacketSize]byte
	// scratchRedundancy stores CELT transition redundancy payload (2..257 bytes).
	scratchRedundancy [257]byte

	// Energy tracking scratch buffers.
	scratchBandLogE2  []float32 // bandLogE2 for transient analysis
	scratchAnalysisE  []float32 // pre-stabilization energies for dynalloc/analysis
	scratchPrevEnergy []float32 // copy of CELT oldBandE/prev energy
	scratchNextEnergy []float32 // next CELT oldBandE state update

	// MDCT scratch buffers for computeMDCTForHybridScratch.
	scratchMDCTInput  []float32 // overlap+samples assembly buffer
	scratchMDCTHist   []float32 // previous-frame overlap snapshot for current MDCT
	scratchMDCTResult []float32 // combined L+R MDCT output
	scratchDeintLeft  []float32 // deinterleaved left channel
	scratchDeintRight []float32 // deinterleaved right channel
}

// encodeHybridFrameWithMaxPacketAndTransition allows callers assembling long packets
// to gate CELT transition redundancy/prefill to the correct 20ms subframe,
// matching libopus multi-frame cadence. celtPCM is the frame's delay-compensated
// CELT input; the caller advances the delay buffer once the frame is coded.
// silkDTX reports that silk_Encode returned no payload: the frame is TOC-only,
// CELT does not run, and the frame ends before the delay buffer, the high-band
// gain and the stereo width advance (src/opus_encoder.c:2242-2248).
func (e *Encoder) encodeHybridFrameWithMaxPacketAndTransition(pcm []opusRes, celtPCM []opusRes, frameSize int, maxPacketBytes int, maxDataBytes int, dredBitrate int, hardMaxPacketBytes bool, allowTransitionRedundancy bool, transitionToCELT bool, runCELTTransitionPrefill bool) (frame []byte, silkDTX bool, err error) {
	// Validate: only 10ms (Fs/100) or 20ms (Fs/50) for hybrid
	if frameSize != int(e.sampleRate)/100 && frameSize != e.frame20ms() {
		return nil, false, ErrInvalidHybridFrameSize
	}

	// Ensure sub-encoders exist
	e.ensureSILKEncoder()
	e.ensureCELTEncoder()
	// A hybrid frame sets the CELT prediction before any redundant frame or
	// transition prefill (src/opus_encoder.c:2288-2295).
	e.celtEncoder.SetPrediction(e.celtPredictionMode())
	// Hybrid CELT highband runs at the native API rate (SWB=24k, FB=48k); the
	// CELT encoder upsamples sub-48k input to the 48 kHz core, matching libopus
	// celt_encode_with_ec frame_size *= st->upsample.
	e.celtEncoder.SetUpsample(e.celtUpsampleFactor())
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(ModeHybrid))
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))

	e.ensureHybridState()

	// Compute target buffer size based on bitrate mode.
	// baseTargetBytes includes the TOC byte; payloadTarget is the shared range payload.
	//
	// Per libopus opus_encoder.c: max_data_bytes = IMIN(orig_max_data_bytes, 1276),
	// and for non-SILK-only modes nb_compr_bytes = (max_data_bytes-1) - redundancy_bytes
	// (line 2392) is the byte budget handed to the CELT sub-encoder. The CELT VBR
	// reservoir (compute_vbr) then chooses the actual per-frame size from within that
	// full budget. For unconstrained/constrained VBR the budget is therefore the caller
	// buffer (clamped to 1276), NOT the nominal bitrate-derived size. SILK rate control
	// stays nominal because compute_silk_rate_for_hybrid clamps bits_target to
	// bitrate_to_bits() regardless of the larger byte budget (line 1960).
	baseTargetBytes := e.targetBytesForBitrate(int(e.bitrate), frameSize)
	// When DRED is carried, libopus reserves dred_bytes*3/4 out of nb_compr_bytes for
	// the CELT part (opus_encoder.c lines 2399-2412) and lets the carried DRED payload
	// absorb the remaining slack, so the primary CELT frame tracks the nominal
	// bitrate-derived size rather than the full max_data_bytes budget. Keep the nominal
	// base target and the CELT-internal reservoir (useFinalHybridVBRTarget gated below)
	// for DRED-carrier frames, matching libopus' per-frame sizing.
	dredCarrier := dredBitrate > 0
	if e.bitrateMode != ModeCBR && maxPacketBytes == 0 && !dredCarrier {
		// libopus max_data_bytes = IMIN(orig_max_data_bytes, 1276).
		opusMaxDataBytes := maxDataBytes
		if opusMaxDataBytes <= 0 {
			opusMaxDataBytes = maxHybridPacketSize + 1
		}
		if opusMaxDataBytes > maxHybridPacketSize+1 {
			opusMaxDataBytes = maxHybridPacketSize + 1
		}
		baseTargetBytes = opusMaxDataBytes
	}
	if maxPacketBytes > 0 {
		baseTargetBytes = maxPacketBytes
	}
	if baseTargetBytes < 2 {
		baseTargetBytes = 2
	}
	payloadTarget := max(baseTargetBytes-1, 1)

	// Transition redundancy reserves bytes and adjusts SILK/CELT budgeting.
	// CELT->Hybrid uses celt_to_silk=1; SILK/Hybrid->CELT uses celt_to_silk=0.
	frameRate := int(e.sampleRate) / frameSize
	prevPacketMode := e.prevPacketMode
	transitionCeltToHybrid := allowTransitionRedundancy && !transitionToCELT && !e.lowDelay && isConcreteMode(prevPacketMode) && prevPacketMode == ModeCELT
	transitionSilkToCELT := allowTransitionRedundancy && transitionToCELT && !e.lowDelay
	prefill := 0
	if e.silkPrefillPending {
		prefill = 1
	}
	// For the first frame at a new SILK bandwidth: the CELT->SILK redundant
	// frame and a SILK prefill that keeps the sampling rate control.
	if e.silkBWSwitch {
		transitionCeltToHybrid = true
		transitionSilkToCELT = false
		e.silkBWSwitch = false
		prefill = 2
	}
	transitionRedundancy := transitionCeltToHybrid || transitionSilkToCELT
	redundancyBytes := 0
	var redundancyData []byte
	var redundantRng uint32
	if transitionRedundancy {
		redundancyBytes = computeRedundancyBytes(baseTargetBytes, int(e.bitrate), frameRate, e.celtInternalChannelsForMode(ModeHybrid))
	}

	// Compute bit allocation between SILK and CELT using the full packet budget
	// (max_data_bytes, including any reserved transition redundancy).
	frame20ms := frameSize == e.frame20ms()
	silkBitrate, celtBitrate, celtBitrateHBGain := e.computeHybridBitAllocationWithBudget(frameSize, baseTargetBytes, redundancyBytes)

	// Compute HB_gain based on TOC-adjusted CELT bitrate (matching libopus line 2060).
	// Lower CELT bitrate means we should attenuate high frequencies.
	hbGain := e.computeHBGain(celtBitrateHBGain)

	// Main shared-range payload target excludes transition redundancy bytes.
	payloadTargetMain := max(payloadTarget-redundancyBytes, 1)

	// In libopus the CELT sub-encoder is handed nb_compr_bytes = (max_data_bytes-1) -
	// redundancy_bytes via ec_enc_shrink() (opus_encoder.c line 2392/2413). That is
	// payloadTargetMain here (payloadTarget already excludes the TOC byte). The CELT
	// VBR reservoir (compute_vbr) then chooses the actual per-frame size from within
	// that budget, so the range-encoder limit is the full nb_compr_bytes for every
	// VBR/CVBR frame, not a soft multiple of the nominal bitrate target.
	maxTargetBytes := payloadTargetMain
	if dredCarrier {
		// DRED-carrier frames keep the pre-regression soft range-encoder limit so the
		// per-frame CELT VBR state (and thus the carried-DRED packet sizes) track
		// libopus. Long-packet callers (hardMaxPacketBytes) still pass libopus-style
		// curr_max values that are hard per-subframe limits.
		switch {
		case e.bitrateMode == ModeCBR:
			maxTargetBytes = payloadTargetMain
		case hardMaxPacketBytes:
			maxTargetBytes = payloadTargetMain
		default:
			maxAllowed := max(baseTargetBytes*2, 2)
			maxTargetBytes = maxAllowed - 2
		}
		if redundancyBytes > 0 {
			maxTargetBytes -= redundancyBytes
		}
		if maxTargetBytes < payloadTargetMain {
			maxTargetBytes = payloadTargetMain
		}
	}
	if maxTargetBytes > maxHybridPacketSize-1 {
		maxTargetBytes = maxHybridPacketSize - 1
	}
	if dredBitrate > 0 && e.bitrateMode != ModeCBR && maxPacketBytes == 0 {
		maxTargetBytes = maxHybridPacketSize - 1
	}

	// Initialize shared range encoder (use scratch packet buffer from HybridState)
	buf := e.hybridState.scratchPacket[:]
	re := &e.hybridState.rangeEncoder
	re.Init(buf)
	if e.bitrateMode == ModeCBR {
		re.Shrink(uint32(maxTargetBytes))
	} else {
		re.Limit(uint32(maxTargetBytes))
	}

	// Step 1: SILK codes the low band first, on the shared range coder
	// (opus_encode_frame_native: silk_mode setup, then silk_Encode).
	//
	// Per libopus: in hybrid CBR mode, SILK is switched to VBR with a max bits cap.
	// This allows SILK to use fewer bits and CELT to absorb the variation.
	// In hybrid VBR/CVBR mode, SILK's maxBits is constrained to the SILK-appropriate
	// portion of the available bits.
	// Start from max_data_bytes-1 (payload excluding TOC), then subtract transition
	// redundancy reservation (bytes plus signaling bits) when active.
	silkMaxBitsPayload := payloadTarget
	if dredBitrate > 0 && e.bitrateMode != ModeCBR && !hardMaxPacketBytes {
		silkMaxBitsPayload = maxHybridPacketSize
	}
	silkMaxBits := silkMaxBitsPayload * 8
	if redundancyBytes >= 2 {
		// 1 bit redundancy position + 20 bits flag+size for hybrid.
		silkMaxBits -= redundancyBytes*8 + 1 + 20
	}
	if silkMaxBits < 0 {
		silkMaxBits = 0
	}
	if e.bitrateMode == ModeCBR {
		// Hybrid CBR: switch SILK to VBR with cap (libopus behavior)
		otherBits := max(silkMaxBits-silkBitrate*frameSize/int(e.sampleRate), 0)
		silkMaxBits -= otherBits * 3 / 4
		if silkMaxBits < 0 {
			silkMaxBits = 0
		}
	} else {
		// Hybrid VBR/CVBR: constrain SILK maxBits using the rate table.
		maxBitsAsBitrate := silkMaxBits * int(e.sampleRate) / frameSize
		maxSilkRate := e.computeSilkRateForMax(maxBitsAsBitrate, frame20ms)
		silkMaxBits = maxSilkRate * frameSize / int(e.sampleRate)
	}
	e.configureSILKMode(ModeHybrid, frameSize, baseTargetBytes, silkBitrate, silkMaxBits, false)
	activity := e.silkActivity()
	if err := e.runPendingSILKPrefill(prefill, activity); err != nil {
		return nil, false, err
	}
	nBytes, err := e.silk.Encode(&e.silkMode, pcm, frameSize, re, 0, activity)
	if err != nil {
		return nil, false, err
	}
	e.silkMode.OpusCanSwitch = e.silkMode.SwitchReady && !e.nonfinalFrame
	if nBytes == 0 {
		e.hybridFinalRange = 0
		return nil, true, nil
	}
	if e.silkMode.OpusCanSwitch {
		// A hybrid frame codes SILK at 16 kHz (minInternalSampleRate), where
		// SILK never asks to switch; the redundant CELT frame of a switch would
		// end this frame.
		redundancyBytes = computeRedundancyBytes(baseTargetBytes, int(e.bitrate), frameRate, e.celtInternalChannelsForMode(ModeHybrid))
		transitionCeltToHybrid = false
		transitionSilkToCELT = redundancyBytes != 0
		transitionRedundancy = transitionSilkToCELT
		e.silkBWSwitch = true
	}

	// Retrieve SILK signal info for CELT VBR target feedback.
	// Per libopus opus_encoder.c line 2420-2424: after SILK encodes, its signal
	// type and quantization offset are forwarded to CELT via silk_info.
	e.celtEncoder.SetSilkInfo(int(e.silkMode.SignalType), int(e.silkMode.Offset))

	// Step 2: HB_gain fade and stereo width reduction of the delay-compensated
	// CELT input (src/opus_encoder.c:2313-2348). They precede the redundancy
	// signaling, so a CELT->Hybrid redundant frame codes the faded input too.
	celtInput := e.applyHBGainFade(celtPCM, hbGain)
	celtInput = e.applyStereoWidthReduction(ModeHybrid, celtInput, frameSize)

	// Step 2b: Encode redundancy flag between SILK and CELT.
	// Per libopus opus_encoder.c: in hybrid mode, a redundancy flag is always
	// written between the SILK and CELT portions (logp=12).
	// Condition matches libopus: ec_tell(&enc)+17+20 <= 8*(max_data_bytes-1)
	redundancyActive := false
	if re.Tell()+17+20 <= 8*payloadTarget {
		if transitionRedundancy && redundancyBytes >= 2 {
			redundancyBytes = clampRedundancyBytesAfterSilk(baseTargetBytes, re.Tell(), redundancyBytes, true)
			if transitionCeltToHybrid {
				data, rng, err := e.encodeCELTToSILKRedundancy(celtInput, e.effectiveBandwidth(), redundancyBytes)
				if err != nil {
					return nil, false, err
				}
				redundancyData = data
				redundantRng = rng
				redundancyBytes = len(redundancyData)
			}
			if redundancyBytes >= 2 {
				redundancyActive = true
				re.EncodeBit(1, 12) // redundancy = 1
				if transitionCeltToHybrid {
					re.EncodeBit(1, 1) // celt_to_silk = 1
				} else {
					re.EncodeBit(0, 1) // celt_to_silk = 0
				}
				re.EncodeUniform(uint32(redundancyBytes-2), 256) // redundancy length
			} else {
				re.EncodeBit(0, 12)
			}
		} else {
			re.EncodeBit(0, 12)
		}
	}
	if !redundancyActive {
		e.silkBWSwitch = false
	}
	if dredBitrate > 0 {
		dredBytes := e.bitrateToBits(dredBitrate, frameSize) / 8
		if dredBytes > 0 {
			maxCELTBytes := maxTargetBytes - dredBytes*3/4
			minCELTBytes := (re.Tell()+7)/8 + 5
			if hardMaxPacketBytes && e.silkStereoCollapsed() {
				// Match libopus' collapsed-stereo hybrid path: when SILK has
				// driven stereo width to zero, the inactive side channel lowers
				// the CELT guard needed to preserve redundancy signaling.
				minCELTBytes -= 5
			}
			if minCELTBytes < 5 {
				minCELTBytes = 5
			}
			if maxCELTBytes < minCELTBytes {
				maxCELTBytes = minCELTBytes
			}
			if maxCELTBytes < maxTargetBytes {
				maxTargetBytes = maxCELTBytes
				if e.bitrateMode == ModeCBR {
					re.Shrink(uint32(maxTargetBytes))
				} else {
					re.Limit(uint32(maxTargetBytes))
				}
			}
		}
	}

	// CELT rate setup for the hybrid frame (opus_encode_frame_native): VBR
	// frames hand CELT the bitrate SILK leaves and always run unconstrained.
	// It follows the transition redundancy frame, which codes at
	// OPUS_BITRATE_MAX, and precedes the transition prefill.
	e.configureCELTRate(ModeHybrid, celtBitrate)

	// libopus resets+prefills CELT for mode transitions before main CELT coding.
	// In long packets this happens on the first 20ms hybrid subframe, after any
	// transition redundancy reset on that subframe.
	if maxPacketBytes == 0 || runCELTTransitionPrefill {
		// For CELT->Hybrid this is intentionally after transition redundancy encoding.
		e.maybePrefillCELTOnModeTransition(ModeHybrid)
	}

	// Step 4: CELT encodes high frequencies (bands 17-21)
	e.celtEncoder.SetRangeEncoder(re)
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))
	// Per libopus opus_encoder.c line 2392/2493: for non-DRED unconstrained/constrained
	// VBR the CELT sub-encoder is given the full nb_compr_bytes budget
	// (= maxTargetBytes = payloadTargetMain) and the final-VBR-target reservoir
	// (computeHybridCELTVBRTargetBytes) picks the per-frame size from within it. CBR
	// keeps the fixed packet-level shrink behavior.
	hybridCELTTargetBytes := payloadTargetMain
	useFinalHybridVBRTarget := e.bitrateMode != ModeCBR
	if dredCarrier {
		// When DRED is carried, libopus reserves dred_bytes*3/4 of nb_compr_bytes for
		// CELT and lets the carried payload absorb the slack (opus_encoder.c lines
		// 2399-2412), so the primary CELT frame uses the nominal bitrate-derived target
		// via CELT's internal reservoir instead of the final-VBR-target shrink. Long
		// packets still pass libopus-style curr_max values via hardMaxPacketBytes.
		useFinalHybridVBRTarget = hardMaxPacketBytes
	}
	if useFinalHybridVBRTarget {
		hybridCELTTargetBytes = maxTargetBytes
	}
	// Once SILK has busted the frame budget the frame becomes a PLC frame and
	// CELT does not run, so none of its state advances
	// (src/opus_encoder.c:2487-2488).
	if re.Tell() <= 8*payloadTarget {
		e.encodeCELTHybridImproved(celtInput, frameSize, hybridCELTTargetBytes, useFinalHybridVBRTarget, !useFinalHybridVBRTarget && maxPacketBytes == 0, dredCarrier)
	}
	mainRng := e.celtEncoder.FinalRange()

	// Update state for next frame
	e.hybridState.prevHBGain = hbGain

	// Finalize and append optional transition redundancy payload.
	tell := re.Tell()
	mainPayload := re.Done()
	if tell > 8*payloadTarget && len(mainPayload) > 0 {
		// SILK busted the frame budget: a single zero byte makes the decoder
		// run the PLC (src/opus_encoder.c:2578-2588).
		e.hybridFinalRange = 0
		mainPayload[0] = 0
		return mainPayload[:1], false, nil
	}
	if !redundancyActive {
		e.hybridFinalRange = mainRng
		return mainPayload, false, nil
	}
	if transitionSilkToCELT {
		redundancyData, redundantRng, err = e.encodeSILKToCELTRedundancy(celtInput, frameSize, e.effectiveBandwidth(), redundancyBytes)
		if err != nil {
			return nil, false, err
		}
	}
	if len(redundancyData) == 0 {
		return nil, false, ErrEncodingFailed
	}
	if len(mainPayload)+len(redundancyData) > len(e.hybridState.scratchPacket) {
		return nil, false, ErrEncodingFailed
	}
	out := e.hybridState.scratchPacket[:len(mainPayload)+len(redundancyData)]
	copy(out, mainPayload)
	copy(out[len(mainPayload):], redundancyData)
	e.hybridFinalRange = mainRng ^ redundantRng
	return out, false, nil
}

// computeRedundancyBytes matches libopus compute_redundancy_bytes().
func computeRedundancyBytes(maxDataBytes, bitrateBps, frameRate, channels int) int {
	if maxDataBytes <= 0 || bitrateBps <= 0 || frameRate <= 0 || channels <= 0 {
		return 0
	}
	baseBits := 40*channels + 20
	redundancyRate := bitrateBps + baseBits*(200-frameRate)
	redundancyRate = (3 * redundancyRate) / 2
	redundancyBytes := redundancyRate / 1600
	availableBits := maxDataBytes*8 - 2*baseBits
	if availableBits <= 0 {
		return 0
	}
	den := 240 + 48000/frameRate
	if den <= 0 {
		return 0
	}
	redundancyBytesCap := (availableBits*240/den + baseBits) / 8
	if redundancyBytes > redundancyBytesCap {
		redundancyBytes = redundancyBytesCap
	}
	if redundancyBytes > 4+8*channels {
		if redundancyBytes > 257 {
			redundancyBytes = 257
		}
		if redundancyBytes < 0 {
			return 0
		}
		return redundancyBytes
	}
	return 0
}

func clampRedundancyBytesAfterSilk(maxDataBytes, tellBits, redundancyBytes int, hybrid bool) int {
	if redundancyBytes <= 0 {
		return 0
	}
	maxRedundancy := 0
	if hybrid {
		maxRedundancy = (maxDataBytes - 1) - ((tellBits + 8 + 3 + 7) >> 3)
	} else {
		maxRedundancy = (maxDataBytes - 1) - ((tellBits + 7) >> 3)
	}
	if redundancyBytes > maxRedundancy {
		redundancyBytes = maxRedundancy
	}
	if redundancyBytes < 2 {
		redundancyBytes = 2
	}
	if redundancyBytes > 257 {
		redundancyBytes = 257
	}
	return redundancyBytes
}

// encodeCELTToSILKRedundancy codes the 5 ms redundant CELT frame that starts a
// switch from CELT to SILK or Hybrid, or a SILK internal bandwidth switch
// (src/opus_encoder.c:2427-2441): the first Fs/200 samples of pcm_buf continue
// the CELT stream at OPUS_BITRATE_MAX, coded to redundancyBytes at the TOC
// bandwidth bw with the stream's channels and the CELT prediction setting in
// force, and CELT is reset afterwards. It returns the redundant frame and its
// final range.
func (e *Encoder) encodeCELTToSILKRedundancy(celtPCM []opusRes, bw types.Bandwidth, redundancyBytes int) ([]byte, uint32, error) {
	channels := int(e.channels)
	redundancyFrameSize := int(e.sampleRate) / 200
	redundancySamples := redundancyFrameSize * channels
	if redundancyBytes < 2 || redundancyFrameSize <= 0 || len(celtPCM) < redundancySamples {
		return nil, 0, nil
	}

	e.ensureCELTEncoder()
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(ModeCELT))
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetTopLevelDelayCompensatedInput(true)
	e.celtEncoder.SetBitrate(celt.BitrateMax)
	e.celtEncoder.SetVBR(false)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(bw))
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))
	e.celtEncoder.SetDCRejectEnabled(false)
	e.celtEncoder.SetMaxPayloadBytes(redundancyBytes)
	redundancyData, err := e.celtEncoder.EncodeFrame(celtPCM[:redundancySamples], redundancyFrameSize)
	redundantRng := e.celtEncoder.FinalRange()
	e.celtEncoder.SetMaxPayloadBytes(0)
	// libopus resets CELT after CELT->SILK redundancy generation.
	e.celtEncoder.Reset()
	if err != nil {
		return nil, 0, err
	}
	return e.keepRedundancy(redundancyData, redundantRng)
}

// encodeSILKToCELTRedundancy codes the 5 ms redundant CELT frame that ends a
// switch from SILK or Hybrid to CELT, or ends the last frame before a SILK
// internal bandwidth switch (src/opus_encoder.c:2519-2551): CELT is reset,
// prefilled with the Fs/400 samples ahead of the last Fs/200 samples of
// pcm_buf, and codes those last samples at OPUS_BITRATE_MAX to
// redundancyBytes at the TOC bandwidth bw. It returns the redundant frame and
// its final range.
func (e *Encoder) encodeSILKToCELTRedundancy(celtPCM []opusRes, frameSize int, bw types.Bandwidth, redundancyBytes int) ([]byte, uint32, error) {
	channels := int(e.channels)
	sampleRate := int(e.sampleRate)
	n2 := sampleRate / 200
	n4 := sampleRate / 400
	frameSamples := frameSize * channels
	if redundancyBytes < 2 || n2 <= 0 || n4 <= 0 || frameSize < n2+n4 || len(celtPCM) < frameSamples {
		return nil, 0, nil
	}
	prefillSamples := n4 * channels
	redundancySamples := n2 * channels
	prefillStart := frameSamples - redundancySamples - prefillSamples
	redundancyStart := frameSamples - redundancySamples

	e.ensureCELTEncoder()
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(ModeCELT))
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.Reset()
	e.celtEncoder.SetRangeEncoder(nil)
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetTopLevelDelayCompensatedInput(true)
	e.celtEncoder.SetPrediction(0)
	e.celtEncoder.SetVBR(false)
	e.celtEncoder.SetBitrate(celt.BitrateMax)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(bw))
	e.celtEncoder.SetLSBDepth(int(e.lsbDepth))
	e.celtEncoder.SetDCRejectEnabled(false)

	// Prefill 2.5 ms so CELT state matches decoder-side startup for the first CELT frame.
	e.celtEncoder.SetMaxPayloadBytes(2)
	_, _ = e.celtEncoder.EncodeFrame(celtPCM[prefillStart:prefillStart+prefillSamples], n4)

	e.celtEncoder.SetMaxPayloadBytes(redundancyBytes)
	redundancyData, err := e.celtEncoder.EncodeFrame(celtPCM[redundancyStart:redundancyStart+redundancySamples], n2)
	redundantRng := e.celtEncoder.FinalRange()
	e.celtEncoder.SetMaxPayloadBytes(0)
	if err != nil {
		return nil, 0, err
	}
	return e.keepRedundancy(redundancyData, redundantRng)
}

// keepRedundancy copies a redundant CELT frame into the scratch that outlives
// the next CELT encode.
func (e *Encoder) keepRedundancy(data []byte, rng uint32) ([]byte, uint32, error) {
	if len(data) < 2 {
		return nil, 0, nil
	}
	hs := e.ensureHybridState()
	if len(data) > len(hs.scratchRedundancy) {
		return nil, 0, ErrEncodingFailed
	}
	out := hs.scratchRedundancy[:len(data)]
	copy(out, data)
	return out, rng, nil
}

// ensureHybridState creates the high-band gain and stereo width state at
// unity gain and full width.
func (e *Encoder) ensureHybridState() *HybridState {
	if e.hybridState == nil {
		e.hybridState = &HybridState{
			prevHBGain:     1.0,
			stereoWidthQ14: 16384,
		}
	}
	return e.hybridState
}

// computeHybridBitAllocation computes SILK/CELT bitrates using the default packet
// budget for the current frame size (no transition redundancy reservation).
func (e *Encoder) computeHybridBitAllocation(frame20ms bool) (silkBitrate, celtBitrate, celtBitrateHBGain int) {
	frameSize := int(e.sampleRate) / 100
	if frame20ms {
		frameSize = e.frame20ms()
	}
	maxDataBytes := max(e.targetBytesForBitrate(int(e.bitrate), frameSize), 2)
	return e.computeHybridBitAllocationWithBudget(frameSize, maxDataBytes, 0)
}

// computeHybridBitAllocationWithBudget computes SILK/CELT bitrates from the exact
// per-frame budget, including optional transition redundancy reservation.
func (e *Encoder) computeHybridBitAllocationWithBudget(frameSize, maxDataBytes, redundancyBytes int) (silkBitrate, celtBitrate, celtBitrateHBGain int) {
	if frameSize <= 0 {
		return 0, 0, 0
	}
	if maxDataBytes < 2 {
		maxDataBytes = 2
	}
	if redundancyBytes < 0 {
		redundancyBytes = 0
	}
	if redundancyBytes > maxDataBytes-1 {
		redundancyBytes = maxDataBytes - 1
	}

	// Match libopus bits_target:
	// bits_target = min(8*(max_data_bytes-redundancy_bytes), bitrate_to_bits(...)) - 8
	bitsTarget := 8 * (maxDataBytes - redundancyBytes)
	bitrateBits := e.bitrateToBits(int(e.bitrate), frameSize)
	if bitsTarget > bitrateBits {
		bitsTarget = bitrateBits
	}
	bitsTarget -= 8
	if bitsTarget < 0 {
		bitsTarget = 0
	}
	totalRate := bitsTarget * int(e.sampleRate) / frameSize // bits_to_bitrate()
	channels := max(e.silkInternalChannels(), 1)

	// Per-channel rate for table lookup
	ratePerChannel := totalRate / channels

	// Determine table entry based on frame size and FEC.
	entry := 1 // 10ms no FEC
	if frameSize == e.frame20ms() {
		entry = 2 // 20ms no FEC
	}
	if e.lbrrCoded {
		entry += 2 // FEC entries
	}

	// Find the appropriate row in the rate table.
	silkRatePerChannel := 0
	tableLen := len(hybridRateTable)
	breakIdx := tableLen
	for i := 1; i < tableLen; i++ {
		if hybridRateTable[i][0] > ratePerChannel {
			breakIdx = i
			break
		}
	}
	if breakIdx == tableLen {
		lastRow := hybridRateTable[tableLen-1]
		silkRatePerChannel = lastRow[entry]
		silkRatePerChannel += (ratePerChannel - lastRow[0]) / 2
	} else {
		lo := hybridRateTable[breakIdx-1][entry]
		hi := hybridRateTable[breakIdx][entry]
		x0 := hybridRateTable[breakIdx-1][0]
		x1 := hybridRateTable[breakIdx][0]
		if x1 > x0 {
			silkRatePerChannel = (lo*(x1-ratePerChannel) + hi*(ratePerChannel-x0)) / (x1 - x0)
		} else {
			silkRatePerChannel = lo
		}
	}

	// CBR/SWB adjustments from libopus compute_silk_rate_for_hybrid().
	if e.bitrateMode == ModeCBR {
		silkRatePerChannel += 100
	}
	if e.effectiveBandwidth() == types.BandwidthSuperwideband {
		silkRatePerChannel += 300
	}

	silkBitrate = silkRatePerChannel * channels
	if channels == 2 && ratePerChannel >= 12000 {
		silkBitrate -= 1000
	}

	celtBitrateHBGain = totalRate - silkBitrate
	celtBitrate = int(e.bitrate) - silkBitrate
	return silkBitrate, celtBitrate, celtBitrateHBGain
}

// computeSilkRateForMax computes the SILK rate corresponding to a maximum available
// bitrate. This is used to constrain SILK's maxBits in hybrid VBR mode.
// Matches libopus: compute_silk_rate_for_hybrid(maxBits*Fs/frame_size, ...).
func (e *Encoder) computeSilkRateForMax(maxBitrate int, frame20ms bool) int {
	channels := max(e.silkInternalChannels(), 1)
	ratePerChannel := maxBitrate / channels

	entry := 1 // 10ms no FEC
	if frame20ms {
		entry = 2
	}
	if e.lbrrCoded {
		entry += 2
	}

	tableLen := len(hybridRateTable)
	silkRatePerChannel := 0
	breakIdx := tableLen
	for i := 1; i < tableLen; i++ {
		if hybridRateTable[i][0] > ratePerChannel {
			breakIdx = i
			break
		}
	}
	if breakIdx == tableLen {
		lastRow := hybridRateTable[tableLen-1]
		silkRatePerChannel = lastRow[entry]
		silkRatePerChannel += (ratePerChannel - lastRow[0]) / 2
	} else {
		lo := hybridRateTable[breakIdx-1][entry]
		hi := hybridRateTable[breakIdx][entry]
		x0 := hybridRateTable[breakIdx-1][0]
		x1 := hybridRateTable[breakIdx][0]
		if x1 > x0 {
			silkRatePerChannel = (lo*(x1-ratePerChannel) + hi*(ratePerChannel-x0)) / (x1 - x0)
		} else {
			silkRatePerChannel = lo
		}
	}

	if e.bitrateMode == ModeCBR {
		silkRatePerChannel += 100
	}
	if e.effectiveBandwidth() == types.BandwidthSuperwideband {
		silkRatePerChannel += 300
	}

	silkRate := silkRatePerChannel * channels
	if channels == 2 && ratePerChannel >= 12000 {
		silkRate -= 1000
	}
	return silkRate
}

// computeHBGain computes the hybrid high-band attenuation gain using the
// libopus float-path formula and exp2 approximation:
// HB_gain = 1 - celt_exp2(-celt_rate/1024).
func (e *Encoder) computeHBGain(celtBitrate int) opusVal16 {
	expArg := -float32(celtBitrate) * (1.0 / 1024.0)
	return 1.0 - opusVal16(celtExp2Approx(expArg))
}

func celtExp2Approx(x float32) float32 {
	return opusmath.CeltExp2(x)
}

// applyHBGainFade applies HB_gain to the CELT input with smooth gain fading.
// This implements libopus gain_fade() for artifact-free transitions.
//
// IMPORTANT: This function does NOT add any delay. The CELT input is already
// delay-compensated by applyDelayCompensation (Fs/250 = 192 samples at 48kHz).
// In libopus, gain_fade operates in-place on pcm_buf which already contains
// the delay-compensated samples.
func (e *Encoder) applyHBGainFade(pcm []opusRes, hbGain opusVal16) []opusRes {
	// libopus opus_encode_native() runs gain_fade() whenever either gain is
	// below one, including when the gain is unchanged: the overlap samples
	// still take the crossfaded w*g2 + (1-w)*g1 gain, which can round away
	// from g2.
	prevGain := e.hybridState.prevHBGain
	if prevGain < 1 || hbGain < 1 {
		pcm = e.applyGainFade(pcm, prevGain, hbGain)
	}
	return pcm
}

// applyUnityHBGainFade mirrors opus_encode_native() for frames that are not
// hybrid: HB_gain is one, so gain_fade() runs only while the previous hybrid
// frame left prev_HB_gain below one, fading the CELT input back up to unity,
// and prev_HB_gain then resets to one.
func (e *Encoder) applyUnityHBGainFade(celtPCM []opusRes) []opusRes {
	if e.hybridState == nil {
		return celtPCM
	}
	if e.hybridState.prevHBGain < 1 {
		celtPCM = e.applyGainFade(celtPCM, e.hybridState.prevHBGain, 1)
	}
	e.hybridState.prevHBGain = 1
	return celtPCM
}

// applyGainFade implements libopus gain_fade(): across the CELT overlap
// (sampled at window[i*inc] with inc = 48000/Fs) the gain moves from g1 to g2
// with the squared window, and the rest of the frame takes g2.
func (e *Encoder) applyGainFade(samples []opusRes, g1, g2 opusVal16) []opusRes {
	channels := int(e.channels)
	frameSize := len(samples) / channels
	inc := 1
	if e.sampleRate > 0 && e.sampleRate < 48000 {
		inc = max(48000/int(e.sampleRate), 1)
	}
	overlap := min(hybridOverlap/inc, frameSize)
	window := celt.GetWindowBufferF32(hybridOverlap)

	for i := range overlap {
		w := opusVal16(window[i*inc])
		w = round32(w * w)
		g := fma32(w, g2, round32((1-w)*g1))
		for c := range channels {
			samples[i*channels+c] = g * samples[i*channels+c]
		}
	}
	for i := overlap * channels; i < frameSize*channels; i++ {
		samples[i] = g2 * samples[i]
	}
	return samples
}

// applyStereoWidthFade applies stereo width reduction with smooth transition.
// This mirrors libopus stereo_fade() for hybrid/CELT preprocessing.
func (e *Encoder) applyStereoWidthFade(samples []opusRes, widthQ14Prev, widthQ14 int16) []opusRes {
	if e.channels != 2 {
		return samples
	}

	frameSize := len(samples) / 2
	if frameSize <= 0 {
		return samples
	}

	// Clamp widths to [0, 16384]
	if widthQ14Prev < 0 {
		widthQ14Prev = 0
	}
	if widthQ14Prev > 16384 {
		widthQ14Prev = 16384
	}
	if widthQ14 < 0 {
		widthQ14 = 0
	}
	if widthQ14 > 16384 {
		widthQ14 = 16384
	}

	// Convert width to "collapse factor" g (0=full width, 1=mono)
	g1 := opusVal16(1) - opusVal16(widthQ14Prev)*(1.0/16384.0)
	g2 := opusVal16(1) - opusVal16(widthQ14)*(1.0/16384.0)

	// libopus stereo_fade(): inc = max(1, 48000/Fs), overlap = overlap48/inc,
	// window sampled at window[i*inc]. The 48 kHz core window is hybridOverlap=120
	// long; at native sub-48k rates the crossfade spans 120/inc samples.
	inc := 1
	if e.sampleRate > 0 && e.sampleRate < 48000 {
		inc = max(48000/int(e.sampleRate), 1)
	}
	overlap := min(hybridOverlap/inc, frameSize)

	window := celt.GetWindowBufferF32(hybridOverlap)
	if window == nil || len(window) < (overlap-1)*inc+1 {
		// Fallback: no window available, apply constant g2
		for i := range frameSize {
			diff := opusVal32(0.5) * (samples[i*2] - samples[i*2+1])
			diff *= g2
			samples[i*2] -= diff
			samples[i*2+1] += diff
		}
		return samples
	}

	for i := range overlap {
		w := opusVal16(window[i*inc])
		w2 := w * w
		g := g1*(1.0-w2) + g2*w2
		diff := opusVal32(0.5) * (samples[i*2] - samples[i*2+1])
		diff *= g
		samples[i*2] -= diff
		samples[i*2+1] += diff
	}
	for i := overlap; i < frameSize; i++ {
		diff := opusVal32(0.5) * (samples[i*2] - samples[i*2+1])
		diff *= g2
		samples[i*2] -= diff
		samples[i*2+1] += diff
	}

	return samples
}

// celtBandwidthFromTypes maps the frame's bandwidth to the CELT bandwidth that
// sets the end band opus_encode_frame_native hands CELT (CELT_SET_END_BAND):
// 13 for narrowband, 17 for mediumband and wideband, 19 for superwideband and
// 21 for fullband.
func celtBandwidthFromTypes(bw types.Bandwidth) celt.CELTBandwidth {
	switch bw {
	case types.BandwidthNarrowband:
		return celt.CELTNarrowband
	case types.BandwidthMediumband, types.BandwidthWideband:
		return celt.CELTWideband
	case types.BandwidthSuperwideband:
		return celt.CELTSuperwideband
	case types.BandwidthFullband:
		return celt.CELTFullband
	default:
		return celt.CELTFullband
	}
}

// encodeCELTHybridImproved encodes CELT data for hybrid mode with improvements.
// Implements proper energy matching at the crossover frequency.
// targetPayloadBytes is the desired total payload budget (excluding TOC) for the full packet.
// The SILK signal classification CELT reads (st->silk_info) is the one the
// Opus layer forwarded before any transition reset; a reset clears it, exactly
// as OPUS_RESET_STATE clears it in libopus.
func (e *Encoder) encodeCELTHybridImproved(pcm []opusRes, frameSize int, targetPayloadBytes int, useFinalVBRTarget, useInitialVBRAdjust, dredCarrier bool) {
	// Set hybrid mode flag on CELT encoder
	e.celtEncoder.SetHybrid(true)
	e.celtEncoder.SetStreamChannels(e.celtInternalChannelsForMode(ModeHybrid))
	e.celtEncoder.SetPrediction(e.celtPredictionModeForFrame())
	silkSignalType, silkOffset := e.celtEncoder.SilkInfo()

	// Ensure CELT scratch buffers are properly sized for this frame.
	// The hybrid path bypasses EncodeFrame, so we must initialize them here.
	e.celtEncoder.EnsureScratch(frameSize)

	// Get mode configuration
	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM

	// Apply pre-emphasis with signal scaling (zero-alloc scratch version)
	preemph := e.celtEncoder.ApplyPreemphasisWithScalingScratch(pcm)

	// Get the range encoder
	re := e.celtEncoder.RangeEncoder()
	if re == nil {
		return
	}

	if targetPayloadBytes < 1 {
		targetPayloadBytes = 1
	}
	// Frame budget of the CELT part (celt_encode_with_ec): the payload budget
	// counts the bytes SILK already filled in the shared range coder, VBR
	// frames read the rate-derived effectiveBytes, and equiv_rate derives from
	// the entry budget.
	tell0Frac := re.TellFrac()
	budget := e.celtEncoder.HybridFrameBudget(frameSize, lm, targetPayloadBytes)
	totalBits := budget.TotalBits()
	if used := re.Tell(); totalBits < used+8 {
		// Ensure we don't end up with negative budgets if SILK used more bits.
		totalBits = used + 8
	}
	// quant_coarse_energy() reads nbAvailableBytes: the bytes left to CELT
	// after the already-coded SILK/range bits.
	e.celtEncoder.SetCoarseEnergyAvailableBytes(max(budget.AvailableBytes(), 0))
	defer e.celtEncoder.SetCoarseEnergyAvailableBytes(0)

	// Weak transients are allowed on low-rate non-voiced hybrid frames
	// (celt_encoder.c:2028).
	effectiveBytes := budget.EffectiveBytes()
	allowWeakTransients := effectiveBytes < 15 && silkSignalType != 2

	// Hybrid CELT only encodes bands starting at HybridCELTStartBand.
	start := celt.HybridCELTStartBand
	bw := celtBandwidthFromTypes(e.effectiveBandwidth())
	end := max(min(celt.EffectiveBandsForFrameSize(bw, frameSize), mode.EffBands), 1)
	if end < start {
		end = start
	}
	nbBands := end
	overlap := min(celt.Overlap, frameSize)
	channels := int(e.channels)
	mdctHistLen := overlap * channels
	mdctHistory := e.hybridState.scratchMDCTHist
	if cap(mdctHistory) < mdctHistLen {
		mdctHistory = make([]float32, mdctHistLen)
		e.hybridState.scratchMDCTHist = mdctHistory
	}
	mdctHistory = mdctHistory[:mdctHistLen]
	if mdctHistLen > 0 {
		e.celtEncoder.OverlapBufferInto(mdctHistory)
	}

	// Transient analysis (pre-MDCT) to decide short blocks and tf metrics.
	transient, weakTransient, tfEstimate, toneFreq, toneishness, shortBlocks, bandLogE2 := e.celtEncoder.TransientAnalysisHybrid(
		preemph, frameSize, nbBands, lm, allowWeakTransients,
	)

	if useInitialVBRAdjust && e.bitrateMode != ModeCBR {
		shift := max(3-lm, 0)
		if silkOffset < 100 {
			totalBits += 12 >> shift
		}
		if silkOffset > 100 {
			totalBits -= 18 >> shift
		}
		tfAdj := int((tfEstimate - 0.25) * 50.0)
		totalBits += tfAdj
		silkUsedBits := re.Tell()
		if tfEstimate > 0.7 && totalBits-silkUsedBits < 50 {
			totalBits = silkUsedBits + 50
		}
		if totalBits < silkUsedBits+8 {
			totalBits = silkUsedBits + 8
		}
	}

	e.celtEncoder.ApplyHybridPrefilter(preemph, frameSize, tfEstimate, budget.AvailableBytes(), toneFreq, toneishness)

	// Compute MDCT with overlap history using the selected block size.

	mdctCoeffs := computeMDCTForHybridScratch(preemph, frameSize, channels, mdctHistory, shortBlocks, e.hybridState, e.celtEncoder)
	if len(mdctCoeffs) == 0 {
		return
	}
	// Keep float-path cadence aligned with libopus (opus_res/celt_sig are float).

	// Compute band energies
	energies := e.celtEncoder.ComputeBandEnergiesF32(mdctCoeffs, nbBands, frameSize)
	if bandLogE2 == nil {
		if cap(e.hybridState.scratchBandLogE2) < len(energies) {
			e.hybridState.scratchBandLogE2 = make([]float32, len(energies))
		}
		bandLogE2 = e.hybridState.scratchBandLogE2[:len(energies)]
		for i := range energies {
			bandLogE2[i] = float32(energies[i])
		}
	}

	// Keep natural MDCT-derived band energies for bands 0-16.
	// In libopus, compute_band_energies runs on the full MDCT output and
	// dynalloc_analysis uses all band energies (0 to end) even in hybrid mode.
	// Previously this code set bands 0-16 to -28 dB, which caused maxDepth,
	// masking model, and spread_weight to diverge from libopus.

	// NOTE: No crossover energy matching. libopus does not apply any energy
	// smoothing at the SILK/CELT boundary (band 17). The band energies are
	// used directly as computed from the MDCT coefficients.

	// Normalize bands to arrays (linear amplitudes) for PVQ input.
	var normL, normR []celt.CeltNorm
	var bandE []celt.CeltEner
	if e.channels == 1 {
		normL, bandE = e.celtEncoder.NormalizeBandsToArrayMonoWithBandEF32(mdctCoeffs, nbBands, frameSize)
	} else {
		if len(mdctCoeffs) < frameSize*2 {
			return
		}
		mdctLeft := mdctCoeffs[:frameSize]
		mdctRight := mdctCoeffs[frameSize:]
		normL, normR, bandE = e.celtEncoder.NormalizeBandsToArrayStereoWithBandEF32(mdctLeft, mdctRight, nbBands, frameSize)
	}

	// Encode silence flag ONLY if tell==1 (match libopus/decoder gating).
	if re.Tell() == 1 {
		re.EncodeBit(0, 15)
	}

	// In hybrid mode, postfilter flag is SKIPPED (not encoded)

	// Encode transient flag (only for LM >= 1).
	// Keep libopus transient_got_disabled state cadence so consecutive-transient
	// history advances even when budget forces transient coding off.
	transientGotDisabled := false
	if lm >= 1 && re.Tell()+3 <= totalBits {
		if transient {
			re.EncodeBit(1, 3)
		} else {
			re.EncodeBit(0, 3)
		}
	} else {
		// celt_encoder.c:2063-2069: a frame whose transient flag does not fit
		// advances the consecutive-transient history.
		transientGotDisabled = true
		transient = false
		shortBlocks = 1
	}

	// Snapshot previous frame energies for dynalloc/coarse state decisions.
	prevEnergy := e.celtEncoder.CopyPrevEnergyFloat32(e.hybridState.scratchPrevEnergy)
	e.hybridState.scratchPrevEnergy = prevEnergy

	// dynalloc_analysis in libopus uses pre-stabilization energies.
	if cap(e.hybridState.scratchAnalysisE) < len(energies) {
		e.hybridState.scratchAnalysisE = make([]float32, len(energies))
	}
	analysisEnergies := e.hybridState.scratchAnalysisE[:len(energies)]
	for i := range energies {
		analysisEnergies[i] = float32(energies[i])
	}

	oldBandE := prevEnergy
	if maxLen := nbBands * channels; maxLen > 0 && len(oldBandE) > maxLen {
		oldBandE = oldBandE[:maxLen]
	}

	// Compute dynalloc analysis for TF/spread and offsets.
	lsbDepth := int(e.lsbDepth)

	dynallocResult := e.celtEncoder.DynallocAnalysisHybridScratch(
		analysisEnergies,
		bandLogE2,
		oldBandE,
		nbBands,
		start,
		end,
		lsbDepth,
		lm,
		effectiveBytes,
		transient,
		e.celtEncoder.VBR(),
		e.celtEncoder.ConstrainedVBR(),
		toneFreq,
		toneishness,
	)

	// TF resolution for hybrid mode.
	// Per libopus celt_encoder.c line 2242: variable TF analysis is DISABLED
	// for hybrid mode (!hybrid flag). Instead, use fixed TF patterns based on
	// transient detection and signal type.
	// Reference: libopus celt_encoder.c lines 2261-2279.
	tfRes := e.celtEncoder.TFResScratch(nbBands)
	tfSelect := celt.FillHybridTFResolution(tfRes, end, transient, weakTransient, allowWeakTransients)
	// Match libopus pre-coarse stabilization before intra/coarse energy coding.
	// Apply only on coded bands [start,end).
	e.celtEncoder.StabilizeEnergiesBeforeCoarseHybrid(energies, start, end, nbBands)

	// Encode intra flag using libopus-style coarse-energy two-pass decision.
	intra := e.celtEncoder.DecideIntraMode(energies, start, nbBands, lm)
	if re.Tell()+3 <= totalBits {
		if intra {
			re.EncodeBit(1, 3)
		} else {
			re.EncodeBit(0, 3)
		}
	} else {
		intra = false
	}

	// Encode coarse energy.
	quantizedEnergies := e.celtEncoder.EncodeCoarseEnergyRange(energies, start, end, intra, lm)

	celt.TFEncodeWithSelect(re, start, end, transient, tfRes, lm, tfSelect)

	// Encode spread decision (analysis-based) only if budget allows.
	spread := celt.SpreadNormal
	if re.Tell()+4 <= totalBits {
		// Hybrid spread selection follows libopus fixed policy and does not use
		// spreading_decision() analysis.
		if e.celtEncoder.Complexity() == 0 {
			spread = celt.SpreadNone
		} else if transient {
			spread = celt.SpreadNormal
		} else {
			spread = celt.SpreadAggressive
		}
		re.EncodeICDF(spread, celt.SpreadICDF, 5)
	}

	// Initialize caps and offsets for allocation (hybrid bands only).
	caps := e.celtEncoder.CapsScratch(nbBands)
	celt.InitCapsInto(caps, nbBands, lm, channels)
	for i := 0; i < start && i < len(caps); i++ {
		caps[i] = 0
	}

	offsets := dynallocResult.Offsets
	if offsets == nil || len(offsets) < nbBands {
		offsets = e.celtEncoder.OffsetsScratch(nbBands)
		for i := range offsets {
			offsets[i] = 0
		}
	}

	// Encode dynalloc offsets.
	dynallocLogp := 6
	totalBitsQ3ForDynalloc := totalBits << celt.BitRes
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()
	for i := start; i < end; i++ {
		width := channels * celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := max(width, 6<<celt.BitRes)
		quanta := min(width<<celt.BitRes, innerMax)

		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for ; tellFracDynalloc+(dynallocLoopLogp<<celt.BitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < int(caps[i]); j++ {
			flag := 0
			if j < int(offsets[i]) {
				flag = 1
			}
			re.EncodeBit(flag, uint(dynallocLoopLogp))
			tellFracDynalloc = re.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
		}
		if j > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		offsets[i] = int32(boost)
	}

	allocTrim := 5
	if tellFracDynalloc+(6<<celt.BitRes) <= totalBitsQ3ForDynalloc-totalBoost {
		// In hybrid mode start>0, so libopus keeps alloc_trim fixed at 5 and
		// does not run alloc_trim_analysis().
		re.EncodeICDF(allocTrim, celt.TrimICDF, 7)
	}
	if useFinalVBRTarget && budget.VBR() {
		if dredCarrier {
			targetBytes := e.computeHybridCELTVBRTargetBytes(targetPayloadBytes, frameSize, opusVal16(tfEstimate), totalBoost, re.TellFrac(), tell0Frac, silkOffset)
			totalBits = targetBytes * 8
			re.Shrink(uint32(targetBytes))
		} else {
			e.celtEncoder.ApplyHybridVBR(&budget, lm, tfEstimate, totalBoost)
			totalBits = budget.TotalBits()
			re.Shrink(uint32(budget.CompressedBytes()))
		}
		if re.Error() != 0 {
			return
		}
	}

	// Compute bit allocation (hybrid bands only).
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (totalBits << celt.BitRes) - bitsUsed - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && totalBitsQ3 >= (lm+2)<<celt.BitRes {
		antiCollapseRsv = 1 << celt.BitRes
	}
	totalBitsQ3 -= antiCollapseRsv

	intensity := nbBands
	dualStereo := false
	equivRate := budget.EquivRate()
	signalBandwidth := e.celtEncoder.SignalBandwidthForAllocation(nbBands, equivRate)
	// Stereo mode parameters: libopus runs the intensity-band hysteresis decision
	// and stereo_analysis (dual_stereo) for C==2 in celt_encode_with_ec right after
	// dynalloc, clamping intensity to [start, end]. For hybrid, start is
	// HybridCELTStartBand, so the low-bitrate hysteresis result is clamped up to
	// start (all high bands become intensity-stereo). Defaulting intensity to
	// nbBands (as before) diverged the high-band stereo coupling from libopus.
	if channels == 2 {
		intensity, dualStereo = e.celtEncoder.DecideStereoParams(normL, normR, equivRate, lm, nbBands, start, end)
	}

	allocResult := e.celtEncoder.ComputeAllocationHybridScratch(
		re,
		totalBitsQ3,
		nbBands,
		caps,
		offsets,
		allocTrim,
		intensity,
		dualStereo,
		lm,
		e.celtEncoder.LastCodedBands(),
		signalBandwidth,
	)
	prevCoded := e.celtEncoder.LastCodedBands()
	if prevCoded != 0 {
		coded := max(prevCoded-1, allocResult.CodedBands)
		coded = min(prevCoded+1, coded)
		e.celtEncoder.SetLastCodedBands(coded)
	} else {
		e.celtEncoder.SetLastCodedBands(allocResult.CodedBands)
	}

	// Encode fine energy using libopus residual state (error[]).
	e.celtEncoder.EncodeFineEnergyRangeFromError(quantizedEnergies, start, end, allocResult.FineBits)

	// Encode bands (PVQ quant_all_bands).
	totalBitsAllQ3 := (totalBits << celt.BitRes) - antiCollapseRsv
	dualStereoVal := 0
	if allocResult.DualStereo {
		dualStereoVal = 1
	}
	tapset := e.celtEncoder.TapsetDecision()
	rng := e.celtEncoder.RNG()
	e.celtEncoder.QuantAllBandsEncodeScratch(
		re,
		channels,
		frameSize,
		lm,
		start,
		end,
		normL,
		normR,
		allocResult.BandBits,
		shortBlocks,
		spread,
		tapset,
		dualStereoVal,
		allocResult.Intensity,
		tfRes,
		totalBitsAllQ3,
		allocResult.Balance,
		allocResult.CodedBands,
		&rng,
		e.celtEncoder.Complexity(),
		bandE,
	)

	// Encode anti-collapse flag if reserved.
	if antiCollapseRsv > 0 {
		antiCollapseOn := 0
		if e.celtEncoder.ConsecTransient() < 2 {
			antiCollapseOn = 1
		}
		re.EncodeRawBits(uint32(antiCollapseOn), 1)
	}

	// Encode energy finalization bits (leftover budget).
	bitsLeft := max(totalBits-re.Tell(), 0)
	e.celtEncoder.EncodeEnergyFinaliseRangeFromError(quantizedEnergies, start, end, allocResult.FineBits, allocResult.FinePriority, bitsLeft)
	e.celtEncoder.UpdateEnergyErrorHybridFromError(start, end, nbBands)

	// Update state: prev energy, RNG, frame count, transient history.
	if cap(e.hybridState.scratchNextEnergy) < len(prevEnergy) {
		e.hybridState.scratchNextEnergy = make([]float32, len(prevEnergy))
	}
	nextEnergy := e.hybridState.scratchNextEnergy[:len(prevEnergy)]
	// Match libopus oldBandE cadence: bands outside [start,end) are reset.
	for i := range nextEnergy {
		nextEnergy[i] = 0
	}
	for c := range channels {
		base := c * celt.MaxBands
		for band := start; band < end; band++ {
			idx := c*nbBands + band
			if idx < len(quantizedEnergies) && base+band < len(nextEnergy) {
				nextEnergy[base+band] = float32(quantizedEnergies[idx])
			}
		}
	}
	e.celtEncoder.SetPrevEnergyWithPrevFloat32(prevEnergy, nextEnergy)
	e.celtEncoder.SetRNG(re.Range())
	e.celtEncoder.IncrementFrameCount()
	e.celtEncoder.UpdateConsecTransientWithDisabled(transient, transientGotDisabled)
}

// computeHybridCELTVBRTargetBytes sizes the CELT part of a DRED-carrier hybrid
// frame from the hybrid VBR target of celt_encode_with_ec
// (celt/celt_encoder.c:2445-2482). When SILK has collapsed the stereo side
// channel, the already-coded side information and min_allowed drive the size
// instead of a stereo high-band base, which keeps the carried-DRED packet sizes
// aligned with libopus.
func (e *Encoder) computeHybridCELTVBRTargetBytes(limitBytes, frameSize int, tfEstimate opusVal16, totalBoost, tellFrac, tell0Frac, silkOffset int) int {
	if limitBytes < 2 {
		return 2
	}
	mode := celt.GetModeConfig(frameSize)
	lmDiff := max(3-mode.LM, 0)

	vbrRateQ3 := e.celtEncoder.BitrateToBits(frameSize) << celt.BitRes
	channels := int(e.celtInternalChannelsForMode(ModeHybrid))
	baseTargetQ3 := vbrRateQ3 - ((9*channels + 4) << celt.BitRes)
	if e.silkStereoCollapsed() {
		baseTargetQ3 = 0
	}
	if baseTargetQ3 < 0 {
		baseTargetQ3 = 0
	}

	targetQ3 := baseTargetQ3
	if silkOffset < 100 {
		targetQ3 += (12 << celt.BitRes) >> lmDiff
	} else if silkOffset > 100 {
		targetQ3 -= (18 << celt.BitRes) >> lmDiff
	}
	tfBoost := int((tfEstimate - opusVal16(0.25)) * opusVal16(50<<celt.BitRes))
	targetQ3 += tfBoost
	if tfEstimate > 0.7 && targetQ3 < 50<<celt.BitRes {
		targetQ3 = 50 << celt.BitRes
	}
	targetQ3 += tellFrac

	targetBytes := (targetQ3 + (1 << (celt.BitRes + 2))) >> (celt.BitRes + 3)
	minAllowed := ((tellFrac + totalBoost + (1 << (celt.BitRes + 3)) - 1) >> (celt.BitRes + 3)) + 2
	hybridMinAllowed := (tell0Frac + (37 << celt.BitRes) + totalBoost + (1 << (celt.BitRes + 3)) - 1) >> (celt.BitRes + 3)
	if minAllowed < hybridMinAllowed {
		minAllowed = hybridMinAllowed
	}
	if targetBytes < minAllowed {
		targetBytes = minAllowed
	}
	if targetBytes > limitBytes {
		targetBytes = limitBytes
	}
	if targetBytes < 2 {
		targetBytes = 2
	}
	return targetBytes
}

// computeMDCTForHybridScratch computes MDCT for hybrid mode encoding using scratch buffers.
// ce provides the CELT encoder's scratch buffers for the MDCT transform.
// hs provides hybrid-specific scratch buffers for deinterleaving and assembly.
func computeMDCTForHybridScratch(samples []float32, frameSize, channels int, history []float32, shortBlocks int, hs *HybridState, ce *celt.Encoder) []float32 {
	if len(samples) == 0 {
		return nil
	}

	overlap := min(celt.Overlap, frameSize)

	if channels == 1 {
		if len(history) >= overlap {
			// Match regular CELT path: round overlap history to float32 cadence.
			needed := overlap + len(samples)
			if cap(hs.scratchMDCTInput) < needed {
				hs.scratchMDCTInput = make([]float32, needed)
			}
			input := hs.scratchMDCTInput[:needed]
			for i := range overlap {
				input[i] = history[i]
			}
			copy(input[overlap:], samples)
			if shortBlocks > 1 {
				return ce.MDCTShortScratchCoeffsF32(input, shortBlocks)
			}
			return ce.MDCTScratchCoeffsF32(input)
		}
		// No history: zero-pad and compute
		needed := overlap + len(samples)
		if cap(hs.scratchMDCTInput) < needed {
			hs.scratchMDCTInput = make([]float32, needed)
		}
		input := hs.scratchMDCTInput[:needed]
		for i := range overlap {
			input[i] = 0
		}
		copy(input[overlap:], samples)
		if shortBlocks > 1 {
			return ce.MDCTShortScratchCoeffsF32(input, shortBlocks)
		}
		return ce.MDCTScratchCoeffsF32(input)
	}

	// Stereo: MDCT each channel separately (L/R) using scratch deinterleave buffers
	n := len(samples) / 2
	if cap(hs.scratchDeintLeft) < n {
		hs.scratchDeintLeft = make([]float32, n)
	}
	if cap(hs.scratchDeintRight) < n {
		hs.scratchDeintRight = make([]float32, n)
	}
	left := hs.scratchDeintLeft[:n]
	right := hs.scratchDeintRight[:n]
	celt.DeinterleaveStereoIntoF32(samples, left, right)

	if len(history) >= overlap*2 {
		needed := overlap + n
		if cap(hs.scratchMDCTInput) < needed {
			hs.scratchMDCTInput = make([]float32, needed)
		}
		input := hs.scratchMDCTInput[:needed]
		// Left channel with rounded overlap history.
		for i := range overlap {
			input[i] = history[i]
		}
		copy(input[overlap:], left)
		var mdctLeft, mdctRight []float32
		if shortBlocks > 1 {
			mdctLeft = ce.MDCTShortScratchCoeffsF32(input, shortBlocks)
		} else {
			mdctLeft = ce.MDCTScratchCoeffsF32(input)
		}
		leftLen := len(mdctLeft)
		resultLen := leftLen + n
		if cap(hs.scratchMDCTResult) < resultLen {
			hs.scratchMDCTResult = make([]float32, resultLen)
		}
		result := hs.scratchMDCTResult[:resultLen]
		copy(result[:leftLen], mdctLeft)

		// Right channel with rounded overlap history.
		for i := range overlap {
			input[i] = history[overlap+i]
		}
		copy(input[overlap:], right)
		if shortBlocks > 1 {
			mdctRight = ce.MDCTShortScratchCoeffsF32(input, shortBlocks)
		} else {
			mdctRight = ce.MDCTScratchCoeffsF32(input)
		}
		copy(result[leftLen:], mdctRight)
		return result
	}

	// No history: zero-pad and compute each channel using L/R scratch methods
	needed := overlap + n
	if cap(hs.scratchMDCTInput) < needed {
		hs.scratchMDCTInput = make([]float32, needed)
	}
	input := hs.scratchMDCTInput[:needed]
	// Left channel
	for i := range overlap {
		input[i] = 0
	}
	copy(input[overlap:], left)
	var mdctLeft, mdctRight []float32
	if shortBlocks > 1 {
		mdctLeft = ce.MDCTShortScratchCoeffsF32(input, shortBlocks)
	} else {
		mdctLeft = ce.MDCTScratchCoeffsF32(input)
	}
	// Copy left result before computing right (they share the same mdctCoeffs scratch)
	leftLen := len(mdctLeft)
	rightLen := n // will be same size
	resultLen := leftLen + rightLen
	if cap(hs.scratchMDCTResult) < resultLen {
		hs.scratchMDCTResult = make([]float32, resultLen)
	}
	result := hs.scratchMDCTResult[:resultLen]
	copy(result[:leftLen], mdctLeft)

	// Right channel
	for i := range overlap {
		input[i] = 0
	}
	copy(input[overlap:], right)
	if shortBlocks > 1 {
		mdctRight = ce.MDCTShortScratchCoeffsF32(input, shortBlocks)
	} else {
		mdctRight = ce.MDCTScratchCoeffsF32(input)
	}
	copy(result[leftLen:], mdctRight)

	return result
}

// ComputeStereoWidth computes the stereo width for hybrid mode encoding.
// At low bitrates, stereo width is reduced to improve coding efficiency.
// This matches libopus compute_stereo_width().
func ComputeStereoWidth(pcm []opusRes, frameSize, channels int) OpusVal16 {
	if channels != 2 || len(pcm) < frameSize*2 {
		return 0.0
	}

	// Compute correlation between left and right channels
	var sumLeft, sumRight, sumCross opusVal32
	for i := range frameSize {
		l := pcm[i*2]
		r := pcm[i*2+1]
		sumLeft += opusVal32(l) * opusVal32(l)
		sumRight += opusVal32(r) * opusVal32(r)
		sumCross += opusVal32(l) * opusVal32(r)
	}

	// Compute correlation coefficient
	if sumLeft < 1e-10 || sumRight < 1e-10 {
		return 0.0
	}

	correlation := sumCross / celtSqrtOpusVal32(sumLeft*sumRight)

	// Convert correlation to stereo width
	// High correlation (mono-like) -> low width
	// Low correlation (wide stereo) -> high width
	width := celtSqrtOpusVal32(opusVal32(0.5) * (opusVal32(1.0) - correlation*correlation))

	if width > 1.0 {
		width = 1.0
	}
	if width < 0.0 {
		width = 0.0
	}

	return opusVal16(width)
}

// silkStereoCollapsed reports whether silk_Encode coded the current frame of a
// stereo stream at zero width (silk_mode.stereoWidth_Q14 == 0), leaving no
// side channel.
func (e *Encoder) silkStereoCollapsed() bool {
	return e.channels == 2 && e.silkMode.NChannelsInternal == 2 && e.silkMode.StereoWidthQ14 == 0
}
