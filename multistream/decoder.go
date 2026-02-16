package multistream

import (
	"errors"
	"fmt"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/hybrid"
	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/silk"
)

// Errors for multistream decoder creation and operation.
var (
	// ErrInvalidChannels indicates channels is not in the valid range (1-255).
	ErrInvalidChannels = errors.New("multistream: invalid channel count (must be 1-255)")

	// ErrInvalidStreams indicates streams is not in the valid range (1-255).
	ErrInvalidStreams = errors.New("multistream: invalid stream count (must be 1-255)")

	// ErrInvalidCoupledStreams indicates coupledStreams is invalid (must be 0 to streams).
	ErrInvalidCoupledStreams = errors.New("multistream: invalid coupled streams (must be 0 to streams)")

	// ErrTooManyChannels indicates the total channel count exceeds the maximum.
	ErrTooManyChannels = errors.New("multistream: too many channels (streams + coupled_streams must be <= 255)")

	// ErrInvalidMapping indicates the mapping table is malformed.
	ErrInvalidMapping = errors.New("multistream: invalid mapping table")

	// ErrInvalidProjectionMatrix indicates malformed projection demixing metadata.
	ErrInvalidProjectionMatrix = errors.New("multistream: invalid projection demixing matrix")
)

// streamDecoder is an internal interface that wraps the different decoder types.
// This allows the multistream decoder to manage heterogeneous stream decoders uniformly.
type streamDecoder interface {
	// Decode decodes a packet and returns PCM samples as float64.
	// For stereo decoders, samples are interleaved [L0, R0, L1, R1, ...].
	Decode(data []byte, frameSize int) ([]float64, error)

	// DecodeStereo decodes a stereo packet and returns interleaved samples.
	// Only valid for stereo (2-channel) decoders.
	DecodeStereo(data []byte, frameSize int) ([]float64, error)

	// Reset clears decoder state for a new stream.
	Reset()

	// Channels returns the number of channels this decoder produces (1 or 2).
	Channels() int
}

const (
	streamModeSILK = iota
	streamModeHybrid
	streamModeCELT
)

type streamTOC struct {
	mode      int
	bandwidth int
	stereo    bool
}

// parseStreamTOC extracts mode/bandwidth/stereo from an Opus TOC byte.
// Bandwidth uses Opus values 0=NB,1=MB,2=WB,3=SWB,4=FB.
func parseStreamTOC(toc byte) streamTOC {
	config := toc >> 3
	stereo := (toc & 0x04) != 0

	switch {
	case config < 4:
		return streamTOC{mode: streamModeSILK, bandwidth: 0, stereo: stereo}
	case config < 8:
		return streamTOC{mode: streamModeSILK, bandwidth: 1, stereo: stereo}
	case config < 12:
		return streamTOC{mode: streamModeSILK, bandwidth: 2, stereo: stereo}
	case config < 14:
		return streamTOC{mode: streamModeHybrid, bandwidth: 3, stereo: stereo}
	case config < 16:
		return streamTOC{mode: streamModeHybrid, bandwidth: 4, stereo: stereo}
	case config < 20:
		return streamTOC{mode: streamModeCELT, bandwidth: 0, stereo: stereo}
	case config < 24:
		return streamTOC{mode: streamModeCELT, bandwidth: 2, stereo: stereo}
	case config < 28:
		return streamTOC{mode: streamModeCELT, bandwidth: 3, stereo: stereo}
	default:
		return streamTOC{mode: streamModeCELT, bandwidth: 4, stereo: stereo}
	}
}

// opusStreamDecoder wraps per-mode decoders and dispatches by packet TOC.
// This mirrors libopus multistream decode behavior where each stream owns a
// full Opus decoder state (SILK/CELT/Hybrid), not just hybrid-only state.
type opusStreamDecoder struct {
	channels int

	hybridDec *hybrid.Decoder
	celtDec   *celt.Decoder
	silkDec   *silk.Decoder

	lastMode         int
	lastBandwidth    int
	lastPacketStereo bool
	haveDecoded      bool
}

func newOpusStreamDecoder(channels int) *opusStreamDecoder {
	return &opusStreamDecoder{
		channels:      channels,
		hybridDec:     hybrid.NewDecoder(channels),
		celtDec:       celt.NewDecoder(channels),
		silkDec:       silk.NewDecoder(),
		lastMode:      streamModeHybrid,
		lastBandwidth: 4,
	}
}

// Decode decodes a packet for mono streams.
func (d *opusStreamDecoder) Decode(data []byte, frameSize int) ([]float64, error) {
	return d.decodePacket(data, frameSize)
}

// DecodeStereo decodes a packet for coupled (stereo) streams.
func (d *opusStreamDecoder) DecodeStereo(data []byte, frameSize int) ([]float64, error) {
	return d.decodePacket(data, frameSize)
}

