package encoder

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

var libopusAnalysisHelper libopustest.HelperCache
var libopusFixedAnalysisHelper libopustest.HelperCache

func buildLibopusAnalysisHelper() (string, error) {
	config := libopustest.CHelperConfig{
		Label:       "tonality analysis",
		OutputBase:  "gopus_libopus_analysis_info",
		SourceFile:  "libopus_analysis_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	}
	switch {
	case fixedPointBuild && extsupport.QEXT:
		config.FixedQEXTRef = true
		config.Libs = []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"}
	case fixedPointBuild:
		config.FixedRef = true
		config.Libs = []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"}
	case extsupport.QEXT:
		config.QEXTRef = true
		config.Libs = []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"}
	default:
		config.Libs = []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}
	}
	return libopustest.BuildCHelper(config)
}

func buildLibopusFixedAnalysisHelper() (string, error) {
	config := libopustest.CHelperConfig{
		Label:       "fixed-point tonality analysis",
		OutputBase:  "gopus_libopus_fixed_analysis_info",
		SourceFile:  "libopus_analysis_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	}
	switch {
	case extsupport.QEXT:
		config.FixedQEXTRef = true
		config.Libs = []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"}
	default:
		config.FixedRef = true
		config.Libs = []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"}
	}
	return libopustest.BuildCHelper(config)
}

// analysisOracleInfo is one AnalysisInfo as the helper reports it.
type analysisOracleInfo struct {
	valid                         uint32
	tonality, tonalitySlope       uint32
	noisiness, activity           uint32
	musicProb, musicMin, musicMax uint32
	bandwidth                     uint32
	activityProb, maxPitchRatio   uint32
	leakBoost                     [19]uint8
}

// analysisOracleFrame is the helper's per-frame record.
type analysisOracleFrame struct {
	ret, latest  analysisOracleInfo
	scalars      [12]uint32
	hashes       [12]uint32
	downmixState [3]int32
	inmem        []int32
}

var analysisOracleScalarNames = [12]string{
	"Etracker", "lowECount", "hp_ener_accum", "prev_tonality", "E_count", "count",
	"analysis_offset", "write_pos", "read_pos", "read_subframe", "mem_fill", "prev_bandwidth",
}

var analysisOracleHashNames = [12]string{
	"angle", "d_angle", "d2_angle", "E", "logE", "lowE",
	"highE", "meanE", "mem", "cmean", "std", "rnn_state",
}

func runLibopusAnalysisOracle(t *testing.T, fs, channels, frameSize, lsbDepth int, pcm []float32) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfigured(t, fs, channels, frameSize, lsbDepth, 0, -2, 0, pcm, nil)
}

func runLibopusAnalysisOracleShort(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2 int, pcm []int16) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfigured(t, fs, channels, frameSize, lsbDepth, c1, c2, 1, nil, pcm)
}

func runLibopusAnalysisOracleConfigured(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2, downmix int, pcm32 []float32, pcm16 []int16) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfiguredWithHelper(t, fs, channels, frameSize, lsbDepth, c1, c2, downmix, pcm32, pcm16,
		&libopusAnalysisHelper, buildLibopusAnalysisHelper)
}

func runLibopusFixedAnalysisOracle(t *testing.T, fs, channels, frameSize, lsbDepth int, pcm []float32) []analysisOracleFrame {
	t.Helper()
	return runLibopusAnalysisOracleConfiguredWithHelperVersion(t, fs, channels, frameSize, lsbDepth, 0, -2, 0, pcm, nil,
		&libopusFixedAnalysisHelper, buildLibopusFixedAnalysisHelper, 2)
}

func runLibopusAnalysisOracleConfiguredWithHelper(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2, downmix int, pcm32 []float32, pcm16 []int16,
	helper *libopustest.HelperCache, build func() (string, error)) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfiguredWithHelperVersion(t, fs, channels, frameSize, lsbDepth, c1, c2, downmix, pcm32, pcm16,
		helper, build, 1)
}

