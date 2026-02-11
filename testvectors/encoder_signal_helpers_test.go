package testvectors

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/thesyncim/gopus/internal/testsignal"
)

const defaultEncoderSignalVariant = testsignal.EncoderVariantAMMultisineV1

func generateEncoderTestSignal(samples int, channels int) []float32 {
	signal, err := testsignal.GenerateEncoderSignalVariant(defaultEncoderSignalVariant, 48000, samples, channels)
	if err != nil {
		panic(fmt.Sprintf("generate encoder signal: %v", err))
	}
	return signal
}

func writeFloat32LEFile(path string, samples []float32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var b [4]byte
	for _, s := range samples {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(s))
		if _, err := f.Write(b[:]); err != nil {
			return err
		}
	}
	return nil
}

func parseOpusDemoEncodeBitstream(path string) ([][]byte, []uint32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	packets, err := ParseOpusDemoBitstream(raw)
	if err != nil {
		return nil, nil, err
	}
	outPackets := make([][]byte, len(packets))
	outRanges := make([]uint32, len(packets))
	for i := range packets {
		outPackets[i] = packets[i].Data
		outRanges[i] = packets[i].FinalRange
	}
	return outPackets, outRanges, nil
}

func packetModeFromTOC(pkt []byte) string {
	if len(pkt) == 0 {
		return "empty"
	}
	cfg := int(pkt[0] >> 3)
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}