// Reset resets all mode decoder states.
func (d *opusStreamDecoder) Reset() {
	d.hybridDec.Reset()
	d.celtDec.Reset()
	d.silkDec.Reset()
	d.lastMode = streamModeHybrid
	d.lastBandwidth = 4
	d.lastPacketStereo = false
	d.haveDecoded = false
}

// Channels returns the channel count for this decoder.
func (d *opusStreamDecoder) Channels() int {
	return d.channels
}

func float32ToFloat64Slice(in []float32) []float64 {
	out := make([]float64, len(in))
	for i := range in {
		out[i] = float64(in[i])
	}
	return out
}

func (d *opusStreamDecoder) decodeSILK(data []byte, frameSize int, packetStereo bool, opusBandwidth int) ([]float64, error) {
	bw, ok := silk.BandwidthFromOpus(opusBandwidth)
	if !ok {
		return nil, fmt.Errorf("multistream: invalid SILK bandwidth: %d", opusBandwidth)
	}

	var out32 []float32
	var err error
	switch {
	case packetStereo && d.channels == 2:
		out32, err = d.silkDec.DecodeStereo(data, bw, frameSize, true)
	case packetStereo && d.channels == 1:
		out32, err = d.silkDec.DecodeStereoToMono(data, bw, frameSize, true)
	case !packetStereo && d.channels == 2:
		out32, err = d.silkDec.DecodeMonoToStereo(data, bw, frameSize, true, d.lastPacketStereo)
	default:
		out32, err = d.silkDec.Decode(data, bw, frameSize, true)
	}
	if err != nil {
		return nil, err
	}

	return float32ToFloat64Slice(out32), nil
}

func (d *opusStreamDecoder) decodeFramePayload(frame []byte, frameSize int, toc streamTOC) ([]float64, error) {
	var out []float64
	var err error

	switch toc.mode {
	case streamModeSILK:
		out, err = d.decodeSILK(frame, frameSize, toc.stereo, toc.bandwidth)
	case streamModeHybrid:
		if !hybrid.ValidHybridFrameSize(frameSize) {
			return nil, fmt.Errorf("multistream: invalid hybrid frame size %d", frameSize)
		}
		out, err = d.hybridDec.DecodeWithPacketStereo(frame, frameSize, toc.stereo)
	case streamModeCELT:
		d.celtDec.SetBandwidth(celt.BandwidthFromOpusConfig(toc.bandwidth))
		out, err = d.celtDec.DecodeFrameWithPacketStereo(frame, frameSize, toc.stereo)
	default:
		return nil, ErrInvalidPacket
	}
	if err != nil {
		return nil, err
	}

	d.lastMode = toc.mode
	d.lastBandwidth = toc.bandwidth
	d.lastPacketStereo = toc.stereo
	d.haveDecoded = true
	return out, nil
}

func (d *opusStreamDecoder) decodePLC(frameSize int) ([]float64, error) {
	if !d.haveDecoded {
		return make([]float64, frameSize*d.channels), nil
	}

	switch d.lastMode {
	case streamModeSILK:
		return d.decodeSILK(nil, frameSize, d.lastPacketStereo, d.lastBandwidth)
	case streamModeHybrid:
		return d.hybridDec.DecodeWithPacketStereo(nil, frameSize, d.lastPacketStereo)
	case streamModeCELT:
		d.celtDec.SetBandwidth(celt.BandwidthFromOpusConfig(d.lastBandwidth))
		return d.celtDec.DecodeFrameWithPacketStereo(nil, frameSize, d.lastPacketStereo)
	default:
		return make([]float64, frameSize*d.channels), nil
	}
}

func (d *opusStreamDecoder) decodePacket(data []byte, frameSize int) ([]float64, error) {
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize)
	}
	if len(data) < 1 {
		return nil, ErrPacketTooShort
	}

	toc := parseStreamTOC(data[0])
	parsed, err := parseOpusPacket(data, false)
	if err != nil {
		return nil, err
	}

	frameCount := len(parsed.frames)
	if frameCount == 0 {
		return nil, ErrInvalidPacket
	}

	if frameCount == 1 {
		return d.decodeFramePayload(parsed.frames[0], frameSize, toc)
	}
	if frameSize%frameCount != 0 {
		return nil, fmt.Errorf("multistream: frameSize %d not divisible by packet frame count %d", frameSize, frameCount)
	}

	subFrameSize := frameSize / frameCount
	out := make([]float64, 0, frameSize*d.channels)
	for i := 0; i < frameCount; i++ {
		frameDecoded, err := d.decodeFramePayload(parsed.frames[i], subFrameSize, toc)
		if err != nil {
			return nil, err
		}
		out = append(out, frameDecoded...)
	}

	return out, nil
}

