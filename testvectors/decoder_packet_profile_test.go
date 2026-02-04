package testvectors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDecoderPacketDiffProfile reports where packet diffs occur for a specific vector/packet.
// Enable with DEBUG_PKT=1.
func TestDecoderPacketDiffProfile(t *testing.T) {
	if os.Getenv("DEBUG_PKT") == "" {
		t.Skip("set DEBUG_PKT=1 to enable")
		return
	}
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	const (
		vector   = "testvector02"
		pktIndex = 602
	)

	packets, err := ReadBitstreamFile(filepath.Join(testVectorDir, vector+".bit"))
	if err != nil {
		t.Fatalf("read bitstream: %v", err)
	}
	reference, err := readPCMFile(filepath.Join(testVectorDir, vector+".dec"))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	if pktIndex < 0 || pktIndex >= len(packets) {
		t.Fatalf("packet index out of range: %d", pktIndex)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}

	var decoded []int16
	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt.Data[0])
		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			t.Fatalf("pkt %d decode error: %v", i, err)
		}
		decoded = append(decoded, pcm...)
		if i == pktIndex-1 || i == pktIndex {
			t.Logf("pkt %d toc mode=%v bw=%v fs=%d stereo=%v prevMode=%v prevRed=%v", i, toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo, dec.DebugPrevMode(), dec.DebugPrevRedundancy())
		}
		if i != pktIndex {
			continue
		}

		refStart := len(decoded) - len(pcm)
		refEnd := refStart + len(pcm)
		if refStart < 0 {
			refStart = 0
		}
		if refEnd > len(reference) {
			refEnd = len(reference)
		}
		if refStart >= refEnd {
			t.Fatalf("ref slice out of range for pkt %d", i)
		}

		segDecoded := decoded[len(decoded)-len(pcm):]
		segRef := reference[refStart:refEnd]

		ch := dec.Channels()
		overlap := 120 * ch
		if overlap > len(segDecoded) {
			overlap = len(segDecoded)
		}

		type bucket struct {
			name      string
			start     int
			end       int
			diffCount int
			maxDiff   int
		}
		buckets := []bucket{
			{name: "start", start: 0, end: overlap},
			{name: "middle", start: overlap, end: len(segDecoded) - overlap},
			{name: "end", start: len(segDecoded) - overlap, end: len(segDecoded)},
		}

		for bi := range buckets {
			b := &buckets[bi]
			if b.start < 0 {
				b.start = 0
			}
			if b.end < b.start {
				b.end = b.start
			}
			if b.end > len(segDecoded) {
				b.end = len(segDecoded)
			}
			for j := b.start; j < b.end; j++ {
				diff := int(segDecoded[j]) - int(segRef[j])
				if diff < 0 {
					diff = -diff
				}
				if diff != 0 {
					b.diffCount++
					if diff > b.maxDiff {
						b.maxDiff = diff
					}
				}
			}
		}

		for _, b := range buckets {
			t.Logf("pkt %d %s: diffCount=%d maxDiff=%d (range %d..%d)", i, b.name, b.diffCount, b.maxDiff, b.start, b.end)
		}
	}
}
