package ogg

// Ogg CRC-32 implementation using polynomial 0x04C11DB7.
//
// Note: This is NOT the standard IEEE CRC-32 (polynomial 0xEDB88320).
// The standard library hash/crc32 package cannot be used here.

// oggCRCTable is the pre-computed lookup table for Ogg CRC-32.
var oggCRCTable [256]uint32

func init() {
	// Initialize Ogg CRC lookup table.
	// Polynomial: 0x04C11DB7 (CRC-32)
	const poly = uint32(0x04C11DB7)
	for i := 0; i < 256; i++ {
		crc := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
		oggCRCTable[i] = crc
	}
}

// oggCRC computes the Ogg CRC-32 checksum from scratch.
func oggCRC(data []byte) uint32 {
	return oggCRCUpdate(0, data)
}

// oggCRCUpdate updates a running CRC with additional data.
func oggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}
