package lpcnetplc

import (
	"math"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

const (
	FARGANSubframeSize       = 40
	FARGANFrameSize          = FARGANNBSubframes * FARGANSubframeSize
	FARGANDeemphasis         = 0.85
	SigNetInputSize          = FARGANCondSize + 2*FARGANSubframeSize + 4
	SigNetFWC0ConvInputs     = 2 * SigNetInputSize
	SigNetFWC0MemSize        = SigNetFWC0ConvInputs - SigNetInputSize
	SigNetFWC0StateSize      = SigNetFWC0ConvInputs
	SigNetFWC0ConvOutSize    = 192
	SigNetFWC0GLUGateOutSize = 192
	SigNetGRU1OutSize        = 160
	SigNetGRU1StateSize      = 160
	SigNetGRU2OutSize        = 128
	SigNetGRU2StateSize      = 128
	SigNetGRU3OutSize        = 128
	SigNetGRU3StateSize      = 128
	SigNetGRU1InputSize      = SigNetFWC0GLUGateOutSize + 2*FARGANSubframeSize
	SigNetGRU2InputSize      = SigNetGRU1OutSize + 2*FARGANSubframeSize
	SigNetGRU3InputSize      = SigNetGRU2OutSize + 2*FARGANSubframeSize
	SigNetSkipDenseInputSize = SigNetGRU1OutSize + SigNetGRU2OutSize + SigNetGRU3OutSize + SigNetFWC0ConvOutSize + 2*FARGANSubframeSize
	SigNetSkipDenseOutSize   = 128
	SigNetSigDenseOutSize    = FARGANSubframeSize
	SigNetPitchGateSize      = 4
	farganMaxLinearInputs    = SigNetSkipDenseInputSize
	farganMaxRNNNeurons      = SigNetGRU1StateSize
	farganMaxActivation      = SigNetFWC0ConvOutSize
)

type FARGANModel struct {
	PEmbed        LinearLayer
	Dense1        LinearLayer
	Conv1         LinearLayer
	Dense2        LinearLayer
	CondGainDense LinearLayer
	FWC0Conv      LinearLayer
	FWC0GLUGate   LinearLayer
	GRU1Input     LinearLayer
	GRU1Recurrent LinearLayer
	GRU2Input     LinearLayer
	GRU2Recurrent LinearLayer
	GRU3Input     LinearLayer
	GRU3Recurrent LinearLayer
	GRU1GLUGate   LinearLayer
	GRU2GLUGate   LinearLayer
	GRU3GLUGate   LinearLayer
	SkipGLUGate   LinearLayer
	SkipDense     LinearLayer
	SigDenseOut   LinearLayer
	GainDenseOut  LinearLayer
}

type FARGANState struct {
	contInitialized bool
	deemphMem       float32
	pitchBuf        [PitchMaxPeriod]float32
	condConv1State  [FARGANCondConv1State]float32
	fwc0Mem         [SigNetFWC0StateSize]float32
	gru1State       [SigNetGRU1StateSize]float32
	gru2State       [SigNetGRU2StateSize]float32
	gru3State       [SigNetGRU3StateSize]float32
	lastPeriod      int
}

type farganScratch struct {
	conditioner farganConditionerScratch
	cond        [FARGANCondDense2Size]float32
	x0          [FARGANContSamples]float32
	dummy       [FARGANSubframeSize]float32
	gain        [1]float32
	fwc0In      [SigNetInputSize]float32
	gru1In      [SigNetGRU1InputSize]float32
	gru2In      [SigNetGRU2InputSize]float32
	gru3In      [SigNetGRU3InputSize]float32
	pred        [FARGANSubframeSize + 4]float32
	prev        [FARGANSubframeSize]float32
	pitchGate   [SigNetPitchGateSize]float32
	skipCat     [SigNetSkipDenseInputSize]float32
	skipOut     [SigNetSkipDenseOutSize]float32
	convTemp    [SigNetFWC0ConvInputs]float32
	zrh         [3 * farganMaxRNNNeurons]float32
	recur       [3 * farganMaxRNNNeurons]float32
	act         [farganMaxActivation]float32
	quant       [farganMaxLinearInputs]int16
}

type FARGAN struct {
	model   *FARGANModel
	state   FARGANState
	scratch farganScratch
}

var farganSignalLayerSpecs = []LinearLayerSpec{
	{
		Name:         "sig_net_cond_gain_dense",
		Bias:         "sig_net_cond_gain_dense_bias",
		FloatWeights: "sig_net_cond_gain_dense_weights_float",
		NbInputs:     FARGANCondSize,
		NbOutputs:    1,
	},
	{
		Name:         "sig_net_fwc0_conv",
		Bias:         "sig_net_fwc0_conv_bias",
		Subias:       "sig_net_fwc0_conv_subias",
		Weights:      "sig_net_fwc0_conv_weights_int8",
		FloatWeights: "sig_net_fwc0_conv_weights_float",
		Scale:        "sig_net_fwc0_conv_scale",
		NbInputs:     SigNetFWC0ConvInputs,
		NbOutputs:    SigNetFWC0ConvOutSize,
	},
	{
		Name:         "sig_net_fwc0_glu_gate",
		Bias:         "sig_net_fwc0_glu_gate_bias",
		Subias:       "sig_net_fwc0_glu_gate_subias",
		Weights:      "sig_net_fwc0_glu_gate_weights_int8",
		FloatWeights: "sig_net_fwc0_glu_gate_weights_float",
		Scale:        "sig_net_fwc0_glu_gate_scale",
		NbInputs:     SigNetFWC0ConvOutSize,
		NbOutputs:    SigNetFWC0GLUGateOutSize,
	},
	{
		Name:         "sig_net_gru1_input",
		Subias:       "sig_net_gru1_input_subias",
		Weights:      "sig_net_gru1_input_weights_int8",
		FloatWeights: "sig_net_gru1_input_weights_float",
		Scale:        "sig_net_gru1_input_scale",
		NbInputs:     SigNetGRU1InputSize,
		NbOutputs:    3 * SigNetGRU1OutSize,
	},
	{
		Name:         "sig_net_gru1_recurrent",
		Subias:       "sig_net_gru1_recurrent_subias",
		Weights:      "sig_net_gru1_recurrent_weights_int8",
		FloatWeights: "sig_net_gru1_recurrent_weights_float",
		Scale:        "sig_net_gru1_recurrent_scale",
		NbInputs:     SigNetGRU1StateSize,
		NbOutputs:    3 * SigNetGRU1OutSize,
	},
	{
		Name:         "sig_net_gru2_input",
		Subias:       "sig_net_gru2_input_subias",
		Weights:      "sig_net_gru2_input_weights_int8",
		FloatWeights: "sig_net_gru2_input_weights_float",
		Scale:        "sig_net_gru2_input_scale",
		NbInputs:     SigNetGRU2InputSize,
		NbOutputs:    3 * SigNetGRU2OutSize,
	},
	{
		Name:         "sig_net_gru2_recurrent",
		Subias:       "sig_net_gru2_recurrent_subias",
		Weights:      "sig_net_gru2_recurrent_weights_int8",
		FloatWeights: "sig_net_gru2_recurrent_weights_float",
		Scale:        "sig_net_gru2_recurrent_scale",
		NbInputs:     SigNetGRU2StateSize,
		NbOutputs:    3 * SigNetGRU2OutSize,
	},
	{
		Name:         "sig_net_gru3_input",
		Subias:       "sig_net_gru3_input_subias",
		Weights:      "sig_net_gru3_input_weights_int8",
		FloatWeights: "sig_net_gru3_input_weights_float",
		Scale:        "sig_net_gru3_input_scale",
		NbInputs:     SigNetGRU3InputSize,
		NbOutputs:    3 * SigNetGRU3OutSize,
	},
	{
		Name:         "sig_net_gru3_recurrent",
		Subias:       "sig_net_gru3_recurrent_subias",
		Weights:      "sig_net_gru3_recurrent_weights_int8",
		FloatWeights: "sig_net_gru3_recurrent_weights_float",
		Scale:        "sig_net_gru3_recurrent_scale",
		NbInputs:     SigNetGRU3StateSize,
		NbOutputs:    3 * SigNetGRU3OutSize,
	},
	{
		Name:         "sig_net_gru1_glu_gate",
		Bias:         "sig_net_gru1_glu_gate_bias",
		Subias:       "sig_net_gru1_glu_gate_subias",
		Weights:      "sig_net_gru1_glu_gate_weights_int8",
		FloatWeights: "sig_net_gru1_glu_gate_weights_float",
		Scale:        "sig_net_gru1_glu_gate_scale",
		NbInputs:     SigNetGRU1OutSize,
		NbOutputs:    SigNetGRU1OutSize,
	},
	{
		Name:         "sig_net_gru2_glu_gate",
		Bias:         "sig_net_gru2_glu_gate_bias",
		Subias:       "sig_net_gru2_glu_gate_subias",
		Weights:      "sig_net_gru2_glu_gate_weights_int8",
		FloatWeights: "sig_net_gru2_glu_gate_weights_float",
		Scale:        "sig_net_gru2_glu_gate_scale",
		NbInputs:     SigNetGRU2OutSize,
		NbOutputs:    SigNetGRU2OutSize,
	},
	{
		Name:         "sig_net_gru3_glu_gate",
		Bias:         "sig_net_gru3_glu_gate_bias",
		Subias:       "sig_net_gru3_glu_gate_subias",
		Weights:      "sig_net_gru3_glu_gate_weights_int8",
		FloatWeights: "sig_net_gru3_glu_gate_weights_float",
		Scale:        "sig_net_gru3_glu_gate_scale",
		NbInputs:     SigNetGRU3OutSize,
		NbOutputs:    SigNetGRU3OutSize,
	},
	{
		Name:         "sig_net_skip_glu_gate",
		Bias:         "sig_net_skip_glu_gate_bias",
		Subias:       "sig_net_skip_glu_gate_subias",
		Weights:      "sig_net_skip_glu_gate_weights_int8",
		FloatWeights: "sig_net_skip_glu_gate_weights_float",
		Scale:        "sig_net_skip_glu_gate_scale",
		NbInputs:     SigNetSkipDenseOutSize,
		NbOutputs:    SigNetSkipDenseOutSize,
	},
	{
		Name:         "sig_net_skip_dense",
		Bias:         "sig_net_skip_dense_bias",
		Subias:       "sig_net_skip_dense_subias",
		Weights:      "sig_net_skip_dense_weights_int8",
		FloatWeights: "sig_net_skip_dense_weights_float",
		Scale:        "sig_net_skip_dense_scale",
		NbInputs:     SigNetSkipDenseInputSize,
		NbOutputs:    SigNetSkipDenseOutSize,
	},
	{
		Name:         "sig_net_sig_dense_out",
		Bias:         "sig_net_sig_dense_out_bias",
		Subias:       "sig_net_sig_dense_out_subias",
		Weights:      "sig_net_sig_dense_out_weights_int8",
		FloatWeights: "sig_net_sig_dense_out_weights_float",
		Scale:        "sig_net_sig_dense_out_scale",
		NbInputs:     SigNetSkipDenseOutSize,
		NbOutputs:    SigNetSigDenseOutSize,
	},
	{
		Name:         "sig_net_gain_dense_out",
		Bias:         "sig_net_gain_dense_out_bias",
		FloatWeights: "sig_net_gain_dense_out_weights_float",
		NbInputs:     SigNetFWC0ConvOutSize,
		NbOutputs:    SigNetPitchGateSize,
	},
}

var farganModelLayerSpecs = func() []LinearLayerSpec {
	specs := make([]LinearLayerSpec, 0, len(farganConditionerLayerSpecs)+len(farganSignalLayerSpecs))
	specs = append(specs, farganConditionerLayerSpecs...)
	specs = append(specs, farganSignalLayerSpecs...)
	return specs
}()

func FARGANModelLayerSpecs() []LinearLayerSpec {
	return farganModelLayerSpecs
}

func LoadFARGANModel(blob *dnnblob.Blob) (*FARGANModel, error) {
	if blob == nil {
		return nil, errInvalidFARGANModel
	}
	var model FARGANModel
	for _, spec := range farganModelLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidFARGANModel
		}
		switch spec.Name {
		case "cond_net_pembed":
			model.PEmbed = layer
		case "cond_net_fdense1":
			model.Dense1 = layer
		case "cond_net_fconv1":
			model.Conv1 = layer
		case "cond_net_fdense2":
			model.Dense2 = layer
		case "sig_net_cond_gain_dense":
			model.CondGainDense = layer
		case "sig_net_fwc0_conv":
			model.FWC0Conv = layer
		case "sig_net_fwc0_glu_gate":
			model.FWC0GLUGate = layer
		case "sig_net_gru1_input":
			model.GRU1Input = layer
		case "sig_net_gru1_recurrent":
			model.GRU1Recurrent = layer
		case "sig_net_gru2_input":
			model.GRU2Input = layer
		case "sig_net_gru2_recurrent":
			model.GRU2Recurrent = layer
		case "sig_net_gru3_input":
			model.GRU3Input = layer
		case "sig_net_gru3_recurrent":
			model.GRU3Recurrent = layer
		case "sig_net_gru1_glu_gate":
			model.GRU1GLUGate = layer
		case "sig_net_gru2_glu_gate":
			model.GRU2GLUGate = layer
		case "sig_net_gru3_glu_gate":
			model.GRU3GLUGate = layer
		case "sig_net_skip_glu_gate":
			model.SkipGLUGate = layer
		case "sig_net_skip_dense":
			model.SkipDense = layer
		case "sig_net_sig_dense_out":
			model.SigDenseOut = layer
		case "sig_net_gain_dense_out":
			model.GainDenseOut = layer
		}
	}
	return &model, nil
}

