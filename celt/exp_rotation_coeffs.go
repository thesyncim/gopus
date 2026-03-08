package celt

import "math"

const maxExpRotationLength = maxBandWidth

var expRotationSpreadFactors = [3]int{15, 10, 5}

type expRotationCoeff struct {
	c float32
	s float32
}

var expRotationCoeffTable [len(expRotationSpreadFactors)][maxExpRotationLength + 1][MaxPVQK + 1]expRotationCoeff

func init() {
	for spreadIdx, spreadFactor := range expRotationSpreadFactors {
		for length := 1; length <= maxExpRotationLength; length++ {
			maxK := (length - 1) >> 1
			if maxK > MaxPVQK {
				maxK = MaxPVQK
			}
			for k := 0; k <= maxK; k++ {
				gain := float32(length) / float32(length+spreadFactor*k)
				theta := 0.5 * gain * gain
				expRotationCoeffTable[spreadIdx][length][k] = expRotationCoeff{
					c: float32(math.Cos(0.5 * math.Pi * float64(theta))),
					s: float32(math.Sin(0.5 * math.Pi * float64(theta))),
				}
			}
		}
	}
}

func expRotationCoefficients(length, k, spread int) (float64, float64, bool) {
	if spread < spreadLight || spread > spreadAggressive {
		return 0, 0, false
	}
	if length < 1 || length > maxExpRotationLength || k < 0 || k > MaxPVQK || 2*k >= length {
		return 0, 0, false
	}
	coeff := expRotationCoeffTable[spread-1][length][k]
	return float64(coeff.c), float64(coeff.s), true
}
