//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/testsignal"
)

func TestAnalysisGetInfoMinMaxHistoryThresholdReplayTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis getInfo min/max history replay trace")

	const fs, channels, frameSize, frames, lsbDepth = 16000, 2, 320, 60, 24
	samples, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusPureToneV1, fs, frameSize*channels*frames, channels)
	if err != nil {
		t.Fatal(err)
	}
	pcmBytes := make([]byte, 4*len(samples))
	for i, sample := range samples {
		binary.LittleEndian.PutUint32(pcmBytes[4*i:], math.Float32bits(sample))
	}
	input := analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth, samples)
	const (
		expectedPCMHash  = "b3d8d47ddc460ce32bdc0dea61e55a7b5a3642ddc3b0237f446349c3552de46d"
		expectedGANIHash = "199ef69119ffdd5425ad68a0f7bdd346e580d7b9a4f07cabcbf9dc5dbf2fb329"
	)
	pcmHash, inputHash := fmt.Sprintf("%x", sha256.Sum256(pcmBytes)), fmt.Sprintf("%x", sha256.Sum256(input))
	if pcmHash != expectedPCMHash || inputHash != expectedGANIHash {
		t.Fatalf("pure-tone corpus hashes float32LE=%s GANI=%s want float32LE=%s GANI=%s",
			pcmHash, inputHash, expectedPCMHash, expectedGANIHash)
	}
	t.Logf("exact corpus input: generator=GenerateCorpusSignal(CorpusPureToneV1) fs=%d channels=%d samples=%d frames=%d frameSize=%d lsbDepth=%d clampToOpusDemoF32InPlace=false float32LE_SHA256=%s GANI_SHA256=%s",
		fs, channels, len(samples), frames, frameSize, lsbDepth, pcmHash, inputHash)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		t.Fatal(err)
	}

	for _, captureFrame := range [...]uint32{0, 2} {
		t.Run(fmt.Sprintf("frame-%d", captureFrame), func(t *testing.T) {
			tracePath, driverHash := buildLibopusAnalysisGetInfoTraceHelper(t, captureFrame)
			traced, err := libopustest.RunHelper(tracePath, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
				t.Fatal("C getInfo trace driver changed the ordinary GANO output")
			}
			trace, err := parseAnalysisGetInfoTrace(traced[len(baseline):])
			if err != nil {
				t.Fatalf("parse GGET: %v", err)
			}
			if trace.frame != captureFrame || trace.frames != frames || trace.fs != fs || trace.channels != channels ||
				trace.frameSize != frameSize || trace.lsbDepth != lsbDepth || trace.driverHash != driverHash {
				t.Fatalf("GGET fixture metadata frame=%d frames=%d fs=%d channels=%d size=%d lsb=%d hash=%s want frame=%d hash=%s",
					trace.frame, trace.frames, trace.fs, trace.channels, trace.frameSize, trace.lsbDepth,
					trace.driverHash, captureFrame, driverHash)
			}
			var wantMeta [analysisGetInfoTraceMetaLen]uint32
			switch captureFrame {
			case 0:
				wantMeta = [analysisGetInfoTraceMetaLen]uint32{0, 0, 1, 0, 0, 0, 1, 0, 1, 1, 0, 1, 1, 1, 1, 1}
			case 2:
				wantMeta = [analysisGetInfoTraceMetaLen]uint32{2, 0, 3, 0, 2, 0, 3, 0, 3, 3, 0, 3, 1, 1, 1, 1}
			default:
				t.Fatalf("unexpected unfixed getInfo capture frame %d", captureFrame)
			}
			if trace.meta != wantMeta {
				t.Fatalf("frame%d complete original C metadata=%v want %v", captureFrame, trace.meta, wantMeta)
			}
			if trace.meta[12] != 1 || trace.meta[13] != 1 || trace.meta[14] != 1 || trace.meta[15] != 1 {
				t.Fatalf("C replay flags calls/info/state/source-shape=%v", trace.meta[12:])
			}

			baseFrame, err := parseAnalysisGetInfoBaselineFrame(baseline, frames, int(captureFrame))
			if err != nil {
				t.Fatal(err)
			}
			if d := diffAnalysisInfo(trace.linkedReturn, baseFrame.ret); d != "" {
				t.Fatalf("original linked C GGET versus GANO frame%d: %s", captureFrame, d)
			}
			if d := diffAnalysisInfo(trace.linkedReturn, trace.replayReturn); d != "" {
				t.Fatalf("original linked/replayed C getter differ: %s", d)
			}

			// Replay the original C state before attributing a failure to the Go
			// analyzer or its FFT/MLP path.
			cState := analysisGetInfoStateFromTrace(trace)
			goCReplay := cState.tonalityGetInfo(frameSize)
			cReplayDiff := diffAnalysisInfo(analysisInfoToOracle(goCReplay), trace.replayReturn)

			goState := NewTonalityAnalysisState(fs)
			goState.SetLSBDepth(lsbDepth)
			var goReturn AnalysisInfo
			for frame := 0; frame <= int(captureFrame); frame++ {
				start := frame * frameSize * channels
				goReturn = goState.RunAnalysis(samples[start:start+frameSize*channels], frameSize, channels)
			}
			if d := analysisStateDiff(goState, baseFrame); d != "" {
				t.Fatalf("frame%d analyzer state differs before getter replay: %s", captureFrame, d)
			}
			compareAnalysisGetInfoRing(t, trace.info, goState.Info)
			goEndpointDiff := diffAnalysisInfo(analysisInfoToOracle(goReturn), baseFrame.ret)

			model := modelAnalysisGetInfoPMaxHistory(trace)
			t.Logf("same-original-C-state frame=%d cursor pre=%d/%d post=%d/%d restored=%d/%d replay-final=%d/%d write=%d count=%d lookahead=%d history=%d pos0=%d",
				captureFrame, trace.meta[0], trace.meta[1], trace.meta[2], trace.meta[3], trace.meta[4], trace.meta[5],
				trace.meta[6], trace.meta[7], trace.meta[8], trace.meta[9], model.lookahead, model.history, model.pos0)
			t.Logf("original C GGET complete metadata=%v", trace.meta)
			t.Logf("captured C ring pmax history operands oldest-to-current=%v", model.historyBits)
			t.Logf("source-shaped model (not C temporaries): raw=%08x vad=%08x pmaxBeforeHistory=%08x pmaxAfterHistory=%08x biasMul=%08x biased=%08x blend=%08x delta=%08x roundedMul=%08x separateSum=%08x fusedFMA=%08x linkedC=%08x GoCReplay=%08x GoRunAnalysis=%08x",
				model.rawCurrent, model.vad, model.pmaxBeforeHistory, model.pmaxAfterHistory, model.biasMul,
				model.biasedPmax, model.blend, model.delta, model.blendProduct, model.separateFinal,
				model.fusedFinal, trace.replayReturn.musicMax, math.Float32bits(goCReplay.MusicProbMax), math.Float32bits(goReturn.MusicProbMax))
			t.Logf("original C linked-vs-Go RunAnalysis diff=%q; same-C-state Go replay diff=%q", goEndpointDiff, cReplayDiff)
			if model.fusedFinal != trace.replayReturn.musicMax && model.separateFinal != trace.replayReturn.musicMax {
				t.Fatalf("captured C output %08x matches neither source-shaped separate %08x nor fused %08x model",
					trace.replayReturn.musicMax, model.separateFinal, model.fusedFinal)
			}
			if cReplayDiff != "" {
				t.Fatalf("Go getter replay from original C snapshot differs from linked original C replay: %s", cReplayDiff)
			}
			if goEndpointDiff != "" {
				t.Fatalf("same-input Go RunAnalysis frame%d differs from original GANO: %s", captureFrame, goEndpointDiff)
			}
		})
	}
}

