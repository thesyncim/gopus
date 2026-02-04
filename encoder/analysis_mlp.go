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
}

// AnalysisGRULayer represents a Gated Recurrent Unit layer.
type AnalysisGRULayer struct {
	Bias             []int8
	InputWeights     []int8
	RecurrentWeights []int8
	NbInputs         int
	NbNeurons        int
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
	num := ((N2*x2 + N1) * x2 + N0) * x
	den := (D2*x2 + D1)*x2 + D0
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
func gemmAccum(out []float32, weights []int8, rows, cols, colStride int, x []float32) {
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			out[i] += float32(weights[j*colStride+i]) * x[j]
		}
	}
}

// ComputeDense computes the output of a dense layer.
func (l *AnalysisDenseLayer) ComputeDense(output []float32, input []float32) {
	stride := l.NbNeurons
	for i := 0; i < l.NbNeurons; i++ {
		output[i] = float32(l.Bias[i])
	}
	gemmAccum(output, l.InputWeights, l.NbNeurons, l.NbInputs, stride, input)
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
	gemmAccum(z[:n], l.InputWeights, n, m, stride, input)
	gemmAccum(z[:n], l.RecurrentWeights, n, n, stride, state)
	for i := 0; i < n; i++ {
		z[i] = sigmoidApprox(WeightsScale * z[i])
	}

	// Compute reset gate r
	for i := 0; i < n; i++ {
		r[i] = float32(l.Bias[n+i])
	}
	gemmAccum(r[:n], l.InputWeights[n:], n, m, stride, input)
	gemmAccum(r[:n], l.RecurrentWeights[n:], n, n, stride, state)
	for i := 0; i < n; i++ {
		r[i] = sigmoidApprox(WeightsScale * r[i])
	}

	// Compute output h
	for i := 0; i < n; i++ {
		h[i] = float32(l.Bias[2*n+i])
		tmp[i] = state[i] * r[i]
	}
	gemmAccum(h[:n], l.InputWeights[2*n:], n, m, stride, input)
	gemmAccum(h[:n], l.RecurrentWeights[2*n:], n, n, stride, tmp[:n])
	for i := 0; i < n; i++ {
		state[i] = z[i]*state[i] + (1.0-z[i])*tansigApprox(WeightsScale*h[i])
	}
}
