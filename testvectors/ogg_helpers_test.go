// Package testvectors provides shared OGG container helpers for tests.
package testvectors

import (
	"encoding/binary"
	"io"
)

// OGG CRC-32 (polynomial 0x04c11db7)
var oggCRCTable [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		oggCRCTable[i] = r
	}
}

func oggCRC(data []byte) uint32 {
	return oggCRCUpdate(0, data)
}

func oggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

// writeOggPage writes a single OGG page to the writer.
func writeOggPage(w io.Writer, serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) error {
	// Calculate segment table
	var segmentTable []byte
	for _, seg := range segments {
		remaining := len(seg)
		for remaining >= 255 {
			segmentTable = append(segmentTable, 255)
			remaining -= 255
		}
		segmentTable = append(segmentTable, byte(remaining))
	}

	// Page header
	header := make([]byte, 27+len(segmentTable))
	copy(header[0:4], "OggS")
	header[4] = 0 // Version
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	// CRC will be at [22:26]
	header[26] = byte(len(segmentTable))
	copy(header[27:], segmentTable)

	// Compute CRC
	crc := oggCRC(header)
	for _, seg := range segments {
		crc = oggCRCUpdate(crc, seg)
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	// Write header
	if _, err := w.Write(header); err != nil {
		return err
	}

	// Write segments
	for _, seg := range segments {
		if _, err := w.Write(seg); err != nil {
			return err
		}
	}

	return nil
}
