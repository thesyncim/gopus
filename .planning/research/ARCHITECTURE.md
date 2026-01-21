# Architecture Research: gopus

**Domain:** Pure Go Opus Audio Codec Implementation
**Researched:** 2026-01-21
**Overall Confidence:** HIGH (based on RFC 6716, official documentation, reference implementations)

## Opus Codec Structure

Opus is a hybrid audio codec combining two distinct coding technologies unified by a shared entropy coder:

```
                    +------------------+
                    |   Opus Packet    |
                    | (TOC + Frames)   |
                    +--------+---------+
                             |
              +--------------+--------------+
              |              |              |
        +-----v-----+  +-----v-----+  +-----v-----+
        | SILK-only |  |  Hybrid   |  | CELT-only |
        |   Mode    |  |   Mode    |  |   Mode    |
        +-----------+  +-----------+  +-----------+
              |              |              |
              |        +-----+-----+        |
              |        |           |        |
        +-----v-----+  |     +-----v-----+  |
        |   SILK    |<-+     |   CELT    |<-+
        |  (LP/LPC) |        |  (MDCT)   |
        +-----------+        +-----------+
              |                    |
              +--------+-----------+
                       |
              +--------v---------+
              |   Range Coder    |
              | (Shared Entropy) |
              +------------------+
```

### Three Operating Modes

| Mode | Use Case | Bandwidths | Frame Sizes | TOC Config |
|------|----------|------------|-------------|------------|
| SILK-only | Low-bitrate speech | NB, MB, WB | 10, 20, 40, 60 ms | 0-11 |
| Hybrid | Medium-bitrate SWB/FB speech | SWB, FB | 10, 20 ms | 12-15 |
| CELT-only | Music, low-delay speech | NB, WB, SWB, FB | 2.5, 5, 10, 20 ms | 16-31 |

### Audio Bandwidth Definitions

| Bandwidth | Abbreviation | Audio Range | Internal Sample Rate |
|-----------|--------------|-------------|---------------------|
| Narrowband | NB | 0-4 kHz | 8 kHz |
| Medium-band | MB | 0-6 kHz | 12 kHz |
| Wideband | WB | 0-8 kHz | 16 kHz |
| Super-wideband | SWB | 0-12 kHz | 24 kHz |
| Fullband | FB | 0-20 kHz | 48 kHz |

## Component Deep Dive

### Range Coder

The range coder is the foundational entropy coding layer shared by both SILK and CELT.

**Architecture:**
- Based on arithmetic coding principles but operates byte-wise (not bit-wise) for speed
- Uses 32-bit integer arithmetic exclusively (no floating point)
- Maintains state: `range` (current interval width), `value` (code value for decoder)
- All calculations must be bit-exact across implementations

**Key Operations:**
```
Encoder:
  1. Divide current range proportionally to symbol probabilities
  2. Select sub-range corresponding to symbol
  3. Renormalize when range becomes too small (emit bytes)

Decoder:
  1. Read encoded value
  2. Determine which sub-range contains the value
  3. Output corresponding symbol
  4. Update range and value, renormalize as needed
```

