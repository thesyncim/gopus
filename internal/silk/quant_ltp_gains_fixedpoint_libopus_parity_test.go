//go:build gopus_fixed_point

package silk

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKFixedQuantLTPGainsInputMagic  = "QLGI"
	libopusSILKFixedQuantLTPGainsOutputMagic = "QLGO"

	quantLTPKindQuant = uint32(0)
	quantLTPKindScale = uint32(1)
)

type silkFixedQuantLTPCase struct {
	name         string
	nbSubfr      int
	subfrLen     int32
	sumLogGainQ7 int32
	xxQ17        []int32 // nbSubfr*LTP_ORDER*LTP_ORDER
	xXQ17        []int32 // nbSubfr*LTP_ORDER
}

type silkFixedQuantLTPResult struct {
	periodicityIndex int32
	sumLogGainQ7     int32
	predGainQ7       int32
	cbkIndex         []int32 // nbSubfr
	bQ14             []int32 // nbSubfr*LTP_ORDER
}

type silkFixedScaleCtrlCase struct {
	name             string
	ltpredCodGainQ7  int32
	packetLossPerc   int32
	nFramesPerPacket int32
	lbrrFlag         int32
	snrDBQ7          int32
	condCoding       int32
}

type silkFixedScaleCtrlResult struct {
	ltpScaleIndex int32
	ltpScaleQ14   int32
}

