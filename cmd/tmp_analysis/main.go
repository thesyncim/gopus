package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/testvectors"
)

func main() {
	bitFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.bit")
	decFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.dec")

	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil {
		fmt.Printf("read bitstream: %v\n", err)
		return
	}
	if len(packets) == 0 {
		fmt.Println("no packets")
		return
	}

	printPacketHeader(packets[0], 0)
	printPacketHeader(packets[2], 2)

	channels := 1
	if gopus.ParseTOC(packets[0].Data[0]).Stereo {
		channels = 2
	}

	// CELT-only decoder for range compare
	cdec := celt.NewDecoder(channels)

	// Compare final range per packet
	mismatchIdx := -1
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		ptoc := gopus.ParseTOC(pkt.Data[0])
		if ptoc.Mode != gopus.ModeCELT {
			continue
		}
		cdec.SetBandwidth(celt.BandwidthFromOpusConfig(int(ptoc.Bandwidth)))
		info, err := gopus.ParsePacket(pkt.Data)
		if err != nil {
			fmt.Printf("packet %d parse error: %v\n", i, err)
			break
		}
		if i == 0 || i == 2 {
			fmt.Printf("packet %d: size=%d frameCount=%d frameSizes=%v padding=%d frameSize=%d\n", i, len(pkt.Data), info.FrameCount, info.FrameSizes, info.Padding, ptoc.FrameSize)
		}
		frames := extractFrames(pkt.Data, info)
		for fi := 0; fi < len(frames); fi++ {
			_, _ = cdec.DecodeFrame(frames[fi], ptoc.FrameSize)
			rd := cdec.RangeDecoder()
			if i == 0 || i == 2 {
				fmt.Printf("  packet %d frame %d: bytes=%d bits=%d tell=%d range=0x%08X\n", i, fi, len(frames[fi]), len(frames[fi])*8, rd.Tell(), rd.Range())
			}
		}
		rd := cdec.RangeDecoder()
		if rd == nil {
			fmt.Printf("packet %d: no range decoder\n", i)
			break
		}
		if rd.Range() != pkt.FinalRange {
			mismatchIdx = i
			fmt.Printf("first final range mismatch at packet %d: got=0x%08X want=0x%08X\n", i, rd.Range(), pkt.FinalRange)
			break
		}
	}
	if mismatchIdx == -1 {
		fmt.Println("all packets matched final range")
	}

	// Also compute global correlation for reference
	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		fmt.Printf("new decoder: %v\n", err)
		return
	}

	var decoded []int16
	for _, pkt := range packets {
		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			pktTOC := gopus.ParseTOC(pkt.Data[0])
			frameSize := pktTOC.FrameSize
			info, perr := gopus.ParsePacket(pkt.Data)
			frameCount := 1
			if perr == nil {
				frameCount = info.FrameCount
			}
			zeros := make([]int16, frameSize*frameCount*channels)
			decoded = append(decoded, zeros...)
			continue
		}
		decoded = append(decoded, pcm...)
	}

	refData, err := os.ReadFile(decFile)
	if err != nil {
		fmt.Printf("read dec: %v\n", err)
		return
	}
	reference := make([]int16, len(refData)/2)
	for i := 0; i < len(reference); i++ {
		reference[i] = int16(binary.LittleEndian.Uint16(refData[i*2:]))
	}

	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		fmt.Println("no samples to compare")
		return
	}

	var sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		x := float64(decoded[i])
		y := float64(reference[i])
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}

	alpha := 0.0
	if sumX2 > 0 {
		alpha = sumXY / sumX2
	}
	corr := 0.0
	if sumX2 > 0 && sumY2 > 0 {
		corr = sumXY / (sqrt(sumX2) * sqrt(sumY2))
	}

	fmt.Printf("samples compared: %d\n", n)
	fmt.Printf("alpha (best scale): %.6f\n", alpha)
	fmt.Printf("corr: %.6f\n", corr)
}

func printPacketHeader(pkt testvectors.Packet, idx int) {
	if len(pkt.Data) == 0 {
		fmt.Printf("packet %d: empty\n", idx)
		return
	}
	fmt.Printf("packet %d len=%d finalRange=0x%08X\n", idx, len(pkt.Data), pkt.FinalRange)
	max := 10
	if len(pkt.Data) < max {
		max = len(pkt.Data)
	}
	fmt.Printf("packet %d first%d: % X\n", idx, max, pkt.Data[:max])
	if len(pkt.Data) > 1 {
		fc := pkt.Data[1]
		vbr := (fc & 0x80) != 0
		pad := (fc & 0x40) != 0
		m := int(fc & 0x3F)
		fmt.Printf("packet %d framecount byte=0x%02X vbr=%v pad=%v m=%d\n", idx, fc, vbr, pad, m)
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

func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 20; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}
