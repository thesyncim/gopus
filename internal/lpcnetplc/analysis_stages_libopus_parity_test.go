//go:build gopus_extra_controls

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

var libopusAnalysisStagesHelper libopustest.HelperCache

const (
	libopusAnalysisStagesInputMagic  = "GLAI"
	libopusAnalysisStagesOutputMagic = "GLAO"
)

type analysisStagesResult struct {
	Window   []float32
	Spectrum []complex64
	BandE    []float32
	Ly       []float32
	Features []float32
	LPC      []float32
}

func getLibopusAnalysisStagesHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusAnalysisStagesHelper, "libopus_lpcnet_analysis_stages_info.c", "gopus_libopus_lpcnet_analysis_stages")
}

func probeLibopusAnalysisStages(preWindow []float32) (analysisStagesResult, error) {
	binPath, err := getLibopusAnalysisStagesHelperPath()
	if err != nil {
		return analysisStagesResult{}, err
	}
	payload := libopustest.NewOraclePayload(libopusAnalysisStagesInputMagic)
	payload.Float32s(preWindow...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "analysis stages", libopusAnalysisStagesOutputMagic)
	if err != nil {
		return analysisStagesResult{}, err
	}
	readBits := func(n int) []float32 {
		v := make([]float32, n)
		for i := range v {
			v[i] = reader.Float32()
		}
		return v
	}
	readComplex := func(n int) []complex64 {
		v := make([]complex64, n)
		for i := range v {
			re := reader.Float32()
			im := reader.Float32()
			v[i] = complex(re, im)
		}
		return v
	}
	res := analysisStagesResult{}
	res.Window = readBits(analysisWindowSize)
	res.Spectrum = readComplex(analysisFreqSize)
	res.BandE = readBits(NumBands)
	res.Ly = readBits(NumBands)
	res.Features = readBits(NumBands)
	res.LPC = readBits(analysisLPCOrder)
	if err := reader.Err(); err != nil {
		return analysisStagesResult{}, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return analysisStagesResult{}, err
	}
	return res, nil
}

// firstDiffBits scans two float32 slices and returns the first index whose bit
// patterns differ, or -1 if identical.
func firstDiffBits(got, want []float32) int {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			return i
		}
	}
	return -1
}

func reportBitDiffs(t *testing.T, label string, got, want []float32) (anyDiff bool) {
	t.Helper()
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	count := 0
	var maxAbs float64
	firstIdx := -1
	for i := 0; i < n; i++ {
		gb := math.Float32bits(got[i])
		wb := math.Float32bits(want[i])
		if gb != wb {
			if firstIdx < 0 {
				firstIdx = i
			}
			count++
			d := math.Abs(float64(got[i]) - float64(want[i]))
			if d > maxAbs {
				maxAbs = d
			}
			if count <= 6 {
				t.Logf("%s[%d] got=%.9g (0x%08x) want=%.9g (0x%08x) absdiff=%g", label, i, got[i], gb, want[i], wb, d)
			}
		}
	}
	if count > 0 {
		t.Logf("%s: %d/%d bit-differing, maxabs=%g, first=%d", label, count, n, maxAbs, firstIdx)
		return true
	}
	t.Logf("%s: bit-exact (%d values)", label, n)
	return false
}

