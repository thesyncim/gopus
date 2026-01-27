package main

import (
	"fmt"
	"path/filepath"

	"github.com/thesyncim/gopus"
	cgopkg "github.com/thesyncim/gopus/internal/celt/cgo_test"
	"github.com/thesyncim/gopus/internal/testvectors"
)

func main() {
	bitFile := filepath.Join("internal", "testvectors", "testdata", "opus_testvectors", "testvector07.bit")
	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil || len(packets) == 0 {
		panic("read bitstream failed")
	}

	channels := 1
	if len(packets[0].Data) > 0 && (packets[0].Data[0]&0x04) != 0 {
		channels = 2
	}

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := cgopkg.NewLibopusDecoder(48000, channels)
	if libDec == nil {
		panic("libopus decoder not available")
	}
	defer libDec.Destroy()

	for i, pkt := range packets {
		goOut, err := goDec.DecodeFloat32(pkt.Data)
		if err != nil {
			fmt.Printf("packet %d: gopus decode error: %v\n", i, err)
			return
		}
		_, libN := libDec.DecodeFloat(pkt.Data, 5760)
		if libN <= 0 {
			fmt.Printf("packet %d: libopus decode error: %d\n", i, libN)
			return
		}
		libTotal := libN * channels
		if len(goOut) != libTotal {
			fmt.Printf("packet %d: sample count mismatch gopus=%d libopus=%d\n", i, len(goOut), libTotal)
			return
		}
	}

	fmt.Println("no sample count mismatches")
}
