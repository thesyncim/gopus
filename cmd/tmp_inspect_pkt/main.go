package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/testvectors"
)

func main() {
	bitFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.bit")
	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil {
		panic(err)
	}
	idx := 148
	if len(os.Args) > 1 {
		if v, err := strconv.Atoi(os.Args[1]); err == nil {
			idx = v
		}
	}
	if idx >= len(packets) {
		fmt.Printf("packet idx %d out of range (%d)\n", idx, len(packets))
		return
	}
	pkt := packets[idx]
	fmt.Printf("packet %d len=%d finalRange=0x%08X\n", idx, len(pkt.Data), pkt.FinalRange)
	if len(pkt.Data) == 0 {
		return
	}
	max := 12
	if len(pkt.Data) < max {
		max = len(pkt.Data)
	}
	fmt.Printf("first%d: % X\n", max, pkt.Data[:max])
	toc := gopus.ParseTOC(pkt.Data[0])
	fmt.Printf("toc: mode=%v frameCode=%d stereo=%v frameSize=%d bandwidth=%d\n", toc.Mode, toc.FrameCode, toc.Stereo, toc.FrameSize, toc.Bandwidth)
	if len(pkt.Data) > 1 {
		fc := pkt.Data[1]
		vbr := (fc & 0x80) != 0
		pad := (fc & 0x40) != 0
		m := int(fc & 0x3F)
		fmt.Printf("framecount byte=0x%02X vbr=%v pad=%v m=%d\n", fc, vbr, pad, m)
	}
	info, err := gopus.ParsePacket(pkt.Data)
	if err != nil {
		fmt.Printf("ParsePacket error: %v\n", err)
		return
	}
	fmt.Printf("ParsePacket: frameCount=%d frameSizes=%v padding=%d total=%d\n", info.FrameCount, info.FrameSizes, info.Padding, info.TotalSize)

	channels := 1
	if toc.Stereo {
		channels = 2
	}
	cdec := celt.NewDecoder(channels)
	cdec.SetBandwidth(celt.BandwidthFromOpusConfig(int(toc.Bandwidth)))
	frames := extractFrames(pkt.Data, info)
	for fi, f := range frames {
		_, _ = cdec.DecodeFrame(f, toc.FrameSize)
		rd := cdec.RangeDecoder()
		fmt.Printf(" frame %d: bytes=%d range=0x%08X tell=%d\n", fi, len(f), rd.Range(), rd.Tell())
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