// TestLPCNetAnalysisStagesBisect drives a set of synthetic pre-window analysis
// buffers through both the Go analysis frontend stages and the libopus
// per-stage oracle, then reports the FIRST sub-stage that diverges at the bit
// level. This pinpoints whether windowing, FFT, band energy, log shaping, DCT,
// or lpc_from_cepstrum is the source of the sub-1e-5 drift the SWB tests amplify.
func TestLPCNetAnalysisStagesBisect(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := map[string][]float32{}
	// Smooth multi-sine buffer similar to the DRED parity excitation.
	{
		buf := make([]float32, analysisWindowSize)
		for i := range buf {
			tm := float64(i) / 16000.0
			buf[i] = float32(0.3*math.Sin(2*math.Pi*120*tm) +
				0.18*math.Sin(2*math.Pi*250*tm+0.2) +
				0.07*math.Sin(2*math.Pi*512*tm+0.5))
		}
		cases["multisine"] = buf
	}
	// Int16-grid quantized version (the SWB failure lives on the int16 grid).
	{
		buf := make([]float32, analysisWindowSize)
		for i := range buf {
			tm := float64(i) / 16000.0
			v := 0.3*math.Sin(2*math.Pi*120*tm) + 0.2*math.Sin(2*math.Pi*440*tm)
			q := math.RoundToEven(v*32768) / 32768
			buf[i] = float32(q)
		}
		cases["int16grid"] = buf
	}
	// Ramp/impulsey buffer to stress band energy accumulation.
	{
		buf := make([]float32, analysisWindowSize)
		for i := range buf {
			buf[i] = float32((i%37)-18) / 23
		}
		cases["ramp"] = buf
	}

	for name, preWindow := range cases {
		t.Run(name, func(t *testing.T) {
			want, err := probeLibopusAnalysisStages(preWindow)
			if err != nil {
				libopustest.HelperUnavailable(t, "analysis stages", err)
			}

			var sc analysisScratch

			// Stage 1: windowing.
			win := make([]float32, analysisWindowSize)
			copy(win, preWindow)
			applyAnalysisWindow(win)
			winDiff := reportBitDiffs(t, "window", win, want.Window)

			// Stage 2: forward FFT.
			scale := float32(1.0 / analysisWindowSize)
			for i := 0; i < analysisWindowSize; i++ {
				sc.fftIn[i] = complex(win[i], 0)
			}
			celt.KissFFT32ToScaledWithScratch(sc.fftOut[:], sc.fftIn[:], scale, sc.fftScratch[:])
			spec := make([]complex64, analysisFreqSize)
			copy(spec, sc.fftOut[:analysisFreqSize])
			specRe := make([]float32, analysisFreqSize)
			specIm := make([]float32, analysisFreqSize)
			wantRe := make([]float32, analysisFreqSize)
			wantIm := make([]float32, analysisFreqSize)
			for i := 0; i < analysisFreqSize; i++ {
				specRe[i] = real(spec[i])
				specIm[i] = imag(spec[i])
				wantRe[i] = real(want.Spectrum[i])
				wantIm[i] = imag(want.Spectrum[i])
			}
			fftReDiff := reportBitDiffs(t, "fft.re", specRe, wantRe)
			fftImDiff := reportBitDiffs(t, "fft.im", specIm, wantIm)

			// Stage 3: band energy. Drive from libopus spectrum so this stage is
			// isolated from any upstream FFT drift.
			bandE := make([]float32, NumBands)
			computeBandEnergy(bandE, want.Spectrum, false)
			beDiff := reportBitDiffs(t, "bandE", bandE, want.BandE)

			// Stage 4: log shaping, driven from libopus band energy.
			ly := make([]float32, NumBands)
			logMax := float32(-2)
			follow := float32(-2)
			for i := 0; i < NumBands; i++ {
				v := log10f(1e-2 + want.BandE[i])
				v = maxF32(logMax-8, maxF32(follow-2.5, v))
				ly[i] = v
				logMax = maxF32(logMax, v)
				follow = maxF32(follow-2.5, v)
			}
			lyDiff := reportBitDiffs(t, "Ly", ly, want.Ly)

			// Stage 5: DCT, driven from libopus Ly.
			feat := make([]float32, NumBands)
			dctTransform(feat, want.Ly)
			feat[0] -= 4
			featDiff := reportBitDiffs(t, "features", feat, want.Features)

			// Stage 6: lpc_from_cepstrum, driven from libopus features.
			var lpc [analysisLPCOrder]float32
			lpcFromCepstrum(lpc[:], want.Features, &sc)
			lpcDiff := reportBitDiffs(t, "lpc", lpc[:], want.LPC)

			t.Logf("SUMMARY %s: window=%v fft.re=%v fft.im=%v bandE=%v Ly=%v features=%v lpc=%v",
				name, winDiff, fftReDiff, fftImDiff, beDiff, lyDiff, featDiff, lpcDiff)
			if winDiff || fftReDiff || fftImDiff || beDiff || lyDiff || featDiff || lpcDiff {
				t.Errorf("analysis frontend stage diverged for %q (see per-stage logs above)", name)
			}
			_ = firstDiffBits
		})
	}
}

