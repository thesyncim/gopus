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
	"math"

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

	// crossoverBuffer stores smoothed crossover-band energies (per channel)
	// to reduce frame-to-frame discontinuities at the SILK/CELT boundary.
	crossoverBuffer []float64

	// prevDecodeOnlyMiddle tracks the previous mid-only (no side) decision.
	prevDecodeOnlyMiddle bool

	// --- Scratch buffers for zero-allocation hybrid encoding ---

	// rangeEncoder is reused across frames to avoid heap allocation.
	rangeEncoder rangecoding.Encoder

	// scratchPacket is the shared range encoder output buffer.
	scratchPacket [maxHybridPacketSize]byte

	// Lookahead resampling scratch buffers.
	scratchLookahead32   []float32 // float64 -> float32 conversion
	scratchSilkLookahead []float32 // resampled lookahead output
	scratchLaLeft        []float32 // deinterleaved left lookahead
	scratchLaRight       []float32 // deinterleaved right lookahead
	scratchLaOutLeft     []float32 // resampled left lookahead
	scratchLaOutRight    []float32 // resampled right lookahead

	// Energy tracking scratch buffers.
	scratchBandLogE2  []float64 // bandLogE2 for transient analysis
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
	// Validate: only 480 (10ms) or 960 (20ms) for hybrid
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidHybridFrameSize
	}

	// Update Opus-level VAD activity (used to gate SILK VAD)
	e.updateOpusVAD(pcm, frameSize)

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
			prevHBGain:     1.0,
			stereoWidthQ14: 16384, // Full width (Q14 = 1.0)
		}
	}

	// Propagate bitrate mode to CELT encoder for hybrid mode.
	// Per libopus opus_encoder.c: in hybrid VBR mode, the CELT VBR constraint
	// is always disabled (OPUS_SET_VBR_CONSTRAINT(0)), regardless of whether
	// the outer encoder uses CVBR. The SILK portion handles rate control.
	switch e.bitrateMode {
	case ModeCBR:
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(false)
	case ModeCVBR, ModeVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false)
	}

	// Compute bit allocation between SILK and CELT
	frame20ms := frameSize == 960
	silkBitrate, celtBitrate := e.computeHybridBitAllocation(frame20ms)

	// Compute HB_gain based on CELT bitrate allocation
	// Lower CELT bitrate means we should attenuate high frequencies
	hbGain := e.computeHBGain(celtBitrate)

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

	maxTargetBytes := payloadTarget
	switch e.bitrateMode {
	case ModeCBR:
		maxTargetBytes = payloadTarget
	case ModeCVBR:
		maxAllowed := int(float64(baseTargetBytes) * (1 + CVBRTolerance))
		if maxAllowed < 2 {
			maxAllowed = 2
		}
		// Reserve one extra byte to account for range coder end bits.
		maxTargetBytes = maxAllowed - 2
	case ModeVBR:
		// Allow up to 2x target in VBR (matches libopus compute_vbr cap).
		maxAllowed := int(float64(baseTargetBytes) * 2.0)
		if maxAllowed < 2 {
			maxAllowed = 2
		}
		// Reserve one extra byte to account for range coder end bits.
		maxTargetBytes = maxAllowed - 2
	}
	if maxTargetBytes < payloadTarget {
		maxTargetBytes = payloadTarget
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
	e.silkEncoder.SetFEC(e.fecEnabled)
	e.silkEncoder.SetPacketLoss(e.packetLoss)

	// Per libopus: in hybrid CBR mode, SILK is switched to VBR with a max bits cap.
	// This allows SILK to use fewer bits and CELT to absorb the variation.
	// In hybrid VBR/CVBR mode, SILK's maxBits is constrained to the SILK-appropriate
	// portion of the available bits.
	silkMaxBits := (maxTargetBytes) * 8
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
		e.silkSideEncoder.SetFEC(e.fecEnabled)
		e.silkSideEncoder.SetPacketLoss(e.packetLoss)
		e.silkSideEncoder.SetVBR(true)
		e.silkSideEncoder.SetMaxBits(silkMaxBits)
	}
	e.encodeSILKHybrid(silkInput, silkLookahead, frameSize, silkBitrate)

	// Step 2b: Encode redundancy flag between SILK and CELT.
	// Per libopus opus_encoder.c: in hybrid mode, a redundancy flag is always
	// written between the SILK and CELT portions (logp=12, value=0 for no redundancy).
	// The decoder reads this flag in its afterSilk hook; if we don't write it,
	// the CELT decoder reads shifted data and produces garbage output.
	// Condition matches libopus: ec_tell(&enc)+17+20 <= 8*(max_data_bytes-1)
	if re.Tell()+17+20 <= 8*maxTargetBytes {
		re.EncodeBit(0, 12) // redundancy = 0 (no redundancy)
	}

	// Step 3: Apply HB_gain fade on the delay-compensated CELT input.
	// The CELT input is already delay-compensated by applyDelayCompensation
	// in the caller (Fs/250 = 192 samples). No additional delay is needed here.
	celtInput := e.applyHBGainFade(celtPCM, hbGain)
	if e.channels == 2 {
		frameRate := 48000 / frameSize
		vbr := e.bitrateMode != ModeCBR
		equivRate := computeEquivRate(e.bitrate, e.channels, frameRate, vbr, ModeHybrid, e.complexity, e.packetLoss)
		targetWidthQ14 := computeStereoWidthQ14(equivRate)
		if e.hybridState.stereoWidthQ14 < (1<<14) || targetWidthQ14 < (1<<14) {
			celtInput = e.applyStereoWidthFade(celtInput, e.hybridState.stereoWidthQ14, targetWidthQ14)
		}
		e.hybridState.stereoWidthQ14 = targetWidthQ14
	}

	// Step 4: CELT encodes high frequencies (bands 17-21)
	e.celtEncoder.SetRangeEncoder(re)
	e.celtEncoder.SetBitrate(celtBitrate)
	e.celtEncoder.SetLSBDepth(e.lsbDepth)
	e.encodeCELTHybridImproved(celtInput, frameSize, payloadTarget)

	// Update state for next frame
	e.hybridState.prevHBGain = hbGain

	// Finalize and return encoded bytes
	return re.Done(), nil
}

