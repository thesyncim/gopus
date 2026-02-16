# gopus Examples

Practical examples demonstrating gopus usage patterns for real-world applications.

## Prerequisites

- Go 1.25+
- ffmpeg/ffprobe (optional, for interoperability verification)
- ffplay/afplay/aplay/paplay (optional, for decode-play audio playback)

## Examples

### ffmpeg-interop

Demonstrates interoperability between gopus and ffmpeg tooling.

**What it does:**
1. Generates a stereo 440Hz test tone
2. Encodes it to Ogg Opus using gopus
3. Prints commands to verify with ffprobe/ffplay
4. Can also decode ffmpeg-encoded files

**Usage:**
```bash
cd examples/ffmpeg-interop
go build .

# Encode a test signal
./ffmpeg-interop -out output.opus -duration 2

# Verify with ffmpeg
ffprobe output.opus
ffplay output.opus

# Decode an ffmpeg-encoded file
ffmpeg -f lavfi -i "sine=frequency=440:duration=2" -c:a libopus test.opus
./ffmpeg-interop -in test.opus
```

**Expected output:**
```
=== Part 1: Encode with gopus ===
Generating 2.0s stereo 440Hz sine wave...
  Sample rate: 48000 Hz
  Channels: 2
  Frame size: 960 samples (20.0 ms)
  Total frames: 100
  Encoded 100 frames, 9124 bytes total
  Average bitrate: 36.5 kbps

Created: output.opus
```

### roundtrip

Validates encode-decode quality with comprehensive metrics.

**What it does:**
1. Generates test signals (sine, sweep, noise, speech)
2. Encodes with gopus encoder
3. Decodes back to PCM
4. Calculates SNR, correlation, peak error, MSE
5. Reports quality assessment

**Usage:**
```bash
cd examples/roundtrip
go build .

# Single test
./roundtrip -duration 1 -signal sine -bitrate 64000

# Test all configurations
./roundtrip -all
```

**Flags:**
- `-duration`: Test duration in seconds (default: 1)
- `-signal`: Signal type: sine, sweep, noise, speech (default: sine)
- `-bitrate`: Target bitrate in bps (default: 64000)
- `-channels`: Number of channels, 1 or 2 (default: 2)
- `-all`: Run all test configurations

**Expected output:**
```
=== Roundtrip Test: sine signal, 64 kbps, 2 ch ===

--- Quality Report ---
Samples compared: 96000

Signal Quality:
  SNR:              XX.XX dB
  Correlation:      X.XXXX
  Peak Error:       X.XXXXXX
  MSE:              X.XXXXXX
```

### ogg-file

Creates and reads Ogg Opus files, suitable for podcast-style content.

**What it does:**
1. Creates Ogg Opus files with test audio (chord progression)
2. Reads and analyzes Ogg Opus files
3. Reports file statistics and duration

**Usage:**
```bash
cd examples/ogg-file
go build .

# Create a file
./ogg-file -out podcast.opus -duration 5 -bitrate 64000

# Read a file
./ogg-file -in podcast.opus
```

**Flags:**
- `-out`: Output file path
- `-in`: Input file path to analyze
- `-duration`: Duration in seconds (default: 5)
- `-bitrate`: Target bitrate in bps (default: 64000)

**Expected output:**
```
Creating Ogg Opus file: podcast.opus
  Duration: 5.0 seconds
  Bitrate: 64 kbps
  Frames: 250
  ...
  File size: 30585 bytes
  Compression: 31.4:1
```

### decode-play

Decodes an Ogg Opus file to WAV with optional playback, or streams raw PCM to ffplay.

**What it does:**
1. Reads Ogg Opus packets (from a file or URL)
2. Decodes to float32 PCM with gopus
3. Drops Opus pre-skip samples
4. Writes 16-bit PCM WAV (optional)
5. Plays audio via ffplay (optional, no temp files with `-pipe`)

**Usage:**
```bash
cd examples/decode-play
go build .

# Download the default stereo sample and play via ffplay (no temp files)
./decode-play -pipe

# Play the source with ffplay first, then the gopus-decoded output
./decode-play -pipe -ffplay-first

# Decode a local file to WAV
./decode-play -in input.opus -out output.wav

# Decode and play (uses ffplay/afplay/aplay/paplay if available)
./decode-play -in input.opus -play

# Stream raw PCM directly to ffplay (no WAV file)
./decode-play -url https://example.com/file.opus -pipe
```

**Flags:**
- `-in`: Input Ogg Opus file (optional if `-url` is used)
- `-url`: Download Ogg Opus file from URL (overrides `-sample`)
- `-sample`: Preset sample to download: `stereo` (default) or `speech`
- `-ffplay-first`: Play the source with ffplay, then the gopus-decoded output
- `-out`: Output WAV file (defaults to `decoded.wav` if not set)
- `-play`: Play the decoded WAV with a system player
- `-pipe`: Stream raw PCM directly to ffplay (no temp files)

### encode-play

Encodes a generated test signal with gopus and optionally plays the resulting
Ogg Opus file. Playback uses `ffplay` if available, otherwise it decodes with
gopus and plays a WAV via the system's default audio player.