func (f *FARGAN) SetModel(blob *dnnblob.Blob) error {
	model, err := LoadFARGANModel(blob)
	if err != nil {
		f.model = nil
		f.Reset()
		return err
	}
	f.model = model
	f.Reset()
	return nil
}

func (f *FARGAN) Loaded() bool {
	return f != nil && f.model != nil
}

func (f *FARGAN) Reset() {
	if f == nil {
		return
	}
	f.state = FARGANState{}
}

func (f *FARGAN) PrimeContinuity(pcm0, features0 []float32) int {
	if f == nil || f.model == nil || len(pcm0) < FARGANContSamples || len(features0) < ContVectors*NumFeatures {
		return 0
	}
	period := 0
	for i := 0; i < ContVectors; i++ {
		features := features0[i*NumFeatures:]
		f.state.lastPeriod = period
		period = PeriodFromFeatures(features)
		f.computeConditioning(f.scratch.cond[:], features[:NumFeatures], period)
	}

	f.scratch.x0[0] = 0
	for i := 1; i < FARGANContSamples; i++ {
		f.scratch.x0[i] = pcm0[i] - float32(FARGANDeemphasis)*pcm0[i-1]
	}

	copy(f.state.pitchBuf[PitchMaxPeriod-FARGANFrameSize:], f.scratch.x0[:FARGANFrameSize])
	f.state.contInitialized = true
	for i := 0; i < FARGANNBSubframes; i++ {
		cond := f.scratch.cond[i*FARGANCondSize : (i+1)*FARGANCondSize]
		f.runSubframe(f.scratch.dummy[:], cond, f.state.lastPeriod)
		copy(f.state.pitchBuf[PitchMaxPeriod-FARGANSubframeSize:], f.scratch.x0[FARGANFrameSize+i*FARGANSubframeSize:FARGANFrameSize+(i+1)*FARGANSubframeSize])
	}
	f.state.deemphMem = pcm0[FARGANContSamples-1]
	return FARGANContSamples
}