func runLibopusAnalysisOracleConfiguredWithHelperVersion(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2, downmix int, pcm32 []float32, pcm16 []int16,
	helper *libopustest.HelperCache, build func() (string, error), version uint32) []analysisOracleFrame {
	t.Helper()
	bin, err := helper.Path(build)
	if err != nil {
		libopustest.HelperUnavailable(t, "tonality analysis", err)
	}
	numSamples := len(pcm32)
	if downmix == 1 {
		numSamples = len(pcm16)
	}
	numFrames := numSamples / (frameSize * channels)
	payload := libopustest.NewOraclePayloadVersion("GANI", version,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(numFrames), uint32(lsbDepth),
		uint32(c1), uint32(int32(c2)), uint32(downmix), uint32(numFrames*frameSize*channels))
	if downmix == 1 {
		for _, v := range pcm16[:numFrames*frameSize*channels] {
			payload.I16(v)
		}
		if numFrames*frameSize*channels&1 != 0 {
			payload.I16(0)
		}
	} else {
		for _, v := range pcm32[:numFrames*frameSize*channels] {
			payload.U32(math.Float32bits(v))
		}
	}
	out, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		t.Fatalf("run tonality analysis helper: %v", err)
	}
	if len(out) < 12 || string(out[:4]) != "GANO" || binary.LittleEndian.Uint32(out[4:]) != version {
		t.Fatalf("malformed helper output header")
	}
	if n := int(binary.LittleEndian.Uint32(out[8:])); n != numFrames {
		t.Fatalf("helper returned %d frames, want %d", n, numFrames)
	}
	const infoBytes = 12*4 + 20
	const baseRecordBytes = 2*infoBytes + 12*4 + 12*4
	recordBytes := baseRecordBytes
	if version == 2 {
		recordBytes += (3 + AnalysisBufSize) * 4
	}
	body := out[12:]
	if len(body) != numFrames*recordBytes {
		t.Fatalf("helper output has %d bytes, want %d", len(body), numFrames*recordBytes)
	}
	readInfo := func(b []byte) analysisOracleInfo {
		u := func(i int) uint32 { return binary.LittleEndian.Uint32(b[4*i:]) }
		in := analysisOracleInfo{
			valid: u(0), tonality: u(1), tonalitySlope: u(2), noisiness: u(3), activity: u(4),
			musicProb: u(5), musicMin: u(6), musicMax: u(7), bandwidth: u(8),
			activityProb: u(9), maxPitchRatio: u(10),
		}
		copy(in.leakBoost[:], b[48:48+19])
		return in
	}
	frames := make([]analysisOracleFrame, numFrames)
	for f := range frames {
		rec := body[f*recordBytes:]
		frames[f].ret = readInfo(rec)
		frames[f].latest = readInfo(rec[infoBytes:])
		for i := range 12 {
			frames[f].scalars[i] = binary.LittleEndian.Uint32(rec[2*infoBytes+4*i:])
			frames[f].hashes[i] = binary.LittleEndian.Uint32(rec[2*infoBytes+48+4*i:])
		}
		if version == 2 {
			stage := rec[baseRecordBytes:]
			for i := range 3 {
				frames[f].downmixState[i] = int32(binary.LittleEndian.Uint32(stage[4*i:]))
			}
			frames[f].inmem = make([]int32, AnalysisBufSize)
			for i := range frames[f].inmem {
				frames[f].inmem[i] = int32(binary.LittleEndian.Uint32(stage[4*(3+i):]))
			}
		}
	}
	return frames
}

func TestAnalysisShortProjectionInputMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frameCount = 960, 24
	for _, channels := range []int{9, 16} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			pcm := make([]int16, channels*frameSize*frameCount)
			for sample := range frameSize * frameCount {
				tt := float64(sample) / 48000
				amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
				for ch := range channels {
					v := float32(amp * math.Sin(2*math.Pi*110*float64(ch+1)*tt))
					pcm[sample*channels+ch] = int16(math.Round(float64(v) * 32768))
				}
			}
			want := runLibopusAnalysisOracleShort(t, 48000, channels, frameSize, 16, 0, 1, pcm)
			an := NewTonalityAnalysisState(48000)
			an.SetLSBDepth(16)
			frame := make([]float32, 2*frameSize)
			for f := range frameCount {
				for sample := range frameSize {
					frame[2*sample] = float32(pcm[(f*frameSize+sample)*channels]) / 32768
					frame[2*sample+1] = float32(pcm[(f*frameSize+sample)*channels+1]) / 32768
				}
				got := analysisInfoToOracle(an.RunAnalysis(frame, frameSize, 2))
				if d := diffAnalysisInfo(got, want[f].ret); d != "" {
					t.Errorf("frame %d returned info: %s; state:%s", f, d, analysisStateDiff(an, want[f]))
					break
				}
				if d := analysisStateDiff(an, want[f]); d != "" {
					t.Errorf("frame %d state: %s", f, d)
					break
				}
			}
		})
	}
}