**What it does:**
1. Generates a test signal (sine, sweep, noise, chord, speech)
1. Encodes to Ogg Opus with gopus
1. Plays the `.opus` file with `ffplay` (optional)

**Usage:**
```bash
cd examples/encode-play
go build .

# Encode and play (zero-config; uses system player if ffplay is missing)
./encode-play -play

# Sweep at higher bitrate
./encode-play -signal sweep -duration 3 -bitrate 96000 -play

# Encode to a file without playback
./encode-play -out demo.opus
```

**Flags:**
- `-out`: Output Ogg Opus file path
- `-duration`: Duration in seconds (default: 2)
- `-bitrate`: Target bitrate in bps (default: 64000)
- `-channels`: Number of channels (1 or 2)
- `-signal`: Signal type: sine, sweep, noise, chord, speech
- `-frame`: Frame size at 48kHz (default: 960)
- `-play`: Play the encoded file with `ffplay`
- `-libopus`: Use external libopus encoder (`opusenc`/`ffmpeg`) instead of gopus

### mix-arrivals

Mixes WebRTC-like tracks that arrive at different times into one output.

**What it does:**
1. Downloads real speech clips from the open-source Free Spoken Digit Dataset (CC BY 4.0)
2. Resamples and pans clips into three concurrent speaker tracks
3. Splits each track into timestamped PCM frames (packetization out of scope)
4. Simulates jittered/out-of-order arrival and frame loss (with optional PLC-style concealment)
5. Uses a runtime track mixer (`AddTrack`/`RemoveTrack`) with bounded lookahead
6. Normalizes peak level and writes one Ogg Opus file

**Usage:**
```bash
cd examples/mix-arrivals
go build .

# Mix speech tracks into one Ogg Opus file
./mix-arrivals -out mixed_arrivals.opus -bitrate 128000

# Listen clean (no simulated network impairments)
./mix-arrivals -clean -play

# Increase simulated loss profile (rough network)
./mix-arrivals -loss 0.12 -burst-start 0.18 -burst-keep 0.60

# Hear the result immediately
./mix-arrivals -play
```

**Important:**
- This sample downloads clips from GitHub on first run.
- License source: `https://github.com/Jakobovski/free-spoken-digit-dataset` (CC BY 4.0).
- Downloaded clips are cached in `.cache/mix-arrivals` by default.
- Defaults use a realistic mild impairment profile; use `-clean` for pristine output.
- `-play` uses `ffplay` when available; otherwise it falls back to local OS players.

**Expected output:**
```
Mixing open-source speech tracks into one output
  Source dataset: Free Spoken Digit Dataset
  Source license: CC BY 4.0
  Downloaded clips: 12 (cache: .cache/mix-arrivals)
  - speaker-george: start=0ms, duration=...
  - speaker-jackson: start=550ms, duration=...
  - speaker-nicolas: start=1100ms, duration=...
  Network simulation: generated=..., dropped=..., concealed=...
  Stream ingest: accepted=..., droppedLate=..., droppedAhead=...
  Peak before normalize: X.XXX, applied gain: X.XXX
  Output: mixed_arrivals.opus
```

## Building All Examples

```bash
# From repository root
go build ./examples/ffmpeg-interop
go build ./examples/roundtrip
go build ./examples/ogg-file
go build ./examples/decode-play
go build ./examples/encode-play
go build ./examples/mix-arrivals

# Or build all at once
go build ./examples/...
```

## API Overview

### Encoder

```go
// Create encoder
enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

// Configure
enc.SetBitrate(128000)        // 128 kbps
enc.SetComplexity(10)         // Max quality
enc.SetFrameSize(960)         // 20ms frames

// Encode PCM to Opus
packet, err := enc.EncodeFloat32(pcmSamples)
```

### Decoder

```go
// Create decoder
cfg := gopus.DefaultDecoderConfig(48000, 2)
dec, err := gopus.NewDecoder(cfg)
pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

// Decode Opus to PCM
n, err := dec.Decode(packet, pcmOut)
samples := pcmOut[:n*cfg.Channels]
```

### Ogg Container

```go
// Write Ogg Opus
w, err := ogg.NewWriter(file, 48000, 2)
w.WritePacket(packet, frameSize)
w.Close()

// Read Ogg Opus
r, err := ogg.NewReader(file)
packet, granule, err := r.ReadPacket()
```

## Notes

- **Development status:** gopus is in active development; check the repository `README.md` status snapshot for current parity/compliance markers.
- **Sample rate:** Opus internally operates at 48kHz. Other rates are converted.
- **Frame sizes:** Standard frame is 960 samples (20ms at 48kHz). Supported: 120, 240, 480, 960, 1920, 2880.
- **Applications:** Use `ApplicationVoIP` for speech, `ApplicationAudio` for music, `ApplicationLowDelay` for real-time.

## Troubleshooting

**FFmpeg doesn't play the file:**
- Ensure the file has proper Ogg headers (use `ffprobe -v debug file.opus`)
- Check that the file isn't truncated (look for EOS page)

**Quality metrics are poor:**
- Compare against profile-specific compliance outputs in `go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`
- See roundtrip example for detailed quality analysis
- Compare with libopus reference encoder for baseline

**Build errors:**
- Ensure Go 1.25+ is installed
- Run `go mod tidy` in the repository root