func (f *FARGAN) Synthesize(pcm, features []float32) int {
	if f == nil || f.model == nil || !f.state.contInitialized || len(pcm) < FARGANFrameSize || len(features) < NumFeatures {
		return 0
	}
	period := PeriodFromFeatures(features)
	f.computeConditioning(f.scratch.cond[:], features[:NumFeatures], period)
	for i := 0; i < FARGANNBSubframes; i++ {
		subPCM := pcm[i*FARGANSubframeSize : (i+1)*FARGANSubframeSize]
		subCond := f.scratch.cond[i*FARGANCondSize : (i+1)*FARGANCondSize]
		f.runSubframe(subPCM, subCond, f.state.lastPeriod)
	}
	f.state.lastPeriod = period
	return FARGANFrameSize
}

func (f *FARGAN) computeConditioning(out, features []float32, period int) {
	slot := clampInt(period-PitchMinPeriod, 0, FARGANPEmbedInputs-1)
	copy(f.scratch.conditioner.denseIn[:NumFeatures], features[:NumFeatures])
	for i := 0; i < FARGANPEmbedOutSize; i++ {
		f.scratch.conditioner.denseIn[NumFeatures+i] = f.model.PEmbed.FloatWeights.At(slot*FARGANPEmbedOutSize + i)
	}
	computeFARGANDense(&f.model.Dense1, f.scratch.conditioner.convIn[:], f.scratch.conditioner.denseIn[:], activationTanh, &f.scratch.conditioner)
	computeFARGANConv1D(&f.model.Conv1, f.scratch.conditioner.fdense2[:], f.state.condConv1State[:], f.scratch.conditioner.convIn[:], FARGANCondConv1InSize, activationTanh, &f.scratch.conditioner)
	computeFARGANDense(&f.model.Dense2, out[:FARGANCondDense2Size], f.scratch.conditioner.fdense2[:], activationTanh, &f.scratch.conditioner)
}