func analysisInfoToOracle(in AnalysisInfo) analysisOracleInfo {
	out := analysisOracleInfo{
		tonality: math.Float32bits(in.Tonality), tonalitySlope: math.Float32bits(in.TonalitySlope),
		noisiness: math.Float32bits(in.NoisySpeech), activity: math.Float32bits(in.Activity),
		musicProb: math.Float32bits(in.MusicProb), musicMin: math.Float32bits(in.MusicProbMin),
		musicMax: math.Float32bits(in.MusicProbMax), bandwidth: uint32(in.BandwidthIndex),
		activityProb: math.Float32bits(in.VADProb), maxPitchRatio: math.Float32bits(in.MaxPitchRatio),
		leakBoost: in.LeakBoost,
	}
	if in.Valid {
		out.valid = 1
	}
	return out
}

func hashAnalysisFloats(v []float32) uint32 {
	h := uint32(2166136261)
	for _, x := range v {
		h = (h ^ math.Float32bits(x)) * 16777619
	}
	return h
}

func analysisStateScalars(s *TonalityAnalysisState) [12]uint32 {
	return [12]uint32{
		math.Float32bits(s.ETracker), math.Float32bits(s.LowECount), math.Float32bits(s.HPEnerAccum),
		math.Float32bits(s.PrevTonality), uint32(s.ECount), uint32(s.Count), uint32(s.AnalysisOffset),
		uint32(s.WritePos), uint32(s.ReadPos), uint32(s.ReadSubframe), uint32(s.MemFill), uint32(s.PrevBandwidth),
	}
}

func analysisStateHashes(s *TonalityAnalysisState) [12]uint32 {
	flat := func(rows [][NbTBands]float32) []float32 {
		out := make([]float32, 0, len(rows)*NbTBands)
		for i := range rows {
			out = append(out, rows[i][:]...)
		}
		return out
	}
	return [12]uint32{
		hashAnalysisFloats(s.Angle[:]), hashAnalysisFloats(s.DAngle[:]), hashAnalysisFloats(s.D2Angle[:]),
		hashAnalysisFloats(flat(s.E[:])), hashAnalysisFloats(flat(s.LogE[:])), hashAnalysisFloats(s.LowE[:]),
		hashAnalysisFloats(s.HighE[:]), hashAnalysisFloats(s.MeanE[:]), hashAnalysisFloats(s.Mem[:]),
		hashAnalysisFloats(s.CMean[:]), hashAnalysisFloats(s.Std[:]), hashAnalysisFloats(s.RNNState[:]),
	}
}

func diffAnalysisInfo(got, want analysisOracleInfo) string {
	switch {
	case got.valid != want.valid:
		return fmt.Sprintf("valid %d want %d", got.valid, want.valid)
	case got.tonality != want.tonality:
		return fmt.Sprintf("tonality %08x want %08x", got.tonality, want.tonality)
	case got.tonalitySlope != want.tonalitySlope:
		return fmt.Sprintf("tonality_slope %08x want %08x", got.tonalitySlope, want.tonalitySlope)
	case got.noisiness != want.noisiness:
		return fmt.Sprintf("noisiness %08x want %08x", got.noisiness, want.noisiness)
	case got.activity != want.activity:
		return fmt.Sprintf("activity %08x want %08x", got.activity, want.activity)
	case got.musicProb != want.musicProb:
		return fmt.Sprintf("music_prob %08x want %08x", got.musicProb, want.musicProb)
	case got.musicMin != want.musicMin:
		return fmt.Sprintf("music_prob_min %08x want %08x", got.musicMin, want.musicMin)
	case got.musicMax != want.musicMax:
		return fmt.Sprintf("music_prob_max %08x want %08x", got.musicMax, want.musicMax)
	case got.bandwidth != want.bandwidth:
		return fmt.Sprintf("bandwidth %d want %d", got.bandwidth, want.bandwidth)
	case got.activityProb != want.activityProb:
		return fmt.Sprintf("activity_probability %08x want %08x", got.activityProb, want.activityProb)
	case got.maxPitchRatio != want.maxPitchRatio:
		return fmt.Sprintf("max_pitch_ratio %08x want %08x", got.maxPitchRatio, want.maxPitchRatio)
	case got.leakBoost != want.leakBoost:
		return fmt.Sprintf("leak_boost %v want %v", got.leakBoost, want.leakBoost)
	}
	return ""
}

