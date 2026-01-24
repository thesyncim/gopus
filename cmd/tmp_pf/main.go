package main

import (
	"fmt"
	"path/filepath"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/testvectors"
)

func main() {
	bitFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector01.bit")
	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil {
		panic(err)
	}
	if len(packets) == 0 {
		fmt.Println("no packets")
		return
	}
	pkt := packets[0]
	dec := celt.NewDecoder(2)
	frameSize := 960
	_, err = dec.DecodeFrame(pkt.Data, frameSize)
	if err != nil {
		fmt.Println("decode error:", err)
	}
	fmt.Printf("postfilter period=%d gain=%.4f tapset=%d\n", dec.PostfilterPeriod(), dec.PostfilterGain(), dec.PostfilterTapset())
}