// TestLPCNetSequenceFeatureIndexBitDiffs runs the exact DRED parity sequence
// (the input the SWB tests amplify) through the stateful Go analysis frontend
// and reports, per feature index across all frames, how many frames differ at
// the bit level and the max absolute diff.
func TestLPCNetSequenceFeatureIndexBitDiffs(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn model", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone blob: %v", err)
	}

	for _, frameSize := range []int{1920, 2880} {
		var analysis Analysis
		analysis.SetDREDEncoderMode(true)
		if err := analysis.SetModel(blob); err != nil {
			t.Fatalf("SetModel: %v", err)
		}
		frames := dredParityAnalysisFrames(4, frameSize)
		want, err := probeLibopusLPCNetFeatures(frames)
		if err != nil {
			libopustest.HelperUnavailable(t, "lpcnet features", err)
		}
		nFrames := len(frames) / FrameSize
		diffFrames := make([]int, NumTotalFeatures)
		maxAbs := make([]float64, NumTotalFeatures)
		for frame := 0; frame < nFrames; frame++ {
			var got [NumTotalFeatures]float32
			analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[frame*FrameSize:(frame+1)*FrameSize])
			base := frame * NumTotalFeatures
			for k := 0; k < NumTotalFeatures; k++ {
				if math.Float32bits(got[k]) != math.Float32bits(want.Features[base+k]) {
					diffFrames[k]++
					d := math.Abs(float64(got[k]) - float64(want.Features[base+k]))
					if d > maxAbs[k] {
						maxAbs[k] = d
					}
				}
			}
		}
		for k := 0; k < NumTotalFeatures; k++ {
			label := "cepstrum"
			if k == NumBands {
				label = "dnn_pitch"
			} else if k == NumBands+1 {
				label = "frame_corr"
			} else if k >= NumBands+2 {
				label = "lpc"
			}
			if diffFrames[k] > 0 {
				t.Logf("fsz=%d feat[%2d] (%s): %d/%d frames bit-differ maxabs=%g", frameSize, k, label, diffFrames[k], nFrames, maxAbs[k])
			}
		}
	}
}

// TestLPCNetPitchDNNNetIsolation feeds Go's own per-frame PitchDNN inputs (the
// if_features / xcorr_features it computed, plus the pitch state snapshot taken
// before the call) into the libopus PitchDNN oracle and checks bit-equality
// against Go's PitchDNN.Compute output. If the inputs are bit-identical to what
// Go fed but the outputs differ, the divergence is in the net; if Go and
// libopus produce the same pitch from the same inputs, the drift is upstream in
// if_features/xcorr_features.
func TestLPCNetPitchDNNNetIsolation(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn model", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone blob: %v", err)
	}
	var analysis Analysis
	analysis.SetDREDEncoderMode(true)
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	frames := dredParityAnalysisFrames(4, 1920)
	nFrames := len(frames) / FrameSize

	netDiff := 0
	for frame := 0; frame < nFrames; frame++ {
		// Snapshot pitch state before this frame's Compute.
		preState := analysis.pitch.state
		var got [NumTotalFeatures]float32
		analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[frame*FrameSize:(frame+1)*FrameSize])
		goPitch := analysis.dnnPitch

		// Run the oracle PitchDNN on the exact inputs Go used.
		oraclePitch, _, perr := probeLibopusPitchDNN(analysis.ifFeatures[:], analysis.xcorrFeatures[:], preState)
		if perr != nil {
			libopustest.HelperUnavailable(t, "pitchdnn", perr)
		}
		if math.Float32bits(goPitch) != math.Float32bits(oraclePitch) {
			netDiff++
			if netDiff <= 8 {
				t.Errorf("frame %d: go pitch=0x%08x oracle=0x%08x absdiff=%g",
					frame, math.Float32bits(goPitch), math.Float32bits(oraclePitch),
					math.Abs(float64(goPitch)-float64(oraclePitch)))
			}
		}
	}
	if netDiff != 0 {
		t.Errorf("PitchDNN dnn_pitch diverged on %d/%d frames when fed Go's own inputs", netDiff, nFrames)
	}
}

