package ogg

import "testing"

// bitwiseOggCRC divides the input polynomial by x^32 + 0x04c11db7 directly,
// independently of the lookup-table update. Ogg starts at zero and does not
// reflect its bits or complement the final CRC (RFC 3533 section 6.1).
func bitwiseOggCRC(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc ^= uint32(b) << 24
		for range 8 {
			top := crc >> 31
			crc <<= 1
			if top != 0 {
				crc ^= 0x04c11db7
			}
		}
	}
	return crc
}

func TestOggCRCExact(t *testing.T) {
	if got := oggCRC([]byte("123456789")); got != 0x89a1897f {
		t.Fatalf("CRC check value = %08x, want 89a1897f", got)
	}
	data := make([]byte, maxPagePayload+pageHeaderSize+255+16)
	for i := range data {
		data[i] = byte(i*29 + i/256*17)
	}
	// Exercise every alignment and every scalar tail around several block
	// boundaries, then complete page sizes, including nonzero initial state.
	for _, initial := range []uint32{0, 1, 0x80000000, 0x12345678, 0xffffffff} {
		for start := range 16 {
			for _, size := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 23, 24, 25, 31, 32, 33, 80, 255, 256, 257, 400, 1200, 4096, maxPagePayload + pageHeaderSize + 255} {
				input := data[start : start+size]
				want := bitwiseOggCRC(initial, input)
				if got := oggCRCUpdate(initial, input); got != want {
					t.Fatalf("initial=%08x start=%d size=%d: CRC=%08x, want %08x", initial, start, size, got, want)
				}
				for _, split := range []int{0, size / 3, size / 2, size} {
					got := oggCRCUpdate(oggCRCUpdate(initial, input[:split]), input[split:])
					if got != want {
						t.Fatalf("initial=%08x start=%d size=%d split=%d: CRC=%08x, want %08x", initial, start, size, split, got, want)
					}
				}
			}
		}
	}
}
