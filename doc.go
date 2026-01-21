// Package gopus implements the Opus audio codec in pure Go.
//
// Opus is a lossy audio codec designed for interactive speech and music
// transmission. It supports bitrates from 6 to 510 kbit/s, sampling rates
// from 8 to 48 kHz, and frame sizes from 2.5 to 60 ms.
//
// This implementation follows RFC 6716 and is compatible with the
// reference libopus implementation. It requires no cgo dependencies.
//
// # Opus Modes
//
// Opus operates in three modes:
//   - SILK: speech-optimized, 8-24 kHz bandwidth
//   - CELT: audio-optimized, full 48 kHz bandwidth
//   - Hybrid: SILK for low frequencies + CELT for high frequencies
//
// The mode is determined by the TOC byte in each packet.
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
package gopus
