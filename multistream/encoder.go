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

	"github.com/thesyncim/gopus/celt"
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

	// Optional projection-family mixing matrix coefficients (column-major).
	// Coefficients are normalized to [-1, 1) by dividing S16 entries by 32768.
	projectionMixing  []float64
	projectionCols    int
	projectionRows    int
	projectionScratch []float64
	projectionFrame   []float64

	// streamBitrates stores per-stream rates computed by allocation policy.
	streamBitrates []int

	// streamSurroundTrim stores per-stream surround trim derived from surround masks.
	streamSurroundTrim []float64

	// streamEnergyMask stores per-stream CELT energy masks (max 42 values/stream).
	streamEnergyMask []float64

	// surroundBandSMR stores per-channel surround masks (21 bands per channel).
	surroundBandSMR []float64

	// surroundWindowMem mirrors libopus surround_analysis overlap history (per channel).
	surroundWindowMem []float64

	// surroundPreemphMem mirrors libopus surround_analysis preemphasis memory (per channel).
	surroundPreemphMem []float64

	// surroundInputScratch holds per-channel overlap+frame analysis input.
	surroundInputScratch []float64

	// surroundBandScratch stores temporary per-band energies for one channel.
	surroundBandScratch [surroundBands]float64

	// surroundAnalysisEncoder computes CELT band energies for surround analysis.
	surroundAnalysisEncoder *celt.Encoder
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
		streamEnc := encoder.NewEncoder(sampleRate, chans)
		// Match libopus multistream defaults: VBR enabled with constraint on.
		streamEnc.SetVBRConstraint(true)
		encoders[i] = streamEnc
	}

	// Copy mapping to avoid external mutation
	mappingCopy := make([]byte, len(mapping))
	copy(mappingCopy, mapping)

	mappingFamily := inferMappingFamily(channels, streams, coupledStreams, mappingCopy)
	lfeStream := inferLFEStream(mappingFamily, channels, streams)

	enc := &Encoder{
		sampleRate:              sampleRate,
		inputChannels:           channels,
		streams:                 streams,
		coupledStreams:          coupledStreams,
		mapping:                 mappingCopy,
		encoders:                encoders,
		bitrate:                 256000, // Default 256 kbps total
		mappingFamily:           mappingFamily,
		lfeStream:               lfeStream,
		streamBitrates:          make([]int, streams),
		streamSurroundTrim:      make([]float64, streams),
		streamEnergyMask:        make([]float64, streams*2*surroundBands),
		surroundBandSMR:         make([]float64, channels*surroundBands),
		surroundWindowMem:       make([]float64, channels*celt.Overlap),
		surroundPreemphMem:      make([]float64, channels),
		surroundAnalysisEncoder: celt.NewEncoder(1),
	}
	enc.applyLFEFlags()
	return enc, nil
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
	enc.applyLFEFlags()
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
	enc.applyLFEFlags()
	if mappingFamily == 3 {
		if err := enc.initProjectionMixingDefaults(); err != nil {
			return nil, err
		}
	}
	return enc, nil
}

// Reset clears all encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	for i, enc := range e.encoders {
		enc.Reset()
		enc.SetLFE(i == e.lfeStream)
		enc.SetCELTSurroundTrim(0)
		if i < len(e.streamSurroundTrim) {
			e.streamSurroundTrim[i] = 0
		}
	}
	if len(e.surroundBandSMR) > 0 {
		clear(e.surroundBandSMR)
	}
	if len(e.streamEnergyMask) > 0 {
		clear(e.streamEnergyMask)
	}
	if len(e.surroundWindowMem) > 0 {
		clear(e.surroundWindowMem)
	}
	if len(e.surroundPreemphMem) > 0 {
		clear(e.surroundPreemphMem)
	}
}

func (e *Encoder) applyLFEFlags() {
	for i, enc := range e.encoders {
		enc.SetLFE(i == e.lfeStream)
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

// SetMode sets the base mode for all stream encoders.
func (e *Encoder) SetMode(mode encoder.Mode) {
	for _, enc := range e.encoders {
		enc.SetMode(mode)
	}
}

// Mode returns the base mode from the first stream encoder.
func (e *Encoder) Mode() encoder.Mode {
	if len(e.encoders) > 0 {
		return e.encoders[0].Mode()
	}
	return encoder.ModeAuto
}

// SetLowDelay toggles low-delay application behavior for all stream encoders.
func (e *Encoder) SetLowDelay(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetLowDelay(enabled)
	}
}

// LowDelay reports low-delay application behavior from the first stream encoder.
func (e *Encoder) LowDelay() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].LowDelay()
	}
	return false
}

