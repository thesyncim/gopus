// Package testvectors parses opus_demo bitstreams and supports quality checks
// against the RFC 8251 Opus test vectors.
package testvectors

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Errors returned by the parser.
var (
	// ErrTruncatedHeader indicates insufficient data for packet header.
	ErrTruncatedHeader = errors.New("testvectors: truncated packet header (need 8 bytes)")

	// ErrTruncatedPacket indicates packet data shorter than header specified.
	ErrTruncatedPacket = errors.New("testvectors: truncated packet data")

	// ErrEmptyFile indicates the file contains no packets.
	ErrEmptyFile = errors.New("testvectors: empty file (no packets)")
)

var bitstreamFileCache sync.Map

// Packet contains an encoded Opus packet and its recorded encoder final range.
type Packet struct {
	// Data is the raw Opus packet data (including TOC byte).
	Data []byte

	// FinalRange is the range coder final state from encoding.
	// This can be used to verify range decoder state matches after decoding.
	FinalRange uint32
}

// ParseOpusDemoBitstream reads opus_demo .bit file format from a byte slice.
//
// The format per packet is (big-endian, network byte order):
//   - uint32_be: packet_length (4 bytes)
//   - uint32_be: enc_final_range (4 bytes, range coder verification)
//   - byte[packet_length]: opus_packet_data
//
// Packet payloads are copied from data. Empty input returns a nil slice and
// nil error. Framing errors return an error without partial results; payloads
// are not validated as Opus packets.
func ParseOpusDemoBitstream(data []byte) ([]Packet, error) {
	if len(data) == 0 {
		return nil, nil // Empty data is valid (no packets)
	}

	var packets []Packet
	offset := 0

	for offset < len(data) {
		// Check we have at least 8 bytes for header
		if offset+8 > len(data) {
			return nil, fmt.Errorf("%w: at offset %d, have %d bytes",
				ErrTruncatedHeader, offset, len(data)-offset)
		}

		// Read packet length (4 bytes, big-endian / network byte order)
		packetLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4

		// Read enc_final_range (4 bytes, big-endian / network byte order)
		finalRange := binary.BigEndian.Uint32(data[offset:])
		offset += 4

		// Validate packet length
		if offset+int(packetLen) > len(data) {
			return nil, fmt.Errorf("%w: header says %d bytes, have %d",
				ErrTruncatedPacket, packetLen, len(data)-offset)
		}

		// Read packet data
		packetData := make([]byte, packetLen)
		copy(packetData, data[offset:offset+int(packetLen)])
		offset += int(packetLen)

		packets = append(packets, Packet{
			Data:       packetData,
			FinalRange: finalRange,
		})
	}

	return packets, nil
}

// ReadBitstreamFile reads an opus_demo .bit file, caching the result by filename.
// Each call returns its own Packet slice, but Data slices share cached storage
// and must not be modified. After a successful load is cached, subsequent
// calls do not observe file changes. Read or parse failures are not cached.
func ReadBitstreamFile(filename string) ([]Packet, error) {
	if cached, ok := bitstreamFileCache.Load(filename); ok {
		return clonePacketViews(cached.([]Packet)), nil
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("testvectors: failed to read file: %w", err)
	}

	packets, err := ParseOpusDemoBitstream(data)
	if err != nil {
		return nil, fmt.Errorf("testvectors: failed to parse %s: %w", filename, err)
	}

	actual, _ := bitstreamFileCache.LoadOrStore(filename, packets)
	return clonePacketViews(actual.([]Packet)), nil
}

func clonePacketViews(src []Packet) []Packet {
	if src == nil {
		return nil
	}
	return append([]Packet(nil), src...)
}

// BitstreamInfo contains summary information about a parsed bitstream.
type BitstreamInfo struct {
	PacketCount int  // Number of packets in the bitstream
	TotalBytes  int  // Total bytes of packet data (excluding headers)
	FirstTOC    byte // TOC byte of first packet (for mode detection)
	Duration    int  // Estimated duration in samples at 48kHz
}

// GetBitstreamInfo returns summary information about a parsed bitstream.
// Duration assumes one frame per packet and a constant frame duration taken
// from the first packet's TOC. It is not a full packet-duration calculation.
func GetBitstreamInfo(packets []Packet) BitstreamInfo {
	info := BitstreamInfo{
		PacketCount: len(packets),
	}

	for i, p := range packets {
		info.TotalBytes += len(p.Data)
		if i == 0 && len(p.Data) > 0 {
			info.FirstTOC = p.Data[0]

			// Parse TOC to estimate duration per frame
			// TOC byte: config (bits 7-3), stereo (bit 2), code (bits 1-0)
			config := p.Data[0] >> 3
			frameSize := getFrameSizeFromConfig(config)
			info.Duration = len(packets) * frameSize
		}
	}

	return info
}

// getFrameSizeFromConfig returns frame size in samples at 48kHz for a config index.
// Based on RFC 6716 Section 3.1 Table.
func getFrameSizeFromConfig(config byte) int {
	// Frame sizes for each config group
	switch {
	case config <= 3: // SILK NB: 10/20/40/60ms
		return []int{480, 960, 1920, 2880}[config]
	case config <= 7: // SILK MB: 10/20/40/60ms
		return []int{480, 960, 1920, 2880}[config-4]
	case config <= 11: // SILK WB: 10/20/40/60ms
		return []int{480, 960, 1920, 2880}[config-8]
	case config <= 13: // Hybrid SWB: 10/20ms
		return []int{480, 960}[config-12]
	case config <= 15: // Hybrid FB: 10/20ms
		return []int{480, 960}[config-14]
	case config <= 19: // CELT NB: 2.5/5/10/20ms
		return []int{120, 240, 480, 960}[config-16]
	case config <= 23: // CELT WB: 2.5/5/10/20ms
		return []int{120, 240, 480, 960}[config-20]
	case config <= 27: // CELT SWB: 2.5/5/10/20ms
		return []int{120, 240, 480, 960}[config-24]
	case config <= 31: // CELT FB: 2.5/5/10/20ms
		return []int{120, 240, 480, 960}[config-28]
	default:
		return 960 // Default 20ms
	}
}

// getModeFromConfig returns the mode name from TOC config field.
// RFC 6716 Section 3.1 defines the mapping:
//   - config 0-3: SILK-only (NB)
//   - config 4-7: SILK-only (MB)
//   - config 8-11: SILK-only (WB)
//   - config 12-13: Hybrid (SWB)
//   - config 14-15: Hybrid (FB)
//   - config 16-19: CELT-only (NB)
//   - config 20-23: CELT-only (WB)
//   - config 24-27: CELT-only (SWB)
//   - config 28-31: CELT-only (FB)
func getModeFromConfig(config byte) string {
	switch {
	case config <= 11:
		return "SILK"
	case config <= 15:
		return "Hybrid"
	default:
		return "CELT"
	}
}
