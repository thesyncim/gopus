package silk

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeFrame decodes a single SILK mono frame from the bitstream.
// Returns decoded samples at native SILK sample rate (8/12/16kHz).
//
// Parameters:
//   - rd: Range decoder initialized with the SILK bitstream
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - duration: Frame duration (10/20/40/60ms)
//   - vadFlag: Voice Activity Detection flag from header
//
// For 40/60ms frames, the frame is decoded as multiple 20ms sub-blocks.
func (d *Decoder) DecodeFrame(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]float32, error) {
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	d.SetLPCOrder(config.LPCOrder)

	numSubframes := getSubframeCount(duration)
	samplesPerSubframe := config.SubframeSamples
	totalSamples := numSubframes * samplesPerSubframe

	output := make([]float32, totalSamples)

	// For 40/60ms frames, decode as multiple 20ms sub-blocks
	if is40or60ms(duration) {
		subBlocks := getSubBlockCount(duration)
		subBlockSamples := 4 * samplesPerSubframe // 20ms = 4 subframes

		for block := 0; block < subBlocks; block++ {
			blockOutput := output[block*subBlockSamples : (block+1)*subBlockSamples]
			err := d.decode20msBlock(bandwidth, vadFlag, blockOutput)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// 10ms or 20ms: single block
		err := d.decodeBlock(bandwidth, vadFlag, duration, output)
		if err != nil {
			return nil, err
		}
	}

	// Update decoder state for next frame
	d.haveDecoded = true

	return output, nil
}

// decode20msBlock decodes one 20ms sub-block.
func (d *Decoder) decode20msBlock(
	bandwidth Bandwidth,
	vadFlag bool,
	output []float32,
) error {
	return d.decodeBlock(bandwidth, vadFlag, Frame20ms, output)
}

// decodeBlock decodes a 10ms or 20ms block.
func (d *Decoder) decodeBlock(
	bandwidth Bandwidth,
	vadFlag bool,
	duration FrameDuration,
	output []float32,
) error {
	config := GetBandwidthConfig(bandwidth)
	numSubframes := len(output) / config.SubframeSamples

	// 1. Decode frame type
	signalType, quantOffset := d.DecodeFrameType(vadFlag)

	// 2. Decode subframe gains
	gains := d.decodeSubframeGains(signalType, numSubframes)

	// 3. Decode LSF -> LPC coefficients
	lsfQ15 := d.decodeLSFCoefficients(bandwidth, signalType)
	lpcQ12 := lsfToLPC(lsfQ15)
	limitLPCFilterGain(lpcQ12)

	// 4. Decode pitch/LTP (voiced only)
	var pitchLags []int
	var ltpCoeffs [][]int8
	var ltpScale int
	if signalType == 2 { // Voiced
		pitchLags = d.decodePitchLag(bandwidth, numSubframes)
		ltpCoeffs, ltpScale = d.decodeLTPCoefficients(bandwidth, numSubframes)
	}

	// 5. Decode and synthesize each subframe
	for sf := 0; sf < numSubframes; sf++ {
		sfStart := sf * config.SubframeSamples
		sfEnd := sfStart + config.SubframeSamples
		sfOutput := output[sfStart:sfEnd]

		// Decode excitation
		excitation := d.decodeExcitation(config.SubframeSamples, signalType, quantOffset)

		// Scale excitation by gain
		scaleExcitation(excitation, gains[sf])

		// Apply LTP synthesis (voiced only)
		if signalType == 2 && pitchLags != nil {
			d.ltpSynthesis(excitation, pitchLags[sf], ltpCoeffs[sf], ltpScale)
		}

		// Apply LPC synthesis
		d.lpcSynthesis(excitation, lpcQ12, gains[sf], sfOutput)

		// Update output history for LTP lookback
		d.updateHistory(sfOutput)
	}

	// Update voiced flag for next frame
	d.isPreviousFrameVoiced = (signalType == 2)

	return nil
}

// DecodeStereoFrame decodes a SILK stereo frame from the bitstream.
// Returns left and right channel samples at native sample rate.
//
// Stereo SILK uses mid-side coding with prediction.
// The mid channel is decoded first, then the side channel,
// and finally they are unmixed to left and right.
func (d *Decoder) DecodeStereoFrame(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) (left, right []float32, err error) {
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	d.SetLPCOrder(config.LPCOrder)

	numSubframes := getSubframeCount(duration)
	totalSamples := numSubframes * config.SubframeSamples

	// Decode stereo prediction weights
	w0, w1 := d.decodeStereoWeights()

	// Decode mid channel
	mid := make([]float32, totalSamples)
	err = d.decodeChannel(bandwidth, duration, vadFlag, mid)
	if err != nil {
		return nil, nil, err
	}

	// Decode side channel
	side := make([]float32, totalSamples)
	err = d.decodeChannel(bandwidth, duration, vadFlag, side)
	if err != nil {
		return nil, nil, err
	}

	// Unmix to left/right
	left = make([]float32, totalSamples)
	right = make([]float32, totalSamples)
	stereoUnmix(mid, side, w0, w1, left, right)

	// Update stereo state
	d.prevStereoWeights = [2]int16{w0, w1}
	d.haveDecoded = true

	return left, right, nil
}

// decodeChannel decodes a single channel (used for stereo).
func (d *Decoder) decodeChannel(
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
	output []float32,
) error {
	if is40or60ms(duration) {
		subBlocks := getSubBlockCount(duration)
		config := GetBandwidthConfig(bandwidth)
		subBlockSamples := 4 * config.SubframeSamples

		for block := 0; block < subBlocks; block++ {
			blockOutput := output[block*subBlockSamples : (block+1)*subBlockSamples]
			if err := d.decode20msBlock(bandwidth, vadFlag, blockOutput); err != nil {
				return err
			}
		}
	} else {
		return d.decodeBlock(bandwidth, vadFlag, duration, output)
	}
	return nil
}