func (f *FARGAN) runSubframe(pcm, cond []float32, period int) {
	computeFARGANSignalDense(&f.model.CondGainDense, f.scratch.gain[:], cond[:FARGANCondSize], activationLinear, &f.scratch)
	gain := float32(math.Exp(float64(f.scratch.gain[0])))
	gainInv := 1 / (1e-5 + gain)

	pos := PitchMaxPeriod - period - 2
	for i := 0; i < len(f.scratch.pred); i++ {
		f.scratch.pred[i] = clampFARGANSample(gainInv * f.state.pitchBuf[max(0, pos)])
		pos++
		if pos == PitchMaxPeriod {
			pos -= period
		}
	}
	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.prev[i] = clampFARGANSample(gainInv * f.state.pitchBuf[PitchMaxPeriod-FARGANSubframeSize+i])
	}

	copy(f.scratch.fwc0In[:FARGANCondSize], cond[:FARGANCondSize])
	copy(f.scratch.fwc0In[FARGANCondSize:FARGANCondSize+len(f.scratch.pred)], f.scratch.pred[:])
	copy(f.scratch.fwc0In[FARGANCondSize+len(f.scratch.pred):], f.scratch.prev[:])

	computeFARGANSignalConv1D(&f.model.FWC0Conv, f.scratch.gru1In[:SigNetFWC0ConvOutSize], f.state.fwc0Mem[:SigNetFWC0MemSize], f.scratch.fwc0In[:], SigNetInputSize, activationTanh, &f.scratch)
	computeFARGANGLU(&f.model.FWC0GLUGate, f.scratch.gru1In[:SigNetFWC0ConvOutSize], f.scratch.gru1In[:SigNetFWC0ConvOutSize], &f.scratch)
	computeFARGANSignalDense(&f.model.GainDenseOut, f.scratch.pitchGate[:], f.scratch.gru1In[:SigNetFWC0ConvOutSize], activationSigmoid, &f.scratch)

	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.gru1In[SigNetFWC0GLUGateOutSize+i] = f.scratch.pitchGate[0] * f.scratch.pred[i+2]
	}
	copy(f.scratch.gru1In[SigNetFWC0GLUGateOutSize+FARGANSubframeSize:], f.scratch.prev[:])
	computeFARGANGRU(&f.model.GRU1Input, &f.model.GRU1Recurrent, f.state.gru1State[:], f.scratch.gru1In[:], &f.scratch)
	computeFARGANGLU(&f.model.GRU1GLUGate, f.scratch.gru2In[:SigNetGRU1OutSize], f.state.gru1State[:], &f.scratch)

	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.gru2In[SigNetGRU1OutSize+i] = f.scratch.pitchGate[1] * f.scratch.pred[i+2]
	}
	copy(f.scratch.gru2In[SigNetGRU1OutSize+FARGANSubframeSize:], f.scratch.prev[:])
	computeFARGANGRU(&f.model.GRU2Input, &f.model.GRU2Recurrent, f.state.gru2State[:], f.scratch.gru2In[:], &f.scratch)
	computeFARGANGLU(&f.model.GRU2GLUGate, f.scratch.gru3In[:SigNetGRU2OutSize], f.state.gru2State[:], &f.scratch)

	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.gru3In[SigNetGRU2OutSize+i] = f.scratch.pitchGate[2] * f.scratch.pred[i+2]
	}
	copy(f.scratch.gru3In[SigNetGRU2OutSize+FARGANSubframeSize:], f.scratch.prev[:])
	computeFARGANGRU(&f.model.GRU3Input, &f.model.GRU3Recurrent, f.state.gru3State[:], f.scratch.gru3In[:], &f.scratch)
	computeFARGANGLU(&f.model.GRU3GLUGate, f.scratch.skipCat[SigNetGRU1OutSize+SigNetGRU2OutSize:SigNetGRU1OutSize+SigNetGRU2OutSize+SigNetGRU3OutSize], f.state.gru3State[:], &f.scratch)

	copy(f.scratch.skipCat[:SigNetGRU1OutSize], f.scratch.gru2In[:SigNetGRU1OutSize])
	copy(f.scratch.skipCat[SigNetGRU1OutSize:SigNetGRU1OutSize+SigNetGRU2OutSize], f.scratch.gru3In[:SigNetGRU2OutSize])
	copy(f.scratch.skipCat[SigNetGRU1OutSize+SigNetGRU2OutSize+SigNetGRU3OutSize:], f.scratch.gru1In[:SigNetFWC0ConvOutSize])
	offset := SigNetGRU1OutSize + SigNetGRU2OutSize + SigNetGRU3OutSize + SigNetFWC0ConvOutSize
	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.skipCat[offset+i] = f.scratch.pitchGate[3] * f.scratch.pred[i+2]
	}
	copy(f.scratch.skipCat[offset+FARGANSubframeSize:], f.scratch.prev[:])

	computeFARGANSignalDense(&f.model.SkipDense, f.scratch.skipOut[:], f.scratch.skipCat[:], activationTanh, &f.scratch)
	computeFARGANGLU(&f.model.SkipGLUGate, f.scratch.skipOut[:], f.scratch.skipOut[:], &f.scratch)
	computeFARGANSignalDense(&f.model.SigDenseOut, pcm[:FARGANSubframeSize], f.scratch.skipOut[:], activationTanh, &f.scratch)
	for i := 0; i < FARGANSubframeSize; i++ {
		pcm[i] *= gain
	}

	copy(f.state.pitchBuf[:PitchMaxPeriod-FARGANSubframeSize], f.state.pitchBuf[FARGANSubframeSize:])
	copy(f.state.pitchBuf[PitchMaxPeriod-FARGANSubframeSize:], pcm[:FARGANSubframeSize])
	for i := 0; i < FARGANSubframeSize; i++ {
		pcm[i] += float32(FARGANDeemphasis) * f.state.deemphMem
		f.state.deemphMem = pcm[i]
	}
}

