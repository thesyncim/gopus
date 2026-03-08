package encoder

// WeightsScale is used to dequantize the 8-bit weights from libopus.
const WeightsScale = 1.0 / 128.0

// MaxNeurons is the maximum number of neurons in any layer.
const MaxNeurons = 32

// AnalysisDenseLayer represents a fully connected layer in the analysis MLP.
type AnalysisDenseLayer struct {
	Bias         []int8
	InputWeights []int8
	NbInputs     int
	NbNeurons    int
	Sigmoid      bool

	inputWeightsF32 []float32
}

// AnalysisGRULayer represents a Gated Recurrent Unit layer.
type AnalysisGRULayer struct {
	Bias             []int8
	InputWeights     []int8
	RecurrentWeights []int8
	NbInputs         int
	NbNeurons        int

	inputWeightsF32     []float32
	recurrentWeightsF32 []float32
}

// tansigApprox is a fast rational approximation of the hyperbolic tangent function.
// Matches libopus tansig_approx.
func tansigApprox(x float32) float32 {
	const (
		N0 = 952.52801514
		N1 = 96.39235687
		N2 = 0.60863042
		D0 = 952.72399902
		D1 = 413.36801147
		D2 = 11.88600922
	)
	x2 := x * x
	num := ((N2*x2+N1)*x2 + N0) * x
	den := (D2*x2+D1)*x2 + D0
	res := num / den
	if res < -1.0 {
		return -1.0
	}
	if res > 1.0 {
		return 1.0
	}
	return res
}

// sigmoidApprox is a fast approximation of the sigmoid function.
// Matches libopus sigmoid_approx.
func sigmoidApprox(x float32) float32 {
	return 0.5 + 0.5*tansigApprox(0.5*x)
}

// gemmAccum performs matrix-vector multiplication and accumulation.
// out[i] += weights[j*col_stride + i] * x[j]
// Loop order: j-outer for sequential weights access (stride-1) and x[j] reuse.
func gemmAccum(out []float32, weights []int8, rows, cols, colStride int, x []float32) {
	if rows <= 0 || cols <= 0 {
		return
	}
	_ = out[rows-1] // BCE hint
	_ = x[cols-1]   // BCE hint
	for j := 0; j < cols; j++ {
		xj := x[j]
		wOff := j * colStride
		w := weights[wOff : wOff+rows]
		for i := 0; i < rows; i++ {
			out[i] += float32(w[i]) * xj
		}
	}
}

