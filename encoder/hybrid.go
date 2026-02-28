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
	"fmt"
	"math"
	"os"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
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
	prevHBGain float64

	// stereoWidthQ14 is the stereo width in Q14 format.
	// Reduced at low bitrates to improve coding efficiency.
	stereoWidthQ14 int
	// silkStereoWidthQ14 is the current frame SILK stereo width decision (Q14).
	// Hybrid stereo fade follows this value in libopus.
	silkStereoWidthQ14 int

	// prevDecodeOnlyMiddle tracks the previous mid-only (no side) decision.
	prevDecodeOnlyMiddle bool

	// --- Scratch buffers for zero-allocation hybrid encoding ---

	// rangeEncoder is reused across frames to avoid heap allocation.
	rangeEncoder rangecoding.Encoder

	// scratchPacket is the shared range encoder output buffer.
	scratchPacket [maxHybridPacketSize]byte
	// scratchRedundancy stores CELT transition redundancy payload (2..257 bytes).
	scratchRedundancy [257]byte
	// scratchTransitionPCM stores gain-shaped transition redundancy input samples.
	scratchTransitionPCM []float64

	// Lookahead resampling scratch buffers.
	scratchLookahead32   []float32 // float64 -> float32 conversion
	scratchSilkLookahead []float32 // resampled lookahead output
	scratchLaLeft        []float32 // deinterleaved left lookahead
	scratchLaRight       []float32 // deinterleaved right lookahead
	scratchLaOutLeft     []float32 // resampled left lookahead
	scratchLaOutRight    []float32 // resampled right lookahead

	// Energy tracking scratch buffers.
	scratchBandLogE2  []float64 // bandLogE2 for transient analysis
	scratchAnalysisE  []float64 // pre-stabilization energies for dynalloc/analysis
	scratchPrevEnergy []float64 // copy of prev energy
	scratchNextEnergy []float64 // next energy for state update

	// MDCT scratch buffers for computeMDCTForHybridScratch.
	scratchMDCTInput  []float64 // overlap+samples assembly buffer
	scratchMDCTResult []float64 // combined L+R MDCT output
	scratchDeintLeft  []float64 // deinterleaved left channel
	scratchDeintRight []float64 // deinterleaved right channel
}

// encodeHybridFrame encodes a frame using combined SILK and CELT.
func (e *Encoder) encodeHybridFrame(pcm []float64, celtPCM []float64, lookahead []float64, frameSize int) ([]byte, error) {
	return e.encodeHybridFrameWithMaxPacket(pcm, celtPCM, lookahead, frameSize, 0)
}

// encodeHybridFrameWithMaxPacket mirrors opus_encode_native() per-frame caps for
// multi-frame packet assembly. maxPacketBytes includes TOC and must be >=2 when set.
func (e *Encoder) encodeHybridFrameWithMaxPacket(pcm []float64, celtPCM []float64, lookahead []float64, frameSize int, maxPacketBytes int) ([]byte, error) {
	return e.encodeHybridFrameWithMaxPacketAndTransition(pcm, celtPCM, lookahead, frameSize, maxPacketBytes, true)
}