// SetVoIPApplication toggles VoIP application bias for all stream encoders.
func (e *Encoder) SetVoIPApplication(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVoIPApplication(enabled)
	}
}

// VoIPApplication reports VoIP application bias from the first stream encoder.
func (e *Encoder) VoIPApplication() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].VoIPApplication()
	}
	return false
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
	// Match libopus float logSum() in opus_multistream_encoder.c:
	// log2(4^a + 4^b) / 2
	return a + 0.5*math.Log2(1.0+math.Exp2(2.0*(b-a)))
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

func resamplingFactor(rate int) int {
	switch rate {
	case 48000:
		return 1
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	default:
		return 0
	}
}

func surroundAnalysisFreqSize(frameSize int) (int, bool) {
	switch frameSize {
	case 120, 240, 480, 960:
		return frameSize, true
	default:
		if frameSize > 0 && frameSize%960 == 0 {
			return 960, true
		}
		return 0, false
	}
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

func (e *Encoder) ensureSurroundInputScratch(size int) []float64 {
	if cap(e.surroundInputScratch) < size {
		e.surroundInputScratch = make([]float64, size)
	}
	return e.surroundInputScratch[:size]
}

func (e *Encoder) computeSurroundBandSMR(pcm []float64, frameSize int, bandSMR []float64) bool {
	if frameSize <= 0 || e.inputChannels < 3 || e.inputChannels > 8 {
		return false
	}
	if len(pcm) < frameSize*e.inputChannels {
		return false
	}
	if len(bandSMR) < e.inputChannels*surroundBands {
		return false
	}

	var pos [8]int
	if !channelPositions(e.inputChannels, pos[:]) {
		return false
	}

	upsample := resamplingFactor(e.sampleRate)
	if upsample <= 0 {
		return false
	}
	analysisFrameSize := frameSize * upsample
	freqSize, ok := surroundAnalysisFreqSize(analysisFrameSize)
	if !ok || analysisFrameSize%freqSize != 0 {
		return false
	}
	nbFrames := analysisFrameSize / freqSize
	overlap := celt.Overlap

	if cap(e.surroundWindowMem) < e.inputChannels*overlap {
		e.surroundWindowMem = make([]float64, e.inputChannels*overlap)
	}
	if cap(e.surroundPreemphMem) < e.inputChannels {
		e.surroundPreemphMem = make([]float64, e.inputChannels)
	}
	if e.surroundAnalysisEncoder == nil {
		e.surroundAnalysisEncoder = celt.NewEncoder(1)
	}

	in := e.ensureSurroundInputScratch(overlap + analysisFrameSize)

	var maskLogE [3][surroundBands]float64
	for c := 0; c < 3; c++ {
		for i := 0; i < surroundBands; i++ {
			maskLogE[c][i] = -28.0
		}
	}

	for ch := 0; ch < e.inputChannels; ch++ {
		copy(in[:overlap], e.surroundWindowMem[ch*overlap:(ch+1)*overlap])
		clear(in[overlap:])

		for i := 0; i < frameSize; i++ {
			in[overlap+i*upsample] = pcm[i*e.inputChannels+ch] * celt.CELTSigScale
		}

		m := e.surroundPreemphMem[ch]
		for i := 0; i < analysisFrameSize; i++ {
			x := in[overlap+i]
			in[overlap+i] = x - m
			m = celt.PreemphCoef * x
		}
		e.surroundPreemphMem[ch] = m

		for i := 0; i < surroundBands; i++ {
			e.surroundBandScratch[i] = math.Inf(-1)
		}

		for frame := 0; frame < nbFrames; frame++ {
			start := frame * freqSize
			end := start + freqSize + overlap
			coeffs := celt.MDCTForwardWithOverlap(in[start:end], overlap)
			if upsample != 1 {
				bound := freqSize / upsample
				if bound > len(coeffs) {
					bound = len(coeffs)
				}
				for i := 0; i < bound; i++ {
					coeffs[i] *= float64(upsample)
				}
				for i := bound; i < len(coeffs); i++ {
					coeffs[i] = 0
				}
			}

			var tmp [surroundBands]float64
			e.surroundAnalysisEncoder.ComputeBandEnergiesInto(coeffs, surroundBands, freqSize, tmp[:])
			for i := 0; i < surroundBands; i++ {
				if tmp[i] > e.surroundBandScratch[i] {
					e.surroundBandScratch[i] = tmp[i]
				}
			}
		}

		for i := 1; i < surroundBands; i++ {
			if e.surroundBandScratch[i-1]-1.0 > e.surroundBandScratch[i] {
				e.surroundBandScratch[i] = e.surroundBandScratch[i-1] - 1.0
			}
		}
		for i := surroundBands - 2; i >= 0; i-- {
			if e.surroundBandScratch[i+1]-2.0 > e.surroundBandScratch[i] {
				e.surroundBandScratch[i] = e.surroundBandScratch[i+1] - 2.0
			}
		}

		copy(bandSMR[ch*surroundBands:(ch+1)*surroundBands], e.surroundBandScratch[:])

		switch pos[ch] {
		case 1:
			for i := 0; i < surroundBands; i++ {
				maskLogE[0][i] = logSum(maskLogE[0][i], e.surroundBandScratch[i])
			}
		case 3:
			for i := 0; i < surroundBands; i++ {
				maskLogE[2][i] = logSum(maskLogE[2][i], e.surroundBandScratch[i])
			}
		case 2:
			for i := 0; i < surroundBands; i++ {
				maskLogE[0][i] = logSum(maskLogE[0][i], e.surroundBandScratch[i]-0.5)
				maskLogE[2][i] = logSum(maskLogE[2][i], e.surroundBandScratch[i]-0.5)
			}
		}

		copy(e.surroundWindowMem[ch*overlap:(ch+1)*overlap], in[analysisFrameSize:analysisFrameSize+overlap])
	}

	for i := 0; i < surroundBands; i++ {
		maskLogE[1][i] = math.Min(maskLogE[0][i], maskLogE[2][i])
	}
	channelOffset := 0.5 * math.Log2(2.0/float64(e.inputChannels-1))
	for c := 0; c < 3; c++ {
		for i := 0; i < surroundBands; i++ {
			maskLogE[c][i] += channelOffset
		}
	}

	for ch := 0; ch < e.inputChannels; ch++ {
		row := bandSMR[ch*surroundBands : (ch+1)*surroundBands]
		if pos[ch] == 0 {
			clear(row)
			continue
		}
		mask := maskLogE[pos[ch]-1][:]
		for i := 0; i < surroundBands; i++ {
			row[i] -= mask[i]
		}
	}

	return true
}

func (e *Encoder) updateSurroundTrimFromPCM(pcm []float64, frameSize int) bool {
	if cap(e.streamSurroundTrim) < e.streams {
		e.streamSurroundTrim = make([]float64, e.streams)
	}
	trim := e.streamSurroundTrim[:e.streams]
	for i := range trim {
		trim[i] = 0
	}
	if !e.isSurroundMapping() {
		return false
	}

	needed := e.inputChannels * surroundBands
	if cap(e.surroundBandSMR) < needed {
		e.surroundBandSMR = make([]float64, needed)
	}
	bandSMR := e.surroundBandSMR[:needed]
	clear(bandSMR)
	if !e.computeSurroundBandSMR(pcm, frameSize, bandSMR) {
		return false
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
	return true
}

// multistreamCVBRBoundScale computes a constrained-VBR burst scale that keeps
// aggregate multistream packet bursts within the Opus 1275-byte packet cap.
// A scale of 1 keeps libopus single-stream behavior (~2x base burst ceiling).
func multistreamCVBRBoundScale(totalBitrate, sampleRate, frameSize int) float64 {
	if totalBitrate <= 0 || sampleRate <= 0 || frameSize <= 0 {
		return 1.0
	}
	targetBytes := (totalBitrate * frameSize) / (8 * sampleRate)
	if targetBytes <= 0 {
		return 1.0
	}
	const maxPacketBytes = 1275
	// Reserve a small framing margin for self-delimited multistream headers and
	// per-stream TOC/entropy tail variance.
	const framingMarginBytes = 16
	maxBurstBytes := maxPacketBytes - framingMarginBytes
	if maxBurstBytes < 1 {
		maxBurstBytes = 1
	}
	burstMultiple := float64(maxBurstBytes) / float64(targetBytes)
	if burstMultiple >= 2.0 {
		return 1.0
	}
	if burstMultiple <= 1.0 {
		return 0.0
	}
	return burstMultiple - 1.0
}

func (e *Encoder) applyPerStreamPolicy(frameSize int, pcm []float64) {
	rates := e.allocateRates(frameSize)
	hasSurroundMask := false
	if e.isSurroundMapping() {
		hasSurroundMask = e.updateSurroundTrimFromPCM(pcm, frameSize)
	}
	var streamMasks []float64
	if hasSurroundMask {
		needed := e.streams * 2 * surroundBands
		if cap(e.streamEnergyMask) < needed {
			e.streamEnergyMask = make([]float64, needed)
		}
		streamMasks = e.streamEnergyMask[:needed]
		clear(streamMasks)
	}
	cvbrBoundScale := 1.0
	if len(e.encoders) > 0 && e.encoders[0].GetBitrateMode() == encoder.ModeCVBR {
		cvbrBoundScale = multistreamCVBRBoundScale(e.bitrateForAllocation(frameSize), e.sampleRate, frameSize)
	}

	surroundBandwidth := e.surroundBandwidth(frameSize)
	for i := 0; i < e.streams; i++ {
		enc := e.encoders[i]
		enc.SetBitrate(rates[i])
		enc.SetLFE(i == e.lfeStream)
		enc.SetCELTCVBRBoundScale(cvbrBoundScale)

		switch {
		case e.isSurroundMapping():
			enc.SetBandwidth(surroundBandwidth)
			if i < e.coupledStreams {
				// Preserve surround image parity with libopus on coupled streams.
				enc.SetMode(encoder.ModeCELT)
				enc.SetForceChannels(2)
			}
			if i < len(e.streamSurroundTrim) {
				enc.SetCELTSurroundTrim(e.streamSurroundTrim[i])
			} else {
				enc.SetCELTSurroundTrim(0)
			}
			enc.SetCELTEnergyMask(nil)
			if hasSurroundMask && i != e.lfeStream {
				base := i * 2 * surroundBands
				if i < e.coupledStreams {
					left := e.inputChannelForMapping(byte(2 * i))
					right := e.inputChannelForMapping(byte(2*i + 1))
					if left >= 0 && right >= 0 {
						dst := streamMasks[base : base+2*surroundBands]
						copy(dst[:surroundBands], e.surroundBandSMR[left*surroundBands:(left+1)*surroundBands])
						copy(dst[surroundBands:], e.surroundBandSMR[right*surroundBands:(right+1)*surroundBands])
						enc.SetCELTEnergyMask(dst)
					}
				} else {
					mappingIdx := byte(2*e.coupledStreams + (i - e.coupledStreams))
					mono := e.inputChannelForMapping(mappingIdx)
					if mono >= 0 {
						dst := streamMasks[base : base+surroundBands]
						copy(dst, e.surroundBandSMR[mono*surroundBands:(mono+1)*surroundBands])
						enc.SetCELTEnergyMask(dst)
					}
				}
			}
		case e.isAmbisonicsMapping():
			enc.SetMode(encoder.ModeCELT)
			enc.SetCELTSurroundTrim(0)
			enc.SetCELTEnergyMask(nil)
		default:
			enc.SetCELTSurroundTrim(0)
			enc.SetCELTEnergyMask(nil)
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

func (e *Encoder) initProjectionMixingDefaults() error {
	matrix, ok := defaultProjectionMixingMatrix(e.inputChannels, e.streams, e.coupledStreams)
	if !ok {
		return fmt.Errorf("multistream: missing projection mixing defaults for channels=%d streams=%d coupled=%d",
			e.inputChannels, e.streams, e.coupledStreams)
	}

	needed := len(matrix)
	if cap(e.projectionMixing) < needed {
		e.projectionMixing = make([]float64, needed)
	}
	coeffs := e.projectionMixing[:needed]
	for i, v := range matrix {
		coeffs[i] = float64(v) / 32768.0
	}

	e.projectionRows = e.inputChannels
	e.projectionCols = e.inputChannels
	return nil
}

func (e *Encoder) applyProjectionMixing(pcm []float64, frameSize int) []float64 {
	rows := e.projectionRows
	cols := e.projectionCols
	if len(e.projectionMixing) == 0 || rows <= 0 || cols <= 0 {
		return pcm
	}

	if cap(e.projectionScratch) < len(pcm) {
		e.projectionScratch = make([]float64, len(pcm))
	}
	mixed := e.projectionScratch[:len(pcm)]

	if cap(e.projectionFrame) < cols {
		e.projectionFrame = make([]float64, cols)
	}
	frame := e.projectionFrame[:cols]

	for s := 0; s < frameSize; s++ {
		in := pcm[s*cols : (s+1)*cols]
		out := mixed[s*rows : (s+1)*rows]
		copy(frame, in)
		for row := 0; row < rows; row++ {
			sum := 0.0
			for col := 0; col < cols; col++ {
				sum += e.projectionMixing[col*rows+row] * frame[col]
			}
			out[row] = sum
		}
	}
	return mixed
}

// assembleMultistreamPacket combines individual stream packets into a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 packets use self-delimited packet framing
//   - Last packet uses standard framing
func assembleMultistreamPacket(streamPackets [][]byte) ([]byte, error) {
	if len(streamPackets) == 0 {
		return nil, nil
	}

	encoded := make([][]byte, len(streamPackets))
	totalSize := 0
	for i, packet := range streamPackets {
		if len(packet) == 0 {
			return nil, ErrInvalidPacket
		}

		if i < len(streamPackets)-1 {
			var err error
			packet, err = makeSelfDelimitedPacket(packet)
			if err != nil {
				return nil, err
			}
		}
		encoded[i] = packet
		totalSize += len(packet)
	}

	output := make([]byte, totalSize)
	offset := 0
	for _, packet := range encoded {
		copy(output[offset:], packet)
		offset += len(packet)
	}
	return output, nil
}

// Encode encodes multi-channel PCM samples to an Opus multistream packet.
//
// Parameters:
//   - pcm: input samples as float64, sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
//   - frameSize: number of samples per channel (must be valid for Opus: 120, 240, 480, 960, 1920, 2880)
//
// Returns:
//   - The encoded multistream packet
//   - nil, nil if DTX is active for all streams (all returned 1-byte TOC-only packets)
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

	inputPCM := pcm
	if e.mappingFamily == 3 {
		inputPCM = e.applyProjectionMixing(pcm, frameSize)
	}

	// Route input channels to stream buffers
	streamBuffers := routeChannelsToStreams(inputPCM, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)

	// Encode each stream
	streamPackets := make([][]byte, e.streams)
	allDTX := true

	for i := 0; i < e.streams; i++ {
		packet, err := e.encoders[i].Encode(streamBuffers[i], frameSize)
		if err != nil {
			return nil, fmt.Errorf("stream %d encode failed: %w", i, err)
		}

		if packet == nil {
			streamPackets[i] = []byte{}
		} else {
			streamPackets[i] = packet
			// DTX packets are 1-byte TOC-only; full packets are >1 byte
			if len(packet) > 1 {
				allDTX = false
			}
		}
	}

	// If all streams are DTX (1-byte TOC or nil), return nil to signal silence
	if allDTX {
		return nil, nil
	}

	// Assemble multistream packet with RFC 6716 Appendix B framing.
	packet, err := assembleMultistreamPacket(streamPackets)
	if err != nil {
		return nil, err
	}
	return packet, nil
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

// SetBitrateMode sets the bitrate mode for all stream encoders.
func (e *Encoder) SetBitrateMode(mode encoder.BitrateMode) {
	for _, enc := range e.encoders {
		enc.SetBitrateMode(mode)
	}
}

// BitrateMode returns the current bitrate mode (from first stream encoder).
func (e *Encoder) BitrateMode() encoder.BitrateMode {
	if len(e.encoders) > 0 {
		return e.encoders[0].GetBitrateMode()
	}
	return encoder.ModeCVBR
}

// SetVBR enables or disables VBR mode for all stream encoders.
func (e *Encoder) SetVBR(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetVBR(enabled)
	}
}

// VBR reports whether VBR mode is enabled.
func (e *Encoder) VBR() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].VBR()
	}
	return true
}

// SetVBRConstraint enables or disables constrained VBR mode.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	for _, enc := range e.encoders {
		enc.SetVBRConstraint(constrained)
	}
}