// gemmAccumF32 performs matrix-vector multiplication with preconverted weights.
func gemmAccumF32(out []float32, weights []float32, rows, cols, colStride int, x []float32) {
	if rows <= 0 || cols <= 0 {
		return
	}
	_ = out[rows-1]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+rows-1]
	switch rows {
	case 2:
		o0 := out[0]
		o1 := out[1]
		for j := 0; j < cols; j++ {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += w[0] * xj
			o1 += w[1] * xj
		}
		out[0] = o0
		out[1] = o1
		return
	case 24:
		o0, o1, o2, o3 := out[0], out[1], out[2], out[3]
		o4, o5, o6, o7 := out[4], out[5], out[6], out[7]
		o8, o9, o10, o11 := out[8], out[9], out[10], out[11]
		o12, o13, o14, o15 := out[12], out[13], out[14], out[15]
		o16, o17, o18, o19 := out[16], out[17], out[18], out[19]
		o20, o21, o22, o23 := out[20], out[21], out[22], out[23]
		for j := 0; j < cols; j++ {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += w[0] * xj
			o1 += w[1] * xj
			o2 += w[2] * xj
			o3 += w[3] * xj
			o4 += w[4] * xj
			o5 += w[5] * xj
			o6 += w[6] * xj
			o7 += w[7] * xj
			o8 += w[8] * xj
			o9 += w[9] * xj
			o10 += w[10] * xj
			o11 += w[11] * xj
			o12 += w[12] * xj
			o13 += w[13] * xj
			o14 += w[14] * xj
			o15 += w[15] * xj
			o16 += w[16] * xj
			o17 += w[17] * xj
			o18 += w[18] * xj
			o19 += w[19] * xj
			o20 += w[20] * xj
			o21 += w[21] * xj
			o22 += w[22] * xj
			o23 += w[23] * xj
		}
		out[0], out[1], out[2], out[3] = o0, o1, o2, o3
		out[4], out[5], out[6], out[7] = o4, o5, o6, o7
		out[8], out[9], out[10], out[11] = o8, o9, o10, o11
		out[12], out[13], out[14], out[15] = o12, o13, o14, o15
		out[16], out[17], out[18], out[19] = o16, o17, o18, o19
		out[20], out[21], out[22], out[23] = o20, o21, o22, o23
		return
	case 32:
		o0, o1, o2, o3 := out[0], out[1], out[2], out[3]
		o4, o5, o6, o7 := out[4], out[5], out[6], out[7]
		o8, o9, o10, o11 := out[8], out[9], out[10], out[11]
		o12, o13, o14, o15 := out[12], out[13], out[14], out[15]
		o16, o17, o18, o19 := out[16], out[17], out[18], out[19]
		o20, o21, o22, o23 := out[20], out[21], out[22], out[23]
		o24, o25, o26, o27 := out[24], out[25], out[26], out[27]
		o28, o29, o30, o31 := out[28], out[29], out[30], out[31]
		for j := 0; j < cols; j++ {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += w[0] * xj
			o1 += w[1] * xj
			o2 += w[2] * xj
			o3 += w[3] * xj
			o4 += w[4] * xj
			o5 += w[5] * xj
			o6 += w[6] * xj
			o7 += w[7] * xj
			o8 += w[8] * xj
			o9 += w[9] * xj
			o10 += w[10] * xj
			o11 += w[11] * xj
			o12 += w[12] * xj
			o13 += w[13] * xj
			o14 += w[14] * xj
			o15 += w[15] * xj
			o16 += w[16] * xj
			o17 += w[17] * xj
			o18 += w[18] * xj
			o19 += w[19] * xj
			o20 += w[20] * xj
			o21 += w[21] * xj
			o22 += w[22] * xj
			o23 += w[23] * xj
			o24 += w[24] * xj
			o25 += w[25] * xj
			o26 += w[26] * xj
			o27 += w[27] * xj
			o28 += w[28] * xj
			o29 += w[29] * xj
			o30 += w[30] * xj
			o31 += w[31] * xj
		}
		out[0], out[1], out[2], out[3] = o0, o1, o2, o3
		out[4], out[5], out[6], out[7] = o4, o5, o6, o7
		out[8], out[9], out[10], out[11] = o8, o9, o10, o11
		out[12], out[13], out[14], out[15] = o12, o13, o14, o15
		out[16], out[17], out[18], out[19] = o16, o17, o18, o19
		out[20], out[21], out[22], out[23] = o20, o21, o22, o23
		out[24], out[25], out[26], out[27] = o24, o25, o26, o27
		out[28], out[29], out[30], out[31] = o28, o29, o30, o31
		return
	}
	for j := 0; j < cols; j++ {
		xj := x[j]
		wOff := j * colStride
		w := weights[wOff : wOff+rows]
		i := 0
		for ; i+3 < rows; i += 4 {
			out[i] += w[i] * xj
			out[i+1] += w[i+1] * xj
			out[i+2] += w[i+2] * xj
			out[i+3] += w[i+3] * xj
		}
		for ; i < rows; i++ {
			out[i] += w[i] * xj
		}
	}
}

func weightsInt8ToFloat32(src []int8) []float32 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]float32, len(src))
	for i, v := range src {
		dst[i] = float32(v)
	}
	return dst
}