type analysisGetInfoPMaxModel struct {
	lookahead, history                                                         uint32
	pos0                                                                       int
	historyBits                                                                []uint32
	rawCurrent, vad, pmaxBeforeHistory, pmaxAfterHistory                       uint32
	biasMul, biasedPmax, blend, delta, blendProduct, separateFinal, fusedFinal uint32
}

func modelAnalysisGetInfoPMaxHistory(trace analysisGetInfoTrace) analysisGetInfoPMaxModel {
	m := analysisGetInfoPMaxModel{}
	readPos, writePos := int(trace.meta[0]), int(trace.meta[8])
	look := writePos - readPos
	if look < 0 {
		look += DetectSize
	}
	m.lookahead = uint32(look)
	pos := readPos
	if int(trace.frameSize) > int(trace.fs)/50 && pos != writePos {
		pos++
		if pos == DetectSize {
			pos = 0
		}
	}
	if pos == writePos {
		pos--
	}
	if pos < 0 {
		pos = DetectSize - 1
	}
	m.pos0 = pos
	mpos, vpos := pos, pos
	if look > 15 {
		mpos += 5
		if mpos >= DetectSize {
			mpos -= DetectSize
		}
		vpos++
		if vpos >= DetectSize {
			vpos = 0
		}
	}
	vad := math.Float32frombits(trace.info[vpos].activityProb)
	weight := maxf(0.1, vad)
	probCount := weight
	probAvg := analysisGetInfoTraceMul32(weight, math.Float32frombits(trace.info[mpos].musicProb))
	probMax := float32(0)
	for {
		mpos++
		if mpos == DetectSize {
			mpos = 0
		}
		if mpos == writePos {
			break
		}
		vpos++
		if vpos == DetectSize {
			vpos = 0
		}
		if vpos == writePos {
			break
		}
		posVAD := math.Float32frombits(trace.info[vpos].activityProb)
		posWeight := maxf(0.1, posVAD)
		denom := probCount
		if denom < 1e-9 {
			denom = 1e-9
		}
		candidate := analysisGetInfoTraceAdd32(probAvg,
			-analysisGetInfoTraceMul32(transitionPenalty, vad-posVAD))
		probMin := candidate / denom
		_ = probMin
		candidateMax := analysisGetInfoTraceAdd32(probAvg,
			analysisGetInfoTraceMul32(transitionPenalty, vad-posVAD)) / denom
		probMax = maxf(candidateMax, probMax)
		probCount = analysisGetInfoTraceAdd32(probCount, posWeight)
		probAvg = analysisGetInfoTraceAdd32(probAvg,
			analysisGetInfoTraceMul32(posWeight, math.Float32frombits(trace.info[mpos].musicProb)))
	}
	probMusic := probAvg / probCount
	probMax = maxf(probMusic, probMax)
	probMax = minf(probMax, 1)
	m.rawCurrent = math.Float32bits(probMusic)
	m.vad = math.Float32bits(vad)
	m.pmaxBeforeHistory = math.Float32bits(probMax)
	pmax := probMax
	history := max(min(int(trace.meta[9])-1, 15), 0)
	m.history = uint32(history)
	for i := 0; i < history; i++ {
		pos--
		if pos < 0 {
			pos = DetectSize - 1
		}
		bits := trace.info[pos].musicProb
		m.historyBits = append(m.historyBits, bits)
		pmax = maxf(pmax, math.Float32frombits(bits))
	}
	m.pmaxAfterHistory = math.Float32bits(pmax)
	biasMul := analysisGetInfoTraceMul32(0.1, vad)
	biased := minf(1, analysisGetInfoTraceAdd32(pmax, biasMul))
	blend := analysisGetInfoTraceSub32(1, analysisGetInfoTraceMul32(0.1, float32(look)))
	delta := analysisGetInfoTraceSub32(biased, probMax)
	product := analysisGetInfoTraceMul32(blend, delta)
	separate := analysisGetInfoTraceAdd32(probMax, product)
	fused := opusmath.FMA32(blend, delta, probMax)
	m.biasMul, m.biasedPmax, m.blend, m.delta = math.Float32bits(biasMul), math.Float32bits(biased), math.Float32bits(blend), math.Float32bits(delta)
	m.blendProduct, m.separateFinal, m.fusedFinal = math.Float32bits(product), math.Float32bits(separate), math.Float32bits(fused)
	return m
}

//go:noinline
func analysisGetInfoTraceSub32(a, b float32) float32 { return a - b }