func computeFARGANSignalDense(layer *LinearLayer, output, input []float32, activation int, scratch *farganScratch) {
	computeFARGANSignalLinear(layer, output, input, scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
}

func computeFARGANGRU(inputWeights, recurrentWeights *LinearLayer, state, in []float32, scratch *farganScratch) {
	n := recurrentWeights.NbInputs
	zrh := scratch.zrh[:3*n]
	recur := scratch.recur[:3*n]
	z := zrh[:n]
	r := zrh[n : 2*n]
	h := zrh[2*n : 3*n]

	computeFARGANSignalLinear(inputWeights, zrh[:3*n], in[:inputWeights.NbInputs], scratch)
	computeFARGANSignalLinear(recurrentWeights, recur[:3*n], state[:n], scratch)
	for i := 0; i < 2*n; i++ {
		zrh[i] += recur[i]
	}
	computeActivation(zrh[:2*n], zrh[:2*n], 2*n, activationSigmoid)
	for i := 0; i < n; i++ {
		h[i] += recur[2*n+i] * r[i]
	}
	computeActivation(h, h, n, activationTanh)
	for i := 0; i < n; i++ {
		h[i] = z[i]*state[i] + (1-z[i])*h[i]
		state[i] = h[i]
	}
}

func computeFARGANGLU(layer *LinearLayer, output, input []float32, scratch *farganScratch) {
	n := layer.NbOutputs
	act := scratch.act[:n]
	computeFARGANSignalLinear(layer, act[:n], input[:layer.NbInputs], scratch)
	computeActivation(act[:n], act[:n], n, activationSigmoid)
	for i := 0; i < n; i++ {
		output[i] = input[i] * act[i]
	}
}

func computeFARGANSignalConv1D(layer *LinearLayer, output, mem, input []float32, inputSize, activation int, scratch *farganScratch) {
	tmp := scratch.convTemp[:layer.NbInputs]
	if layer.NbInputs != inputSize {
		copy(tmp[:layer.NbInputs-inputSize], mem[:layer.NbInputs-inputSize])
	}
	copy(tmp[layer.NbInputs-inputSize:], input[:inputSize])
	computeFARGANSignalLinear(layer, output[:layer.NbOutputs], tmp[:layer.NbInputs], scratch)
	computeActivation(output, output, layer.NbOutputs, activation)
	if layer.NbInputs != inputSize {
		copy(mem[:layer.NbInputs-inputSize], tmp[inputSize:layer.NbInputs])
	}
}

func computeFARGANSignalLinear(layer *LinearLayer, out, in []float32, scratch *farganScratch) {
	bias := layer.Bias
	n := layer.NbOutputs
	m := layer.NbInputs

	if !layer.FloatWeights.Empty() {
		sgemv(out[:n], layer.FloatWeights, n, m, n, in[:m])
	} else if !layer.Weights.Empty() {
		cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m], scratch.quant[:m])
		if useSUBias && !layer.Subias.Empty() {
			bias = layer.Subias
		}
	} else {
		clear(out[:n])
	}
	if !bias.Empty() {
		for i := 0; i < n; i++ {
			out[i] += bias.At(i)
		}
	}
}

func clampFARGANSample(x float32) float32 {
	if x < -1 {
		return -1
	}
	if x > 1 {
		return 1
	}
	return x
}
