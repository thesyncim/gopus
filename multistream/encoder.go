// Multistream encoder implementation for Opus surround sound.
// This file contains the Encoder struct and NewEncoder function for encoding
// multi-channel audio into multistream Opus packets.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1

package multistream

import (
	"errors"
	"fmt"
	"math"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// ErrInvalidInput indicates the input samples have incorrect length.
var ErrInvalidInput = errors.New("multistream: invalid input length")

// ErrInvalidLayout indicates the channel mapping has an invalid layout.
// For coupled streams, both left and right channels must be mapped.
var ErrInvalidLayout = errors.New("multistream: invalid layout - coupled stream missing left or right channel")

// ErrInvalidLSBDepth indicates the LSB depth is outside the valid range (8-24).
var ErrInvalidLSBDepth = errors.New("multistream: invalid LSB depth (must be 8-24)")

// Encoder encodes multi-channel audio into Opus multistream packets.
// Each elementary stream is encoded independently using a Phase 8 unified Encoder,
// then combined with self-delimiting framing per RFC 6716 Appendix B.
//
// Multistream packets are used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 7845 Section 5.1.1
type Encoder struct {
	// sampleRate is the input sample rate (8000, 12000, 16000, 24000, or 48000 Hz).
	sampleRate int

	// inputChannels is the total number of input channels (1-255).
	inputChannels int

	// streams is the total number of elementary streams (N).
	streams int

	// coupledStreams is the number of coupled (stereo) streams (M).
	// The first M encoders produce stereo output, the remaining N-M produce mono.
	coupledStreams int

	// mapping is the channel mapping table.
	// mapping[i] indicates which stream channel receives input channel i.
	// Values 0 to 2*M-1 are for coupled streams (even=left, odd=right).
	// Values 2*M to N+M-1 are for uncoupled streams.
	// Value 255 indicates a silent input channel (ignored).
	mapping []byte

	// encoders contains one encoder per stream.
	// First M encoders are stereo (for coupled streams).
	// Remaining N-M encoders are mono (for uncoupled streams).
	encoders []*encoder.Encoder

	// bitrate is the total bitrate in bits per second, distributed across streams.
	bitrate int

	// mappingFamily indicates the channel mapping family used:
	//   0: RTP mapping (mono or stereo only)
	//   1: Vorbis-style mapping (1-8 channels)
	//   2: Ambisonics ACN/SN3D (mostly mono streams)
	//   3: Ambisonics with projection (paired stereo streams)
	//   255: Discrete channels (no predefined mapping)
	mappingFamily int

	// lfeStream is the stream index that carries LFE, or -1 when absent.
	lfeStream int

	// streamBitrates stores per-stream rates computed by allocation policy.
	streamBitrates []int

	// streamSurroundTrim stores per-stream surround trim derived from surround masks.
	streamSurroundTrim []float64

	// surroundBandSMR stores per-channel surround masks (21 bands per channel).
	surroundBandSMR []float64
}

const surroundBands = 21

var surroundEBands = [surroundBands + 1]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 13, 15, 17, 20, 23, 27, 32, 38, 46, 56, 69,
}

func mappingEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func inferMappingFamily(channels, streams, coupledStreams int, mapping []byte) int {
	if channels >= 1 && channels <= 8 {
		ds, dc, dm, err := DefaultMapping(channels)
		if err == nil && ds == streams && dc == coupledStreams && mappingEqual(dm, mapping) {
			return 1
		}
	}

	if channels > 0 {
		ds, dc, err := ValidateAmbisonics(channels)
		if err == nil && ds == streams && dc == coupledStreams {
			if dm, derr := AmbisonicsMapping(channels); derr == nil && mappingEqual(dm, mapping) {
				return 2
			}
		}

		ds, dc, err = ValidateAmbisonicsFamily3(channels)
		if err == nil && ds == streams && dc == coupledStreams {
			if dm, derr := AmbisonicsMappingFamily3(channels); derr == nil && mappingEqual(dm, mapping) {
				return 3
			}
		}
	}

	if streams == channels && coupledStreams == 0 {
		discrete := true
		for i := 0; i < channels; i++ {
			if mapping[i] != byte(i) {
				discrete = false
				break
			}
		}
		if discrete {
			return 255
		}
	}

	return 0
}

func inferLFEStream(mappingFamily, channels, streams int) int {
	if mappingFamily == 1 && channels >= 6 {
		return streams - 1
	}
	return -1
}

