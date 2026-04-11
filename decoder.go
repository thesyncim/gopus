// decoder.go implements the public Decoder API for Opus decoding.

package gopus

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/hybrid"
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

const (
	defaultMaxPacketSamples = 5760
	defaultMaxPacketBytes   = 1500
)

// DecoderConfig configures a Decoder instance.
type DecoderConfig struct {
	// SampleRate must be one of: 8000, 12000, 16000, 24000, 48000.
	SampleRate int
	// Channels must be 1 (mono) or 2 (stereo).
	Channels int
	// MaxPacketSamples caps the maximum decoded samples per channel per packet.
	// If zero, defaultMaxPacketSamples is used.
	MaxPacketSamples int
	// MaxPacketBytes caps the maximum Opus packet size in bytes.
	// If zero, defaultMaxPacketBytes is used.
	MaxPacketBytes int
}

// DefaultDecoderConfig returns a config with default caps for the given stream format.
func DefaultDecoderConfig(sampleRate, channels int) DecoderConfig {
	return DecoderConfig{
		SampleRate:       sampleRate,
		Channels:         channels,
		MaxPacketSamples: defaultMaxPacketSamples,
		MaxPacketBytes:   defaultMaxPacketBytes,
	}
}

// Decoder decodes Opus packets into PCM audio samples.
//
// A Decoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own Decoder instance.
//
// The decoder supports all Opus modes (SILK, Hybrid, CELT) and automatically
// detects the mode from the TOC byte in each packet.
type Decoder struct {
	silkDecoder        *silk.Decoder   // SILK-only mode decoder
	celtDecoder        *celt.Decoder   // CELT-only mode decoder
	hybridDecoder      *hybrid.Decoder // Hybrid mode decoder
	sampleRate         int
	channels           int
	maxPacketSamples   int
	maxPacketBytes     int
	scratchPCM         []float32
	scratchTransition  []float32
	scratchRedundant   []float32
	lastFrameSize      int
	lastPacketDuration int
	prevMode           Mode // Track last mode for PLC
	lastPacketMode     Mode // Track last packet mode (libopus st->mode) for decode_fec gating
	lastBandwidth      Bandwidth
	prevRedundancy     bool
	prevPacketStereo   bool
	haveDecoded        bool
	redundantRng       uint32 // Range from redundancy decoding, XORed with final range
	lastDataLen        int    // Length of last packet data
	mainDecodeRng      uint32 // Final range from main decode (before any redundancy processing)
	decodeGainQ8       int    // Output gain in Q8 dB (libopus OPUS_SET_GAIN semantics)
	ignoreExtensions   bool   // libopus OPUS_SET_IGNORE_EXTENSIONS semantics

	// FEC (Forward Error Correction) state
	// Stores LBRR data from the current packet for use by the next packet's FEC decode.
	fecData       []byte    // Stored packet data containing LBRR for FEC recovery
	fecMode       Mode      // Mode of the packet containing LBRR
	fecBandwidth  Bandwidth // Bandwidth of the packet containing LBRR
	fecStereo     bool      // Whether the packet was stereo
	fecFrameSize  int       // Frame size of the packet containing LBRR
	fecFrameCount int       // Number of frames in packet
	hasFEC        bool      // True if fecData contains valid LBRR data
	scratchFEC    []float32 // Scratch buffer for FEC decode

	// Scratch range decoder to avoid per-frame heap allocations
	scratchRangeDecoder rangecoding.Decoder

	// Soft clipping memory (float decode uses none; int16 decode uses this)
	softClipMem [2]float32
	dnnBlob     *dnnblob.Blob
	dredData    []byte
	dredCache   internaldred.Cache

	// Decoder-side DNN readiness mirrors the validated model families retained
	// by OPUS_SET_DNN_BLOB so optional paths can stay dormant until they are real.
	pitchDNNLoaded     bool
	plcModelLoaded     bool
	farganModelLoaded  bool
	dredModelLoaded    bool
	osceModelsLoaded   bool
	osceBWEModelLoaded bool
}

// NewDecoder creates a new Opus decoder.
func NewDecoder(cfg DecoderConfig) (*Decoder, error) {
	if !validSampleRate(cfg.SampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if cfg.Channels < 1 || cfg.Channels > 2 {
		return nil, ErrInvalidChannels
	}

	maxPacketSamples := cfg.MaxPacketSamples
	if maxPacketSamples == 0 {
		maxPacketSamples = defaultMaxPacketSamples
	}
	if maxPacketSamples < 1 {
		return nil, ErrInvalidMaxPacketSamples
	}

	maxPacketBytes := cfg.MaxPacketBytes
	if maxPacketBytes == 0 {
		maxPacketBytes = defaultMaxPacketBytes
	}
	if maxPacketBytes < 1 {
		return nil, ErrInvalidMaxPacketBytes
	}

	silkDec := silk.NewDecoder()
	celtDec := celt.NewDecoder(cfg.Channels)
	hybridDec := hybrid.NewDecoderWithSharedDecoders(cfg.Channels, silkDec, celtDec)

	transitionSamples := 48000 / 200 // 5ms at 48kHz

	return &Decoder{
		silkDecoder:       silkDec,
		celtDecoder:       celtDec,
		hybridDecoder:     hybridDec,
		sampleRate:        cfg.SampleRate,
		channels:          cfg.Channels,
		maxPacketSamples:  maxPacketSamples,
		maxPacketBytes:    maxPacketBytes,
		scratchPCM:        make([]float32, maxPacketSamples*cfg.Channels),
		scratchTransition: make([]float32, transitionSamples*cfg.Channels),
		scratchRedundant:  make([]float32, transitionSamples*cfg.Channels),
		lastFrameSize:     960,        // Default 20ms at 48kHz
		prevMode:          ModeHybrid, // Default for PLC until first decode
		lastPacketMode:    ModeHybrid,
		lastBandwidth:     BandwidthFullband,
		fecData:           make([]byte, maxPacketBytes),
		dredData:          make([]byte, internaldred.MaxDataSize),
		scratchFEC:        make([]float32, maxPacketSamples*cfg.Channels),
	}, nil
}
