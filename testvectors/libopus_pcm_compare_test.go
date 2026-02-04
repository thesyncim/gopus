//go:build cgo_libopus

package testvectors

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus"
)

// TestDecoderLibopusPCMMatch compares gopus decoder PCM against libopus for all RFC 8251 vectors.
// This is a tight, per-packet, sample-level comparison.
func TestDecoderLibopusPCMMatch(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	const (
		sampleRate = 48000
		channels   = 2
	)

	for _, name := range testVectorNames {
		t.Run(name, func(t *testing.T) {
			packets, err := ReadBitstreamFile(testVectorPath(name + ".bit"))
			if err != nil {
				t.Fatalf("read bitstream: %v", err)
			}
			if len(packets) == 0 {
				t.Fatalf("no packets")
			}

			goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("new gopus decoder: %v", err)
			}
			libDec, err := cgowrap.NewLibopusDecoder(sampleRate, channels)
			if err != nil || libDec == nil {
				t.Fatalf("new libopus decoder failed")
			}
			defer libDec.Destroy()

			maxSamples := gopus.DefaultDecoderConfig(sampleRate, channels).MaxPacketSamples

			totalDiffs := 0
			maxDiff := 0
			firstDiffPkt := -1
			firstDiffIdx := -1

			for pktIdx, pkt := range packets {
				pcmGo, err := decodeInt16(goDec, pkt.Data)
				if err != nil {
					t.Fatalf("gopus decode pkt %d: %v", pktIdx, err)
				}
				pcmLib, nLib := libDec.DecodeInt16(pkt.Data, maxSamples)
				if nLib < 0 {
					t.Fatalf("libopus decode pkt %d failed: %d", pktIdx, nLib)
				}
				if nLib*channels != len(pcmGo) {
					t.Fatalf("pkt %d sample count mismatch: go=%d lib=%d", pktIdx, len(pcmGo), nLib*channels)
				}
				pcmLib = pcmLib[:nLib*channels]

				for i := range pcmGo {
					diff := int(pcmGo[i]) - int(pcmLib[i])
					if diff < 0 {
						diff = -diff
					}
					if diff != 0 {
						totalDiffs++
						if firstDiffPkt < 0 {
							firstDiffPkt = pktIdx
							firstDiffIdx = i
						}
						if diff > maxDiff {
							maxDiff = diff
						}
					}
				}
			}

			t.Logf("pcm diffs: total=%d maxDiff=%d firstPkt=%d firstIdx=%d", totalDiffs, maxDiff, firstDiffPkt, firstDiffIdx)
			if totalDiffs > 0 {
				t.Fatalf("pcm mismatch: total=%d maxDiff=%d", totalDiffs, maxDiff)
			}
		})
	}
}

func testVectorPath(name string) string {
	return testVectorDir + "/" + name
}