// NewEncoder creates a new multistream encoder.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//
// The mapping table determines how input audio is routed to stream encoders:
//   - Values 0 to 2*M-1: to coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: to uncoupled (mono) streams
//   - Value 255: silent channel (input ignored)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Input 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Input 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Input 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Input 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Input 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Input 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*Encoder, error) {
	// Validation exactly mirrors decoder
	if channels < 1 || channels > 255 {
		return nil, ErrInvalidChannels
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if streams+coupledStreams > 255 {
		return nil, ErrTooManyChannels
	}
	if len(mapping) != channels {
		return nil, ErrInvalidMapping
	}

	// Validate each mapping entry
	maxMappingValue := streams + coupledStreams
	for i, m := range mapping {
		if m != 255 && int(m) >= maxMappingValue {
			return nil, fmt.Errorf("%w: mapping[%d]=%d exceeds maximum %d", ErrInvalidMapping, i, m, maxMappingValue-1)
		}
	}

	// Validate layout: ensure coupled streams have valid L/R pairs
	if err := validateEncoderLayout(mapping, coupledStreams); err != nil {
		return nil, err
	}

	// Create stream encoders
	// First M encoders are stereo (coupled), remaining N-M are mono
	encoders := make([]*encoder.Encoder, streams)
	for i := 0; i < streams; i++ {
		var chans int
		if i < coupledStreams {
			chans = 2 // Coupled stream = stereo
		} else {
			chans = 1 // Uncoupled stream = mono
		}
		encoders[i] = encoder.NewEncoder(sampleRate, chans)
	}

	// Copy mapping to avoid external mutation
	mappingCopy := make([]byte, len(mapping))
	copy(mappingCopy, mapping)

	mappingFamily := inferMappingFamily(channels, streams, coupledStreams, mappingCopy)
	lfeStream := inferLFEStream(mappingFamily, channels, streams)

	return &Encoder{
		sampleRate:         sampleRate,
		inputChannels:      channels,
		streams:            streams,
		coupledStreams:     coupledStreams,
		mapping:            mappingCopy,
		encoders:           encoders,
		bitrate:            256000, // Default 256 kbps total
		mappingFamily:      mappingFamily,
		lfeStream:          lfeStream,
		streamBitrates:     make([]int, streams),
		streamSurroundTrim: make([]float64, streams),
		surroundBandSMR:    make([]float64, channels*surroundBands),
	}, nil
}

// NewEncoderDefault creates a multistream encoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (5 streams, 2 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewEncoderDefault(sampleRate, channels int) (*Encoder, error) {
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		return nil, err
	}
	enc, err := NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}
	enc.mappingFamily = 1 // Vorbis-style mapping
	enc.lfeStream = inferLFEStream(enc.mappingFamily, channels, streams)
	return enc, nil
}

// NewEncoderAmbisonics creates a new multistream encoder for ambisonics audio.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (valid ambisonics count: 1, 4, 6, 9, 11, 16, 18, 25, 27...)
//   - mappingFamily: 2 for ACN/SN3D (mostly mono), 3 for projection (paired stereo)
//
// Valid ambisonics channel counts are (order+1)^2 or (order+1)^2 + 2:
//   - Order 0: 1 channel (or 3 with non-diegetic)
//   - Order 1 (FOA): 4 channels (or 6 with non-diegetic)
//   - Order 2 (SOA): 9 channels (or 11 with non-diegetic)
//   - Order 3 (TOA): 16 channels (or 18 with non-diegetic)
//
// For mapping family 2:
//   - ACN channel ordering with SN3D normalization
//   - All ambisonics channels are mono streams
//   - Optional non-diegetic stereo pair as one coupled stream
//
// For mapping family 3:
//   - Projection-based encoding
//   - Channels are paired into stereo coupled streams
//   - streams = (channels+1)/2, coupled = channels/2
//   - libopus parity support is limited to orders 1..5
//     (valid channels: 4, 6, 9, 11, 16, 18, 25, 27, 36, 38)
//
// Reference: RFC 7845 Section 5.1.1.2, libopus opus_multistream_encoder.c
func NewEncoderAmbisonics(sampleRate, channels, mappingFamily int) (*Encoder, error) {
	var streams, coupledStreams int
	var mapping []byte
	var err error

	switch mappingFamily {
	case 2:
		streams, coupledStreams, err = ValidateAmbisonics(channels)
		if err != nil {
			return nil, err
		}
		mapping, err = AmbisonicsMapping(channels)
		if err != nil {
			return nil, err
		}
	case 3:
		streams, coupledStreams, err = ValidateAmbisonicsFamily3(channels)
		if err != nil {
			return nil, err
		}
		mapping, err = AmbisonicsMappingFamily3(channels)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidMappingFamily
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}
	enc.mappingFamily = mappingFamily
	enc.lfeStream = -1
	return enc, nil
}

