//go:build gopus_extra_controls

package lace

import "github.com/thesyncim/gopus/internal/opusmath"

// TraceStage identifies an opt-in LACE diagnostic checkpoint.
type TraceStage int

const (
	TraceStageInput TraceStage = iota + 1
	TraceStageFeatures
	TraceStageNumbits
	TraceStagePeriods
	TraceStagePreemph
	TraceStageFeatureNetConv1
	TraceStageFeatureNetConv2Input
	TraceStageFeatureNetConv2Linear
	TraceStageFeatureNetConv2
	TraceStageFeatureNetTConv
	TraceStageFeatureNetLatent
	TraceStagePostCF1
	TraceStagePostCF2
	TraceStagePostAF1
	TraceStageDeemph
	TraceStageCF1KernelRaw
	TraceStageCF1GainsRaw
	TraceStageCF1KernelScaled
	TraceStageCF1GainsScaled
)

// TraceRecord captures one opt-in LACE diagnostic checkpoint. These records are
// intentionally allocated and are only built by ProcessTrace under the
// extra-controls build tag.
type TraceRecord struct {
	Stage             TraceStage
	Subframe          int
	Channels          int
	SamplesPerChannel int
	Values            []float32
}

// ProcessTrace mirrors Process for one 20 ms LACE frame while snapshotting the
// same checkpoints emitted by the libopus trace helper. Normal Process remains
// branch-free and allocation-free.
func (s *LACEState) ProcessTrace(in, out, features []float32, numbits []float32, periods []int) ([]TraceRecord, error) {
	if !s.Loaded() {
		return nil, errLACENoModel
	}
	if len(in) != frame20msSize {
		return nil, errLACEInLen
	}
	if len(out) < frame20msSize {
		return nil, errLACEOutLen
	}
	if len(features) < subframesPerFrame*laceNumFeatures {
		return nil, errLACEFeatures
	}
	if len(periods) < subframesPerFrame {
		return nil, errLACEPeriods
	}
	if len(numbits) < 2 {
		return nil, errLACENumbits
	}
	if err := validatePeriods(periods, lacePitchMax); err != nil {
		return nil, err
	}
	s.ensureWindow()
	m := &s.model.LACE

	records := make([]TraceRecord, 0, 31)
	appendTraceRecord(&records, TraceStageInput, -1, 1, frame20msSize, in[:frame20msSize])
	appendTraceRecord(&records, TraceStageFeatures, -1, 1, subframesPerFrame*laceNumFeatures, features[:subframesPerFrame*laceNumFeatures])
	appendTraceRecord(&records, TraceStageNumbits, -1, 1, 2, numbits[:2])
	appendTraceRecord(&records, TraceStagePeriods, -1, 1, subframesPerFrame, periodsAsFloat32(periods[:subframesPerFrame]))

	var outputBuf [frame20msSize]float32
	for i := 0; i < frame20msSize; i++ {
		outputBuf[i] = in[i] - lacePreemph*s.preempMem
		s.preempMem = in[i]
	}
	appendTraceRecord(&records, TraceStagePreemph, -1, 1, frame20msSize, outputBuf[:])

	var latent [subframesPerFrame * laceCondDim]float32
	s.featureNetTrace(latent[:], features, numbits, periods, &records)

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		traceAdaCombParams(
			&records, sf,
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.CF1Kernel, &m.CF1Gain, &m.CF1GlobalGain,
			laceCF1KernelSize,
			laceCF1FilterGainA, laceCF1FilterGainB, laceCF1LogGainLimit,
		)
		adacombProcessFrame(
			s.cf1History[:], s.cf1LastKernel[:], &s.cf1LastGlobalGain, &s.cf1LastPitchLag,
			outputBuf[base:base+subframeSize], outputBuf[base:base+subframeSize],
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.CF1Kernel, &m.CF1Gain, &m.CF1GlobalGain,
			periods[sf],
			subframeSize, laceOverlapSize,
			laceCF1KernelSize, laceCF1LeftPadding,
			laceCF1FilterGainA, laceCF1FilterGainB, laceCF1LogGainLimit,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStagePostCF1, -1, 1, frame20msSize, outputBuf[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		adacombProcessFrame(
			s.cf2History[:], s.cf2LastKernel[:], &s.cf2LastGlobalGain, &s.cf2LastPitchLag,
			outputBuf[base:base+subframeSize], outputBuf[base:base+subframeSize],
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.CF2Kernel, &m.CF2Gain, &m.CF2GlobalGain,
			periods[sf],
			subframeSize, laceOverlapSize,
			laceCF2KernelSize, laceCF2LeftPadding,
			laceCF2FilterGainA, laceCF2FilterGainB, laceCF2LogGainLimit,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStagePostCF2, -1, 1, frame20msSize, outputBuf[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		adaconvProcessFrame(
			s.af1History[:], s.af1LastKernel[:],
			outputBuf[base:base+subframeSize], outputBuf[base:base+subframeSize],
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.AF1Kernel, &m.AF1Gain,
			subframeSize, laceOverlapSize,
			laceAF1InChannels, laceAF1OutChannels,
			laceAF1KernelSize, laceAF1LeftPadding,
			laceAF1FilterGainA, laceAF1FilterGainB, 1.0,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStagePostAF1, -1, 1, frame20msSize, outputBuf[:])

	for i := 0; i < frame20msSize; i++ {
		out[i] = outputBuf[i] + lacePreemph*s.deempMem
		s.deempMem = out[i]
	}
	appendTraceRecord(&records, TraceStageDeemph, -1, 1, frame20msSize, out[:frame20msSize])

	return records, nil
}

func (s *LACEState) featureNetTrace(out, features []float32, numbits []float32, periods []int, records *[]TraceRecord) {
	m := &s.model.LACE
	const condDim = laceCondDim
	const hiddenDim = laceHiddenFeatureDim
	const numFeat = laceNumFeatures
	const pitchEmbDim = lacePitchEmbeddingDim
	const numbitsEmbDim = laceNumbitsEmbeddingDim
	const concatDim = numFeat + pitchEmbDim + 2*numbitsEmbDim

	var numbitsEmbedded [2 * numbitsEmbDim]float32
	low := opusmath.LogF32(laceNumbitsRangeLow)
	high := opusmath.LogF32(laceNumbitsRangeHigh)
	computeNumbitsEmbedding(numbitsEmbedded[:numbitsEmbDim], numbits[0], low, high, laceNumbitsScales)
	computeNumbitsEmbedding(numbitsEmbedded[numbitsEmbDim:], numbits[1], low, high, laceNumbitsScales)

	var conv1Out [subframesPerFrame * hiddenDim]float32
	var conv1In [concatDim]float32
	for sf := 0; sf < subframesPerFrame; sf++ {
		copy(conv1In[:numFeat], features[sf*numFeat:sf*numFeat+numFeat])
		period := periods[sf]
		if !m.PitchEmbedding.FloatWeights.Empty() {
			for j := 0; j < pitchEmbDim; j++ {
				conv1In[numFeat+j] = m.PitchEmbedding.FloatWeights.At(period*pitchEmbDim + j)
			}
		}
		copy(conv1In[numFeat+pitchEmbDim:], numbitsEmbedded[:])

		computeGenericConv1D(
			&m.FNetConv1,
			conv1Out[sf*hiddenDim:sf*hiddenDim+hiddenDim],
			nil,
			conv1In[:concatDim],
			concatDim, actTanh,
		)
	}
	appendTraceRecord(records, TraceStageFeatureNetConv1, -1, 1, subframesPerFrame*hiddenDim, conv1Out[:])

	const accInputSize = subframesPerFrame * hiddenDim
	memLen := m.FNetConv2.NbInputs - accInputSize
	var conv2Input [laceHiddenFeatureDim * subframesPerFrame * 2]float32
	if memLen > 0 {
		copy(conv2Input[:memLen], s.fnetConv2State[:memLen])
	}
	copy(conv2Input[memLen:m.FNetConv2.NbInputs], conv1Out[:])
	appendTraceRecord(records, TraceStageFeatureNetConv2Input, -1, 1, m.FNetConv2.NbInputs, conv2Input[:m.FNetConv2.NbInputs])

	var conv2Out [condDim]float32
	computeLinear(
		&m.FNetConv2,
		conv2Out[:],
		conv2Input[:m.FNetConv2.NbInputs],
	)
	appendTraceRecord(records, TraceStageFeatureNetConv2Linear, -1, 1, condDim, conv2Out[:])
	computeActivation(conv2Out[:], conv2Out[:], m.FNetConv2.NbOutputs, actTanh)
	if memLen > 0 {
		copy(s.fnetConv2State[:memLen], conv2Input[accInputSize:m.FNetConv2.NbInputs])
	}
	appendTraceRecord(records, TraceStageFeatureNetConv2, -1, 1, condDim, conv2Out[:])

	var tconvOut [subframesPerFrame * condDim]float32
	computeGenericDense(
		&m.FNetTConv,
		tconvOut[:],
		conv2Out[:],
		actTanh,
	)
	appendTraceRecord(records, TraceStageFeatureNetTConv, -1, 1, subframesPerFrame*condDim, tconvOut[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		computeGenericGRU(
			&m.FNetGRUInput, &m.FNetGRURecurrent,
			s.fnetGRUState[:],
			tconvOut[sf*condDim:sf*condDim+condDim],
		)
		copy(out[sf*condDim:sf*condDim+condDim], s.fnetGRUState[:])
	}
	appendTraceRecord(records, TraceStageFeatureNetLatent, -1, 1, subframesPerFrame*condDim, out[:subframesPerFrame*condDim])
}

func traceAdaCombParams(
	records *[]TraceRecord,
	subframe int,
	features []float32,
	kernelLayer, gainLayer, globalGainLayer *LinearLayer,
	kernelSize int,
	filterGainA, filterGainB, logGainLimit float32,
) {
	var kernel [adaCombMaxKernelSize]float32
	var gains [2]float32
	computeGenericDense(kernelLayer, kernel[:kernelSize], features, actLinear)
	computeGenericDense(gainLayer, gains[:1], features, actRelu)
	computeGenericDense(globalGainLayer, gains[1:2], features, actTanh)
	appendTraceRecord(records, TraceStageCF1KernelRaw, subframe, 1, kernelSize, kernel[:kernelSize])
	appendTraceRecord(records, TraceStageCF1GainsRaw, subframe, 1, len(gains), gains[:])

	gains[0] = opusmath.ExpF32(logGainLimit - gains[0])
	gains[1] = opusmath.ExpF32(filterGainA*gains[1] + filterGainB)
	var norm float32
	for k := 0; k < kernelSize; k++ {
		norm += kernel[k] * kernel[k]
	}
	invNorm := 1.0 / (float32(1e-6) + opusmath.SqrtF32(norm))
	scale := invNorm * gains[0]
	for k := 0; k < kernelSize; k++ {
		kernel[k] *= scale
	}
	appendTraceRecord(records, TraceStageCF1KernelScaled, subframe, 1, kernelSize, kernel[:kernelSize])
	appendTraceRecord(records, TraceStageCF1GainsScaled, subframe, 1, len(gains), gains[:])
}

func appendTraceRecord(records *[]TraceRecord, stage TraceStage, subframe, channels, samplesPerChannel int, values []float32) {
	snapshot := append([]float32(nil), values...)
	*records = append(*records, TraceRecord{
		Stage:             stage,
		Subframe:          subframe,
		Channels:          channels,
		SamplesPerChannel: samplesPerChannel,
		Values:            snapshot,
	})
}

func periodsAsFloat32(periods []int) []float32 {
	out := make([]float32, len(periods))
	for i, period := range periods {
		out[i] = float32(period)
	}
	return out
}