// TestLPCNetBurgCepstrumBitDiffs checks the first-loss Burg cepstrum frontend
// (BurgCepstralAnalysis / computeBurgCepstrum) bit-for-bit against libopus over
// int16-grid frames, since the SWB first-loss path consumes that output too.
func TestLPCNetBurgCepstrumBitDiffs(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := map[string][]float32{}
	{
		buf := make([]float32, FrameSize)
		for i := range buf {
			tm := float64(i) / 16000.0
			v := 0.3*math.Sin(2*math.Pi*120*tm) + 0.18*math.Sin(2*math.Pi*250*tm+0.2)
			buf[i] = float32(math.RoundToEven(v*32768) / 32768)
		}
		cases["int16grid"] = buf
	}
	{
		buf := make([]float32, FrameSize)
		for i := range buf {
			buf[i] = float32((i%37)-18) / 23
		}
		cases["ramp"] = buf
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			want, err := probeLibopusBurgCepstrum(frame)
			if err != nil {
				libopustest.HelperUnavailable(t, "burg cepstrum", err)
			}
			var a Analysis
			var dst [2 * NumBands]float32
			a.BurgCepstralAnalysis(dst[:], frame)
			if reportBitDiffs(t, "burg_ceps", dst[:], want) {
				t.Errorf("burg cepstrum diverged for %q", name)
			}
		})
	}
}

var libopusPitchDNNStagesHelper libopustest.HelperCache

const (
	libopusPitchDNNStagesInputMagic  = "GPSI"
	libopusPitchDNNStagesOutputMagic = "GPSO"
)

type pitchDNNStagesResult struct {
	If1Out      []float32
	If2Out      []float32
	Conv1Out    []float32
	Conv2Out    []float32
	DownsampOut []float32
	GRUState    []float32
	Output      []float32
	ExpWin      []float32
	Pitch       float32
}

func probeLibopusPitchDNNStages(ifFeatures, xcorrFeatures []float32, state pitchDNNState) (pitchDNNStagesResult, error) {
	binPath, err := cachedLibopusPLCHelperPath(&libopusPitchDNNStagesHelper, "libopus_pitchdnn_stages_info.c", "gopus_libopus_pitchdnn_stages")
	if err != nil {
		return pitchDNNStagesResult{}, err
	}
	payload := libopustest.NewOraclePayload(libopusPitchDNNStagesInputMagic)
	payload.Float32s(ifFeatures...)
	payload.Float32s(xcorrFeatures...)
	payload.Float32s(state.gruState[:]...)
	payload.Float32s(state.xcorrMem1[:]...)
	payload.Float32s(state.xcorrMem2[:]...)
	payload.Float32s(state.xcorrMem3[:]...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "pitchdnn stages", libopusPitchDNNStagesOutputMagic)
	if err != nil {
		return pitchDNNStagesResult{}, err
	}
	read := func(n int) []float32 {
		v := make([]float32, n)
		for i := range v {
			v[i] = reader.Float32()
		}
		return v
	}
	res := pitchDNNStagesResult{}
	res.If1Out = read(pitchDenseIF1OutSize)
	res.If2Out = read(pitchDenseIF2OutSize)
	res.Conv1Out = read(pitchXcorrFeatures)
	res.Conv2Out = read(pitchXcorrFeatures)
	res.DownsampOut = read(pitchDenseDownsamplerOut)
	res.GRUState = read(pitchGRUStateSize)
	res.Output = read(pitchDenseFinalOutSize)
	res.ExpWin = read(5)
	res.Pitch = reader.Float32()
	if err := reader.Err(); err != nil {
		return pitchDNNStagesResult{}, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return pitchDNNStagesResult{}, err
	}
	return res, nil
}