// Reset clears all encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	for i, enc := range e.encoders {
		enc.Reset()
		enc.SetCELTSurroundTrim(0)
		if i < len(e.streamSurroundTrim) {
			e.streamSurroundTrim[i] = 0
		}
	}
	if len(e.surroundBandSMR) > 0 {
		clear(e.surroundBandSMR)
	}
}

// Channels returns the total number of input channels.
func (e *Encoder) Channels() int {
	return e.inputChannels
}

// SampleRate returns the input sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}

// Streams returns the total number of elementary streams.
func (e *Encoder) Streams() int {
	return e.streams
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (e *Encoder) CoupledStreams() int {
	return e.coupledStreams
}

// MappingFamily returns the channel mapping family used by this encoder.
//
// Mapping families:
//   - 0: RTP mapping (mono or stereo only)
//   - 1: Vorbis-style mapping (1-8 channels)
//   - 2: Ambisonics ACN/SN3D (mostly mono streams)
//   - 3: Ambisonics with projection (paired stereo streams)
//   - 255: Discrete channels (no predefined mapping)
func (e *Encoder) MappingFamily() int {
	return e.mappingFamily
}

// SetBitrate sets the total bitrate in bits per second.
// The bitrate is distributed across streams with coupled streams getting
// proportionally more bits than mono streams.
//
// Distribution formula:
//   - Coupled streams: 3 units (e.g., 96 kbps at typical settings)
//   - Mono streams: 2 units (e.g., 64 kbps at typical settings)
func (e *Encoder) SetBitrate(totalBitrate int) {
	e.bitrate = totalBitrate
	rates := e.allocateRates(960)
	for i := 0; i < e.streams && i < len(rates); i++ {
		e.encoders[i].SetBitrate(rates[i])
	}
}

// Bitrate returns the total bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return e.bitrate
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func logSum(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	if math.IsInf(b, -1) {
		return a
	}
	return a + math.Log2(1.0+math.Exp2(b-a))
}

func (e *Encoder) isSurroundMapping() bool {
	return e.mappingFamily == 1 && e.inputChannels > 2
}

func (e *Encoder) isAmbisonicsMapping() bool {
	return e.mappingFamily == 2 || e.mappingFamily == 3
}

func (e *Encoder) bitrateForAllocation(frameSize int) int {
	if e.bitrate > 0 {
		return e.bitrate
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	fs := e.sampleRate
	if fs <= 0 {
		fs = 48000
	}
	nbLFE := 0
	if e.lfeStream >= 0 {
		nbLFE = 1
	}
	nbUncoupled := e.streams - e.coupledStreams - nbLFE
	if nbUncoupled < 0 {
		nbUncoupled = 0
	}
	nbNormal := 2*e.coupledStreams + nbUncoupled
	channelOffset := 40 * maxInt(50, fs/frameSize)
	return nbNormal*(channelOffset+fs+10000) + 8000*nbLFE
}

func (e *Encoder) allocateSurroundRates(rates []int, frameSize int) {
	fs := e.sampleRate
	if fs <= 0 {
		fs = 48000
	}
	if frameSize <= 0 {
		frameSize = 960
	}

	nbLFE := 0
	if e.lfeStream >= 0 {
		nbLFE = 1
	}
	nbCoupled := e.coupledStreams
	nbUncoupled := e.streams - nbCoupled - nbLFE
	if nbUncoupled < 0 {
		nbUncoupled = 0
	}
	nbNormal := 2*nbCoupled + nbUncoupled

	bitrate := e.bitrateForAllocation(frameSize)
	if nbNormal <= 0 {
		per := bitrate / maxInt(1, e.streams)
		per = maxInt(per, 500)
		for i := 0; i < e.streams; i++ {
			rates[i] = per
		}
		return
	}

	channelOffset := 40 * maxInt(50, fs/frameSize)
	lfeOffset := minInt(bitrate/20, 3000) + 15*maxInt(50, fs/frameSize)
	if nbLFE == 0 {
		lfeOffset = 0
	}

	streamOffset := (bitrate - channelOffset*nbNormal - lfeOffset*nbLFE) / nbNormal / 2
	streamOffset = maxInt(0, minInt(20000, streamOffset))

	const coupledRatio = 512
	const lfeRatio = 32
	total := (nbUncoupled << 8) + coupledRatio*nbCoupled + nbLFE*lfeRatio
	if total <= 0 {
		total = 1
	}
	numerator := bitrate - lfeOffset*nbLFE - streamOffset*(nbCoupled+nbUncoupled) - channelOffset*nbNormal
	channelRate := (256 * numerator) / total

	for i := 0; i < e.streams; i++ {
		var rate int
		if i < e.coupledStreams {
			rate = 2*channelOffset + maxInt(0, streamOffset+((channelRate*coupledRatio)>>8))
		} else if i != e.lfeStream {
			rate = channelOffset + maxInt(0, streamOffset+channelRate)
		} else {
			rate = maxInt(0, lfeOffset+((channelRate*lfeRatio)>>8))
		}
		rates[i] = maxInt(rate, 500)
	}
}

func (e *Encoder) allocateRates(frameSize int) []int {
	if frameSize <= 0 {
		frameSize = 960
	}
	if cap(e.streamBitrates) < e.streams {
		e.streamBitrates = make([]int, e.streams)
	}
	rates := e.streamBitrates[:e.streams]

	switch {
	case e.isSurroundMapping():
		e.allocateSurroundRates(rates, frameSize)
	case e.isAmbisonicsMapping():
		totalRate := e.bitrateForAllocation(frameSize)
		per := totalRate / maxInt(1, e.streams)
		per = maxInt(per, 500)
		for i := 0; i < e.streams; i++ {
			rates[i] = per
		}
	default:
		totalRate := e.bitrateForAllocation(frameSize)
		monoStreams := e.streams - e.coupledStreams
		totalUnits := e.coupledStreams*3 + monoStreams*2
		if totalUnits <= 0 {
			per := maxInt(totalRate/maxInt(1, e.streams), 500)
			for i := 0; i < e.streams; i++ {
				rates[i] = per
			}
			return rates
		}
		unitRate := totalRate / totalUnits
		for i := 0; i < e.streams; i++ {
			if i < e.coupledStreams {
				rates[i] = maxInt(unitRate*3, 500)
			} else {
				rates[i] = maxInt(unitRate*2, 500)
			}
		}
	}

	return rates
}

func (e *Encoder) surroundBandwidth(frameSize int) types.Bandwidth {
	fs := e.sampleRate
	if fs <= 0 {
		fs = 48000
	}
	totalRate := e.bitrateForAllocation(frameSize)
	equivRate := totalRate
	if frameSize > 0 && frameSize*50 < fs {
		equivRate -= 60 * (fs/frameSize - 50) * e.inputChannels
	}
	if equivRate > 10000*e.inputChannels {
		return types.BandwidthFullband
	}
	if equivRate > 7000*e.inputChannels {
		return types.BandwidthSuperwideband
	}
	if equivRate > 5000*e.inputChannels {
		return types.BandwidthWideband
	}
	return types.BandwidthNarrowband
}

func channelPositions(channels int, pos []int) bool {
	if len(pos) < channels {
		return false
	}
	for i := 0; i < channels; i++ {
		pos[i] = 0
	}
	switch channels {
	case 4:
		pos[0], pos[1], pos[2], pos[3] = 1, 3, 1, 3
	case 3, 5, 6:
		pos[0], pos[1], pos[2], pos[3], pos[4] = 1, 2, 3, 1, 3
		if channels == 6 {
			pos[5] = 0
		}
	case 7:
		pos[0], pos[1], pos[2], pos[3], pos[4], pos[5], pos[6] = 1, 2, 3, 1, 3, 2, 0
	case 8:
		pos[0], pos[1], pos[2], pos[3], pos[4], pos[5], pos[6], pos[7] = 1, 2, 3, 1, 3, 1, 3, 0
	default:
		return false
	}
	return true
}

func channelMaskShape(pcm []float64, frameSize, channels, channel int) (base, slope float64) {
	var low, high float64
	prev := 0.0
	for i := 0; i < frameSize; i++ {
		x := pcm[i*channels+channel]
		low += x * x
		if i > 0 {
			d := x - prev
			high += d * d
		}
		prev = x
	}
	low = low/float64(maxInt(frameSize, 1)) + 1e-12
	high = high/float64(maxInt(frameSize-1, 1)) + 1e-12
	base = math.Log2(low)
	slope = clampFloat(0.5*math.Log2(high/low), -1.0, 1.0)
	return base, slope
}

func surroundTrimFromMask(maskL, maskR []float64) float64 {
	if len(maskL) < surroundBands {
		return 0
	}
	channels := 1
	if len(maskR) >= surroundBands {
		channels = 2
	}
	maskEnd := 17
	if maskEnd > surroundBands {
		maskEnd = surroundBands
	}

	maskAvg := 0.0
	count := 0
	diff := 0.0
	for c := 0; c < channels; c++ {
		mask := maskL
		if c == 1 {
			mask = maskR
		}
		for i := 0; i < maskEnd; i++ {
			m := clampFloat(mask[i], -2.0, 0.25)
			if m > 0 {
				m *= 0.5
			}
			width := surroundEBands[i+1] - surroundEBands[i]
			maskAvg += m * float64(width)
			count += width
			diff += m * float64(1+2*i-maskEnd)
		}
	}
	if count <= 0 {
		return 0
	}
	maskAvg = maskAvg/float64(count) + 0.2
	_ = maskAvg

	denom := float64(channels * (maskEnd - 1) * (maskEnd + 1) * maskEnd)
	if denom <= 0 {
		return 0
	}
	diff = diff * 6.0 / denom
	diff *= 0.5
	diff = clampFloat(diff, -0.031, 0.031)
	return 64.0 * diff
}

func (e *Encoder) inputChannelForMapping(mappingIdx byte) int {
	for i, v := range e.mapping {
		if v == mappingIdx {
			return i
		}
	}
	return -1
}

func (e *Encoder) updateSurroundTrimFromPCM(pcm []float64, frameSize int) {
	if cap(e.streamSurroundTrim) < e.streams {
		e.streamSurroundTrim = make([]float64, e.streams)
	}
	trim := e.streamSurroundTrim[:e.streams]
	for i := range trim {
		trim[i] = 0
	}
	if !e.isSurroundMapping() || frameSize <= 0 || e.inputChannels < 3 || e.inputChannels > 8 {
		return
	}

	needed := e.inputChannels * surroundBands
	if cap(e.surroundBandSMR) < needed {
		e.surroundBandSMR = make([]float64, needed)
	}
	bandSMR := e.surroundBandSMR[:needed]
	clear(bandSMR)

	var pos [8]int
	if !channelPositions(e.inputChannels, pos[:]) {
		return
	}

	var leftMask [surroundBands]float64
	var rightMask [surroundBands]float64
	var centerMask [surroundBands]float64
	for i := 0; i < surroundBands; i++ {
		leftMask[i] = -28.0
		rightMask[i] = -28.0
	}

	var base [8]float64
	var slope [8]float64
	for c := 0; c < e.inputChannels; c++ {
		if pos[c] == 0 {
			continue
		}
		base[c], slope[c] = channelMaskShape(pcm, frameSize, e.inputChannels, c)
		for i := 0; i < surroundBands; i++ {
			v := base[c] + slope[c]*(float64(i)-10.0)/10.0
			bandSMR[c*surroundBands+i] = v
			switch pos[c] {
			case 1:
				leftMask[i] = logSum(leftMask[i], v)
			case 3:
				rightMask[i] = logSum(rightMask[i], v)
			case 2:
				leftMask[i] = logSum(leftMask[i], v-0.5)
				rightMask[i] = logSum(rightMask[i], v-0.5)
			}
		}
	}

	channelOffset := 0.0
	if e.inputChannels > 1 {
		channelOffset = 0.5 * math.Log2(2.0/float64(e.inputChannels-1))
	}
	for i := 0; i < surroundBands; i++ {
		leftMask[i] += channelOffset
		rightMask[i] += channelOffset
		if leftMask[i] < rightMask[i] {
			centerMask[i] = leftMask[i]
		} else {
			centerMask[i] = rightMask[i]
		}
	}

	for c := 0; c < e.inputChannels; c++ {
		if pos[c] == 0 {
			for i := 0; i < surroundBands; i++ {
				bandSMR[c*surroundBands+i] = 0
			}
			continue
		}
		for i := 0; i < surroundBands; i++ {
			mask := centerMask[i]
			if pos[c] == 1 {
				mask = leftMask[i]
			} else if pos[c] == 3 {
				mask = rightMask[i]
			}
			bandSMR[c*surroundBands+i] -= mask
		}
	}

	for s := 0; s < e.streams; s++ {
		if s == e.lfeStream {
			trim[s] = 0
			continue
		}
		if s < e.coupledStreams {
			left := e.inputChannelForMapping(byte(2 * s))
			right := e.inputChannelForMapping(byte(2*s + 1))
			if left < 0 || right < 0 {
				continue
			}
			trim[s] = surroundTrimFromMask(
				bandSMR[left*surroundBands:(left+1)*surroundBands],
				bandSMR[right*surroundBands:(right+1)*surroundBands],
			)
		} else {
			mappingIdx := byte(2*e.coupledStreams + (s - e.coupledStreams))
			mono := e.inputChannelForMapping(mappingIdx)
			if mono < 0 {
				continue
			}
			trim[s] = surroundTrimFromMask(
				bandSMR[mono*surroundBands:(mono+1)*surroundBands],
				nil,
			)
		}
	}
}

func (e *Encoder) applyPerStreamPolicy(frameSize int, pcm []float64) {
	rates := e.allocateRates(frameSize)
	if e.isSurroundMapping() {
		e.updateSurroundTrimFromPCM(pcm, frameSize)
	}

	surroundBandwidth := e.surroundBandwidth(frameSize)
	for i := 0; i < e.streams; i++ {
		enc := e.encoders[i]
		enc.SetBitrate(rates[i])

		switch {
		case e.isSurroundMapping():
			if i == e.lfeStream {
				enc.SetMode(encoder.ModeCELT)
				enc.SetForceChannels(1)
				enc.SetBandwidth(types.BandwidthNarrowband)
				enc.SetCELTSurroundTrim(0)
				continue
			}
			enc.SetBandwidth(surroundBandwidth)
			if i < e.coupledStreams {
				// Preserve surround image parity with libopus on coupled streams.
				enc.SetMode(encoder.ModeCELT)
				enc.SetForceChannels(2)
			} else {
				enc.SetForceChannels(-1)
			}
			if i < len(e.streamSurroundTrim) {
				enc.SetCELTSurroundTrim(e.streamSurroundTrim[i])
			} else {
				enc.SetCELTSurroundTrim(0)
			}
		case e.isAmbisonicsMapping():
			enc.SetMode(encoder.ModeCELT)
			enc.SetForceChannels(-1)
			enc.SetCELTSurroundTrim(0)
		default:
			enc.SetCELTSurroundTrim(0)
		}
	}
}

// routeChannelsToStreams routes interleaved input to stream buffers.
// This is the inverse of applyChannelMapping in multistream.go.
//
// Input format: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
// Output: slice of buffers, one per stream (stereo streams interleaved)
func routeChannelsToStreams(
	input []float64,
	mapping []byte,
	coupledStreams int,
	frameSize int,
	inputChannels int,
	numStreams int,
) [][]float64 {
	// Create buffer for each stream
	streamBuffers := make([][]float64, numStreams)
	for i := 0; i < numStreams; i++ {
		chans := streamChannels(i, coupledStreams)
		streamBuffers[i] = make([]float64, frameSize*chans)
	}

	// Route input channels to appropriate streams
	// Key insight: mapping[outCh] tells us which stream channel feeds outCh
	// For encoding, we use the same mapping direction: input channel outCh
	// routes to the stream/channel specified by mapping[outCh]
	for outCh := 0; outCh < inputChannels; outCh++ {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue // Silent channel, skip
		}

		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= numStreams {
			continue
		}

		srcChannels := streamChannels(streamIdx, coupledStreams)

		// Copy samples from input channel to stream buffer
		for s := 0; s < frameSize; s++ {
			streamBuffers[streamIdx][s*srcChannels+chanInStream] = input[s*inputChannels+outCh]
		}
	}

	return streamBuffers
}

// writeSelfDelimitedLength writes a self-delimiting packet length.
// Per RFC 6716 Section 3.2.1:
//   - If length < 252: single byte encoding
//   - If length >= 252: two-byte encoding where length = 4*secondByte + firstByte
//
// This is the inverse of parseSelfDelimitedLength in stream.go.
//
// Returns the number of bytes written (1 or 2).
func writeSelfDelimitedLength(dst []byte, length int) int {
	if length < 252 {
		dst[0] = byte(length)
		return 1
	}
	// Two-byte encoding: length = 4*secondByte + firstByte
	// firstByte in range [252, 255], so use 252 + (length % 4)
	// secondByte = (length - firstByte) / 4
	firstByte := 252 + (length % 4)
	secondByte := (length - firstByte) / 4
	dst[0] = byte(firstByte)
	dst[1] = byte(secondByte)
	return 2
}

// selfDelimitedLengthBytes returns the number of bytes needed to encode a length.
func selfDelimitedLengthBytes(length int) int {
	if length < 252 {
		return 1
	}
	return 2
}

// assembleMultistreamPacket combines individual stream packets into a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 packets use self-delimiting framing (length prefix before each packet)
//   - Last packet uses standard framing (no length prefix, consumes remaining bytes)
func assembleMultistreamPacket(streamPackets [][]byte) []byte {
	if len(streamPackets) == 0 {
		return nil
	}

	// Calculate total size
	totalSize := 0
	for i, packet := range streamPackets {
		if i < len(streamPackets)-1 {
			// First N-1 packets need length prefix
			totalSize += selfDelimitedLengthBytes(len(packet))
		}
		totalSize += len(packet)
	}

	output := make([]byte, totalSize)
	offset := 0

	// Write first N-1 packets with self-delimiting framing
	for i := 0; i < len(streamPackets)-1; i++ {
		n := writeSelfDelimitedLength(output[offset:], len(streamPackets[i]))
		offset += n
		copy(output[offset:], streamPackets[i])
		offset += len(streamPackets[i])
	}

	// Last packet uses remaining data (standard framing, no length prefix)
	copy(output[offset:], streamPackets[len(streamPackets)-1])

	return output
}

// Encode encodes multi-channel PCM samples to an Opus multistream packet.
//
// Parameters:
//   - pcm: input samples as float64, sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
//   - frameSize: number of samples per channel (must be valid for Opus: 120, 240, 480, 960, 1920, 2880)
//
// Returns:
//   - The encoded multistream packet (N-1 length-prefixed streams + 1 standard stream)
//   - nil, nil if DTX suppresses all frames (silence detected in all streams)
//   - error if encoding fails
//
// The encoding process:
//  1. Routes input channels to stream buffers via mapping table
//  2. Encodes each stream independently using the unified encoder
//  3. Assembles packets with self-delimiting framing per RFC 6716 Appendix B
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
	// Validate input length
	expectedLen := frameSize * e.inputChannels
	if len(pcm) != expectedLen {
		return nil, fmt.Errorf("%w: got %d samples, expected %d (frameSize=%d, channels=%d)",
			ErrInvalidInput, len(pcm), expectedLen, frameSize, e.inputChannels)
	}

	// Mirror libopus per-stream rate/control policy ahead of stream encodes.
	e.applyPerStreamPolicy(frameSize, pcm)

	// Route input channels to stream buffers
	streamBuffers := routeChannelsToStreams(pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)

	// Encode each stream
	streamPackets := make([][]byte, e.streams)
	allNil := true

	for i := 0; i < e.streams; i++ {
		packet, err := e.encoders[i].Encode(streamBuffers[i], frameSize)
		if err != nil {
			return nil, fmt.Errorf("stream %d encode failed: %w", i, err)
		}

		// Handle DTX case (nil packet means silence suppressed)
		if packet == nil {
			// For DTX, we need to signal silence with a minimal packet
			// Use a zero-length indicator or skip based on RFC 6716
			// For now, treat as empty packet - decoder handles this
			streamPackets[i] = []byte{}
		} else {
			streamPackets[i] = packet
			allNil = false
		}
	}

	// If all streams returned nil (DTX), return nil to signal silence
	if allNil {
		return nil, nil
	}

	// Assemble multistream packet with self-delimiting framing
	return assembleMultistreamPacket(streamPackets), nil
}