// encodeHybridFrameWithMaxPacketAndTransition allows callers assembling long packets
// to gate CELT->Hybrid redundancy to the first 20ms subframe, matching libopus
// frame_redundancy cadence in multi-frame mode.
func (e *Encoder) encodeHybridFrameWithMaxPacketAndTransition(pcm []float64, celtPCM []float64, lookahead []float64, frameSize int, maxPacketBytes int, allowTransitionRedundancy bool) ([]byte, error) {
	// Validate: only 480 (10ms) or 960 (20ms) for hybrid
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidHybridFrameSize
	}

	// Ensure sub-encoders exist
	e.ensureSILKEncoder()
	if e.channels == 2 {
		e.ensureSILKSideEncoder()
	}
	e.ensureCELTEncoder()
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))

	// Initialize hybrid state if needed
	if e.hybridState == nil {
		e.hybridState = &HybridState{
			prevHBGain:         1.0,
			stereoWidthQ14:     16384, // Full width (Q14 = 1.0)
			silkStereoWidthQ14: 16384, // Full width (Q14 = 1.0)
		}
	}

	// Propagate bitrate mode to CELT encoder for hybrid mode.
	// Per libopus opus_encoder.c line 2450-2455: in hybrid mode, CELT VBR
	// constraint is ALWAYS disabled regardless of the top-level vbr_constraint.
	// The constraint is applied at the opus level (via SILK maxBits), not CELT.
	switch e.bitrateMode {
	case ModeCBR:
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(false)
	case ModeCVBR, ModeVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false) // Always false in hybrid (libopus line 2455)
	}

	// Compute target buffer size based on bitrate mode.
	// baseTargetBytes includes the TOC byte; payloadTarget is the shared range payload.
	baseTargetBytes := targetBytesForBitrate(e.bitrate, frameSize)
	if maxPacketBytes > 0 && baseTargetBytes > maxPacketBytes {
		baseTargetBytes = maxPacketBytes
	}
	if baseTargetBytes < 2 {
		baseTargetBytes = 2
	}
	payloadTarget := baseTargetBytes - 1
	if payloadTarget < 1 {
		payloadTarget = 1
	}

	// CELT->Hybrid transition uses CELT redundancy for smooth switching. This is a
	// true libopus feature (not FEC/LBRR); it reserves bytes and adjusts SILK budget.
	frameRate := 48000 / frameSize
	transitionCeltToHybrid := allowTransitionRedundancy && !e.lowDelay && isConcreteMode(e.prevMode) && e.prevMode == ModeCELT
	redundancyBytes := 0
	var redundancyData []byte
	if transitionCeltToHybrid {
		redundancyBytes = computeRedundancyBytes(baseTargetBytes, e.bitrate, frameRate, e.channels)
		if redundancyBytes > 0 {
			// Match libopus input shaping for CELT->Hybrid redundancy:
			// redundancy CELT sees the same HB gain contour as the main CELT path.
			_, _, celtBitrateHBGainRed := e.computeHybridBitAllocationWithBudget(frameSize, baseTargetBytes, redundancyBytes)
			hbGainRed := e.computeHBGain(celtBitrateHBGainRed)
			redundancyPCM := e.prepareCELTTransitionRedundancyInput(celtPCM, hbGainRed)
			data, err := e.encodeCELTTransitionRedundancy(redundancyPCM, frameSize, redundancyBytes)
			if err != nil {
				return nil, err
			}
			redundancyData = data
			redundancyBytes = len(redundancyData)
		}
	}

	// Compute bit allocation between SILK and CELT using the full packet budget
	// (max_data_bytes, including any reserved transition redundancy).
	frame20ms := frameSize == 960
	silkBitrate, celtBitrate, celtBitrateHBGain := e.computeHybridBitAllocationWithBudget(frameSize, baseTargetBytes, redundancyBytes)

	// Compute HB_gain based on TOC-adjusted CELT bitrate (matching libopus line 2060).
	// Lower CELT bitrate means we should attenuate high frequencies.
	hbGain := e.computeHBGain(celtBitrateHBGain)

	// Main shared-range payload target excludes transition redundancy bytes.
	payloadTargetMain := payloadTarget - redundancyBytes
	if payloadTargetMain < 1 {
		payloadTargetMain = 1
	}

	maxTargetBytes := payloadTargetMain
	switch e.bitrateMode {
	case ModeCBR:
		maxTargetBytes = payloadTargetMain
	case ModeCVBR, ModeVBR:
		// Allow up to 2x target for both VBR and CVBR. In libopus, the
		// range encoder buffer is large (up to 1275 bytes) regardless of
		// CVBR mode. The CELT encoder's internal CVBR reservoir tracking
		// constrains actual byte usage per frame.
		maxAllowed := int(float64(baseTargetBytes) * 2.0)
		if maxAllowed < 2 {
			maxAllowed = 2
		}
		// Reserve one extra byte to account for range coder end bits.
		maxTargetBytes = maxAllowed - 2
	}
	if redundancyBytes > 0 {
		maxTargetBytes -= redundancyBytes
	}
	if maxTargetBytes < payloadTargetMain {
		maxTargetBytes = payloadTargetMain
	}
	if maxTargetBytes > maxHybridPacketSize-1 {
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

	// Step 1: Downsample 48kHz -> 16kHz for SILK using libopus-matching resampler
	silkInput := e.downsample48to16Hybrid(pcm, frameSize)

	// Resample lookahead if available (save/restore state)
	var silkLookahead []float32
	if len(lookahead) > 0 {
		// Convert to float32 using scratch buffer
		needed := len(lookahead)
		if cap(e.hybridState.scratchLookahead32) < needed {
			e.hybridState.scratchLookahead32 = make([]float32, needed)
		}
		lookahead32 := e.hybridState.scratchLookahead32[:needed]
		for i, v := range lookahead {
			lookahead32[i] = float32(v)
		}

		targetLaSamples := len(lookahead) / 3
		neededOut := targetLaSamples * e.channels
		if cap(e.hybridState.scratchSilkLookahead) < neededOut {
			e.hybridState.scratchSilkLookahead = make([]float32, neededOut)
		}
		silkLookahead = e.hybridState.scratchSilkLookahead[:neededOut]

		if e.channels == 1 {
			state := e.silkResampler.State()
			e.silkResampler.ProcessInto(lookahead32, silkLookahead)
			e.silkResampler.SetState(state)
		} else {
			// Stereo lookahead resampling with scratch buffers
			halfLen := len(lookahead32) / 2
			if cap(e.hybridState.scratchLaLeft) < halfLen {
				e.hybridState.scratchLaLeft = make([]float32, halfLen)
			}
			if cap(e.hybridState.scratchLaRight) < halfLen {
				e.hybridState.scratchLaRight = make([]float32, halfLen)
			}
			leftLa := e.hybridState.scratchLaLeft[:halfLen]
			rightLa := e.hybridState.scratchLaRight[:halfLen]
			for i := 0; i < halfLen; i++ {
				leftLa[i] = lookahead32[i*2]
				rightLa[i] = lookahead32[i*2+1]
			}

			halfOut := targetLaSamples / 2
			if cap(e.hybridState.scratchLaOutLeft) < halfOut {
				e.hybridState.scratchLaOutLeft = make([]float32, halfOut)
			}
			if cap(e.hybridState.scratchLaOutRight) < halfOut {
				e.hybridState.scratchLaOutRight = make([]float32, halfOut)
			}
			leftOut := e.hybridState.scratchLaOutLeft[:halfOut]
			rightOut := e.hybridState.scratchLaOutRight[:halfOut]

			stateL := e.silkResampler.State()
			stateR := e.silkResamplerRight.State()
			e.silkResampler.ProcessInto(leftLa, leftOut)
			e.silkResamplerRight.ProcessInto(rightLa, rightOut)
			e.silkResampler.SetState(stateL)
			e.silkResamplerRight.SetState(stateR)

			// Interleave into silkLookahead
			for i := 0; i < halfOut; i++ {
				silkLookahead[i*2] = leftOut[i]
				silkLookahead[i*2+1] = rightOut[i]
			}
		}
	}

	// Step 2: SILK encodes first (uses shared range encoder)
	e.silkEncoder.SetRangeEncoder(re)
	e.silkEncoder.ResetPacketState()
	if silkBitrate > 0 {
		perChannel := silkBitrate / e.channels
		if perChannel > 0 {
			e.silkEncoder.SetBitrate(perChannel)
			if e.channels == 2 {
				e.silkSideEncoder.SetBitrate(perChannel)
			}
		}
	}
	e.silkEncoder.SetFEC(e.lbrrCoded)
	e.silkEncoder.SetPacketLoss(e.packetLoss)

	// Per libopus: in hybrid CBR mode, SILK is switched to VBR with a max bits cap.
	// This allows SILK to use fewer bits and CELT to absorb the variation.
	// In hybrid VBR/CVBR mode, SILK's maxBits is constrained to the SILK-appropriate
	// portion of the available bits.
	// Start from max_data_bytes-1 (payload excluding TOC), then subtract transition
	// redundancy reservation (bytes plus signaling bits) when active.
	silkMaxBits := payloadTarget * 8
	if redundancyBytes >= 2 {
		// 1 bit redundancy position + 20 bits flag+size for hybrid.
		silkMaxBits -= redundancyBytes*8 + 1 + 20
	}
	if silkMaxBits < 0 {
		silkMaxBits = 0
	}
	if e.bitrateMode == ModeCBR {
		// Hybrid CBR: switch SILK to VBR with cap (libopus behavior)
		e.silkEncoder.SetVBR(true)
		otherBits := silkMaxBits - silkBitrate*frameSize/48000
		if otherBits < 0 {
			otherBits = 0
		}
		silkMaxBits -= otherBits * 3 / 4
		if silkMaxBits < 0 {
			silkMaxBits = 0
		}
	} else {
		// Hybrid VBR/CVBR: constrain SILK maxBits using the rate table.
		e.silkEncoder.SetVBR(true)
		maxBitsAsBitrate := silkMaxBits * 48000 / frameSize
		maxSilkRate := e.computeSilkRateForMax(maxBitsAsBitrate, frame20ms)
		silkMaxBits = maxSilkRate * frameSize / 48000
	}
	e.silkEncoder.SetMaxBits(silkMaxBits)
	if e.channels == 2 {
		e.silkSideEncoder.ResetPacketState()
		e.silkSideEncoder.SetFEC(e.lbrrCoded)
		e.silkSideEncoder.SetPacketLoss(e.packetLoss)
		e.silkSideEncoder.SetVBR(true)
		e.silkSideEncoder.SetMaxBits(silkMaxBits)
	}
	e.encodeSILKHybrid(silkInput, silkLookahead, frameSize, silkBitrate)

	// Retrieve SILK signal info for CELT VBR target feedback.
	// Per libopus opus_encoder.c line 2420-2424: after SILK encodes, its signal
	// type and quantization offset are forwarded to CELT via silk_info.
	silkSignalType, silkOffset := e.silkEncoder.LastEncodedSignalInfo()

	// Step 2b: Encode redundancy flag between SILK and CELT.
	// Per libopus opus_encoder.c: in hybrid mode, a redundancy flag is always
	// written between the SILK and CELT portions (logp=12).
	// Condition matches libopus: ec_tell(&enc)+17+20 <= 8*(max_data_bytes-1)
	redundancyActive := false
	if re.Tell()+17+20 <= 8*payloadTarget {
		if transitionCeltToHybrid && redundancyBytes >= 2 {
			redundancyActive = true
			re.EncodeBit(1, 12)                              // redundancy = 1
			re.EncodeBit(1, 1)                               // celt_to_silk = 1
			re.EncodeUniform(uint32(redundancyBytes-2), 256) // redundancy length
		} else {
			re.EncodeBit(0, 12)
		}
	}

	// libopus resets+prefills CELT for mode transitions before main CELT coding.
	// For long (40/60ms) packets this prefill is managed once at packet level.
	if maxPacketBytes == 0 {
		// For CELT->Hybrid this is intentionally after transition redundancy encoding.
		e.maybePrefillCELTOnModeTransition(ModeHybrid, celtPCM, frameSize)
	}
	if tmpHybridHBDebugEnabled {
		target := tmpHybridHBDebugFrame
		callTag := -1
		if e.celtEncoder != nil {
			callTag = e.celtEncoder.FrameCount()
		}
		if target < 0 || callTag == target {
			fmt.Fprintf(os.Stderr,
				"GOHB frame=%d prev=%.9f hb=%.9f silk=%d celt=%d celt_hb=%d payload=%d base_target=%d red=%d red_active=%v mode=%d prev_mode=%d\n",
				callTag, e.hybridState.prevHBGain, hbGain, silkBitrate, celtBitrate, celtBitrateHBGain,
				payloadTargetMain, baseTargetBytes, redundancyBytes, transitionCeltToHybrid, e.mode, e.prevMode)
		}
	}

	// Step 3: Apply HB_gain fade on the delay-compensated CELT input.
	// The CELT input is already delay-compensated by applyDelayCompensation
	// in the caller (Fs/250 = 192 samples). No additional delay is needed here.
	celtInput := e.applyHBGainFade(celtPCM, hbGain)
	if e.channels == 2 {
		targetWidthQ14 := 16384
		if e.hybridState != nil {
			targetWidthQ14 = e.hybridState.silkStereoWidthQ14
			if targetWidthQ14 < 0 {
				targetWidthQ14 = 0
			}
			if targetWidthQ14 > 16384 {
				targetWidthQ14 = 16384
			}
		}
		if e.hybridState.stereoWidthQ14 < (1<<14) || targetWidthQ14 < (1<<14) {
			celtInput = e.applyStereoWidthFade(celtInput, e.hybridState.stereoWidthQ14, targetWidthQ14)
		}
		e.hybridState.stereoWidthQ14 = targetWidthQ14
	}

	// Step 4: CELT encodes high frequencies (bands 17-21)
	e.celtEncoder.SetRangeEncoder(re)
	if e.bitrateMode == ModeCBR {
		// Match libopus hybrid CBR path: CELT stays at OPUS_BITRATE_MAX and
		// packet-level range limits enforce the actual budget.
		e.celtEncoder.SetBitrate(MaxBitrate)
	} else {
		e.celtEncoder.SetBitrate(celtBitrate)
	}
	e.celtEncoder.SetLSBDepth(e.lsbDepth)
	e.encodeCELTHybridImproved(celtInput, frameSize, payloadTargetMain, silkSignalType, silkOffset)

	// Update state for next frame
	e.hybridState.prevHBGain = hbGain

	// Finalize and append optional CELT->SILK transition redundancy payload.
	mainPayload := re.Done()
	if !redundancyActive || len(redundancyData) == 0 {
		return mainPayload, nil
	}
	if len(mainPayload)+len(redundancyData) > len(e.hybridState.scratchPacket) {
		return nil, ErrEncodingFailed
	}
	out := e.hybridState.scratchPacket[:len(mainPayload)+len(redundancyData)]
	copy(out, mainPayload)
	copy(out[len(mainPayload):], redundancyData)
	return out, nil
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

// encodeCELTTransitionRedundancy encodes the 5ms CELT redundancy frame used for
// CELT->SILK/Hybrid transitions.
func (e *Encoder) encodeCELTTransitionRedundancy(celtPCM []float64, frameSize, redundancyBytes int) ([]byte, error) {
	if redundancyBytes < 2 || frameSize <= 0 {
		return nil, nil
	}
	redundancyFrameSize := e.sampleRate / 200 // 5 ms at 48 kHz
	if redundancyFrameSize <= 0 || frameSize < redundancyFrameSize {
		return nil, nil
	}
	redundancySamples := redundancyFrameSize * e.channels
	if redundancySamples <= 0 || len(celtPCM) < redundancySamples {
		return nil, nil
	}

	e.ensureCELTEncoder()
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetBitrate(MaxBitrate)
	e.celtEncoder.SetVBR(false)
	e.celtEncoder.SetConstrainedVBR(false)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetLSBDepth(e.lsbDepth)
	e.celtEncoder.SetDCRejectEnabled(false)
	e.celtEncoder.SetMaxPayloadBytes(redundancyBytes)
	redundancyData, err := e.celtEncoder.EncodeFrame(celtPCM[:redundancySamples], redundancyFrameSize)
	e.celtEncoder.SetMaxPayloadBytes(0)
	// libopus resets CELT after CELT->SILK redundancy generation.
	e.celtEncoder.Reset()
	if err != nil {
		return nil, err
	}
	if len(redundancyData) < 2 {
		return nil, nil
	}
	if len(redundancyData) > len(e.hybridState.scratchRedundancy) {
		return nil, ErrEncodingFailed
	}
	out := e.hybridState.scratchRedundancy[:len(redundancyData)]
	copy(out, redundancyData)
	return out, nil
}

// prepareCELTTransitionRedundancyInput shapes the 5 ms transition input with the
// same HB gain contour used by hybrid CELT coding, without mutating celtPCM.
func (e *Encoder) prepareCELTTransitionRedundancyInput(celtPCM []float64, hbGain float64) []float64 {
	redundancyFrameSize := e.sampleRate / 200 // 5 ms at 48 kHz
	if redundancyFrameSize <= 0 {
		return celtPCM
	}
	redundancySamples := redundancyFrameSize * e.channels
	if redundancySamples <= 0 || len(celtPCM) < redundancySamples {
		return celtPCM
	}

	prevGain := 1.0
	if e.hybridState != nil {
		prevGain = e.hybridState.prevHBGain
	}
	if prevGain == hbGain && hbGain >= 1.0 {
		return celtPCM
	}

	if cap(e.hybridState.scratchTransitionPCM) < redundancySamples {
		e.hybridState.scratchTransitionPCM = make([]float64, redundancySamples)
	}
	out := e.hybridState.scratchTransitionPCM[:redundancySamples]
	copy(out, celtPCM[:redundancySamples])
	return e.applyHBGainFade(out, hbGain)
}

// computeHybridBitAllocation computes SILK/CELT bitrates using the default packet
// budget for the current frame size (no transition redundancy reservation).
func (e *Encoder) computeHybridBitAllocation(frame20ms bool) (silkBitrate, celtBitrate, celtBitrateHBGain int) {
	frameSize := 480
	if frame20ms {
		frameSize = 960
	}
	maxDataBytes := targetBytesForBitrate(e.bitrate, frameSize)
	if maxDataBytes < 2 {
		maxDataBytes = 2
	}
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
	bitrateBits := bitrateToBits(e.bitrate, frameSize)
	if bitsTarget > bitrateBits {
		bitsTarget = bitrateBits
	}
	bitsTarget -= 8
	if bitsTarget < 0 {
		bitsTarget = 0
	}
	totalRate := bitsTarget * 48000 / frameSize // bits_to_bitrate()
	channels := e.channels
	if channels < 1 {
		channels = 1
	}

	// Per-channel rate for table lookup
	ratePerChannel := totalRate / channels

	// Determine table entry based on frame size and FEC.
	entry := 1 // 10ms no FEC
	if frameSize == 960 {
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
	celtBitrate = e.bitrate - silkBitrate

	// Ensure minimum CELT bitrate for acceptable quality.
	minCeltBitrate := 2000 * channels
	if celtBitrate < minCeltBitrate {
		celtBitrate = minCeltBitrate
		silkBitrate = e.bitrate - celtBitrate
		celtBitrateHBGain = totalRate - silkBitrate
	}
	return silkBitrate, celtBitrate, celtBitrateHBGain
}

// computeSilkRateForMax computes the SILK rate corresponding to a maximum available
// bitrate. This is used to constrain SILK's maxBits in hybrid VBR mode.
// Matches libopus: compute_silk_rate_for_hybrid(maxBits*Fs/frame_size, ...).
func (e *Encoder) computeSilkRateForMax(maxBitrate int, frame20ms bool) int {
	channels := e.channels
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
func (e *Encoder) computeHBGain(celtBitrate int) float64 {
	expArg := -float32(celtBitrate) * (1.0 / 1024.0)
	return 1.0 - float64(celtExp2Approx(expArg))
}

func celtExp2Approx(x float32) float32 {
	integer := int32(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)

	res := hbExp2CoeffA0 + frac*(hbExp2CoeffA1+
		frac*(hbExp2CoeffA2+
			frac*(hbExp2CoeffA3+
				frac*(hbExp2CoeffA4+
					frac*hbExp2CoeffA5))))

	bits := math.Float32bits(res)
	bits = uint32(int32(bits)+int32(uint32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

const (
	hbExp2CoeffA0 float32 = 9.999999403953552246093750000000e-01
	hbExp2CoeffA1 float32 = 6.931530833244323730468750000000e-01
	hbExp2CoeffA2 float32 = 2.401536107063293457031250000000e-01
	hbExp2CoeffA3 float32 = 5.582631751894950866699218750000e-02
	hbExp2CoeffA4 float32 = 8.989339694380760192871093750000e-03
	hbExp2CoeffA5 float32 = 1.877576694823801517486572265625e-03
)

// downsample48to16Hybrid downsamples from 48kHz to 16kHz using the
// libopus-matching SILK downsampler (AR2 + FIR).
func (e *Encoder) downsample48to16Hybrid(samples []float64, frameSize int) []float32 {
	if len(samples) == 0 || frameSize <= 0 {
		return nil
	}

	targetSamples := frameSize / 3 // 48kHz -> 16kHz
	if targetSamples <= 0 {
		return nil
	}

	e.ensureSILKResampler(16000)

	if e.channels == 1 {
		// Mono: convert float64 -> float32 into scratch buffer.
		if frameSize > len(samples) {
			frameSize = len(samples)
		}
		pcm32 := e.scratchPCM32[:frameSize]
		_ = pcm32[frameSize-1]   // BCE hint
		_ = samples[frameSize-1] // BCE hint
		for i := 0; i < frameSize; i++ {
			pcm32[i] = float32(samples[i])
		}
		out := e.ensureSilkResampled(targetSamples)
		n := e.silkResampler.ProcessInto(pcm32, out)
		return out[:n]
	}

	// Stereo: convert float64 -> float32 and deinterleave in a single pass.
	// This avoids a separate full-buffer conversion + separate deinterleave loop.
	totalSamples := frameSize * 2
	if totalSamples > len(samples) {
		totalSamples = len(samples)
		frameSize = totalSamples / 2
	}
	left := e.scratchLeft[:frameSize]
	right := e.scratchRight[:frameSize]
	// Trim samples to exact stereo length for BCE elimination.
	stereoSamples := samples[:frameSize*2]
	for i := 0; i < frameSize; i++ {
		// Use two-element sub-slice to prove both accesses in bounds with one check.
		pair := stereoSamples[i*2 : i*2+2 : i*2+2]
		left[i] = float32(pair[0])
		right[i] = float32(pair[1])
	}
	leftOut := e.ensureSilkResampled(targetSamples)
	rightOut := e.ensureSilkResampledR(targetSamples)
	nL := e.silkResampler.ProcessInto(left, leftOut)
	nR := e.silkResamplerRight.ProcessInto(right, rightOut)
	n := nL
	if nR < n {
		n = nR
	}
	if n <= 0 {
		return nil
	}

	interleaved := e.scratchPCM32[:n*2]
	leftOut = leftOut[:n]
	rightOut = rightOut[:n]
	for i := 0; i < n; i++ {
		// Use two-element sub-slice to prove both accesses in bounds with one check.
		pair := interleaved[i*2 : i*2+2 : i*2+2]
		pair[0] = leftOut[i]
		pair[1] = rightOut[i]
	}

	return interleaved
}

// applyHBGainFade applies HB_gain to the CELT input with smooth gain fading.
// This implements libopus gain_fade() for artifact-free transitions.
//
// IMPORTANT: This function does NOT add any delay. The CELT input is already
// delay-compensated by applyDelayCompensation (Fs/250 = 192 samples at 48kHz).
// In libopus, gain_fade operates in-place on pcm_buf which already contains
// the delay-compensated samples.
func (e *Encoder) applyHBGainFade(pcm []float64, hbGain float64) []float64 {
	// Apply gain fade if gain changed
	prevGain := e.hybridState.prevHBGain
	if prevGain != hbGain {
		pcm = e.applyGainFade(pcm, prevGain, hbGain)
	} else if hbGain < 1.0 {
		// Apply constant gain if less than 1.0
		g := float32(hbGain)
		for i := range pcm {
			pcm[i] = float64(float32(pcm[i]) * g)
		}
	}

	return pcm
}

// applyGainFade applies a smooth window-based transition between two gain values.
// This implements libopus gain_fade() for seamless frame boundaries.
func (e *Encoder) applyGainFade(samples []float64, g1, g2 float64) []float64 {
	channels := e.channels
	frameSize := len(samples) / channels
	overlap := hybridOverlap

	if overlap > frameSize {
		overlap = frameSize
	}

	// Generate CELT window for smooth transition
	window := celt.GetWindow()
	if window == nil || len(window) < overlap {
		// Fallback: use simple linear fade
		return e.applyLinearGainFade(samples, g1, g2, overlap)
	}

	// Apply windowed gain fade during overlap region
	if channels == 1 {
		g1f := float32(g1)
		g2f := float32(g2)
		for i := 0; i < overlap; i++ {
			w := float32(window[i])
			w2 := w * w // Square the window (libopus does this)
			g := g1f*(1-w2) + g2f*w2
			samples[i] = float64(float32(samples[i]) * g)
		}
		// Apply constant g2 for rest of frame
		for i := overlap; i < frameSize; i++ {
			samples[i] = float64(float32(samples[i]) * g2f)
		}
	} else {
		g1f := float32(g1)
		g2f := float32(g2)
		for i := 0; i < overlap; i++ {
			w := float32(window[i])
			w2 := w * w
			g := g1f*(1-w2) + g2f*w2
			samples[i*2] = float64(float32(samples[i*2]) * g)
			samples[i*2+1] = float64(float32(samples[i*2+1]) * g)
		}
		for i := overlap; i < frameSize; i++ {
			samples[i*2] = float64(float32(samples[i*2]) * g2f)
			samples[i*2+1] = float64(float32(samples[i*2+1]) * g2f)
		}
	}

	return samples
}

// applyStereoWidthFade applies stereo width reduction with smooth transition.
// This mirrors libopus stereo_fade() for hybrid/CELT preprocessing.
func (e *Encoder) applyStereoWidthFade(samples []float64, widthQ14Prev, widthQ14 int) []float64 {
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
	g1 := 1.0 - float64(widthQ14Prev)/16384.0
	g2 := 1.0 - float64(widthQ14)/16384.0

	overlap := hybridOverlap
	if overlap > frameSize {
		overlap = frameSize
	}

	window := celt.GetWindow()
	if window == nil || len(window) < overlap {
		// Fallback: no window available, apply constant g2
		for i := 0; i < frameSize; i++ {
			diff := 0.5 * (samples[i*2] - samples[i*2+1])
			diff *= g2
			samples[i*2] -= diff
			samples[i*2+1] += diff
		}
		return samples
	}

	for i := 0; i < overlap; i++ {
		w := window[i]
		w2 := w * w
		g := g1*(1.0-w2) + g2*w2
		diff := 0.5 * (samples[i*2] - samples[i*2+1])
		diff *= g
		samples[i*2] -= diff
		samples[i*2+1] += diff
	}
	for i := overlap; i < frameSize; i++ {
		diff := 0.5 * (samples[i*2] - samples[i*2+1])
		diff *= g2
		samples[i*2] -= diff
		samples[i*2+1] += diff
	}

	return samples
}

// applyLinearGainFade applies a simple linear crossfade between gains.
// Used as fallback when window is not available.
func (e *Encoder) applyLinearGainFade(samples []float64, g1, g2 float64, overlap int) []float64 {
	channels := e.channels
	frameSize := len(samples) / channels

	for i := 0; i < overlap; i++ {
		t := float64(i) / float64(overlap)
		g := g1*(1-t) + g2*t

		for c := 0; c < channels; c++ {
			samples[i*channels+c] *= g
		}
	}

	// Apply constant g2 for rest of frame
	for i := overlap; i < frameSize; i++ {
		for c := 0; c < channels; c++ {
			samples[i*channels+c] *= g2
		}
	}

	return samples
}

// encodeSILKHybrid encodes SILK data for hybrid mode.
// Uses the SILK encoder's EncodeFrame method with a shared range encoder.
//
// SILK supports both 10ms and 20ms frames. Hybrid packets should encode the
// low band at the same duration as the Opus frame (no buffering).
//
// For stereo, uses mid-side encoding per RFC 6716 Section 4.2.8:
// - Encode stereo prediction weights
// - Encode mid channel with main SILK encoder
// - Encode side channel with side SILK encoder
func (e *Encoder) encodeSILKHybrid(pcm []float32, lookahead []float32, frameSize int, totalRateBps int) {
	// For hybrid mode, SILK always operates at WB (16kHz)
	// The input is already downsampled to 16kHz

	// Calculate samples at 16kHz (input is at 16kHz after downsampling)
	silkSamples := frameSize / 3 // 48kHz -> 16kHz (160 for 10ms, 320 for 20ms)

	if e.channels == 1 {
		// Mono encoding
		e.encodeSILKHybridMono(pcm, lookahead, silkSamples, totalRateBps)
	} else {
		// Stereo encoding
		e.encodeSILKHybridStereo(pcm, lookahead, silkSamples, totalRateBps)
	}
}

// encodeSILKHybridMono encodes mono SILK data for hybrid mode.
//
// Per RFC 6716, the SILK layer header contains:
// 1. VAD flag for each frame (1 bit per frame)
// 2. LBRR flag (1 bit)
// 3. [LBRR data if LBRR flag set]
// 4. Frame data
func (e *Encoder) encodeSILKHybridMono(pcm []float32, lookahead []float32, silkSamples int, totalRateBps int) {
	if e.hybridState != nil {
		e.hybridState.silkStereoWidthQ14 = 16384
	}
	if totalRateBps > 0 {
		e.silkEncoder.SetBitrate(totalRateBps)
	}
	inputSamples := pcm[:min(len(pcm), silkSamples)]
	// Match standalone SILK mono buffering: encoder consumes inputBuf+1 with
	// a 1-sample handoff across frames.
	inputSamples = e.alignSilkMonoInput(inputSamples)
	vadFlag := e.computeSilkVAD(inputSamples, len(inputSamples), 16)
	e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityBandsQ15)
	lbrrFlag := false
	if e.lbrrCoded {
		lbrrFlag = e.silkEncoder.HasLBRRData()
	}

	// Get the shared range encoder
	re := e.silkEncoder.GetRangeEncoderPtr()
	if re == nil {
		// Fall back to normal encoding if no shared encoder
		_ = e.silkEncoder.EncodeFrame(inputSamples, lookahead, vadFlag)
		return
	}

	// Header bits to patch at packet start
	nFramesPerPacket := 1 // One SILK frame per packet (10ms or 20ms)
	nChannels := 1        // mono
	nBitsHeader := (nFramesPerPacket + 1) * nChannels

	// Reserve header bits (VAD + LBRR) and encode any LBRR data from previous packet.
	// This must always be called to ensure PatchInitialBits overwrites reserved bits
	// instead of corrupting the frame payload.
	e.silkEncoder.EncodeLBRRData(re, 1, true)

	// Encode the frame (EncodeFrame in hybrid mode skips its own VAD/LBRR)
	_ = e.silkEncoder.EncodeFrame(inputSamples, lookahead, vadFlag)

	// Patch initial bits with actual VAD+LBRR flags
	// Format: [VAD][LBRR]
	flags := uint32(0)
	if vadFlag {
		flags |= 1 << 1
	}
	if lbrrFlag {
		flags |= 1 << 0
	}
	re.PatchInitialBits(flags, uint(nBitsHeader))

	// Match libopus enc_API packet-level nBitsExceeded update for shared range coding.
	payloadSizeMs := (silkSamples * 1000) / 16000
	nBytesOut := (re.Tell() + 7) >> 3
	e.silkEncoder.UpdatePacketBitsExceeded(nBytesOut, payloadSizeMs, totalRateBps)
}

// encodeSILKHybridStereo encodes stereo SILK data for hybrid mode.
// Uses mid-side encoding per RFC 6716 Section 4.2.8.
func (e *Encoder) encodeSILKHybridStereo(pcm []float32, lookahead []float32, silkSamples int, totalRateBps int) {
	// Deinterleave L/R channels and append 2-sample lookahead for LP filtering.
	actualSamples := len(pcm) / 2
	if actualSamples < silkSamples {
		silkSamples = actualSamples
	}

	left := e.scratchLeft[:silkSamples+2]
	right := e.scratchRight[:silkSamples+2]
	for i := 0; i < silkSamples && i*2+1 < len(pcm); i++ {
		left[i] = pcm[i*2]
		right[i] = pcm[i*2+1]
	}
	// Use lookahead if provided, otherwise zero-pad.
	if len(lookahead) >= 2 {
		left[silkSamples] = lookahead[0]
		right[silkSamples] = lookahead[1]
		if len(lookahead) >= 4 {
			left[silkSamples+1] = lookahead[2]
			right[silkSamples+1] = lookahead[3]
		} else {
			left[silkSamples+1] = left[silkSamples]
			right[silkSamples+1] = right[silkSamples]
		}
	} else {
		lastL := float32(0)
		lastR := float32(0)
		if silkSamples > 0 {
			lastL = left[silkSamples-1]
			lastR = right[silkSamples-1]
		}
		left[silkSamples] = lastL
		right[silkSamples] = lastR
		left[silkSamples+1] = lastL
		right[silkSamples+1] = lastR
	}

	// Convert to mid-side with libopus-aligned stereo front-end.
	fsKHz := 16 // SILK wideband uses 16kHz
	mid, side, predIdx, midOnly, midRate, sideRate, widthQ14 := e.silkEncoder.StereoLRToMSWithRates(
		left, right, silkSamples, fsKHz, totalRateBps, e.lastVADActivityQ8, false,
	)
	if e.hybridState != nil {
		e.hybridState.silkStereoWidthQ14 = int(widthQ14)
	}
	// Apply per-channel split from stereo front-end before encoding.
	if midRate > 0 {
		e.silkEncoder.SetBitrate(midRate)
	}
	if e.silkSideEncoder != nil && sideRate > 0 {
		e.silkSideEncoder.SetBitrate(sideRate)
	}

	// Compute VAD flags
	vadMid := e.computeSilkVAD(mid, len(mid), fsKHz)
	e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityBandsQ15)

	vadSide := false
	if !midOnly {
		vadSide = e.computeSilkVADSide(side, len(side), fsKHz)
	}
	if e.silkSideEncoder != nil {
		if e.silkVADSide != nil {
			e.silkSideEncoder.SetVADState(e.silkVADSide.SpeechActivityQ8, e.silkVADSide.InputTiltQ15, e.silkVADSide.InputQualityBandsQ15)
		} else {
			e.silkSideEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityBandsQ15)
		}
	}

	// Get shared range encoder
	re := e.silkEncoder.GetRangeEncoderPtr()
	if re == nil {
		return
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.SetRangeEncoder(re)
		// Keep side packet bit-reservoir state aligned with the shared SILK packet state.
		e.silkSideEncoder.SetBitsExceeded(e.silkEncoder.BitsExceeded())
	}

	// LBRR flags
	lbrrMid := false
	lbrrSide := false
	if e.lbrrCoded {
		lbrrMid = e.silkEncoder.HasLBRRData()
		if e.silkSideEncoder != nil && !midOnly {
			lbrrSide = e.silkSideEncoder.HasLBRRData()
		}
	}

	// Header bits to patch at packet start (VAD/LBRR)
	nBitsHeader := 2

	// 1. Reserve header bits (VAD + LBRR) and encode any LBRR Mid data.
	// Use nChannels=2 to reserve space for both Mid+Side flags.
	e.silkEncoder.EncodeLBRRData(re, 2, true)

	// 2. Encode LBRR Side (no header placeholder; already reserved).
	if e.lbrrCoded && e.silkSideEncoder != nil && !midOnly {
		e.silkSideEncoder.EncodeLBRRData(re, 1, false)
	}

	// 3. Encode Weights (pre-quantized indices)
	silk.EncodeStereoIndices(re, predIdx)

	// 3b. Encode mid-only flag when side VAD is inactive (libopus stereo flag).
	if !vadSide {
		if midOnly {
			silk.EncodeStereoMidOnly(re, 1)
		} else {
			silk.EncodeStereoMidOnly(re, 0)
		}
	}

	// 4. Encode Mid Frame
	_ = e.silkEncoder.EncodeFrame(mid, nil, vadMid)

	// 5. Encode Side Frame (skip if mid-only)
	if e.silkSideEncoder != nil && !midOnly {
		if e.hybridState != nil && e.hybridState.prevDecodeOnlyMiddle {
			e.silkSideEncoder.Reset()
		}
		_ = e.silkSideEncoder.EncodeFrame(side, nil, vadSide)
	}

	// 6. Patch both headers at once (Mid first, then Side).
	flagsMid := uint32(0)
	if vadMid {
		flagsMid |= 1 << 1
	}
	if lbrrMid {
		flagsMid |= 1 << 0
	}
	flagsSide := uint32(0)
	if vadSide {
		flagsSide |= 1 << 1
	}
	if lbrrSide {
		flagsSide |= 1 << 0
	}
	flagsCombined := (flagsMid << 2) | flagsSide
	re.PatchInitialBits(flagsCombined, uint(nBitsHeader*2))

	// Match libopus enc_API packet-level nBitsExceeded update for shared range coding.
	payloadSizeMs := (silkSamples * 1000) / 16000
	nBytesOut := (re.Tell() + 7) >> 3
	e.silkEncoder.UpdatePacketBitsExceeded(nBytesOut, payloadSizeMs, totalRateBps)
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.SetBitsExceeded(e.silkEncoder.BitsExceeded())
	}

	if e.hybridState != nil {
		e.hybridState.prevDecodeOnlyMiddle = midOnly
	}
}

// computeEquivRate computes libopus-style equivalent bitrate for stereo width decisions.
func computeEquivRate(bitrate, channels, frameRate int, vbr bool, mode Mode, complexity int, loss int) int {
	equiv := bitrate
	if frameRate > 50 {
		equiv -= (40*channels + 20) * (frameRate - 50)
	}
	if !vbr {
		equiv -= equiv / 12
	}
	equiv = equiv * (90 + complexity) / 100
	switch mode {
	case ModeSILK, ModeHybrid:
		if complexity < 2 {
			equiv = equiv * 4 / 5
		}
		equiv -= equiv * loss / (6*loss + 10)
	case ModeCELT:
		if complexity < 5 {
			equiv = equiv * 9 / 10
		}
	default:
		equiv -= equiv * loss / (12*loss + 20)
	}
	return equiv
}

// computeStereoWidthQ14 computes target stereo width from equivalent bitrate.
// Matches libopus logic in opus_encoder.c around stereo width reduction.
func computeStereoWidthQ14(equivRate int) int {
	switch {
	case equivRate > 32000:
		return 16384
	case equivRate < 16000:
		return 0
	default:
		den := equivRate - 14000
		if den <= 0 {
			return 0
		}
		return 16384 - 2048*(32000-equivRate)/den
	}
}

// celtBandwidthFromTypes maps types.Bandwidth to CELT bandwidth.
func celtBandwidthFromTypes(bw types.Bandwidth) celt.CELTBandwidth {
	switch bw {
	case types.BandwidthNarrowband:
		return celt.CELTNarrowband
	case types.BandwidthMediumband:
		return celt.CELTMediumband
	case types.BandwidthWideband:
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
// silkSignalType and silkOffset are the SILK encoder's signal classification,
// used for VBR target adjustment per libopus celt_encoder.c line 2463-2475.
func (e *Encoder) encodeCELTHybridImproved(pcm []float64, frameSize int, targetPayloadBytes int, silkSignalType, silkOffset int) {
	// Set hybrid mode flag on CELT encoder
	e.celtEncoder.SetHybrid(true)
	e.celtEncoder.SetPrediction(e.celtPredictionModeForFrame())
	callFrame := e.celtEncoder.FrameCount()

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
	totalBits := targetPayloadBytes * 8
	if used := re.Tell(); totalBits < used+8 {
		// Ensure we don't end up with negative budgets if SILK used more bits.
		totalBits = used + 8
	}
	// Match libopus quant_coarse_energy() nbAvailableBytes for hybrid CBR:
	// bytes available to CELT at entry (after already-coded SILK/range bits).
	e.celtEncoder.SetCoarseEnergyAvailableBytes(0)
	if e.bitrateMode == ModeCBR {
		nbFilledBytes := (re.Tell() + 4) >> 3
		nbAvailableBytes := targetPayloadBytes - nbFilledBytes
		if nbAvailableBytes < 0 {
			nbAvailableBytes = 0
		}
		e.celtEncoder.SetCoarseEnergyAvailableBytes(nbAvailableBytes)
	}
	defer e.celtEncoder.SetCoarseEnergyAvailableBytes(0)

	// Mirror libopus effectiveBytes staging for hybrid before transient analysis.
	// This feeds low-bitrate weak-transient behavior in celt_encoder.c.
	effectiveBytes := 0
	if e.celtEncoder.VBR() {
		baseBits := e.celtEncoder.BitrateToBits(frameSize)
		effectiveBytes = baseBits / 8
	} else {
		nbFilledBytes := (re.Tell() + 4) >> 3
		effectiveBytes = targetPayloadBytes - nbFilledBytes
		if effectiveBytes < 0 {
			effectiveBytes = 0
		}
	}
	allowWeakTransients := effectiveBytes < 15 && silkSignalType != 2

	// Hybrid CELT only encodes bands starting at HybridCELTStartBand.
	start := celt.HybridCELTStartBand
	bw := celtBandwidthFromTypes(e.effectiveBandwidth())
	end := celt.EffectiveBandsForFrameSize(bw, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	if end < start {
		end = start
	}
	nbBands := end

	// Transient analysis (pre-MDCT) to decide short blocks and tf metrics.
	transient, weakTransient, tfEstimate, toneFreq, toneishness, shortBlocks, bandLogE2 := e.celtEncoder.TransientAnalysisHybrid(
		preemph, frameSize, nbBands, lm, allowWeakTransients,
	)
	maybeLogHybridStageDebug(callFrame, "transient_in", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, false, int(celt.SpreadNormal), "")

	// Apply hybrid CELT VBR target adjustment based on SILK signal info.
	// Per libopus celt_encoder.c line 2463-2475:
	// - Tonal frames (offset < 100) get more bits
	// - Noisy frames (offset > 100) get fewer bits
	// - tf_estimate-based transient boost
	// The shift (3-LM) scales the adjustment to the frame size.
	if e.bitrateMode != ModeCBR {
		shift := 3 - lm
		if shift < 0 {
			shift = 0
		}
		if silkOffset < 100 {
			totalBits += 12 >> shift // Tonal boost: +12 bits for 20ms, +6 for 10ms
		}
		if silkOffset > 100 {
			totalBits -= 18 >> shift // Noise reduction: -18 bits for 20ms, -9 for 10ms
		}
		// Transient/vowel temporal spike boost (libopus line 2470).
		// (tf_estimate - 0.25) * 50 bits, where tf_estimate is [0, 1].
		tfAdj := int((tfEstimate - 0.25) * 50.0)
		totalBits += tfAdj
		// Minimum target for strong transients (libopus line 2473).
		// Ensure at least 50 bits for CELT when tf_estimate > 0.7.
		silkUsedBits := re.Tell()
		if tfEstimate > 0.7 && totalBits-silkUsedBits < 50 {
			totalBits = silkUsedBits + 50
		}
		// Don't let adjustment make totalBits negative or below SILK usage.
		if totalBits < silkUsedBits+8 {
			totalBits = silkUsedBits + 8
		}
	}

	// Compute MDCT with overlap history using the selected block size.
	if tmpHybridMDCTInDebugEnabled && e.channels == 1 {
		target := tmpHybridMDCTCall
		if target < 0 || target == callFrame {
			overlap := celt.Overlap
			if overlap > frameSize {
				overlap = frameSize
			}
			if overlap > len(e.celtEncoder.OverlapBuffer()) {
				overlap = len(e.celtEncoder.OverlapBuffer())
			}
			fmt.Fprintf(os.Stderr, "GOMDCTIN call=%d hist=", callFrame)
			for i := 0; i < overlap && i < 32; i++ {
				fmt.Fprintf(os.Stderr, " %.9f", float32(e.celtEncoder.OverlapBuffer()[i]))
			}
			fmt.Fprintf(os.Stderr, " cur=")
			for i := 0; i < len(preemph) && i < 32; i++ {
				fmt.Fprintf(os.Stderr, " %.9f", float32(preemph[i]))
			}
			fmt.Fprintf(os.Stderr, " tail=")
			start := len(preemph) - 32
			if start < 0 {
				start = 0
			}
			for i := start; i < len(preemph); i++ {
				fmt.Fprintf(os.Stderr, " %.9f", float32(preemph[i]))
			}
			fmt.Fprintln(os.Stderr)
		}
	}

	mdctCoeffs := computeMDCTForHybridScratch(preemph, frameSize, e.channels, e.celtEncoder.OverlapBuffer(), shortBlocks, e.hybridState, e.celtEncoder)
	if len(mdctCoeffs) == 0 {
		return
	}
	// Keep float-path cadence aligned with libopus (opus_res/celt_sig are float).
	e.celtEncoder.RoundFloat64ToFloat32(mdctCoeffs)
	if tmpHybridMDCTDebugEnabled && e.channels == 1 {
		target := tmpHybridMDCTCall
		if target < 0 || target == callFrame {
			b17 := celt.EBands[17] << lm
			b18 := celt.EBands[18] << lm
			b19 := celt.EBands[19] << lm
			if b17 >= 0 && b19 <= len(mdctCoeffs) {
				fmt.Fprintf(os.Stderr, "GOMDCT call=%d b17=", callFrame)
				for i := b17; i < b18; i++ {
					fmt.Fprintf(os.Stderr, " %.9f", float32(mdctCoeffs[i]))
				}
				fmt.Fprintf(os.Stderr, " b18=")
				for i := b18; i < b19; i++ {
					fmt.Fprintf(os.Stderr, " %.9f", float32(mdctCoeffs[i]))
				}
				fmt.Fprintln(os.Stderr)
			}
		}
	}

	// Compute band energies
	energies := e.celtEncoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	e.celtEncoder.RoundFloat64ToFloat32(energies)
	if tmpHybridAMPDebugEnabled && e.channels == 1 {
		for band := 14; band <= 18 && band < len(energies); band++ {
			fmt.Fprintf(os.Stderr, "GOAMP call=%d i=%d logE=%.9f\n", callFrame, band, energies[band])
		}
	}
	if bandLogE2 == nil {
		if cap(e.hybridState.scratchBandLogE2) < len(energies) {
			e.hybridState.scratchBandLogE2 = make([]float64, len(energies))
		}
		bandLogE2 = e.hybridState.scratchBandLogE2[:len(energies)]
		copy(bandLogE2, energies)
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
	var normL, normR, bandE []float64
	if e.channels == 1 {
		normL, bandE = e.celtEncoder.NormalizeBandsToArrayMonoWithBandE(mdctCoeffs, nbBands, frameSize)
	} else {
		if len(mdctCoeffs) < frameSize*2 {
			return
		}
		mdctLeft := mdctCoeffs[:frameSize]
		mdctRight := mdctCoeffs[frameSize:]
		normL, normR, bandE = e.celtEncoder.NormalizeBandsToArrayStereoWithBandE(mdctLeft, mdctRight, nbBands, frameSize)
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
	} else if lm >= 1 {
		if transient {
			transientGotDisabled = true
		}
		transient = false
		shortBlocks = 1
	}
	maybeLogHybridStageDebug(callFrame, "transient_flag", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, false, int(celt.SpreadNormal), "")

	// Snapshot previous frame energies for dynalloc/coarse state decisions.
	prevEnergyLen := len(e.celtEncoder.PrevEnergy())
	if cap(e.hybridState.scratchPrevEnergy) < prevEnergyLen {
		e.hybridState.scratchPrevEnergy = make([]float64, prevEnergyLen)
	}
	prevEnergy := e.hybridState.scratchPrevEnergy[:prevEnergyLen]
	copy(prevEnergy, e.celtEncoder.PrevEnergy())

	// dynalloc_analysis in libopus uses pre-stabilization energies.
	if cap(e.hybridState.scratchAnalysisE) < len(energies) {
		e.hybridState.scratchAnalysisE = make([]float64, len(energies))
	}
	analysisEnergies := e.hybridState.scratchAnalysisE[:len(energies)]
	copy(analysisEnergies, energies)

	oldBandE := prevEnergy
	if maxLen := nbBands * e.channels; maxLen > 0 && len(oldBandE) > maxLen {
		oldBandE = oldBandE[:maxLen]
	}

	// Update tonality analysis for next frame's VBR decisions.
	e.celtEncoder.UpdateTonalityAnalysisHybrid(normL, analysisEnergies, nbBands, frameSize)

	// Compute dynalloc analysis for TF/spread and offsets.
	lsbDepth := e.lsbDepth

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
	var tfRes []int
	tfRes = e.celtEncoder.TFResScratch(nbBands)
	tfSelect := 0
	if weakTransient {
		// libopus hybrid weak-transient override (celt_encoder.c line ~2261).
		for i := 0; i < end && i < len(tfRes); i++ {
			tfRes[i] = 1
		}
		tfSelect = 0
	} else if allowWeakTransients {
		// Low bitrate hybrid with non-voiced signal: force 5ms resolution.
		// Per libopus line 2269-2274.
		for i := 0; i < end && i < len(tfRes); i++ {
			tfRes[i] = 0
		}
		if transient {
			tfSelect = 1
		}
	} else {
		// Default hybrid TF: all bands follow the transient flag.
		// Per libopus line 2276-2278.
		for i := 0; i < end && i < len(tfRes); i++ {
			if transient {
				tfRes[i] = 1
			} else {
				tfRes[i] = 0
			}
		}
		tfSelect = 0
	}
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
	maybeLogHybridStageDebug(callFrame, "intra_flag", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(celt.SpreadNormal), "")

	// Encode coarse energy.
	quantizedEnergies := e.celtEncoder.EncodeCoarseEnergyRange(energies, start, end, intra, lm)
	maybeLogHybridStageDebug(callFrame, "coarse", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(celt.SpreadNormal), "")

	celt.TFEncodeWithSelect(re, start, end, transient, tfRes, lm, tfSelect)
	maybeLogHybridStageDebug(callFrame, "tf", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(celt.SpreadNormal), "")

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
	maybeLogHybridStageDebug(callFrame, "spread", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

	// Initialize caps and offsets for allocation (hybrid bands only).
	caps := e.celtEncoder.CapsScratch(nbBands)
	celt.InitCapsInto(caps, nbBands, lm, e.channels)
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
		width := e.channels * celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << celt.BitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << celt.BitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0
		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<celt.BitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
			flag := 0
			if j < offsets[i] {
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
		if boost > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		offsets[i] = boost
	}
	maybeLogHybridStageDebug(callFrame, "dynalloc", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

	allocTrim := 5
	if tellFracDynalloc+(6<<celt.BitRes) <= totalBitsQ3ForDynalloc-totalBoost {
		// In hybrid mode start>0, so libopus keeps alloc_trim fixed at 5 and
		// does not run alloc_trim_analysis().
		re.EncodeICDF(allocTrim, celt.TrimICDF, 7)
	}
	maybeLogHybridStageDebug(callFrame, "trim", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

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
	// Match libopus: equiv_rate is derived from the total compressed frame bytes
	// (nbCompressedBytes), not the post-header effective bytes.
	nbCompressedBytesForEquiv := (totalBits + 7) >> 3
	equivRate := celt.ComputeEquivRate(nbCompressedBytesForEquiv, e.channels, lm, e.celtEncoder.Bitrate())
	signalBandwidth := e.celtEncoder.SignalBandwidthForAllocation(nbBands, equivRate)

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
	maybeLogHybridStageDebug(callFrame, "alloc", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

	// Encode fine energy using libopus residual state (error[]).
	e.celtEncoder.EncodeFineEnergyRangeFromError(quantizedEnergies, start, end, allocResult.FineBits)
	maybeLogHybridStageDebug(callFrame, "fine", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

	// Encode bands (PVQ quant_all_bands).
	totalBitsAllQ3 := (totalBits << celt.BitRes) - antiCollapseRsv
	dualStereoVal := 0
	if allocResult.DualStereo {
		dualStereoVal = 1
	}
	tapset := e.celtEncoder.TapsetDecision()
	rng := e.celtEncoder.RNG()
	e.celtEncoder.PreparePVQDebugFrame(callFrame)
	e.celtEncoder.QuantAllBandsEncodeScratch(
		re,
		e.channels,
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
	maybeLogHybridStageDebug(callFrame, "pvq", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

	// Encode anti-collapse flag if reserved.
	if antiCollapseRsv > 0 {
		antiCollapseOn := 0
		if e.celtEncoder.ConsecTransient() < 2 {
			antiCollapseOn = 1
		}
		re.EncodeRawBits(uint32(antiCollapseOn), 1)
	}
	maybeLogHybridStageDebug(callFrame, "anticollapse", re.Tell(), re.TellFrac(), totalBits, totalBits-re.Tell(), start, end, transient, intra, int(spread), "")

	// Encode energy finalization bits (leftover budget).
	bitsLeft := totalBits - re.Tell()
	if bitsLeft < 0 {
		bitsLeft = 0
	}
	e.celtEncoder.EncodeEnergyFinaliseRangeFromError(quantizedEnergies, start, end, allocResult.FineBits, allocResult.FinePriority, bitsLeft)
	maybeLogHybridStageDebug(callFrame, "finalise", re.Tell(), re.TellFrac(), totalBits, bitsLeft, start, end, transient, intra, int(spread), "")
	e.celtEncoder.UpdateEnergyErrorHybridFromError(start, end, nbBands)
	e.celtEncoder.UpdateHybridPrefilterHistory(preemph, frameSize)

	// Update state: prev energy, RNG, frame count, transient history.
	if cap(e.hybridState.scratchNextEnergy) < len(prevEnergy) {
		e.hybridState.scratchNextEnergy = make([]float64, len(prevEnergy))
	}
	nextEnergy := e.hybridState.scratchNextEnergy[:len(prevEnergy)]
	// Match libopus oldBandE cadence: bands outside [start,end) are reset.
	for i := range nextEnergy {
		nextEnergy[i] = 0
	}
	for c := 0; c < e.channels; c++ {
		base := c * celt.MaxBands
		for band := start; band < end; band++ {
			idx := c*nbBands + band
			if idx < len(quantizedEnergies) && base+band < len(nextEnergy) {
				nextEnergy[base+band] = quantizedEnergies[idx]
			}
		}
	}
	e.celtEncoder.SetPrevEnergyWithPrev(prevEnergy, nextEnergy)
	e.celtEncoder.SetRNG(re.Range())
	e.celtEncoder.IncrementFrameCount()
	e.celtEncoder.UpdateConsecTransientWithDisabled(transient, transientGotDisabled)
}

// computeMDCTForHybridScratch computes MDCT for hybrid mode encoding using scratch buffers.
// ce provides the CELT encoder's scratch buffers for the MDCT transform.
// hs provides hybrid-specific scratch buffers for deinterleaving and assembly.
func computeMDCTForHybridScratch(samples []float64, frameSize, channels int, history []float64, shortBlocks int, hs *HybridState, ce *celt.Encoder) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := celt.Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	if channels == 1 {
		if len(history) >= overlap {
			// Match regular CELT path: round overlap history to float32 cadence.
			needed := overlap + len(samples)
			if cap(hs.scratchMDCTInput) < needed {
				hs.scratchMDCTInput = make([]float64, needed)
			}
			input := hs.scratchMDCTInput[:needed]
			for i := 0; i < overlap; i++ {
				input[i] = float64(float32(history[i]))
			}
			copy(input[overlap:], samples)
			if shortBlocks > 1 {
				return ce.MDCTShortScratch(input, shortBlocks)
			}
			return ce.MDCTScratch(input)
		}
		// No history: zero-pad and compute
		needed := overlap + len(samples)
		if cap(hs.scratchMDCTInput) < needed {
			hs.scratchMDCTInput = make([]float64, needed)
		}
		input := hs.scratchMDCTInput[:needed]
		for i := 0; i < overlap; i++ {
			input[i] = 0
		}
		copy(input[overlap:], samples)
		if shortBlocks > 1 {
			return ce.MDCTShortScratch(input, shortBlocks)
		}
		return ce.MDCTScratch(input)
	}

	// Stereo: MDCT each channel separately (L/R) using scratch deinterleave buffers
	n := len(samples) / 2
	if cap(hs.scratchDeintLeft) < n {
		hs.scratchDeintLeft = make([]float64, n)
	}
	if cap(hs.scratchDeintRight) < n {
		hs.scratchDeintRight = make([]float64, n)
	}
	left := hs.scratchDeintLeft[:n]
	right := hs.scratchDeintRight[:n]
	celt.DeinterleaveStereoInto(samples, left, right)

	if len(history) >= overlap*2 {
		needed := overlap + n
		if cap(hs.scratchMDCTInput) < needed {
			hs.scratchMDCTInput = make([]float64, needed)
		}
		input := hs.scratchMDCTInput[:needed]
		// Left channel with rounded overlap history.
		for i := 0; i < overlap; i++ {
			input[i] = float64(float32(history[i]))
		}
		copy(input[overlap:], left)
		var mdctLeft, mdctRight []float64
		if shortBlocks > 1 {
			mdctLeft = ce.MDCTShortScratch(input, shortBlocks)
		} else {
			mdctLeft = ce.MDCTScratch(input)
		}
		leftLen := len(mdctLeft)
		resultLen := leftLen + n
		if cap(hs.scratchMDCTResult) < resultLen {
			hs.scratchMDCTResult = make([]float64, resultLen)
		}
		result := hs.scratchMDCTResult[:resultLen]
		copy(result[:leftLen], mdctLeft)

		// Right channel with rounded overlap history.
		for i := 0; i < overlap; i++ {
			input[i] = float64(float32(history[overlap+i]))
		}
		copy(input[overlap:], right)
		if shortBlocks > 1 {
			mdctRight = ce.MDCTShortScratch(input, shortBlocks)
		} else {
			mdctRight = ce.MDCTScratch(input)
		}
		copy(result[leftLen:], mdctRight)
		return result
	}

	// No history: zero-pad and compute each channel using L/R scratch methods
	needed := overlap + n
	if cap(hs.scratchMDCTInput) < needed {
		hs.scratchMDCTInput = make([]float64, needed)
	}
	input := hs.scratchMDCTInput[:needed]
	// Left channel
	for i := 0; i < overlap; i++ {
		input[i] = 0
	}
	copy(input[overlap:], left)
	var mdctLeft, mdctRight []float64
	if shortBlocks > 1 {
		mdctLeft = ce.MDCTShortScratch(input, shortBlocks)
	} else {
		mdctLeft = ce.MDCTScratch(input)
	}
	// Copy left result before computing right (they share the same mdctCoeffs scratch)
	leftLen := len(mdctLeft)
	rightLen := n // will be same size
	resultLen := leftLen + rightLen
	if cap(hs.scratchMDCTResult) < resultLen {
		hs.scratchMDCTResult = make([]float64, resultLen)
	}
	result := hs.scratchMDCTResult[:resultLen]
	copy(result[:leftLen], mdctLeft)

	// Right channel
	for i := 0; i < overlap; i++ {
		input[i] = 0
	}
	copy(input[overlap:], right)
	if shortBlocks > 1 {
		mdctRight = ce.MDCTShortScratch(input, shortBlocks)
	} else {
		mdctRight = ce.MDCTScratch(input)
	}
	copy(result[leftLen:], mdctRight)

	return result
}

// ComputeStereoWidth computes the stereo width for hybrid mode encoding.
// At low bitrates, stereo width is reduced to improve coding efficiency.
// This matches libopus compute_stereo_width().
func ComputeStereoWidth(pcm []float64, frameSize, channels int) float64 {
	if channels != 2 || len(pcm) < frameSize*2 {
		return 0.0
	}

	// Compute correlation between left and right channels
	var sumLeft, sumRight, sumCross float64
	for i := 0; i < frameSize; i++ {
		l := pcm[i*2]
		r := pcm[i*2+1]
		sumLeft += l * l
		sumRight += r * r
		sumCross += l * r
	}

	// Compute correlation coefficient
	if sumLeft < 1e-10 || sumRight < 1e-10 {
		return 0.0
	}

	correlation := sumCross / math.Sqrt(sumLeft*sumRight)

	// Convert correlation to stereo width
	// High correlation (mono-like) -> low width
	// Low correlation (wide stereo) -> high width
	width := math.Sqrt(0.5 * (1.0 - correlation*correlation))

	if width > 1.0 {
		width = 1.0
	}
	if width < 0.0 {
		width = 0.0
	}

	return width
}
