// Package gopus implements the Opus audio codec in pure Go.
//
// Opus is a lossy audio codec designed for interactive speech and music
// transmission. It supports bitrates from 6 to 510 kbit/s, sampling rates
// from 8 to 48 kHz, and frame sizes from 2.5 to 60 ms.
//
// This implementation follows RFC 6716 and is compatible with the
// reference libopus implementation. It requires no cgo dependencies.
//
// # Quick Start
//
// Encoding:
//
//	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	pcm := make([]float32, 960*2) // 20ms stereo at 48kHz
//	// ... fill pcm with audio samples ...
//
//	packet, err := enc.EncodeFloat32(pcm)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Decoding:
//
//	dec, err := gopus.NewDecoder(48000, 2)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	pcm, err := dec.DecodeFloat32(packet)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// # Opus Modes
//
// Opus operates in three modes:
//   - SILK: speech-optimized, 8-24 kHz bandwidth
//   - CELT: audio-optimized, full 48 kHz bandwidth
//   - Hybrid: SILK for low frequencies + CELT for high frequencies
//
// The encoder automatically selects the appropriate mode based on the
// Application hint provided to NewEncoder:
//   - ApplicationVoIP: Prefers SILK for speech
//   - ApplicationAudio: Prefers CELT/Hybrid for music
//   - ApplicationLowDelay: Uses CELT for minimum latency
//
// # Sample Formats
//
// Both int16 and float32 PCM formats are supported. float32 is the
// internal format and avoids conversion overhead. int16 is provided
// for compatibility with common audio APIs.
//
// For float32, samples should be normalized to [-1.0, 1.0].
// For int16, the full range [-32768, 32767] is used.
//
// Stereo audio uses interleaved samples: L0, R0, L1, R1, ...
//
// # Thread Safety
//
// Encoder and Decoder instances are NOT safe for concurrent use.
// Each goroutine should create its own instance.
//
// # Buffer Sizing
//
// For caller-provided buffers:
//   - Decode output: max 2880 * channels samples (60ms at 48kHz)
//   - Encode output: 4000 bytes is sufficient for any Opus packet
//
// # Packet Loss Concealment
//
// When a packet is lost, pass nil to Decode to trigger packet loss
// concealment (PLC). The decoder will generate audio to conceal the gap:
//
//	if packetLost {
//	    pcm, err = dec.DecodeFloat32(nil) // PLC
//	} else {
//	    pcm, err = dec.DecodeFloat32(packet)
//	}
//
// # Packet Structure
//
// Each Opus packet starts with a TOC (Table of Contents) byte:
//   - Bits 7-3: Configuration (0-31)
//   - Bit 2: Stereo flag
//   - Bits 1-0: Frame count code (0-3)
//
// Use ParseTOC to extract these fields, and ParsePacket to determine
// the frame boundaries within a packet.
//
// # Configuration
//
// The encoder supports various configuration options:
//
//	enc.SetBitrate(64000)     // Target bitrate (6000-510000 bps)
//	enc.SetComplexity(10)     // Quality vs CPU (0-10)
//	enc.SetFEC(true)          // Forward error correction
//	enc.SetDTX(true)          // Discontinuous transmission
//	enc.SetFrameSize(480)     // Frame size (120-2880 samples)
//
// # Multistream (Surround Sound)
//
// For surround sound applications (5.1, 7.1, etc.), use MultistreamEncoder
// and MultistreamDecoder. These support 1-8 channels with standard Vorbis-style
// channel mapping per RFC 7845.
//
// Multistream encoding example (5.1 surround):
//
//	enc, err := gopus.NewMultistreamEncoderDefault(48000, 6, gopus.ApplicationAudio)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	pcm := make([]float32, 960*6) // 20ms of 6-channel audio at 48kHz
//	// ... fill pcm with interleaved samples: FL, C, FR, RL, RR, LFE ...
//
//	packet, err := enc.EncodeFloat32(pcm)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Multistream decoding example:
//
//	dec, err := gopus.NewMultistreamDecoderDefault(48000, 6)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	pcm, err := dec.DecodeFloat32(packet)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Supported channel configurations:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (5 streams, 2 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
//
// For custom channel mappings, use NewMultistreamEncoder and NewMultistreamDecoder
// with explicit stream and mapping parameters.
package gopus
