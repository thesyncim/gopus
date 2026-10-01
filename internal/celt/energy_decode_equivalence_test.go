package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// coarseEnergyReference is unquant_coarse_energy (celt/quant_bands.c) written
// band-major with an inner channel loop, as in the C source.
func coarseEnergyReference(rd *rangecoding.Decoder, prevEnergy []celtGLog, stride, channels, nbBands, lm int, intra bool) []celtGLog {
	alpha, beta := float32(AlphaCoef[lm]), float32(BetaCoefInter[lm])
	prob := eProbModel[lm][0]
	if intra {
		alpha, beta = 0, float32(BetaIntra)
		prob = eProbModel[lm][1]
	}
	budget := rd.StorageBits()
	dst := make([]celtGLog, channels*nbBands)
	var prevBand [2]float32
	for band := range nbBands {
		for c := range channels {
			qi := -1
			if remaining := budget - rd.Tell(); remaining >= 15 {
				pi := 2 * min(band, 20)
				qi = decodeLaplaceWithRangeDecoder(rd, int(prob[pi])<<7, int(prob[pi+1])<<6)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			}
			old := float32(prevEnergy[c*stride+band])
			if old < -9 {
				old = -9
			}
			q := float32(qi)
			dst[c*nbBands+band] = celtGLog(decodeCoarseEnergyPredict(alpha, old, prevBand[c], q))
			prevBand[c] = decodeCoarseEnergyUpdate(prevBand[c], q, beta)
		}
	}
	for c := range channels {
		copy(prevEnergy[c*stride:c*stride+nbBands], dst[c*nbBands:(c+1)*nbBands])
	}
	return dst
}

// fineEnergyReference is unquant_fine_energy (celt/quant_bands.c) with
// prev_quant == NULL.
func fineEnergyReference(rd *rangecoding.Decoder, energies []celtGLog, extraQuant []int32, channels, end int) {
	for band := range end {
		extra := extraQuant[band]
		if extra <= 0 || rd.Tell()+channels*int(extra) > rd.StorageBits() {
			continue
		}
		for c := range channels {
			q2 := rd.DecodeRawBits(uint(extra))
			offset := (float32(q2)+0.5)*float32(int32(1)<<uint32(14-extra))*(1.0/16384) - 0.5
			offset *= float32(1<<14) * (1.0 / 16384)
			energies[c*end+band] += celtGLog(offset)
		}
	}
}

func TestCoarseAndFineEnergyDecodeMatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc0a5e))
	for iter := range 4000 {
		channels := 1 + iter%2
		lm := rng.Intn(4)
		intra := rng.Intn(3) == 0
		nbBands := 1 + rng.Intn(MaxBands)
		packet := make([]byte, 1+rng.Intn(64), 68)
		if iter%5 == 0 {
			packet = packet[:1+rng.Intn(4)] // exercise the low-budget fallbacks
		}
		for i := range packet {
			packet[i] = byte(rng.Uint32())
		}
		dec := NewDecoder(channels)
		stride := dec.predStride()
		for i := range dec.prevEnergy {
			dec.prevEnergy[i] = celtGLog(float32(rng.NormFloat64() * 8))
		}
		refPrev := append([]celtGLog(nil), dec.prevEnergy...)

		var rd, refRD rangecoding.Decoder
		rd.Init(packet)
		refRD.Init(packet)
		dec.rangeDecoder = &rd
		got := dec.decodeCoarseEnergyGLogInto(make([]celtGLog, channels*nbBands), nbBands, intra, lm)
		want := coarseEnergyReference(&refRD, refPrev, stride, channels, nbBands, lm, intra)

		extra := make([]int32, nbBands)
		for i := range extra {
			extra[i] = int32(rng.Intn(9)) - 1
		}
		d2 := &Decoder{channels: int32(channels), rangeDecoder: &rd}
		d2.prevEnergy = dec.prevEnergy
		d2.decodeFineEnergyGLog(got, nbBands, nil, extra)
		fineEnergyReference(&refRD, want, extra, channels, nbBands)

		for i := range want {
			if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
				t.Fatalf("iter %d C=%d bands=%d LM=%d intra=%t: energy[%d]=%g want %g", iter, channels, nbBands, lm, intra, i, got[i], want[i])
			}
		}
		for i := range refPrev {
			if math.Float32bits(float32(dec.prevEnergy[i])) != math.Float32bits(float32(refPrev[i])) {
				t.Fatalf("iter %d: prevEnergy[%d]=%g want %g", iter, i, dec.prevEnergy[i], refPrev[i])
			}
		}
		if rd.Tell() != refRD.Tell() || rd.Range() != refRD.Range() {
			t.Fatalf("iter %d: range state tell=%d rng=%08x want tell=%d rng=%08x", iter, rd.Tell(), rd.Range(), refRD.Tell(), refRD.Range())
		}
	}
}
