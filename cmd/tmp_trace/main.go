package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/testvectors"
)

func main() {
	defaultBitFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.bit")
	bitFile := flag.String("bitfile", defaultBitFile, "path to .bit testvector")
	packetIdx := flag.Int("pkt", 0, "packet index")
	flag.Parse()

	packets, err := testvectors.ReadBitstreamFile(*bitFile)
	if err != nil || len(packets) == 0 {
		panic("failed to load bitstream")
	}

	if *packetIdx < 0 || *packetIdx >= len(packets) {
		panic("packet index out of range")
	}
	pkt := packets[*packetIdx]
	if len(pkt.Data) == 0 {
		panic("empty packet")
	}

	toc := gopus.ParseTOC(pkt.Data[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	dec := celt.NewDecoder(channels)
	dec.SetBandwidth(celt.BandwidthFromOpusConfig(int(toc.Bandwidth)))

	celt.SetTracer(&celt.LogTracer{W: os.Stdout})
	defer celt.SetTracer(&celt.NoopTracer{})

	info, err := gopus.ParsePacket(pkt.Data)
	if err != nil {
		panic(err)
	}
	frames := extractFrames(pkt.Data, info)
	if len(frames) == 0 {
		panic("no frames")
	}

	baseName := strings.TrimSuffix(filepath.Base(*bitFile), filepath.Ext(*bitFile))
	for i, frame := range frames {
		if len(frame) == 0 {
			continue
		}
		outPath := filepath.Join("/tmp", fmt.Sprintf("%s_pkt%d_frame%d.bin", baseName, *packetIdx, i))
		_ = os.WriteFile(outPath, frame, 0o644)
		_, _ = dec.DecodeFrame(frame, toc.FrameSize)
	}
}

func extractFrames(data []byte, info gopus.PacketInfo) [][]byte {
	frames := make([][]byte, info.FrameCount)
	totalFrameBytes := 0
	for _, size := range info.FrameSizes {
		totalFrameBytes += size
	}
	frameDataStart := len(data) - info.Padding - totalFrameBytes
	if frameDataStart < 1 {
		frameDataStart = 1
	}
	dataEnd := len(data) - info.Padding
	if dataEnd < frameDataStart {
		dataEnd = frameDataStart
	}
	offset := frameDataStart
	for i := 0; i < info.FrameCount; i++ {
		frameLen := info.FrameSizes[i]
		endOffset := offset + frameLen
		if endOffset > dataEnd {
			endOffset = dataEnd
		}
		if offset >= dataEnd {
			frames[i] = nil
		} else {
			frames[i] = data[offset:endOffset]
		}
		offset = endOffset
	}
	return frames
}