// Decoder decodes Opus multistream packets containing multiple elementary streams.
// Each stream is decoded independently and routed to output channels via a mapping table.
//
// Multistream packets are used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 7845 Section 5.1.1
type Decoder struct {
	// sampleRate is the output sample rate (8000, 12000, 16000, 24000, or 48000 Hz).
	sampleRate int

	// outputChannels is the total number of output channels (1-255).
	outputChannels int

	// streams is the total number of elementary streams (N).
	streams int

	// coupledStreams is the number of coupled (stereo) streams (M).
	// The first M streams produce 2 channels each, the remaining N-M produce 1 channel.
	coupledStreams int

	// mapping is the channel mapping table.
	// mapping[i] indicates which decoded channel feeds output channel i.
	// Values 0 to 2*M-1 are from coupled streams (even=left, odd=right).
	// Values 2*M to N+M-1 are from uncoupled streams.
	// Value 255 indicates a silent channel.
	mapping []byte

	// decoders contains one decoder per stream.
	// First M decoders are stereo (for coupled streams).
	// Remaining N-M decoders are mono (for uncoupled streams).
	decoders []streamDecoder

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State

	// Optional projection demixing matrix in column-major layout.
	// Coefficients are normalized to [-1, 1) by dividing S16 entries by 32768.
	projectionDemixing []float64
	projectionCols     int
	projectionScratch  []float64
}

// NewDecoder creates a new multistream decoder.
//
// Parameters:
//   - sampleRate: output sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total output channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//
// The mapping table determines how decoded audio is routed to output channels:
//   - Values 0 to 2*M-1: from coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: from uncoupled (mono) streams
//   - Value 255: silent channel (output zeros)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Channel 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Channel 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Channel 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Channel 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Channel 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Channel 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*Decoder, error) {
	// Validate parameters
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

	// Create stream decoders
	// First M streams are coupled (stereo), remaining N-M are mono
	decoders := make([]streamDecoder, streams)
	for i := 0; i < streams; i++ {
		var channels int
		if i < coupledStreams {
			channels = 2 // Coupled stream = stereo
		} else {
			channels = 1 // Uncoupled stream = mono
		}
		decoders[i] = newOpusStreamDecoder(channels)
	}

	// Copy mapping to avoid external mutation
	mappingCopy := make([]byte, len(mapping))
	copy(mappingCopy, mapping)

	return &Decoder{
		sampleRate:     sampleRate,
		outputChannels: channels,
		streams:        streams,
		coupledStreams: coupledStreams,
		mapping:        mappingCopy,
		decoders:       decoders,
		plcState:       plc.NewState(),
	}, nil
}

// Reset clears all decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	for _, dec := range d.decoders {
		dec.Reset()
	}
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
}

// SetProjectionDemixingMatrix sets optional projection demixing coefficients.
// Matrix data is S16LE, column-major, with dimensions:
//
//	rows = output channels
//	cols = streams + coupledStreams
//
// This method is intended for mapping-family-3 projection flows where
// decoded stream channels are routed with trivial mapping and then demixed
// to output channels.
func (d *Decoder) SetProjectionDemixingMatrix(matrix []byte) error {
	if len(matrix) == 0 {
		d.projectionDemixing = nil
		d.projectionCols = 0
		return nil
	}

	rows := d.outputChannels
	cols := d.streams + d.coupledStreams
	if rows <= 0 || cols <= 0 {
		return ErrInvalidProjectionMatrix
	}
	if len(matrix) != 2*rows*cols {
		return ErrInvalidProjectionMatrix
	}

	// Projection family decoders use trivial channel mapping.
	for i := 0; i < rows; i++ {
		if d.mapping[i] != byte(i) {
			return ErrInvalidProjectionMatrix
		}
	}

	needed := rows * cols
	if cap(d.projectionDemixing) < needed {
		d.projectionDemixing = make([]float64, needed)
	}
	coeffs := d.projectionDemixing[:needed]
	for i := 0; i < needed; i++ {
		v := int16(uint16(matrix[2*i]) | (uint16(matrix[2*i+1]) << 8))
		coeffs[i] = float64(v) / 32768.0
	}
	d.projectionCols = cols
	return nil
}

// Channels returns the total number of output channels.
func (d *Decoder) Channels() int {
	return d.outputChannels
}

// SampleRate returns the output sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}

// Streams returns the total number of elementary streams.
func (d *Decoder) Streams() int {
	return d.streams
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (d *Decoder) CoupledStreams() int {
	return d.coupledStreams
}

// NewDecoderDefault creates a multistream decoder with default Vorbis-style mapping
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
func NewDecoderDefault(sampleRate, channels int) (*Decoder, error) {
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		return nil, err
	}
	return NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
}