func probeLibopusSILKFixedQuantLTP(quant []silkFixedQuantLTPCase, scale []silkFixedScaleCtrlCase) ([]silkFixedQuantLTPResult, []silkFixedScaleCtrlResult, error) {
	binPath, err := buildFixedSILKOracle("libopus_silk_fixed_quant_ltp_gains_info.c", "quant_ltp_gains")
	if err != nil {
		return nil, nil, err
	}
	total := len(quant) + len(scale)
	payload := libopustest.NewOraclePayload(libopusSILKFixedQuantLTPGainsInputMagic, uint32(total))

	for _, tc := range quant {
		payload.U32(quantLTPKindQuant)
		payload.U32(uint32(tc.nbSubfr))
		payload.I32(tc.subfrLen)
		payload.I32(tc.sumLogGainQ7)
		for _, v := range tc.xxQ17 {
			payload.I32(v)
		}
		for _, v := range tc.xXQ17 {
			payload.I32(v)
		}
	}
	for _, tc := range scale {
		payload.U32(quantLTPKindScale)
		payload.I32(tc.ltpredCodGainQ7)
		payload.I32(tc.packetLossPerc)
		payload.I32(tc.nFramesPerPacket)
		payload.I32(tc.lbrrFlag)
		payload.I32(tc.snrDBQ7)
		payload.I32(tc.condCoding)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk fixed quant ltp gains", libopusSILKFixedQuantLTPGainsOutputMagic)
	if err != nil {
		return nil, nil, err
	}
	reader.Count(total)

	qout := make([]silkFixedQuantLTPResult, len(quant))
	for i := range qout {
		nb := quant[i].nbSubfr
		qout[i].periodicityIndex = reader.I32()
		qout[i].sumLogGainQ7 = reader.I32()
		qout[i].predGainQ7 = reader.I32()
		qout[i].cbkIndex = make([]int32, nb)
		for k := range qout[i].cbkIndex {
			qout[i].cbkIndex[k] = reader.I32()
		}
		qout[i].bQ14 = make([]int32, nb*ltpOrder)
		for k := range qout[i].bQ14 {
			qout[i].bQ14[k] = reader.I32()
		}
	}
	sout := make([]silkFixedScaleCtrlResult, len(scale))
	for i := range sout {
		sout[i].ltpScaleIndex = reader.I32()
		sout[i].ltpScaleQ14 = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return qout, sout, nil
}

func TestSILKQuantLTPGainsFixedLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	rng := rand.New(rand.NewSource(0x10ac7))

	const order = ltpOrder

	// Build a Q17 correlation matrix/vector that loosely resembles the output
	// of silk_find_LTP_FIX: the matrix is symmetric and diagonally dominant
	// (positive on the diagonal), the vector is small and signed.
	newCase := func(name string, nbSubfr int, scaleNum int32) silkFixedQuantLTPCase {
		tc := silkFixedQuantLTPCase{
			name:         name,
			nbSubfr:      nbSubfr,
			subfrLen:     int32(40 + 40*rng.Intn(4)),
			sumLogGainQ7: int32(rng.Intn(6 * 128)),
			xxQ17:        make([]int32, nbSubfr*order*order),
			xXQ17:        make([]int32, nbSubfr*order),
		}
		for s := 0; s < nbSubfr; s++ {
			XX := tc.xxQ17[s*order*order : (s+1)*order*order]
			for r := 0; r < order; r++ {
				diag := int32(1+rng.Intn(int(scaleNum))) * 4
				XX[r*order+r] = diag
				for col := r + 1; col < order; col++ {
					off := int32(rng.Intn(int(scaleNum))) - scaleNum/2
					XX[r*order+col] = off
					XX[col*order+r] = off
				}
			}
			xX := tc.xXQ17[s*order : (s+1)*order]
			for r := 0; r < order; r++ {
				xX[r] = int32(rng.Intn(int(scaleNum))) - scaleNum/2
			}
		}
		return tc
	}

	var quant []silkFixedQuantLTPCase
	for _, nb := range []int{2, 4} {
		for _, sc := range []int32{1 << 12, 1 << 16, 1 << 20} {
			quant = append(quant, newCase("std", nb, sc))
		}
	}
	// Zero correlations (degenerate) to hit the safe-default path.
	for _, nb := range []int{2, 4} {
		quant = append(quant, silkFixedQuantLTPCase{
			name:     "zero",
			nbSubfr:  nb,
			subfrLen: 80,
			xxQ17:    make([]int32, nb*order*order),
			xXQ17:    make([]int32, nb*order),
		})
	}
	// Bulk random coverage.
	for i := 0; i < 96; i++ {
		nb := 2 + 2*rng.Intn(2)
		sc := []int32{1 << 10, 1 << 14, 1 << 18, 1 << 22}[rng.Intn(4)]
		quant = append(quant, newCase("bulk", nb, sc))
	}

	// LTP_scale_ctrl cases.
	var scale []silkFixedScaleCtrlCase
	condModes := []int32{codeIndependently, codeConditionally}
	for i := 0; i < 64; i++ {
		scale = append(scale, silkFixedScaleCtrlCase{
			name:             "bulk",
			ltpredCodGainQ7:  int32(rng.Intn(24*128) - 4*128),
			packetLossPerc:   int32(rng.Intn(50)),
			nFramesPerPacket: int32(1 + rng.Intn(3)),
			lbrrFlag:         int32(rng.Intn(2)),
			snrDBQ7:          int32(10*128 + rng.Intn(30*128)),
			condCoding:       condModes[rng.Intn(len(condModes))],
		})
	}
	// Deterministic edges around the two thresholds.
	for _, cond := range condModes {
		for _, lbrr := range []int32{0, 1} {
			scale = append(scale, silkFixedScaleCtrlCase{
				name: "edge", ltpredCodGainQ7: 0, packetLossPerc: 0,
				nFramesPerPacket: 1, lbrrFlag: lbrr, snrDBQ7: 25 * 128, condCoding: cond,
			})
			scale = append(scale, silkFixedScaleCtrlCase{
				name: "edge", ltpredCodGainQ7: 20 * 128, packetLossPerc: 40,
				nFramesPerPacket: 3, lbrrFlag: lbrr, snrDBQ7: 12 * 128, condCoding: cond,
			})
		}
	}

	wantQ, wantS, err := probeLibopusSILKFixedQuantLTP(quant, scale)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk fixed quant ltp gains", err)
		return
	}

	for i, tc := range quant {
		bQ14 := make([]int16, tc.nbSubfr*order)
		cbkIndex := make([]int8, tc.nbSubfr)
		var periodicity int8
		sumLogGain := tc.sumLogGainQ7
		var predGain int32
		XX := make([]int32, len(tc.xxQ17))
		copy(XX, tc.xxQ17)
		xX := make([]int32, len(tc.xXQ17))
		copy(xX, tc.xXQ17)

		silkQuantLTPGainsFixed(bQ14, cbkIndex, &periodicity, &sumLogGain, &predGain,
			XX, xX, int(tc.subfrLen), tc.nbSubfr)

		w := wantQ[i]
		fail := func(field string, got, exp interface{}) {
			t.Fatalf("quant case %d (%s nb=%d): %s=%v want %v", i, tc.name, tc.nbSubfr, field, got, exp)
		}
		if int32(periodicity) != w.periodicityIndex {
			fail("periodicityIndex", periodicity, w.periodicityIndex)
		}
		if sumLogGain != w.sumLogGainQ7 {
			fail("sumLogGainQ7", sumLogGain, w.sumLogGainQ7)
		}
		if predGain != w.predGainQ7 {
			fail("predGainQ7", predGain, w.predGainQ7)
		}
		for k := 0; k < tc.nbSubfr; k++ {
			if int32(cbkIndex[k]) != w.cbkIndex[k] {
				fail(fmt.Sprintf("cbkIndex[%d]", k), cbkIndex[k], w.cbkIndex[k])
			}
		}
		for k := 0; k < tc.nbSubfr*order; k++ {
			if int32(bQ14[k]) != w.bQ14[k] {
				fail(fmt.Sprintf("B_Q14[%d]", k), bQ14[k], w.bQ14[k])
			}
		}
	}

	for i, tc := range scale {
		idx, q14 := silkLTPScaleCtrlFixed(tc.ltpredCodGainQ7, tc.packetLossPerc,
			tc.nFramesPerPacket, tc.lbrrFlag, tc.snrDBQ7, tc.condCoding)
		w := wantS[i]
		if idx != w.ltpScaleIndex {
			t.Fatalf("scale case %d (%s): ltpScaleIndex=%d want %d", i, tc.name, idx, w.ltpScaleIndex)
		}
		if q14 != w.ltpScaleQ14 {
			t.Fatalf("scale case %d (%s): ltpScaleQ14=%d want %d", i, tc.name, q14, w.ltpScaleQ14)
		}
	}
}