// computeHybridBitAllocation computes the SILK and CELT bitrates for hybrid mode.
// This implements libopus compute_silk_rate_for_hybrid() logic.
//
// In libopus, the SILK rate is derived from bits_target (which subtracts 8 bits
// for TOC overhead) rather than the raw bitrate:
//
//	bits_target = min(8*(max_data_bytes-1), bitrate*frame_size/Fs) - 8
//	total_bitRate = bits_target * Fs / frame_size = bitrate - 8*Fs/frame_size
func (e *Encoder) computeHybridBitAllocation(frame20ms bool) (silkBitrate, celtBitrate int) {
	// Apply TOC overhead correction matching libopus bits_target -> bits_to_bitrate roundtrip.
	// For 48kHz/10ms: overhead = 8*48000/480 = 800 bps
	// For 48kHz/20ms: overhead = 8*48000/960 = 400 bps
	frameSize := 480
	if frame20ms {
		frameSize = 960
	}
	tocOverhead := 8 * 48000 / frameSize
	totalRate := e.bitrate - tocOverhead
	if totalRate < 0 {
		totalRate = 0
	}
	channels := e.channels

	// Per-channel rate for table lookup
	ratePerChannel := totalRate / channels

	// Determine table entry based on frame size and FEC
	entry := 1 // 10ms no FEC
	if frame20ms {
		entry = 2 // 20ms no FEC
	}
	if e.fecEnabled {
		entry += 2 // Add 2 for FEC entries
	}

	// Find the appropriate row in the rate table.
	// This matches the libopus loop: find the first entry where rate_table[i][0] > rate,
	// then interpolate between i-1 and i. If no entry exceeds rate, extrapolate from last.
	silkRatePerChannel := 0
	tableLen := len(hybridRateTable)
	breakIdx := tableLen // Will be set to i if we break
	for i := 1; i < tableLen; i++ {
		if hybridRateTable[i][0] > ratePerChannel {
			breakIdx = i
			break
		}
	}
	if breakIdx == tableLen {
		// Past the end of the table: extrapolate from last entry
		lastRow := hybridRateTable[tableLen-1]
		silkRatePerChannel = lastRow[entry]
		// Give 50% of extra bits to SILK (libopus behavior)
		silkRatePerChannel += (ratePerChannel - lastRow[0]) / 2
	} else {
		// Interpolate between breakIdx-1 and breakIdx
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

	// Apply libopus adjustments to SILK rate (before multiplying by channels)

	// 1. CBR boost: tiny boost for CBR mode (libopus: +100)
	if e.bitrateMode == ModeCBR {
		silkRatePerChannel += 100
	}

	// 2. SWB boost: extra bits for superwideband (libopus: +300)
	if e.effectiveBandwidth() == types.BandwidthSuperwideband {
		silkRatePerChannel += 300
	}

	// Multiply by channels
	silkBitrate = silkRatePerChannel * channels

	// 3. Stereo adjustment: small reduction for stereo at higher rates (libopus: -1000)
	if channels == 2 && ratePerChannel >= 12000 {
		silkBitrate -= 1000
	}

	celtBitrate = totalRate - silkBitrate

	// Ensure minimum CELT bitrate for acceptable quality
	minCeltBitrate := 2000 * channels
	if celtBitrate < minCeltBitrate {
		celtBitrate = minCeltBitrate
		silkBitrate = totalRate - celtBitrate
	}

	return silkBitrate, celtBitrate
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
	if e.fecEnabled {
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

// computeHBGain computes the high-band gain for CELT attenuation.
// When CELT has few bits allocated, we attenuate high frequencies
// to prevent artifacts from quantization noise.
//
// This implements libopus HB_gain calculation:
// HB_gain = Q15ONE - SHR32(celt_exp2(-celt_rate * QCONST16(1.f/1024, 10)), 1)
//
// In float: HB_gain = 1.0 - 2^(-celt_rate/1024) / 2
//
// This results in HB_gain very close to 1.0 for typical bitrates:
// - At 8000 bps: HB_gain ~ 0.9978
// - At 16000 bps: HB_gain ~ 0.9999
// - At 4000 bps: HB_gain ~ 0.9902
func (e *Encoder) computeHBGain(celtBitrate int) float64 {
	// Compute: HB_gain = 1.0 - 2^(-celt_rate/1024) / 2
	// This is the libopus formula for high-band attenuation.
	//
	// The exponent -celt_rate/1024 means:
	// - At 1024 bps: 2^(-1) = 0.5, HB_gain = 1.0 - 0.25 = 0.75
	// - At 2048 bps: 2^(-2) = 0.25, HB_gain = 1.0 - 0.125 = 0.875
	// - At 4096 bps: 2^(-4) = 0.0625, HB_gain = 1.0 - 0.03125 = 0.96875
	// - At 8192 bps: 2^(-8) = 0.0039, HB_gain = 1.0 - 0.00195 = 0.998
	//
	// At typical hybrid bitrates (8-25 kbps CELT), gain is essentially 1.0.

	if celtBitrate <= 0 {
		// At zero or negative bitrate, return minimum gain
		return 0.5
	}

	// Compute 2^(-celt_rate/1024) using math.Exp2
	exponent := -float64(celtBitrate) / 1024.0
	exp2Value := math.Exp2(exponent)

	// HB_gain = 1.0 - exp2Value / 2
	gain := 1.0 - exp2Value/2.0

	// Clamp to reasonable range [0.5, 1.0]
	if gain < 0.5 {
		gain = 0.5
	}
	if gain > 1.0 {
		gain = 1.0
	}

	return gain
}

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
		for i := range pcm {
			pcm[i] *= hbGain
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
		for i := 0; i < overlap; i++ {
			w := window[i]
			w2 := w * w // Square the window (libopus does this)
			g := g1*(1-w2) + g2*w2
			samples[i] *= g
		}
		// Apply constant g2 for rest of frame
		for i := overlap; i < frameSize; i++ {
			samples[i] *= g2
		}
	} else {
		for i := 0; i < overlap; i++ {
			w := window[i]
			w2 := w * w
			g := g1*(1-w2) + g2*w2
			samples[i*2] *= g
			samples[i*2+1] *= g
		}
		for i := overlap; i < frameSize; i++ {
			samples[i*2] *= g2
			samples[i*2+1] *= g2
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
	if e.fecEnabled {
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
	mid, side, predIdx, midOnly, midRate, sideRate, _ := e.silkEncoder.StereoLRToMSWithRates(
		left, right, silkSamples, fsKHz, totalRateBps, e.lastVADActivityQ8, false,
	)
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
	}

	// LBRR flags
	lbrrMid := false
	lbrrSide := false
	if e.fecEnabled {
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
	if e.fecEnabled && e.silkSideEncoder != nil && !midOnly {
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
func (e *Encoder) encodeCELTHybridImproved(pcm []float64, frameSize int, targetPayloadBytes int) {
	// Set hybrid mode flag on CELT encoder
	e.celtEncoder.SetHybrid(true)

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
	transient, tfEstimate, toneFreq, toneishness, shortBlocks, bandLogE2 := e.celtEncoder.TransientAnalysisHybrid(preemph, frameSize, nbBands, lm)

	// Compute MDCT with overlap history using the selected block size.
	mdctCoeffs := computeMDCTForHybridScratch(preemph, frameSize, e.channels, e.celtEncoder.OverlapBuffer(), shortBlocks, e.hybridState, e.celtEncoder)
	if len(mdctCoeffs) == 0 {
		return
	}

	// Compute band energies
	energies := e.celtEncoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	e.celtEncoder.RoundFloat64ToFloat32(energies)
	if bandLogE2 == nil {
		if cap(e.hybridState.scratchBandLogE2) < len(energies) {
			e.hybridState.scratchBandLogE2 = make([]float64, len(energies))
		}
		bandLogE2 = e.hybridState.scratchBandLogE2[:len(energies)]
		copy(bandLogE2, energies)
	}

	// In hybrid mode, set low bands (0-16) to very low energy.
	// These bands are handled by SILK.
	for c := 0; c < e.channels; c++ {
		base := c * nbBands
		for band := 0; band < start && base+band < len(energies); band++ {
			energies[base+band] = -28.0
			if bandLogE2 != nil && base+band < len(bandLogE2) {
				bandLogE2[base+band] = -28.0
			}
		}
	}

	// Apply crossover energy matching
	// Ensure smooth transition at band 17 (the first CELT band in hybrid)
	if start < len(energies) {
		energies = e.matchCrossoverEnergy(energies, start)
	}

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

	// Encode transient flag (only for LM >= 1)
	if lm >= 1 && re.Tell()+3 <= totalBits {
		if transient {
			re.EncodeBit(1, 3)
		} else {
			re.EncodeBit(0, 3)
		}
	} else if lm >= 1 {
		transient = false
		shortBlocks = 1
	}

	// Encode intra flag using libopus-style coarse-energy two-pass decision.
	intra := e.celtEncoder.DecideIntraMode(energies, nbBands, lm)
	if re.Tell()+3 <= totalBits {
		if intra {
			re.EncodeBit(1, 3)
		} else {
			re.EncodeBit(0, 3)
		}
	} else {
		intra = false
	}

	// Encode coarse energy
	prevEnergyLen := len(e.celtEncoder.PrevEnergy())
	if cap(e.hybridState.scratchPrevEnergy) < prevEnergyLen {
		e.hybridState.scratchPrevEnergy = make([]float64, prevEnergyLen)
	}
	prevEnergy := e.hybridState.scratchPrevEnergy[:prevEnergyLen]
	copy(prevEnergy, e.celtEncoder.PrevEnergy())
	oldBandE := prevEnergy
	if maxLen := nbBands * e.channels; maxLen > 0 && len(oldBandE) > maxLen {
		oldBandE = oldBandE[:maxLen]
	}
	quantizedEnergies := e.celtEncoder.EncodeCoarseEnergyRange(energies, start, end, intra, lm)

	// Update tonality analysis for next frame's VBR decisions.
	e.celtEncoder.UpdateTonalityAnalysisHybrid(normL, energies, nbBands, frameSize)

	// Compute dynalloc analysis for TF/spread and offsets.
	lsbDepth := e.lsbDepth
	effectiveBytes := 0
	if e.celtEncoder.VBR() {
		baseBits := e.celtEncoder.BitrateToBits(frameSize)
		effectiveBytes = baseBits / 8
	} else {
		effectiveBytes = e.celtEncoder.CBRPayloadBytes(frameSize)
	}

	dynallocResult := e.celtEncoder.DynallocAnalysisHybridScratch(
		energies,
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

	// TF analysis (enable with sufficient bits/complexity).
	enableTFAnalysis := effectiveBytes >= 15*e.channels && e.celtEncoder.Complexity() >= 2 && toneishness < 0.98
	var tfRes []int
	if enableTFAnalysis {
		useTfEstimate := tfEstimate
		if transient && tfEstimate < 0.2 {
			useTfEstimate = 0.2
		}
		tfRes, tfSelect := e.celtEncoder.TFAnalysisHybridScratch(normL, nbBands, transient, lm, useTfEstimate, effectiveBytes, dynallocResult.Importance)
		celt.TFEncodeWithSelect(re, start, end, transient, tfRes, lm, tfSelect)
	} else {
		tfRes = e.celtEncoder.TFResScratch(nbBands)
		for i := range tfRes {
			tfRes[i] = 0
		}
		if transient {
			for i := 0; i < nbBands; i++ {
				tfRes[i] = 1
			}
		}
		celt.TFEncodeWithSelect(re, start, end, transient, tfRes, lm, 0)
	}

	// Encode spread decision (analysis-based) only if budget allows.
	spread := celt.SpreadNormal
	if re.Tell()+4 <= totalBits {
		if shortBlocks > 1 || e.celtEncoder.Complexity() < 3 || effectiveBytes < 10*e.channels {
			if e.celtEncoder.Complexity() == 0 {
				spread = celt.SpreadNone
			} else {
				spread = celt.SpreadNormal
			}
			e.celtEncoder.SetTapsetDecision(0)
		} else {
			updateHF := shortBlocks == 1
			spreadWeights := celt.ComputeSpreadWeights(energies, nbBands, e.channels, lsbDepth)
			spread = e.celtEncoder.SpreadingDecisionWithWeights(normL, nbBands, e.channels, frameSize, updateHF, spreadWeights)
		}
		re.EncodeICDF(spread, celt.SpreadICDF, 5)
	}

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

	allocTrim := 5
	if tellFracDynalloc+(6<<celt.BitRes) <= totalBitsQ3ForDynalloc-totalBoost {
		equivRate := celt.ComputeEquivRate(effectiveBytes, e.channels, lm, e.celtEncoder.Bitrate())
		allocTrim = celt.AllocTrimAnalysis(
			normL,
			energies,
			nbBands,
			lm,
			e.channels,
			normR,
			0,
			tfEstimate,
			equivRate,
			0.0,
			0.0,
		)
		re.EncodeICDF(allocTrim, celt.TrimICDF, 7)
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
	signalBandwidth := end - 1
	if signalBandwidth < 0 {
		signalBandwidth = 0
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

	// Encode fine energy (only for hybrid bands).
	e.celtEncoder.EncodeFineEnergyRange(energies, quantizedEnergies, start, end, allocResult.FineBits)

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

	// Encode anti-collapse flag if reserved.
	if antiCollapseRsv > 0 {
		antiCollapseOn := 0
		if e.celtEncoder.ConsecTransient() < 2 {
			antiCollapseOn = 1
		}
		re.EncodeRawBits(uint32(antiCollapseOn), 1)
	}

	// Encode energy finalization bits (leftover budget).
	bitsLeft := totalBits - re.Tell()
	if bitsLeft < 0 {
		bitsLeft = 0
	}
	e.celtEncoder.EncodeEnergyFinaliseRange(energies, quantizedEnergies, start, end, allocResult.FineBits, allocResult.FinePriority, bitsLeft)

	// Update state: prev energy, RNG, frame count, transient history.
	if cap(e.hybridState.scratchNextEnergy) < len(prevEnergy) {
		e.hybridState.scratchNextEnergy = make([]float64, len(prevEnergy))
	}
	nextEnergy := e.hybridState.scratchNextEnergy[:len(prevEnergy)]
	// Zero-fill since we only populate bands start..end
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
	e.celtEncoder.UpdateConsecTransient(transient)
}

// matchCrossoverEnergy ensures smooth energy transition at the SILK/CELT boundary.
// This prevents audible artifacts at the crossover frequency (8kHz).
func (e *Encoder) matchCrossoverEnergy(energies []float64, startBand int) []float64 {
	if len(energies) <= startBand {
		return energies
	}

	// Get the energy at the crossover band (band 17)
	crossoverEnergy := energies[startBand]

	// If crossover energy is too high relative to neighbors, smooth it
	// This prevents "peaky" artifacts at 8kHz
	if startBand+1 < len(energies) {
		nextBandEnergy := energies[startBand+1]

		// If crossover band is much higher than the next band, blend
		diff := crossoverEnergy - nextBandEnergy
		if diff > 6.0 { // More than 6dB difference
			// Blend toward the higher band's energy
			blendFactor := 0.5
			energies[startBand] = crossoverEnergy*(1-blendFactor) + (nextBandEnergy+3.0)*blendFactor
		}
	}

	// Apply a gentle rolloff to the first few CELT bands
	// This helps smooth the transition from SILK
	rolloffBands := 3
	if startBand+rolloffBands > len(energies) {
		rolloffBands = len(energies) - startBand
	}

	for i := 0; i < rolloffBands; i++ {
		band := startBand + i
		if band < len(energies) {
			// Gentle boost (0.5-1.5 dB) to compensate for crossover filtering
			boost := 0.5 * (1.0 - float64(i)/float64(rolloffBands))
			energies[band] += boost
		}
	}

	// Smooth the crossover band over time to avoid abrupt energy changes.
	if e.hybridState != nil && e.channels > 0 {
		channels := e.channels
		if len(e.hybridState.crossoverBuffer) != channels {
			e.hybridState.crossoverBuffer = make([]float64, channels)
			for i := range e.hybridState.crossoverBuffer {
				e.hybridState.crossoverBuffer[i] = math.NaN()
			}
		}

		bandsPerChannel := len(energies) / channels
		if bandsPerChannel > 0 && startBand < bandsPerChannel {
			const alpha = 0.2
			for c := 0; c < channels; c++ {
				idx := c*bandsPerChannel + startBand
				if idx >= len(energies) {
					continue
				}
				prev := e.hybridState.crossoverBuffer[c]
				if math.IsNaN(prev) {
					e.hybridState.crossoverBuffer[c] = energies[idx]
					continue
				}
				smoothed := prev + alpha*(energies[idx]-prev)
				energies[idx] = smoothed
				e.hybridState.crossoverBuffer[c] = smoothed
			}
		}
	}

	return energies
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
			// Use scratch for the input assembly buffer
			needed := len(samples) + overlap
			if cap(hs.scratchMDCTInput) < needed {
				hs.scratchMDCTInput = make([]float64, needed)
			}
			return ce.ComputeMDCTWithHistoryScratch(hs.scratchMDCTInput[:needed], samples, history[:overlap], shortBlocks)
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
		// Use L/R scratch methods so left result survives right-channel call
		mdctLeft := ce.ComputeMDCTWithHistoryScratchStereoL(left, history[:overlap], shortBlocks)
		mdctRight := ce.ComputeMDCTWithHistoryScratchStereoR(right, history[overlap:overlap*2], shortBlocks)
		// Combine into result scratch
		resultLen := len(mdctLeft) + len(mdctRight)
		if cap(hs.scratchMDCTResult) < resultLen {
			hs.scratchMDCTResult = make([]float64, resultLen)
		}
		result := hs.scratchMDCTResult[:resultLen]
		copy(result[:len(mdctLeft)], mdctLeft)
		copy(result[len(mdctLeft):], mdctRight)
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
