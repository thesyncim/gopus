package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/testvectors"
)

func main() {
	bitFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.bit")
	decFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.dec")

	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil || len(packets) == 0 {
		panic("read bitstream failed")
	}

	refData, err := os.ReadFile(decFile)
	if err != nil {
		panic("read dec failed")
	}
	reference := make([]int16, len(refData)/2)
	for i := 0; i < len(reference); i++ {
		reference[i] = int16(binary.LittleEndian.Uint16(refData[i*2:]))
	}

	channels := 1
	if len(packets[0].Data) > 0 && (packets[0].Data[0]&0x04) != 0 {
		channels = 2
	}
	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		panic(err)
	}

	limit := 10
	if limit > len(packets) {
		limit = len(packets)
	}
	decoded := decodePackets(dec, packets[:limit], channels)

	shifts := []int{-240, -120, -60, 0, 60, 120, 240}
	for _, shift := range shifts {
		corr := correlationShift(decoded, reference, shift)
		fmt.Printf("shift=%d corr=%.6f\n", shift, corr)
	}
}

func decodePackets(dec *gopus.Decoder, packets []testvectors.Packet, channels int) []int16 {
	decoded := make([]int16, 0)
	for _, pkt := range packets {
		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			frameSize := gopus.ParseTOC(pkt.Data[0]).FrameSize
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
	return decoded
}

func correlationShift(a, b []int16, shift int) float64 {
	if shift > 0 {
		if shift >= len(a) || shift >= len(b) {
			return 0
		}
		return correlation(a[shift:], b)
	}
	if shift < 0 {
		shift = -shift
		if shift >= len(a) || shift >= len(b) {
			return 0
		}
		return correlation(a, b[shift:])
	}
	return correlation(a, b)
}

func correlation(a, b []int16) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		x := float64(a[i])
		y := float64(b[i])
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}
	if sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (sqrt(sumX2) * sqrt(sumY2))
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
