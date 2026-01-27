package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	cgo "github.com/thesyncim/gopus/internal/celt/cgo_test"
)

type Packet struct {
	Data []byte
}

func readBitstream(path string) ([]Packet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var packets []Packet
	off := 0
	for off+8 <= len(data) {
		pktLen := int(binary.BigEndian.Uint32(data[off:]))
		off += 4
		off += 4 // final range
		if pktLen <= 0 || off+pktLen > len(data) {
			break
		}
		pkt := make([]byte, pktLen)
		copy(pkt, data[off:off+pktLen])
		off += pktLen
		packets = append(packets, Packet{Data: pkt})
	}
	return packets, nil
}

func snr(a, b []float32) float64 {
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	if min == 0 {
		return 0
	}
	var noise, signal float64
	for i := 0; i < min; i++ {
		d := float64(a[i]) - float64(b[i])
		noise += d * d
		signal += float64(b[i]) * float64(b[i])
	}
	if noise == 0 {
		return 999.0
	}
	return 10 * math.Log10(signal/noise)
}

func main() {
	celt.SetTracer(&celt.NoopTracer{})

	bitFile := filepath.Join("/Users/thesyncim/GolandProjects/gopus/internal/testvectors/testdata/opus_testvectors", "testvector07.bit")
	packets, err := readBitstream(bitFile)
	if err != nil {
		panic(err)
	}

	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := cgo.NewLibopusDecoder(48000, 1)
	if libDec == nil {
		panic("libopus decoder nil")
	}
	defer libDec.Destroy()

	var monoCount, stereoCount int
	var monoSum, stereoSum float64
	var monoMin, stereoMin float64
	monoMin = 1e9
	stereoMin = 1e9

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt.Data[0])
		goOut, err := goDec.DecodeFloat32(pkt.Data)
		if err != nil {
			fmt.Printf("go decode error at %d: %v\n", i, err)
			continue
		}
		libOut, libN := libDec.DecodeFloat(pkt.Data, 5760)
		if libN <= 0 {
			fmt.Printf("lib decode error at %d: %d\n", i, libN)
			continue
		}
		libOut = libOut[:libN]
		s := snr(goOut, libOut)
		if toc.Stereo {
			stereoCount++
			stereoSum += s
			if s < stereoMin {
				stereoMin = s
			}
		} else {
			monoCount++
			monoSum += s
			if s < monoMin {
				monoMin = s
			}
		}
	}

	fmt.Printf("mono packets=%d avgSNR=%.2f minSNR=%.2f\n", monoCount, monoSum/float64(monoCount), monoMin)
	fmt.Printf("stereo packets=%d avgSNR=%.2f minSNR=%.2f\n", stereoCount, stereoSum/float64(stereoCount), stereoMin)
}