// SetComplexity sets encoder complexity (0-10) for all stream encoders.
// Higher values use more CPU for better quality.
// Default is 10 (maximum quality).
func (e *Encoder) SetComplexity(complexity int) {
	for _, enc := range e.encoders {
		enc.SetComplexity(complexity)
	}
}

// Complexity returns the complexity setting of the first encoder.
// All stream encoders use the same complexity.
func (e *Encoder) Complexity() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].Complexity()
	}
	return 10 // Default
}

// SetFEC enables or disables in-band Forward Error Correction for all streams.
// When enabled, encoders include LBRR data for loss recovery.
func (e *Encoder) SetFEC(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetFEC(enabled)
	}
}

// FECEnabled returns whether FEC is enabled (from first encoder).
func (e *Encoder) FECEnabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].FECEnabled()
	}
	return false
}

// SetPacketLoss sets the expected packet loss percentage (0-100) for all streams.
// This affects FEC behavior and bitrate allocation.
func (e *Encoder) SetPacketLoss(lossPercent int) {
	for _, enc := range e.encoders {
		enc.SetPacketLoss(lossPercent)
	}
}

// PacketLoss returns the expected packet loss percentage (from first encoder).
func (e *Encoder) PacketLoss() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].PacketLoss()
	}
	return 0
}