func initAnalysisMLPWeightCaches() {
	layer0.inputWeightsF32 = weightsInt8ToFloat32(layer0.InputWeights)
	layer1.inputWeightsF32 = weightsInt8ToFloat32(layer1.InputWeights)
	layer1.recurrentWeightsF32 = weightsInt8ToFloat32(layer1.RecurrentWeights)
	layer2.inputWeightsF32 = weightsInt8ToFloat32(layer2.InputWeights)
}

func init() {
	initAnalysisMLPWeightCaches()
}

// ComputeDense computes the output of a dense layer.
func (l *AnalysisDenseLayer) ComputeDense(output []float32, input []float32) {
	stride := l.NbNeurons
	for i := 0; i < l.NbNeurons; i++ {
		output[i] = float32(l.Bias[i])
	}
	if len(l.inputWeightsF32) == len(l.InputWeights) {
		gemmAccumF32(output, l.inputWeightsF32, l.NbNeurons, l.NbInputs, stride, input)
	} else {
		gemmAccum(output, l.InputWeights, l.NbNeurons, l.NbInputs, stride, input)
	}
	for i := 0; i < l.NbNeurons; i++ {
		output[i] *= WeightsScale
		if l.Sigmoid {
			output[i] = sigmoidApprox(output[i])
		} else {
			output[i] = tansigApprox(output[i])
		}
	}
}

// ComputeGRU computes the state update of a GRU layer.
func (l *AnalysisGRULayer) ComputeGRU(state []float32, input []float32) {
	var z [MaxNeurons]float32
	var r [MaxNeurons]float32
	var h [MaxNeurons]float32
	var tmp [MaxNeurons]float32

	n := l.NbNeurons
	m := l.NbInputs
	stride := 3 * n

	// Compute update gate z
	for i := 0; i < n; i++ {
		z[i] = float32(l.Bias[i])
	}
	if len(l.inputWeightsF32) == len(l.InputWeights) && len(l.recurrentWeightsF32) == len(l.RecurrentWeights) {
		gemmAccumF32(z[:n], l.inputWeightsF32, n, m, stride, input)
		gemmAccumF32(z[:n], l.recurrentWeightsF32, n, n, stride, state)
	} else {
		gemmAccum(z[:n], l.InputWeights, n, m, stride, input)
		gemmAccum(z[:n], l.RecurrentWeights, n, n, stride, state)
	}
	for i := 0; i < n; i++ {
		z[i] = sigmoidApprox(WeightsScale * z[i])
	}

	// Compute reset gate r
	for i := 0; i < n; i++ {
		r[i] = float32(l.Bias[n+i])
	}
	if len(l.inputWeightsF32) == len(l.InputWeights) && len(l.recurrentWeightsF32) == len(l.RecurrentWeights) {
		gemmAccumF32(r[:n], l.inputWeightsF32[n:], n, m, stride, input)
		gemmAccumF32(r[:n], l.recurrentWeightsF32[n:], n, n, stride, state)
	} else {
		gemmAccum(r[:n], l.InputWeights[n:], n, m, stride, input)
		gemmAccum(r[:n], l.RecurrentWeights[n:], n, n, stride, state)
	}
	for i := 0; i < n; i++ {
		r[i] = sigmoidApprox(WeightsScale * r[i])
	}

	// Compute output h
	for i := 0; i < n; i++ {
		h[i] = float32(l.Bias[2*n+i])
		tmp[i] = state[i] * r[i]
	}
	if len(l.inputWeightsF32) == len(l.InputWeights) && len(l.recurrentWeightsF32) == len(l.RecurrentWeights) {
		gemmAccumF32(h[:n], l.inputWeightsF32[2*n:], n, m, stride, input)
		gemmAccumF32(h[:n], l.recurrentWeightsF32[2*n:], n, n, stride, tmp[:n])
	} else {
		gemmAccum(h[:n], l.InputWeights[2*n:], n, m, stride, input)
		gemmAccum(h[:n], l.RecurrentWeights[2*n:], n, n, stride, tmp[:n])
	}
	for i := 0; i < n; i++ {
		state[i] = z[i]*state[i] + (1.0-z[i])*tansigApprox(WeightsScale*h[i])
	}
}
