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
	if err != nil {
		fmt.Printf("read bitstream: %v\n", err)
		return
	}
	if len(packets) == 0 {
		fmt.Println("no packets")
		return
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

	channels := 1
	if len(packets[0].Data) > 0 && (packets[0].Data[0]&0x04) != 0 {
		channels = 2
	}
	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		fmt.Printf("new decoder: %v\n", err)
		return
	}

	checks := []int{1, 2, 5, 10, 20, 50, 100, 148, 200, 500}
	for _, limit := range checks {
		if limit > len(packets) {
			limit = len(packets)
		}
		decoded := decodePackets(dec, packets[:limit], channels)
		n := len(decoded)
		if len(reference) < n {
			n = len(reference)
		}
		corr := correlation(decoded[:n], reference[:n])
		fmt.Printf("packets=%d samples=%d corr=%.6f\n", limit, n, corr)
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