// VBRConstraint reports whether constrained VBR is enabled.
func (e *Encoder) VBRConstraint() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].VBRConstraint()
	}
	return true
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

// SetBandwidth sets the target bandwidth for all stream encoders.
func (e *Encoder) SetBandwidth(bw types.Bandwidth) {
	for _, enc := range e.encoders {
		enc.SetBandwidth(bw)
	}
}

// Bandwidth returns the target bandwidth from the first stream encoder.
func (e *Encoder) Bandwidth() types.Bandwidth {
	if len(e.encoders) > 0 {
		return e.encoders[0].Bandwidth()
	}
	return types.BandwidthFullband
}

// SetForceChannels sets forced channel count on all stream encoders.
func (e *Encoder) SetForceChannels(channels int) {
	for _, enc := range e.encoders {
		enc.SetForceChannels(channels)
	}
}

// ForceChannels returns forced channel count from the first stream encoder.
func (e *Encoder) ForceChannels() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].ForceChannels()
	}
	return -1
}

// SetPredictionDisabled toggles inter-frame prediction for all stream encoders.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	for _, enc := range e.encoders {
		enc.SetPredictionDisabled(disabled)
	}
}

// PredictionDisabled reports whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].PredictionDisabled()
	}
	return false
}

// SetPhaseInversionDisabled toggles stereo phase inversion on all stream encoders.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	for _, enc := range e.encoders {
		enc.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].PhaseInversionDisabled()
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