// requireAnalysisMatchesLibopus runs gopus's tonality analysis and libopus
// run_analysis() over the same PCM, frame by frame as opus_encode_native calls
// it, and requires every AnalysisInfo field and the analyzer state to match
// bit for bit.
func requireAnalysisMatchesLibopus(t *testing.T, fs, channels, frameSize int, pcm []float32) {
	t.Helper()
	const lsbDepth = 24
	want := runLibopusAnalysisOracle(t, fs, channels, frameSize, lsbDepth, pcm)
	an := NewTonalityAnalysisState(fs)
	an.SetLSBDepth(lsbDepth)
	for f := range want {
		frame := pcm[f*frameSize*channels : (f+1)*frameSize*channels]
		got := analysisInfoToOracle(an.RunAnalysis(frame, frameSize, channels))
		if d := diffAnalysisInfo(got, want[f].ret); d != "" {
			latestIdx := (int(an.WritePos) - 1 + DetectSize) % DetectSize
			latest := diffAnalysisInfo(analysisInfoToOracle(an.Info[latestIdx]), want[f].latest)
			t.Fatalf("frame %d: returned info differs: %s (latest chunk info: %q)%s", f, d, latest, analysisStateDiff(an, want[f]))
		}
		if s := analysisStateDiff(an, want[f]); s != "" {
			t.Fatalf("frame %d: analyzer state differs:%s", f, s)
		}
	}
}

// TestAnalysisMatchesLibopusLive sweeps the corpus signal classes over every
// analysis sample rate, both channel counts and every frame duration.
func TestAnalysisMatchesLibopusLive(t *testing.T) {
	libopustest.RequireOracle(t)
	classes := []string{testsignal.CorpusMusicV1, testsignal.CorpusCleanSpeechV1, testsignal.CorpusMixedV1,
		testsignal.CorpusSilenceBurstsV1, testsignal.CorpusCastanetTransientV1, testsignal.CorpusPureToneV1}
	for _, fs := range []int{48000, 24000, 16000} {
		for _, channels := range []int{1, 2} {
			for _, ms := range []int{5, 10, 20, 40, 60} {
				for _, class := range classes {
					frameSize := fs * ms / 1000
					name := fmt.Sprintf("%dk/ch%d/%dms/%s", fs/1000, channels, ms, class)
					t.Run(name, func(t *testing.T) {
						numFrames := max(1200/ms, 12)
						pcm, err := testsignal.GenerateCorpusSignal(class, fs, numFrames*frameSize*channels, channels)
						if err != nil {
							t.Fatalf("GenerateCorpusSignal: %v", err)
						}
						requireAnalysisMatchesLibopus(t, fs, channels, frameSize, pcm)
					})
				}
			}
		}
	}
}

// TestAnalysisMatchesLibopusLiveEncoderVariants runs the encoder signal
// variants at 48 kHz, quantized to 24 bits as opus_demo reads float input,
// over one second of audio per frame duration.
func TestAnalysisMatchesLibopusLiveEncoderVariants(t *testing.T) {
	libopustest.RequireOracle(t)
	const fs = 48000
	layouts := []struct{ ms, channels int }{{10, 1}, {20, 1}, {20, 2}, {40, 1}, {60, 1}}
	for _, variant := range testsignal.EncoderSignalVariants() {
		for _, l := range layouts {
			frameSize := fs * l.ms / 1000
			t.Run(fmt.Sprintf("%s/ch%d/%dms", variant, l.channels, l.ms), func(t *testing.T) {
				numFrames := 1000 / l.ms
				pcm, err := testsignal.GenerateEncoderSignalVariant(variant, fs, numFrames*frameSize*l.channels, l.channels)
				if err != nil {
					t.Fatalf("GenerateEncoderSignalVariant: %v", err)
				}
				clampToOpusDemoF32InPlace(pcm)
				requireAnalysisMatchesLibopus(t, fs, l.channels, frameSize, pcm)
			})
		}
	}
}

func analysisStateDiff(an *TonalityAnalysisState, want analysisOracleFrame) string {
	out := ""
	scalars := analysisStateScalars(an)
	for i, v := range scalars {
		if v != want.scalars[i] {
			out += fmt.Sprintf(" %s=%08x want %08x;", analysisOracleScalarNames[i], v, want.scalars[i])
		}
	}
	hashes := analysisStateHashes(an)
	for i, v := range hashes {
		if v != want.hashes[i] {
			out += fmt.Sprintf(" %s hash differs;", analysisOracleHashNames[i])
		}
	}
	return out
}
