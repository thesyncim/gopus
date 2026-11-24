//go:build !((arm64 || amd64) && goexperiment.simd && !nosimd && !purego)

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

func toneSumStereo(dst, srcL, srcR []float32) {
	n := len(dst)
	dst = dst[:n:n]
	srcL = srcL[:n:n]
	srcR = srcR[:n:n]
	for k := range dst {
		dst[k] = srcL[k] + srcR[k]
	}
}

func transientStereoForwardBackward(srcL, srcR, energyL, energyR []float32, forwardDecay, forwardRetain float32) (meanL, meanR, maxEL, maxER float32) {
	const (
		hpFeedback     = float32(0.5)
		backwardRetain = float32(0.875)
		backwardScale  = float32(0.125)
		warmupPairs    = 6
	)
	len2 := len(energyL)
	if len2 == 0 {
		return 0, 0, 0, 0
	}
	srcL = srcL[: 2*len2 : 2*len2]
	srcR = srcR[: 2*len2 : 2*len2]
	energyL = energyL[:len2:len2]
	energyR = energyR[:len2:len2]

	var hp0L, hp1L float32
	var hp0R, hp1R float32
	var maskL, maskR float32

	warm := min(warmupPairs, len2)
	for i := range warm {
		xL0, xL1 := srcL[2*i], srcL[2*i+1]
		xR0, xR1 := srcR[2*i], srcR[2*i+1]
		hp00L := hp0L
		hp00R := hp0R
		hp0L = hp0L - xL0 + hpFeedback*hp1L
		hp0R = hp0R - xR0 + hpFeedback*hp1R
		hp1L = xL0 - hp00L
		hp1R = xR0 - hp00R
		hp00L = hp0L
		hp00R = hp0R
		hp0L = hp0L - xL1 + hpFeedback*hp1L
		hp0R = hp0R - xR1 + hpFeedback*hp1R
		hp1L = xL1 - hp00L
		hp1R = xR1 - hp00R
		energyL[i] = 0
		energyR[i] = 0
	}
	for i := warm; i < len2; i++ {
		idx := 2 * i
		xL0, xL1 := srcL[idx], srcL[idx+1]
		xR0, xR1 := srcR[idx], srcR[idx+1]
		yL0 := hp0L + xL0
		yR0 := hp0R + xR0
		hp00L := hp0L
		hp00R := hp0R
		hp0L = hp0L - xL0 + hpFeedback*hp1L
		hp0R = hp0R - xR0 + hpFeedback*hp1R
		hp1L = xL0 - hp00L
		hp1R = xR0 - hp00R

		yL1 := hp0L + xL1
		yR1 := hp0R + xR1
		hp00L = hp0L
		hp00R = hp0R
		hp0L = hp0L - xL1 + hpFeedback*hp1L
		hp0R = hp0R - xR1 + hpFeedback*hp1R
		hp1L = xL1 - hp00L
		hp1R = xR1 - hp00R

		pairL := yL0*yL0 + yL1*yL1
		pairR := yR0*yR0 + yR1*yR1
		meanL += pairL
		meanR += pairR
		maskL = pairL + forwardRetain*maskL
		maskR = pairR + forwardRetain*maskR
		energyL[i] = forwardDecay * maskL
		energyR[i] = forwardDecay * maskR
	}

	maskL = 0
	maskR = 0
	for i := len2 - 1; i >= 0; i-- {
		maskL = energyL[i] + backwardRetain*maskL
		maskR = energyR[i] + backwardRetain*maskR
		eiL := backwardScale * maskL
		eiR := backwardScale * maskR
		energyL[i] = eiL
		energyR[i] = eiR
		maxEL = opusmath.MaxF32(maxEL, eiL)
		maxER = opusmath.MaxF32(maxER, eiR)
	}
	return meanL, meanR, maxEL, maxER
}