// SetDTX enables or disables Discontinuous Transmission for all streams.
// When enabled, packets are suppressed during silence.
func (e *Encoder) SetDTX(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetDTX(enabled)
	}
}

// DTXEnabled returns whether DTX is enabled (from first encoder).
func (e *Encoder) DTXEnabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].DTXEnabled()
	}
	return false
}

// GetFinalRange returns the final range coder state for all streams.
// The values from all streams are XOR combined to produce a single verification value.
// This matches libopus OPUS_GET_FINAL_RANGE for multistream encoders.
// Must be called after Encode() to get a meaningful value.
func (e *Encoder) GetFinalRange() uint32 {
	var combined uint32
	for _, enc := range e.encoders {
		combined ^= enc.FinalRange()
	}
	return combined
}

// Lookahead returns the encoder's algorithmic delay in samples at 48kHz.
// This includes both CELT delay compensation and mode-specific delay.
// For multistream, all stream encoders have the same lookahead.
// Reference: libopus OPUS_GET_LOOKAHEAD
func (e *Encoder) Lookahead() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].Lookahead()
	}
	// Default: 2.5ms base + 130 samples delay compensation
	return e.sampleRate/400 + 130
}

// Signal returns the current signal type hint (from first encoder).
// All stream encoders share the same signal type setting.
func (e *Encoder) Signal() types.Signal {
	if len(e.encoders) > 0 {
		return e.encoders[0].SignalType()
	}
	return types.SignalAuto
}

