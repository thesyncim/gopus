//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

const (
	libopusFARGANTraceInputMagic  = "GFTI"
	libopusFARGANTraceOutputMagic = "GFTO"
)

var libopusFARGANTraceHelper libopustest.HelperCache

func getLibopusFARGANTraceHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusFARGANTraceHelper, "libopus_fargan_subframe_trace.c", "gopus_libopus_fargan_subframe_trace")
}

type farganSubframeTrace struct {
	gain      float32
	pred      []float32
	prev      []float32
	fwc0Conv  []float32
	pitchGate []float32
	gru1      []float32
	gru2      []float32
	gru3      []float32
	skipOut   []float32
	pcm       []float32
}

func probeLibopusFARGANSubframe(state FARGANState, cond []float32, period int) (farganSubframeTrace, error) {
	binPath, err := getLibopusFARGANTraceHelperPath()
	if err != nil {
		return farganSubframeTrace{}, err
	}
	payload := libopustest.NewOraclePayload(libopusFARGANTraceInputMagic)
	payload.I32(int32(period))
	payload.Float32s(state.deemphMem)
	payload.Float32s(state.pitchBuf[:]...)
	payload.Float32s(state.fwc0Mem[:]...)
	payload.Float32s(state.gru1State[:]...)
	payload.Float32s(state.gru2State[:]...)
	payload.Float32s(state.gru3State[:]...)
	payload.Float32s(cond[:FARGANCondSize]...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fargan subframe trace", libopusFARGANTraceOutputMagic)
	if err != nil {
		return farganSubframeTrace{}, err
	}
	rd := func(n int) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = reader.Float32()
		}
		return out
	}
	var tr farganSubframeTrace
	tr.gain = reader.Float32()
	tr.pred = rd(FARGANSubframeSize + 4)
	tr.prev = rd(FARGANSubframeSize)
	tr.fwc0Conv = rd(SigNetFWC0ConvOutSize)
	tr.pitchGate = rd(SigNetPitchGateSize)
	tr.gru1 = rd(SigNetGRU1StateSize)
	tr.gru2 = rd(SigNetGRU2StateSize)
	tr.gru3 = rd(SigNetGRU3StateSize)
	tr.skipOut = rd(SigNetSkipDenseOutSize)
	tr.pcm = rd(FARGANSubframeSize)
	if err := reader.ExpectConsumed(); err != nil {
		return farganSubframeTrace{}, err
	}
	return tr, nil
}

// runSubframeTraced mirrors runSubframe but captures intermediate stages.
func (f *FARGAN) runSubframeTraced(pcm, cond []float32, period int) farganSubframeTrace {
	var tr farganSubframeTrace
	computeFARGANSignalDense(&f.model.CondGainDense, f.scratch.gain[:], cond[:FARGANCondSize], activationLinear, &f.scratch)
	gain := opusmath.ExpF32(f.scratch.gain[0])
	gainInv := 1 / (1e-5 + gain)
	tr.gain = gain

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
	tr.pred = append([]float32{}, f.scratch.pred[:]...)
	tr.prev = append([]float32{}, f.scratch.prev[:]...)

	copy(f.scratch.fwc0In[:FARGANCondSize], cond[:FARGANCondSize])
	copy(f.scratch.fwc0In[FARGANCondSize:FARGANCondSize+len(f.scratch.pred)], f.scratch.pred[:])
	copy(f.scratch.fwc0In[FARGANCondSize+len(f.scratch.pred):], f.scratch.prev[:])

	computeFARGANSignalConv1D(&f.model.FWC0Conv, f.scratch.gru1In[:SigNetFWC0ConvOutSize], f.state.fwc0Mem[:SigNetFWC0MemSize], f.scratch.fwc0In[:], SigNetInputSize, activationTanh, &f.scratch)
	tr.fwc0Conv = append([]float32{}, f.scratch.gru1In[:SigNetFWC0ConvOutSize]...)
	computeFARGANGLU(&f.model.FWC0GLUGate, f.scratch.gru1In[:SigNetFWC0ConvOutSize], f.scratch.gru1In[:SigNetFWC0ConvOutSize], &f.scratch)
	computeFARGANSignalDense(&f.model.GainDenseOut, f.scratch.pitchGate[:], f.scratch.gru1In[:SigNetFWC0ConvOutSize], activationSigmoid, &f.scratch)
	tr.pitchGate = append([]float32{}, f.scratch.pitchGate[:]...)

	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.gru1In[SigNetFWC0GLUGateOutSize+i] = f.scratch.pitchGate[0] * f.scratch.pred[i+2]
	}
	copy(f.scratch.gru1In[SigNetFWC0GLUGateOutSize+FARGANSubframeSize:], f.scratch.prev[:])
	computeFARGANGRU(&f.model.GRU1Input, &f.model.GRU1Recurrent, f.state.gru1State[:], f.scratch.gru1In[:], &f.scratch)
	tr.gru1 = append([]float32{}, f.state.gru1State[:]...)
	computeFARGANGLU(&f.model.GRU1GLUGate, f.scratch.gru2In[:SigNetGRU1OutSize], f.state.gru1State[:], &f.scratch)

	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.gru2In[SigNetGRU1OutSize+i] = f.scratch.pitchGate[1] * f.scratch.pred[i+2]
	}
	copy(f.scratch.gru2In[SigNetGRU1OutSize+FARGANSubframeSize:], f.scratch.prev[:])
	computeFARGANGRU(&f.model.GRU2Input, &f.model.GRU2Recurrent, f.state.gru2State[:], f.scratch.gru2In[:], &f.scratch)
	tr.gru2 = append([]float32{}, f.state.gru2State[:]...)
	computeFARGANGLU(&f.model.GRU2GLUGate, f.scratch.gru3In[:SigNetGRU2OutSize], f.state.gru2State[:], &f.scratch)

	for i := 0; i < FARGANSubframeSize; i++ {
		f.scratch.gru3In[SigNetGRU2OutSize+i] = f.scratch.pitchGate[2] * f.scratch.pred[i+2]
	}
	copy(f.scratch.gru3In[SigNetGRU2OutSize+FARGANSubframeSize:], f.scratch.prev[:])
	computeFARGANGRU(&f.model.GRU3Input, &f.model.GRU3Recurrent, f.state.gru3State[:], f.scratch.gru3In[:], &f.scratch)
	tr.gru3 = append([]float32{}, f.state.gru3State[:]...)
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
	tr.skipOut = append([]float32{}, f.scratch.skipOut[:]...)
	computeFARGANSignalDense(&f.model.SigDenseOut, pcm[:FARGANSubframeSize], f.scratch.skipOut[:], activationTanh, &f.scratch)
	for i := 0; i < FARGANSubframeSize; i++ {
		pcm[i] *= gain
	}
	tr.pcm = append([]float32{}, pcm[:FARGANSubframeSize]...)
	return tr
}

func TestFARGANSubframeStageTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusFARGANModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan model", err)
	}
	blob, _ := dnnblob.Clone(modelBlob)
	var rt FARGAN
	if err := rt.SetModel(blob); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	var pcm0 [FARGANContSamples]float32
	var contFeatures [ContVectors * NumFeatures]float32
	fillFARGANPrimeInputs(pcm0[:], contFeatures[:])
	wantCont, err := probeLibopusFARGANContinuity(pcm0[:], contFeatures[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan continuity", err)
	}
	rt.PrimeContinuity(pcm0[:], contFeatures[:])
	rt.state = farganStateFromLibopusResult(wantCont)

	// Build a conditioning vector exactly as Synthesize would, then run a single
	// subframe with cond[0..FARGAN_COND_SIZE].
	var frameFeatures [NumFeatures]float32
	fillFARGANFeatures(frameFeatures[:])
	period := PeriodFromFeatures(frameFeatures[:])
	rt.computeConditioning(rt.scratch.cond[:], frameFeatures[:NumFeatures], period)
	cond := append([]float32{}, rt.scratch.cond[:FARGANCondSize]...)

	want, err := probeLibopusFARGANSubframe(rt.state, cond, rt.state.lastPeriod)
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan subframe trace", err)
	}
	var pcm [FARGANSubframeSize]float32
	got := rt.runSubframeTraced(pcm[:], cond, rt.state.lastPeriod)

	cmp := func(label string, g, w []float32) {
		for i := range g {
			if math.Float32bits(g[i]) != math.Float32bits(w[i]) {
				t.Errorf("%s[%d] got=0x%08x(%.9g) want=0x%08x(%.9g)", label, i,
					math.Float32bits(g[i]), g[i], math.Float32bits(w[i]), w[i])
				return
			}
		}
	}
	if math.Float32bits(got.gain) != math.Float32bits(want.gain) {
		t.Errorf("gain got=0x%08x want=0x%08x", math.Float32bits(got.gain), math.Float32bits(want.gain))
	}
	cmp("pred", got.pred, want.pred)
	cmp("prev", got.prev, want.prev)
	cmp("fwc0Conv", got.fwc0Conv, want.fwc0Conv)
	cmp("pitchGate", got.pitchGate, want.pitchGate)
	cmp("gru1", got.gru1, want.gru1)
	cmp("gru2", got.gru2, want.gru2)
	cmp("gru3", got.gru3, want.gru3)
	cmp("skipOut", got.skipOut, want.skipOut)
	cmp("pcm", got.pcm, want.pcm)
}
