package gopus

import (
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/hybrid"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

const (
	defaultMaxPacketSamples = 5760
	defaultMaxPacketBytes   = 1500
)

// DecoderConfig sets the sample rate, channel count, and packet limits for a
// Decoder.
type DecoderConfig struct {
	// SampleRate must be 8000, 12000, 16000, 24000, or 48000 Hz.
	// Builds with gopus_qext also accept 96000 Hz.
	SampleRate int
	// Channels must be 1 (mono) or 2 (stereo).
	Channels int
	// MaxPacketSamples caps the maximum decoded samples per channel per packet.
	// Zero selects 5,760 samples, or 120 ms at 48 kHz. The limit must fit
	// the decoder work buffers; [NewDecoder] rejects overflowing sizes.
	MaxPacketSamples int
	// MaxPacketBytes caps the maximum Opus packet size in bytes.
	// Zero selects 1,500 bytes.
	MaxPacketBytes int
}

// DefaultDecoderConfig returns a DecoderConfig with default packet limits for
// the given stream format. It allows up to 5,760 decoded samples per channel
// and 1,500 packet bytes; set MaxPacketSamples or MaxPacketBytes to raise a
// limit.
func DefaultDecoderConfig(sampleRate, channels int) DecoderConfig {
	return DecoderConfig{
		SampleRate:       sampleRate,
		Channels:         channels,
		MaxPacketSamples: defaultMaxPacketSamples,
		MaxPacketBytes:   defaultMaxPacketBytes,
	}
}

// Decoder decodes Opus packets into PCM samples. It retains stream state and is
// not safe for concurrent use; use one Decoder per stream. Construct it with
// [NewDecoder]; the zero value is not ready for use.
type Decoder struct {
	// Pointer-bearing fields are grouped first so Go's GC stops scanning at
	// ptrdata, leaving scalar state in the noscan tail.
	silkDecoder   *silk.Decoder   // SILK-only mode decoder
	celtDecoder   *celt.Decoder   // CELT-only mode decoder
	hybridDecoder *hybrid.Decoder // Hybrid mode decoder
	dnnBlob       *dnnblob.Blob
	// scratchF32 backs the four fixed float32 decode work buffers below with one
	// contiguous allocation; see NewDecoder.
	scratchF32        arena.Bump[float32]
	scratchPCM        []float32
	scratchTransition []float32
	scratchRedundant  []float32
	scratchSilkPLC    []float32 // 10 ms SILK PLC scratch for <10 ms concealment chunks
	fecData           []byte    // Stored packet data containing LBRR for FEC recovery
	// Scratch range decoder to avoid per-frame heap allocations
	scratchRangeDecoder rangecoding.Decoder
	decoderDREDFields
	decoderOSCEFields
	decoderHD96kFields
	decoderFixedFields
	fixedQEXT decoderFixedQEXTFields
	// Embedded sub-decoders are ordered so silkDecVal (which has an ~8.7 KB
	// noscan tail) sits last at the pointer/noscan boundary, leaving its tail
	// and all scalar fields and fecBuf below excluded from GC scanning.
	hybridDecVal hybrid.Decoder
	celtDecVal   celt.Decoder
	silkDecVal   silk.Decoder

	// --- Pointer-free (noscan) tail ---
	sampleRate         int32
	channels           int32
	maxPacketSamples   int
	maxPacketBytes     int
	lastFrameSize      int32
	lastPacketDuration int32
	prevMode           Mode // Track last mode for PLC
	lastPacketMode     Mode // Track last packet mode (libopus st->mode) for decode_fec gating
	lastBandwidth      Bandwidth
	prevRedundancy     bool
	prevPacketStereo   bool
	haveDecoded        bool
	bandwidthKnown     bool   // true once a non-PLC packet has been decoded (gates Bandwidth() vs 0)
	redundantRng       uint32 // Range from redundancy decoding, XORed with final range
	lastDataLen        int32  // Length of last packet data
	mainDecodeRng      uint32 // Final range from main decode (before any redundancy processing)
	decodeGainQ8       int    // Output gain in Q8 dB (libopus OPUS_SET_GAIN semantics)
	ignoreExtensions   bool   // libopus OPUS_SET_IGNORE_EXTENSIONS semantics
	complexity         int32  // libopus decoder complexity, default 0

	// FEC (Forward Error Correction) state
	// Stages the following packet's LBRR payload to recover the missing audio.
	fecMode       Mode      // Mode of the packet containing LBRR
	fecBandwidth  Bandwidth // Bandwidth of the packet containing LBRR
	fecStereo     bool      // Whether the packet was stereo
	fecFrameSize  int       // Frame size of the packet containing LBRR
	fecFrameCount int       // Number of frames in packet
	hasFEC        bool      // True if fecData contains valid LBRR data

	// Soft clipping memory (float decode uses none; int16 decode uses this)
	softClipMem [2]float32

	// Decoder-side DNN readiness mirrors the validated model families retained
	// by OPUS_SET_DNN_BLOB; optional paths require their corresponding models.
	pitchDNNLoaded    bool
	plcModelLoaded    bool
	farganModelLoaded bool
	fecBuf            [defaultMaxPacketBytes]byte
}

// NewDecoder returns a Decoder configured by cfg. It returns an error if the
// sample rate, channel count, or packet limits are invalid.
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

	// Under gopus_qext, 96 kHz requests route through the 48 kHz internal
	// pipeline with 2x upsampling at the output boundary.
	// C ref: opus_decoder.c opus_decoder_init() ENABLE_QEXT (Fs != 96000) gate.
	internalRate := cfg.SampleRate
	if cfg.SampleRate == 96000 {
		internalRate = 48000
	}

	// The contiguous float32 arena holds one packet-sized buffer, two
	// transition buffers, one 10 ms SILK PLC buffer, and the 20 ms hybrid SILK
	// upsample scratch. Bound its element count by the largest length whose byte
	// size fits in int before multiplying or constructing decoder state.
	transitionSamples := max(48000, cfg.SampleRate) / 200 // 5 ms in the largest configured CELT geometry.
	transLen := transitionSamples * cfg.Channels
	silkPLCLen := (internalRate / 100) * cfg.Channels
	hybridSilkLen := 960 * cfg.Channels
	maxFloatElements := int(^uint(0)>>1) / 4
	if maxPacketSamples > (maxFloatElements-2*transLen-silkPLCLen-hybridSilkLen)/cfg.Channels {
		return nil, ErrInvalidMaxPacketSamples
	}

	d := &Decoder{
		sampleRate:       int32(internalRate),
		channels:         int32(cfg.Channels),
		maxPacketSamples: maxPacketSamples,
		maxPacketBytes:   maxPacketBytes,
		lastFrameSize:    int32(internalRate / 50), // Default 20ms at the internal rate
		prevMode:         ModeHybrid,               // Default for PLC until first decode
		lastPacketMode:   ModeHybrid,
		lastBandwidth:    BandwidthFullband,
	}
	if maxPacketBytes <= len(d.fecBuf) {
		d.fecData = d.fecBuf[:maxPacketBytes]
	} else {
		d.fecData = make([]byte, maxPacketBytes)
	}
	d.silkDecVal.Init()
	d.silkDecVal.SetAPISampleRate(internalRate)
	d.silkDecoder = &d.silkDecVal
	d.celtDecVal.Init(cfg.Channels)
	if err := d.celtDecVal.SetAPISampleRate(internalRate); err != nil {
		return nil, err
	}
	d.celtDecoder = &d.celtDecVal
	// Back the fixed float32 decode work buffers with one contiguous arena.
	pcmLen := maxPacketSamples * cfg.Channels
	d.scratchF32.Ensure(pcmLen + 2*transLen + silkPLCLen + hybridSilkLen)
	d.scratchPCM = d.scratchF32.AllocN(pcmLen)
	d.scratchTransition = d.scratchF32.AllocN(transLen)
	d.scratchRedundant = d.scratchF32.AllocN(transLen)
	d.scratchSilkPLC = d.scratchF32.AllocN(silkPLCLen)
	hybridSilkScratch := d.scratchF32.AllocN(hybridSilkLen)
	d.hybridDecVal.InitWithSharedDecoders(cfg.Channels, d.silkDecoder, d.celtDecoder, hybridSilkScratch)
	d.hybridDecVal.SetAPISampleRate(internalRate)
	d.hybridDecoder = &d.hybridDecVal
	if cfg.SampleRate == 96000 {
		init96kDecoder(d)
	}
	d.initOSCELossHook()
	return d, nil
}