// TestLPCNetPitchDNNStageBisect drives Go's per-frame PitchDNN inputs through
// both the Go net and a per-layer libopus oracle, reporting the FIRST layer
// whose output diverges at the bit level.
func TestLPCNetPitchDNNStageBisect(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn model", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone blob: %v", err)
	}
	var analysis Analysis
	analysis.SetDREDEncoderMode(true)
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	frames := dredParityAnalysisFrames(4, 1920)
	nFrames := len(frames) / FrameSize

	stageNames := []string{"if1_out", "if2_out", "conv1_out", "conv2_out", "downsamp_out", "gru_state", "output", "pitch"}
	stageDiff := make([]int, len(stageNames))

	for frame := 0; frame < nFrames; frame++ {
		preState := analysis.pitch.state
		var got [NumTotalFeatures]float32
		analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[frame*FrameSize:(frame+1)*FrameSize])

		// Re-run Go's net in isolation on the captured inputs/state so the
		// scratch reflects exactly this frame.
		var iso PitchDNN
		iso.model = analysis.pitch.model
		iso.state = preState
		goPitch := iso.Compute(analysis.ifFeatures[:], analysis.xcorrFeatures[:])

		want, perr := probeLibopusPitchDNNStages(analysis.ifFeatures[:], analysis.xcorrFeatures[:], preState)
		if perr != nil {
			libopustest.HelperUnavailable(t, "pitchdnn stages", perr)
		}

		// Compare Go's exp() of the argmax window against libopus exp().
		pos := 0
		maxv := float32(-1)
		for k := 0; k < pitchPitchClassCount; k++ {
			if iso.scratch.output[k] > maxv {
				pos = k
				maxv = iso.scratch.output[k]
			}
		}
		s := pos - 2
		if s < 0 {
			s = 0
		}
		k := 0
		for kk := s; kk <= pos+2 && kk < pitchPitchClassCount && k < 5; kk++ {
			goExp := opusmath.ExpF32(iso.scratch.output[kk])
			if math.Float32bits(goExp) != math.Float32bits(want.ExpWin[k]) {
				t.Logf("frame %d exp[%d] (out idx %d): go=%.9g (0x%08x) want=%.9g (0x%08x)",
					frame, k, kk, goExp, math.Float32bits(goExp), want.ExpWin[k], math.Float32bits(want.ExpWin[k]))
			}
			k++
		}

		goStages := [][]float32{
			iso.scratch.if1Out[:],
			iso.scratch.downsampler[pitchXcorrFeatures:],
			iso.scratch.conv1Tmp2[1 : 1+pitchXcorrFeatures],
			iso.scratch.downsampler[:pitchXcorrFeatures],
			iso.scratch.downOut[:],
			iso.state.gruState[:],
			iso.scratch.output[:],
			{goPitch},
		}
		wantStages := [][]float32{
			want.If1Out, want.If2Out, want.Conv1Out, want.Conv2Out,
			want.DownsampOut, want.GRUState, want.Output, {want.Pitch},
		}
		for s := range stageNames {
			if firstDiffBits(goStages[s], wantStages[s]) >= 0 {
				stageDiff[s]++
				if stageDiff[s] <= 2 {
					idx := firstDiffBits(goStages[s], wantStages[s])
					t.Logf("frame %d %s: first diff at [%d] go=%.9g (0x%08x) want=%.9g (0x%08x)",
						frame, stageNames[s], idx, goStages[s][idx], math.Float32bits(goStages[s][idx]),
						wantStages[s][idx], math.Float32bits(wantStages[s][idx]))
				}
			}
		}
	}
	for s := range stageNames {
		t.Logf("stage %-13s: %d/%d frames bit-differ", stageNames[s], stageDiff[s], nFrames)
		// The per-layer stages (dense/conv/gru/dense) are produced by the real
		// libopus nnet primitives in the oracle, so they must be bit-exact. The
		// trailing "pitch" stage is a hand-inlined reimplementation of
		// compute_pitchdnn()'s argmax/softmax tail whose scalar codegen differs
		// from the vectorized library build; the real-function tail is asserted
		// bit-exact by TestLPCNetPitchDNNNetIsolation instead.
		if stageNames[s] != "pitch" && stageDiff[s] != 0 {
			t.Errorf("PitchDNN stage %q diverged on %d/%d frames", stageNames[s], stageDiff[s], nFrames)
		}
	}
}