// SetSignal sets the signal type hint for all stream encoders.
// SignalVoice biases toward SILK mode, SignalMusic toward CELT mode.
func (e *Encoder) SetSignal(signal types.Signal) {
	for _, enc := range e.encoders {
		enc.SetSignalType(signal)
	}
}

// SetMaxBandwidth sets the maximum bandwidth limit for all stream encoders.
// The actual bandwidth will be clamped to this limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	for _, enc := range e.encoders {
		enc.SetMaxBandwidth(bw)
	}
}

// MaxBandwidth returns the maximum bandwidth limit (from first encoder).
// All stream encoders share the same max bandwidth setting.
func (e *Encoder) MaxBandwidth() types.Bandwidth {
	if len(e.encoders) > 0 {
		return e.encoders[0].MaxBandwidth()
	}
	return types.BandwidthFullband
}

// SetLSBDepth sets the input signal's LSB depth for all stream encoders.
// Valid range is 8-24 bits. This affects DTX sensitivity.
func (e *Encoder) SetLSBDepth(depth int) error {
	if depth < 8 || depth > 24 {
		return ErrInvalidLSBDepth
	}
	for _, enc := range e.encoders {
		enc.SetLSBDepth(depth)
	}
	return nil
}

// LSBDepth returns the current LSB depth setting (from first encoder).
// All stream encoders share the same LSB depth setting.
func (e *Encoder) LSBDepth() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].LSBDepth()
	}
	return 24 // Default
}

// validateEncoderLayout verifies that all coupled streams have valid L/R pairs.
// For a coupled stream to be valid, both the left channel (even index) and
// right channel (odd index) must be mapped to an input channel (not silent).
// This catches invalid configurations early during encoder creation.
func validateEncoderLayout(mapping []byte, coupledStreams int) error {
	// Track which coupled stream channels are mapped
	// For each coupled stream, we need both left (even) and right (odd)
	leftMapped := make([]bool, coupledStreams)
	rightMapped := make([]bool, coupledStreams)

	for _, m := range mapping {
		if m == 255 {
			continue // Silent channel, skip
		}

		idx := int(m)
		if idx < 2*coupledStreams {
			// This is a coupled stream channel
			streamIdx := idx / 2
			if idx%2 == 0 {
				leftMapped[streamIdx] = true
			} else {
				rightMapped[streamIdx] = true
			}
		}
		// Uncoupled streams don't need validation - mono is always valid
	}

	// Verify each coupled stream has both channels mapped
	for i := 0; i < coupledStreams; i++ {
		if !leftMapped[i] || !rightMapped[i] {
			return fmt.Errorf("%w: coupled stream %d", ErrInvalidLayout, i)
		}
	}

	return nil
}