**Raw Bits Bypass:**
- Certain symbols bypass the range coder entirely
- Raw bits packed at end of packet, reading backwards
- Provides error resilience (corruption in raw bits doesn't desynchronize range decoder)
- Used for symbols with power-of-two probability distributions

**Interface Boundary:**
```go
type RangeDecoder interface {
    // Decode symbol with given probability model
    DecodeSymbol(icdf []uint16) uint32

    // Decode uniform distribution [0, ft)
    DecodeUniform(ft uint32) uint32

    // Read raw bits directly from end of packet
    DecodeRawBits(bits uint32) uint32

    // Check remaining bits
    RemainingBits() int32
}

type RangeEncoder interface {
    // Encode symbol with given probability model
    EncodeSymbol(symbol uint32, icdf []uint16)

    // Encode uniform distribution
    EncodeUniform(symbol, ft uint32)

    // Write raw bits to end of packet
    EncodeRawBits(value, bits uint32)

    // Finalize and return encoded bytes
    Finalize() []byte
}
```

### SILK Codec

SILK handles speech coding using Linear Predictive Coding (LPC) with noise feedback coding.

**Architecture Components:**

```
Input PCM
    |
    v
+-------------------+
| Pre-emphasis      |  (high-pass filter to flatten spectrum)
+-------------------+
    |
    v
+-------------------+
| Frame Analysis    |  (VAD, bandwidth detection)
+-------------------+
    |
    v
+-------------------+
| LSF Extraction    |  (convert LPC coefficients to Line Spectral Frequencies)
+-------------------+
    |
    v
+-------------------+
| Pitch Analysis    |  (pitch lag for voiced frames)
+-------------------+
    |
    v
+-------------------+
| LTP Filter        |  (Long-Term Prediction for voiced speech)
+-------------------+
    |
    v
+-------------------+
| Residual Coding   |  (quantize excitation as sum of pulses)
+-------------------+
    |
    v
+-------------------+
| Range Encoding    |  (entropy code all parameters)
+-------------------+
    |
    v
Bitstream
```

**Decoder Signal Flow (RFC 6716 Section 4.2):**
```
Bitstream
    |
    v
+-------------------+
| Range Decoding    |  (decode all parameters)
+-------------------+
    |
    +--> Frame Type, Gains, LSF, LTP coefficients, Excitation pulses
    |
    v
+-------------------+
| Excitation        |  (reconstruct excitation from pulses + LCG dither)
| Reconstruction    |
+-------------------+
    |
    v
+-------------------+
| LTP Synthesis     |  (apply Long-Term Prediction filter, voiced frames only)
+-------------------+
    |
    v
+-------------------+
| LPC Synthesis     |  (apply Linear Prediction filter)
+-------------------+
    |
    v
+-------------------+
| De-emphasis       |  (inverse of pre-emphasis)
+-------------------+
    |
    v
Output PCM
```

**Frame Structure:**
- 10ms or 20ms frames internally
- 20ms frames contain 4 subframes (5ms each)
- 10ms frames contain 2 subframes (5ms each)
- Each subframe can have different gains and filter parameters

**Key Parameters Encoded Per Frame:**
| Parameter | Purpose | Varies By |
|-----------|---------|-----------|
| Frame type | Voiced/unvoiced/inactive | Frame |
| Quantization gains | Excitation scaling | Subframe |
| LSF coefficients | Spectral envelope | Frame (interpolated per subframe) |
| Pitch lag | Periodicity for LTP | Subframe |
| LTP coefficients | Long-term prediction filter (5 taps) | Subframe |
| Excitation pulses | Residual signal | Sample |

### CELT Codec

CELT handles music and general audio using Modified Discrete Cosine Transform (MDCT).

**Architecture Components:**

```
Input PCM
    |
    v
+-------------------+
| Pre-emphasis      |  (simple first-order high-pass)
+-------------------+
    |
    v
+-------------------+
| MDCT Analysis     |  (transform to frequency domain)
+-------------------+
    |
    +--> Transient detection (switch to short blocks if needed)
    |
    v
+-------------------+
| Band Energy       |  (compute energy for ~21 Bark-scale bands)
| Extraction        |
+-------------------+
    |
    v
+-------------------+
| Coarse Energy     |  (6 dB resolution, inter-band prediction)
| Quantization      |
+-------------------+
    |
    v
+-------------------+
| Fine Energy       |  (refine based on bit allocation)
| Quantization      |
+-------------------+
    |
    v
+-------------------+
| Band Normalization|  (divide by energy -> unit vectors)
+-------------------+
    |
    v
+-------------------+
| PVQ Encoding      |  (Pyramid Vector Quantization of normalized bands)
+-------------------+
    |
    v
+-------------------+
| Spreading         |  (time-frequency spreading for perceptual masking)
+-------------------+
    |
    v
+-------------------+
| Range Encoding    |  (entropy code energies + PVQ indices)
+-------------------+
    |
    v
Bitstream
```

**Decoder Signal Flow:**
```
Bitstream
    |
    v
+-------------------+
| Range Decoding    |
+-------------------+
    |
    +--> Band energies (coarse + fine)
    +--> PVQ codewords for each band
    +--> Transient flag, pitch info
    |
    v
+-------------------+
| PVQ Decoding      |  (reconstruct normalized band vectors)
+-------------------+
    |
    v
+-------------------+
| Band Folding      |  (copy from lower bands if no bits allocated)
+-------------------+
    |
    v
+-------------------+
| Energy Denorm     |  (multiply bands by decoded energies)
+-------------------+
    |
    v
+-------------------+
| IMDCT Synthesis   |  (transform back to time domain)
+-------------------+
    |
    v
+-------------------+
| De-emphasis       |
+-------------------+
    |
    v
Output PCM
```

**Band Structure:**
- ~21 bands approximating Bark critical bands
- Band edges depend on sample rate and frame size
- Each band must contain at least 3 MDCT bins
- Band 20+ (above 20 kHz) not coded

**Key Features:**
| Feature | Purpose |
|---------|---------|
| Transient detection | Switch to 8 short MDCTs for attacks |
| Band folding | Reconstruct uncoded bands from lower frequencies |
| Energy preservation | Spectral envelope always preserved |
| PVQ | Fixed-length codewords, no entropy coding needed |

### Hybrid Mode

Hybrid mode combines SILK and CELT for super-wideband and fullband speech.

**Architecture:**
```
Input PCM (48 kHz)
    |
    +---> Downsample to 16 kHz
    |           |
    |           v
    |     +----------+
    |     |   SILK   |  (0-8 kHz)
    |     +----------+
    |           |
    v           |
+----------+    |
|   CELT   |    |  (8-20/24 kHz only, lower bands zeroed)
+----------+    |
    |           |
    +-----------+
          |
          v
    Combined Bitstream
```

**Key Points:**
- SILK operates at WB (16 kHz) internally
- CELT operates at FB (48 kHz) but only codes bands above 8 kHz
- Crossover frequency is 8 kHz
- CELT adds 2.7ms additional delay for synchronization
- Only supports 10ms and 20ms frame sizes

**Decoder Combining:**
```
Bitstream
    |
    +---> SILK Decoder (WB)
    |           |
    |           v
    |     Upsample to 48 kHz
    |           |
    +---> CELT Decoder (FB, bands > 8 kHz)
    |           |
    +-----------+
          |
          v
       Sum outputs
          |
          v
    Output PCM (48 kHz)
```

### Multistream

Multistream encapsulates multiple Opus streams for surround sound.

**Architecture:**
```
+------------------+
| Multistream Packet |
+------------------+
    |
    +---> Stream 0 (coupled stereo) -> L, R
    +---> Stream 1 (coupled stereo) -> Ls, Rs
    +---> Stream 2 (mono) -> Center
    +---> Stream 3 (mono) -> LFE
    ...
```

**Key Concepts:**
- Up to 255 elementary streams per multistream packet
- Streams are either "coupled" (stereo) or "uncoupled" (mono)
- Channel mapping table defines routing
- All streams in a packet share the same duration
- Vorbis channel ordering convention

**Not Required for MVP:** Multistream is an extension. Focus on mono/stereo single-stream first.

## Data Flow

### Encode Path

```
+---------------+     +----------------+     +------------------+
| PCM Samples   | --> | Mode Selection | --> | Active Encoder   |
| (int16/float) |     | (SILK/CELT/    |     | (based on mode)  |
+---------------+     |  Hybrid)       |     +------------------+
                      +----------------+              |
                                                      v
                      +----------------+     +------------------+
                      | Opus Packet    | <-- | Range Encoder    |
                      | (TOC + Data)   |     | (finalize)       |
                      +----------------+     +------------------+
```

### Decode Path

```
+---------------+     +----------------+     +------------------+
| Opus Packet   | --> | TOC Parsing    | --> | Mode Dispatch    |
+---------------+     | (config, s, c) |     | (SILK/CELT/both) |
                      +----------------+     +------------------+
                                                      |
                      +----------------+              v
                      | PCM Samples    | <-- +------------------+
                      | (int16/float)  |     | Active Decoder   |
                      +----------------+     | + Combining      |
                                             +------------------+
```

### Packet Structure

```
+------+-----------------------------------+
| TOC  |          Frame Data               |
| (1B) |                                   |
+------+-----------------------------------+
   |
   +---> config (5 bits): mode + bandwidth + frame size
   +---> s (1 bit): stereo flag
   +---> c (2 bits): frame count code (0-3)
```

**Frame Count Codes:**
| Code | Meaning |
|------|---------|
| 0 | 1 frame |
| 1 | 2 frames, equal size |
| 2 | 2 frames, different sizes |
| 3 | Arbitrary number of frames (signaled in next byte) |

## Suggested Build Order

Based on component dependencies, recommended implementation order:

### Phase 1: Foundation (No Dependencies)

```
1. Range Coder
   - Decoder first (simpler, can test immediately)
   - Encoder second
   - Dependencies: None
   - Why first: Everything else needs this

2. Packet Parsing (TOC)
   - Parse TOC byte
   - Extract config, stereo, frame count
   - Handle frame length coding
   - Dependencies: None (just bit manipulation)
   - Why early: Needed to route to correct decoder
```

### Phase 2: SILK Decoder

```
3. SILK Tables
   - LSF codebooks
   - Pitch tables
   - Gain tables
   - Dependencies: None (static data)

4. SILK Parameter Decoding
   - Frame type decoding
   - Gain decoding
   - LSF decoding (quantized Line Spectral Frequencies)
   - Pitch lag decoding
   - LTP coefficient decoding
   - Dependencies: Range Coder, SILK Tables

5. SILK Excitation
   - Pulse decoding
   - LCG (Linear Congruential Generator) for dither
   - Excitation reconstruction
   - Dependencies: Range Coder

6. SILK Synthesis Filters
   - LPC synthesis filter
   - LTP synthesis filter
   - Dependencies: Parameter decoding, Excitation

7. SILK Post-processing
   - De-emphasis filter
   - Stereo unmixing (if stereo)
   - Sample rate conversion
   - Dependencies: Synthesis filters
```

### Phase 3: CELT Decoder

```
8. CELT Tables
   - Band structure tables (Bark-scale bands)
   - Bit allocation tables
   - PVQ codebook parameters
   - Dependencies: None (static data)

9. CELT Energy Decoding
   - Coarse energy (6 dB quantization)
   - Fine energy (variable precision)
   - Inter-band/inter-frame prediction
   - Dependencies: Range Coder, CELT Tables

10. CELT PVQ Decoding
    - Decode PVQ indices
    - Reconstruct normalized vectors
    - Band folding for uncoded bands
    - Dependencies: Range Coder, CELT Tables

11. CELT Synthesis
    - IMDCT implementation
    - Overlap-add
    - De-emphasis
    - Dependencies: Energy + PVQ decoding
```

### Phase 4: Hybrid Mode & Integration

```
12. Hybrid Decoder
    - Coordinate SILK and CELT decoding
    - Sample rate conversion for SILK output
    - Delay compensation
    - Output summing
    - Dependencies: SILK Decoder, CELT Decoder

13. Resampling
    - Polyphase resampling filters
    - Support 8/12/16/24/48 kHz
    - Dependencies: None (but needed for hybrid)
```

### Phase 5: Encoders (Mirror decoder order)

```
14. SILK Encoder
    - LPC analysis
    - Pitch detection
    - Excitation coding
    - Dependencies: SILK Decoder (for testing), Range Encoder

15. CELT Encoder
    - MDCT analysis
    - Energy quantization
    - PVQ encoding
    - Bit allocation
    - Dependencies: CELT Decoder (for testing), Range Encoder

16. Hybrid Encoder
    - Mode selection logic
    - Bitrate allocation between SILK/CELT
    - Dependencies: SILK Encoder, CELT Encoder
```

### Phase 6: Streaming & Polish

```
17. io.Reader/Writer wrappers
18. Ogg container support (optional)
19. Performance optimization
```

## Go Package Structure

Recommended organization following Go conventions:

```
gopus/
|-- opus.go              # Public API: Encoder, Decoder types
|-- encoder.go           # Top-level encoder implementation
|-- decoder.go           # Top-level decoder implementation
|-- packet.go            # TOC parsing, packet structure
|-- errors.go            # Error definitions
|-- config.go            # Configuration constants, options
|
|-- internal/
|   |-- rangecoding/
|   |   |-- decoder.go   # Range decoder implementation
|   |   |-- encoder.go   # Range encoder implementation
|   |   |-- tables.go    # Probability tables shared by SILK/CELT
|   |
|   |-- silk/
|   |   |-- decoder.go   # SILK decoder orchestration
|   |   |-- encoder.go   # SILK encoder orchestration
|   |   |-- lpc.go       # LPC analysis and synthesis
|   |   |-- lsf.go       # Line Spectral Frequency handling
|   |   |-- ltp.go       # Long-Term Prediction
|   |   |-- excitation.go # Excitation generation
|   |   |-- tables.go    # SILK-specific codebooks
|   |   |-- stereo.go    # Mid-side stereo processing
|   |
|   |-- celt/
|   |   |-- decoder.go   # CELT decoder orchestration
|   |   |-- encoder.go   # CELT encoder orchestration
|   |   |-- mdct.go      # MDCT/IMDCT implementation
|   |   |-- bands.go     # Band structure, energy processing
|   |   |-- pvq.go       # Pyramid Vector Quantization
|   |   |-- cwrs.go      # Combinatoric indexing for PVQ
|   |   |-- tables.go    # CELT-specific tables
|   |
|   |-- hybrid/
|   |   |-- decoder.go   # Hybrid mode coordination
|   |   |-- encoder.go   # Hybrid mode coordination
|   |
|   |-- resample/
|   |   |-- resample.go  # Polyphase resampling
|   |   |-- tables.go    # Resampling filter coefficients
|   |
|   |-- bitdepth/
|   |   |-- convert.go   # int16 <-> float32 conversion
|
|-- pkg/
|   |-- ogg/
|   |   |-- reader.go    # Ogg container parsing (optional)
|   |   |-- writer.go    # Ogg container writing (optional)
|
|-- testdata/
|   |-- vectors/         # Official Opus test vectors
|   |-- samples/         # Test audio files
```

### Package Responsibilities

| Package | Responsibility | Exports |
|---------|----------------|---------|
| `gopus` (root) | Public API | `Encoder`, `Decoder`, `Config` |
| `internal/rangecoding` | Entropy coding | Internal only |
| `internal/silk` | Speech codec | Internal only |
| `internal/celt` | Audio codec | Internal only |
| `internal/hybrid` | Mode coordination | Internal only |
| `internal/resample` | Sample rate conversion | Internal only |
| `internal/bitdepth` | Sample format conversion | Internal only |
| `pkg/ogg` | Container format | Optional public API |

## Interface Boundaries

### Public API (gopus package)

```go
// Core types
type Decoder struct { ... }
type Encoder struct { ... }

// Configuration
type Config struct {
    SampleRate    int           // 8000, 12000, 16000, 24000, 48000
    Channels      int           // 1 or 2
    Application   Application   // VoIP, Audio, LowDelay
    Bitrate       int           // Target bitrate
    Complexity    int           // 0-10
    // ...
}

type Application int
const (
    AppVoIP     Application = iota
    AppAudio
    AppLowDelay
)

// Decoder interface
func NewDecoder(sampleRate, channels int) (*Decoder, error)
func (d *Decoder) Decode(packet []byte, pcm []int16) (int, error)
func (d *Decoder) DecodeFloat(packet []byte, pcm []float32) (int, error)
func (d *Decoder) Reset()

// Encoder interface
func NewEncoder(cfg Config) (*Encoder, error)
func (e *Encoder) Encode(pcm []int16, packet []byte) (int, error)
func (e *Encoder) EncodeFloat(pcm []float32, packet []byte) (int, error)
func (e *Encoder) Reset()

// Streaming wrappers
type Reader struct { ... }  // io.Reader for decoded PCM
type Writer struct { ... }  // io.Writer for encoded packets
```

### Internal Component Interfaces

```go
// internal/rangecoding
type Decoder interface {
    DecodeSymbol(icdf []uint16) uint32
    DecodeUniform(ft uint32) uint32
    DecodeRawBits(bits uint32) uint32
    RemainingBits() int32
    Tell() uint32  // Bits consumed
}

// internal/silk
type Decoder interface {
    Decode(rc *rangecoding.Decoder, bandwidth Bandwidth,
           frameSize int, stereo bool) ([]float32, error)
    Reset()
}

// internal/celt
type Decoder interface {
    Decode(rc *rangecoding.Decoder, bandwidth Bandwidth,
           frameSize int, stereo bool) ([]float32, error)
    Reset()
}
```

### Data Flow Across Boundaries

```
User Code
    |
    | []int16 or []float32 (PCM samples)
    v
gopus.Encoder
    |
    | internal types (config, state)
    v
internal/silk or internal/celt
    |
    | symbols, raw bits
    v
internal/rangecoding.Encoder
    |
    | []byte (compressed data)
    v
gopus.Encoder
    |
    | []byte (Opus packet with TOC)
    v
User Code
```

## Anti-Patterns to Avoid

### 1. Bit-Exactness Violations
**What:** Range coder produces different output than reference
**Why bad:** Packets become undecodable by other implementations
**Prevention:**
- Use only 32-bit integer arithmetic in range coder
- Test against official test vectors early and often
- No floating point in entropy coding paths

### 2. State Leakage Between Frames
**What:** Decoder/encoder state not properly maintained
**Why bad:** Audio artifacts, crashes on edge cases
**Prevention:**
- Clear separation of per-frame and persistent state
- Proper Reset() implementations
- Test with packet loss scenarios

### 3. Premature Optimization
**What:** SIMD/assembly before correctness
**Why bad:** Harder to debug, may introduce subtle errors
**Prevention:**
- Get pure Go working first with test vectors
- Profile before optimizing
- Keep reference implementation as fallback

### 4. Monolithic Decoder
**What:** SILK and CELT tightly coupled
**Why bad:** Hard to test, hard to maintain
**Prevention:**
- Clear interfaces between components
- Test SILK and CELT independently
- Dependency injection for range coder

## Sources

**HIGH Confidence (Official RFC/Specifications):**
- [RFC 6716: Definition of the Opus Audio Codec](https://www.rfc-editor.org/rfc/rfc6716) - Normative specification
- [Opus Official Website](https://opus-codec.org/) - Reference implementation, documentation

**MEDIUM Confidence (Academic Papers, Technical Documentation):**
- [High-Quality, Low-Delay Music Coding in the Opus Codec (arXiv)](https://ar5iv.labs.arxiv.org/html/1602.04845) - CELT architecture details
- [DeepWiki: Core Opus Codec](https://deepwiki.com/xiph/opus/2-core-opus-codec) - Architecture overview
- [Opus Multistream API](https://www.opus-codec.org/docs/opus_api-1.1.2/group__opus__multistream.html) - Multistream documentation

**MEDIUM Confidence (Reference Implementations):**
- [pion/opus (GitHub)](https://github.com/pion/opus) - Pure Go implementation (SILK only currently)
- [xiph/opus (GitHub)](https://github.com/xiph/opus) - Official C reference implementation

**LOW Confidence (General Resources):**
- [Opus Wikipedia](https://en.wikipedia.org/wiki/Opus_(audio_format)) - General overview
- [Range Coding Wikipedia](https://en.wikipedia.org/wiki/Range_coding) - Algorithm background
