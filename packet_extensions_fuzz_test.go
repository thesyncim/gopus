package gopus

import "testing"

func maxFuzzPacketExtensionCount(dataLen, nbFrames int) int {
	if dataLen <= 0 || nbFrames <= 0 {
		return 0
	}
	// Repeat markers can replay a compact extension run across every frame.
	return dataLen * nbFrames
}

func FuzzPacketExtensionIterator_NoPanic(f *testing.F) {
	f.Add([]byte{0x41, 0x02, 'A', 'B'}, uint8(1), uint8(32))
	f.Add([]byte{0x06, 0x04}, uint8(3), uint8(3))
	f.Add([]byte{0x01, 0x01, 0x06}, uint8(1), uint8(3))
	f.Add([]byte{0x02, 0x42, 'x', 'y', 'z'}, uint8(3), uint8(33))
	f.Add([]byte{0xff, 0x00, 0x03, 0x02}, uint8(2), uint8(64))

	f.Fuzz(func(t *testing.T, data []byte, rawFrames uint8, rawID uint8) {
		if len(data) > 512 {
			data = data[:512]
		}
		nbFrames := int(rawFrames%maxRepacketizerFrames) + 1
		id := 3 + int(rawID%125)

		count, err := countPacketExtensions(data, nbFrames)
		if err != nil {
			return
		}
		maxCount := maxFuzzPacketExtensionCount(len(data), nbFrames)
		if count < 0 || count > maxCount {
			t.Fatalf("extension count out of range: %d > %d", count, maxCount)
		}

		counts := make([]int, nbFrames)
		countByFrame, err := countPacketExtensionsByFrame(data, nbFrames, counts)
		if err != nil {
			t.Fatalf("countPacketExtensionsByFrame after count success: %v", err)
		}
		if countByFrame != count {
			t.Fatalf("extension count mismatch: total=%d byFrame=%d", count, countByFrame)
		}

		extensions := make([]packetExtensionData, count)
		parsed, err := parsePacketExtensions(data, nbFrames, extensions)
		if err != nil {
			t.Fatalf("parsePacketExtensions after count success: %v", err)
		}
		if parsed != count {
			t.Fatalf("parsePacketExtensions count=%d want %d", parsed, count)
		}

		frameOrder := make([]packetExtensionData, count)
		ordered, err := parsePacketExtensionsFrameOrder(data, nbFrames, counts, frameOrder)
		if err != nil {
			t.Fatalf("parsePacketExtensionsFrameOrder after count success: %v", err)
		}
		if ordered != count {
			t.Fatalf("frame-order extension count=%d want %d", ordered, count)
		}

		if ext, ok, err := findPacketExtension(data, nbFrames, id); err != nil {
			t.Fatalf("findPacketExtension after count success: %v", err)
		} else if ok && ext.ID != id {
			t.Fatalf("findPacketExtension id=%d want %d", ext.ID, id)
		}

		if count == 0 {
			return
		}
		n, err := generatePacketExtensions(nil, len(data), extensions[:parsed], nbFrames, false)
		if err != nil {
			return
		}
		if n < 0 || n > len(data) {
			t.Fatalf("generatePacketExtensions size=%d outside input bound %d", n, len(data))
		}
		out := make([]byte, n)
		written, err := generatePacketExtensions(out, n, extensions[:parsed], nbFrames, false)
		if err != nil {
			t.Fatalf("generatePacketExtensions write after size success: %v", err)
		}
		if written != n {
			t.Fatalf("generatePacketExtensions wrote=%d want %d", written, n)
		}
		regeneratedCount, err := countPacketExtensions(out, nbFrames)
		if err != nil {
			t.Fatalf("countPacketExtensions regenerated output: %v", err)
		}
		if regeneratedCount != count {
			t.Fatalf("regenerated extension count=%d want %d", regeneratedCount, count)
		}
	})
}
