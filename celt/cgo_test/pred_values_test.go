//go:build cgo_libopus
// +build cgo_libopus

// Package cgo traces stereo predictor values
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
)

// silk_stereo_pred_quant_Q13 table
var silk_stereo_pred_quant_Q13 = []int16{
	-13732, -10050, -8266, -7526, -6500, -5000, -2950, -820, 820, 2950, 5000, 6500, 7526, 8266, 10050, 13732,
}

// silk_stereo_pred_joint_iCDF table
var silk_stereo_pred_joint_iCDF = []uint8{
	249, 247, 246, 245, 244, 234, 210, 202, 201, 200, 197, 174, 82, 59, 56, 55, 54, 46, 22, 12, 11, 10, 9, 7, 0,
}

var silk_uniform3_iCDF = []uint8{171, 85, 0}
var silk_uniform5_iCDF = []uint8{205, 154, 102, 51, 0}

const stereoQuantSubSteps = 5

func silkSMULWB(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

func silkSMLABB(a, b, c int32) int32 {
	return a + int32(int16(b))*int32(int16(c))
}

func silkFixConst(x float64, q int) int {
	if q < 0 {
		return int(x)
	}
	return int(x*float64(int64(1)<<q) + 0.5)
}

// decodePredQ13 decodes the stereo prediction coefficients
func decodePredQ13(rd *rangecoding.Decoder) ([]int32, []int, error) {
	predQ13 := make([]int32, 2)
	ix := [2][3]int{}

	n := rd.DecodeICDF(silk_stereo_pred_joint_iCDF, 8)
	ix[0][2] = n / 5
	ix[1][2] = n - 5*ix[0][2]

	for i := 0; i < 2; i++ {
		ix[i][0] = rd.DecodeICDF(silk_uniform3_iCDF, 8)
		ix[i][1] = rd.DecodeICDF(silk_uniform5_iCDF, 8)
	}

	for i := 0; i < 2; i++ {
		ix[i][0] += 3 * ix[i][2]
		lowQ13 := int32(silk_stereo_pred_quant_Q13[ix[i][0]])
		stepQ13 := silkSMULWB(int32(silk_stereo_pred_quant_Q13[ix[i][0]+1])-lowQ13, int32(silkFixConst(0.5/float64(stereoQuantSubSteps), 16)))
		predQ13[i] = silkSMLABB(lowQ13, stepQ13, int32(2*ix[i][1]+1))
	}
	predQ13[0] -= predQ13[1]

	// Return indices for debugging
	indices := []int{n, ix[0][0], ix[0][1], ix[0][2], ix[1][0], ix[1][1], ix[1][2]}
	return predQ13, indices, nil
}

// TestTracePredQ13Values traces predQ13 for each packet
func TestTracePredQ13Values(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	t.Logf("Tracing predQ13 values for packets 12-16:")
	t.Logf("%3s  %6s  %6s  %20s  %20s", "pkt", "pred0", "pred1", "indices_0", "indices_1")

	for pktIdx := 12; pktIdx < 17 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		if len(pkt) == 0 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != 0 && toc.Mode != 2 {
			// Not SILK or Hybrid
			t.Logf("%3d  Not SILK/Hybrid (mode=%d)", pktIdx, toc.Mode)
			continue
		}

		// For SILK/Hybrid stereo, the payload starts after TOC
		// We need to create a range decoder for the payload
		payload := pkt[1:]

		rd := &rangecoding.Decoder{}
		rd.Init(payload)
		predQ13, indices, err := decodePredQ13(rd)
		if err != nil {
			t.Logf("%3d  Error: %v", pktIdx, err)
			continue
		}

		t.Logf("%3d  %6d  %6d  n=%d ix0=[%d,%d,%d]  ix1=[%d,%d,%d]",
			pktIdx, predQ13[0], predQ13[1],
			indices[0],
			indices[1]-3*indices[3], indices[2], indices[3], // Undo the ix[i][0] += 3*ix[i][2]
			indices[4]-3*indices[6], indices[5], indices[6])
	}
}
