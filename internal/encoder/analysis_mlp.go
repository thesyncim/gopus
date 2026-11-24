package encoder

// WeightsScale is used to dequantize the 8-bit weights from libopus.
const WeightsScale = 1.0 / 128.0

// MaxNeurons is the maximum number of neurons in any layer.
const MaxNeurons = 32

// AnalysisDenseLayer represents a fully connected layer in the analysis MLP.
// It mirrors libopus DenseLayer (src/mlp.h); weights are stored as the original
// 8-bit quantized values. The float32 cache stores those values without scaling.
type AnalysisDenseLayer struct {
	// Bias holds one int8 bias per output neuron (libopus "bias").
	Bias []int8
	// InputWeights stores NbInputs rows of NbNeurons int8 weights (libopus
	// "input_weights"); the neuron index is contiguous within each input row.
	InputWeights []int8
	// NbInputs is the layer input dimension (libopus "nb_inputs").
	NbInputs int
	// NbNeurons is the layer output dimension (libopus "nb_neurons").
	NbNeurons int
	// Sigmoid selects the output activation: true for logistic sigmoid, false for
	// tansig (libopus "sigmoid").
	Sigmoid bool

	// inputWeightsF32 caches the unscaled InputWeights as float32 to avoid
	// repeated int8->float32 conversion. ComputeDense applies WeightsScale after
	// the bias and dot product have been accumulated.
	inputWeightsF32 []float32
}

// AnalysisGRULayer represents a Gated Recurrent Unit layer of the music/speech
// classifier. It mirrors libopus GRULayer (src/mlp.h) and feeds its hidden state
// back through TonalityAnalysisState.RNNState across frames.
type AnalysisGRULayer struct {
	// Bias holds the int8 biases for the GRU gates (libopus "bias").
	Bias []int8
	// InputWeights is the int8 input-to-gate weight matrix (libopus
	// "input_weights").
	InputWeights []int8
	// RecurrentWeights is the int8 state-to-gate weight matrix (libopus
	// "recurrent_weights").
	RecurrentWeights []int8
	// NbInputs is the input dimension (libopus "nb_inputs").
	NbInputs int
	// NbNeurons is the hidden-state dimension (libopus "nb_neurons").
	NbNeurons int

	// inputWeightsF32 and recurrentWeightsF32 cache the unscaled int8 values as
	// float32. ComputeGRU applies WeightsScale after each bias-plus-dot result.
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
	for j := range cols {
		xj := x[j]
		wOff := j * colStride
		w := weights[wOff : wOff+rows]
		for i := range rows {
			out[i] += analysisMLPGemmProduct(float32(w[i]), xj)
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

	if n == 24 && len(l.inputWeightsF32) == len(l.InputWeights) && len(l.recurrentWeightsF32) == len(l.RecurrentWeights) {
		for i := range n {
			z[i] = float32(l.Bias[i])
			r[i] = float32(l.Bias[n+i])
			h[i] = float32(l.Bias[2*n+i])
		}
		gemmAccumF32Rows24Triple(z[:n], r[:n], h[:n], l.inputWeightsF32, m, stride, input)
		gemmAccumF32Rows24Pair(z[:n], r[:n], l.recurrentWeightsF32, n, stride, state)
		for i := range n {
			z[i] = sigmoidApprox(WeightsScale * z[i])
			r[i] = sigmoidApprox(WeightsScale * r[i])
			tmp[i] = state[i] * r[i]
		}
		gemmAccumF32(h[:n], l.recurrentWeightsF32[2*n:], n, n, stride, tmp[:n])
		for i := range n {
			candidate := round32((1.0 - z[i]) * tansigApprox(WeightsScale*h[i]))
			state[i] = analysisMLPGRUStateUpdate(z[i], state[i], candidate)
		}
		return
	}

	// Compute update gate z
	for i := range n {
		z[i] = float32(l.Bias[i])
	}
	if len(l.inputWeightsF32) == len(l.InputWeights) && len(l.recurrentWeightsF32) == len(l.RecurrentWeights) {
		gemmAccumF32(z[:n], l.inputWeightsF32, n, m, stride, input)
		gemmAccumF32(z[:n], l.recurrentWeightsF32, n, n, stride, state)
	} else {
		gemmAccum(z[:n], l.InputWeights, n, m, stride, input)
		gemmAccum(z[:n], l.RecurrentWeights, n, n, stride, state)
	}
	for i := range n {
		z[i] = sigmoidApprox(WeightsScale * z[i])
	}

	// Compute reset gate r
	for i := range n {
		r[i] = float32(l.Bias[n+i])
	}
	if len(l.inputWeightsF32) == len(l.InputWeights) && len(l.recurrentWeightsF32) == len(l.RecurrentWeights) {
		gemmAccumF32(r[:n], l.inputWeightsF32[n:], n, m, stride, input)
		gemmAccumF32(r[:n], l.recurrentWeightsF32[n:], n, n, stride, state)
	} else {
		gemmAccum(r[:n], l.InputWeights[n:], n, m, stride, input)
		gemmAccum(r[:n], l.RecurrentWeights[n:], n, n, stride, state)
	}
	for i := range n {
		r[i] = sigmoidApprox(WeightsScale * r[i])
	}

	// Compute output h
	for i := range n {
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
	for i := range n {
		candidate := round32((1.0 - z[i]) * tansigApprox(WeightsScale*h[i]))
		state[i] = analysisMLPGRUStateUpdate(z[i], state[i], candidate)
	}
}
