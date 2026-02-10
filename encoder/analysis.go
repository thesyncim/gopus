package encoder

import (
	"math"

	"github.com/thesyncim/gopus/types"
)

const (
	NbFrames        = 8
	NbTBands        = 18
	AnalysisBufSize = 720 // 30ms at 24kHz
	DetectSize      = 100
)

var dctTable = [128]float32{
	0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000,
	0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000,
	0.351851, 0.338330, 0.311806, 0.273300, 0.224292, 0.166664, 0.102631, 0.034654,
	-0.034654, -0.102631, -0.166664, -0.224292, -0.273300, -0.311806, -0.338330, -0.351851,
	0.346760, 0.293969, 0.196424, 0.068975, -0.068975, -0.196424, -0.293969, -0.346760,
	-0.346760, -0.293969, -0.196424, -0.068975, 0.068975, 0.196424, 0.293969, 0.346760,
	0.338330, 0.224292, 0.034654, -0.166664, -0.311806, -0.351851, -0.273300, -0.102631,
	0.102631, 0.273300, 0.351851, 0.311806, 0.166664, -0.034654, -0.224292, -0.338330,
	0.326641, 0.135299, -0.135299, -0.326641, -0.326641, -0.135299, 0.135299, 0.326641,
	0.326641, 0.135299, -0.135299, -0.326641, -0.326641, -0.135299, 0.135299, 0.326641,
	0.311806, 0.034654, -0.273300, -0.338330, -0.102631, 0.224292, 0.351851, 0.166664,
	-0.166664, -0.351851, -0.224292, 0.102631, 0.338330, 0.273300, -0.034654, -0.311806,
	0.293969, -0.068975, -0.346760, -0.196424, 0.196424, 0.346760, 0.068975, -0.293969,
	-0.293969, 0.068975, 0.346760, 0.196424, -0.196424, -0.346760, -0.068975, 0.293969,
	0.273300, -0.166664, -0.338330, 0.034654, 0.351851, 0.102631, -0.311806, -0.224292,
	0.224292, 0.311806, -0.102631, -0.351851, -0.034654, 0.338330, 0.166664, -0.273300,
}

var analysisWindow = [240]float32{
	0.000043, 0.000171, 0.000385, 0.000685, 0.001071, 0.001541, 0.002098, 0.002739,
	0.003466, 0.004278, 0.005174, 0.006156, 0.007222, 0.008373, 0.009607, 0.010926,
	0.012329, 0.013815, 0.015385, 0.017037, 0.018772, 0.020590, 0.022490, 0.024472,
	0.026535, 0.028679, 0.030904, 0.033210, 0.035595, 0.038060, 0.040604, 0.043227,
	0.045928, 0.048707, 0.051564, 0.054497, 0.057506, 0.060591, 0.063752, 0.066987,
	0.070297, 0.073680, 0.077136, 0.080665, 0.084265, 0.087937, 0.091679, 0.095492,
	0.099373, 0.103323, 0.107342, 0.111427, 0.115579, 0.119797, 0.124080, 0.128428,
	0.132839, 0.137313, 0.141849, 0.146447, 0.151105, 0.155823, 0.160600, 0.165435,
	0.170327, 0.175276, 0.180280, 0.185340, 0.190453, 0.195619, 0.200838, 0.206107,
	0.211427, 0.216797, 0.222215, 0.227680, 0.233193, 0.238751, 0.244353, 0.250000,
	0.255689, 0.261421, 0.267193, 0.273005, 0.278856, 0.284744, 0.290670, 0.296632,
	0.302628, 0.308658, 0.314721, 0.320816, 0.326941, 0.333097, 0.339280, 0.345492,
	0.351729, 0.357992, 0.364280, 0.370590, 0.376923, 0.383277, 0.389651, 0.396044,
	0.402455, 0.408882, 0.415325, 0.421783, 0.428254, 0.434737, 0.441231, 0.447736,
	0.454249, 0.460770, 0.467298, 0.473832, 0.480370, 0.486912, 0.493455, 0.500000,
	0.506545, 0.513088, 0.519630, 0.526168, 0.532702, 0.539230, 0.545751, 0.552264,
	0.558769, 0.565263, 0.571746, 0.578217, 0.584675, 0.591118, 0.597545, 0.603956,
	0.610349, 0.616723, 0.623077, 0.629410, 0.635720, 0.642008, 0.648271, 0.654508,
	0.660720, 0.666903, 0.673059, 0.679184, 0.685279, 0.691342, 0.697372, 0.703368,
	0.709330, 0.715256, 0.721144, 0.726995, 0.732807, 0.738579, 0.744311, 0.750000,
	0.755647, 0.761249, 0.766807, 0.772320, 0.777785, 0.783203, 0.788573, 0.793893,
	0.799162, 0.804381, 0.809547, 0.814660, 0.819720, 0.824724, 0.829673, 0.834565,
	0.839400, 0.844177, 0.848895, 0.853553, 0.858151, 0.862687, 0.867161, 0.871572,
	0.875920, 0.880203, 0.884421, 0.888573, 0.892658, 0.896677, 0.900627, 0.904508,
	0.908321, 0.912063, 0.915735, 0.919335, 0.922864, 0.926320, 0.929703, 0.933013,
	0.936248, 0.939409, 0.942494, 0.945503, 0.948436, 0.951293, 0.954072, 0.956773,
	0.959396, 0.961940, 0.964405, 0.966790, 0.969096, 0.971321, 0.973465, 0.975528,
	0.977510, 0.979410, 0.981228, 0.982963, 0.984615, 0.986185, 0.987671, 0.989074,
	0.990393, 0.991627, 0.992778, 0.993844, 0.994826, 0.995722, 0.996534, 0.997261,
	0.997902, 0.998459, 0.998929, 0.999315, 0.999615, 0.999829, 0.999957, 1.000000,
}

var tbands = [NbTBands + 1]int{
	4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 136, 160, 192, 240,
}

func silkResamplerDown2HP(s []float32, out []float32, in []float32) float32 {
	len2 := len(in) / 2
	if len(out) < len2 {
		len2 = len(out)
	}
	if len2 <= 0 {
		return 0
	}
	// BCE hints: ensure all accesses are in bounds
	_ = in[2*len2-1]
	_ = out[len2-1]
	_ = s[2]

	// Hoist filter state into locals to avoid repeated slice access
	s0, s1, s2 := s[0], s[1], s[2]

	// Use float32 constants to avoid repeated float64 conversion
	const (
		coef0 = float32(0.6074371)
		coef1 = float32(0.15063)
	)

	var hpEner float64
	for k := 0; k < len2; k++ {
		in32 := in[2*k]
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = in[2*k+1]
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += float64(out32HP * out32HP)
		out[k] = 0.5 * out32
	}

	// Write back filter state
	s[0], s[1], s[2] = s0, s1, s2

	return float32(hpEner / 256.0)
}

type AnalysisInfo struct {
	Valid            bool
	Tonality         float32
	TonalitySlope    float32
	NoisySpeech      float32
	StationarySpeech float32
	MusicProb        float32
	VADProb          float32
	Loudness         float32
	BandwidthIndex   int
	Bandwidth        types.Bandwidth
	Activity         float32
	MaxPitchRatio    float32
}

type TonalityAnalysisState struct {
	Fs               int32
	Angle            [240]float32
	DAngle           [240]float32
	D2Angle          [240]float32
	InMem            [AnalysisBufSize]float32
	MemFill          int
	PrevBandTonality [NbTBands]float32
	PrevTonality     float32
	PrevBandwidth    int
	E                [NbFrames][NbTBands]float32
	LogE             [NbFrames][NbTBands]float32
	LowE             [NbTBands]float32
	HighE            [NbTBands]float32
	MeanE            [NbTBands + 1]float32
	Mem              [32]float32
	CMean            [8]float32
	Std              [9]float32
	ETracker         float32
	LowECount        float32
	ECount           int
	Count            int
	AnalysisOffset   int
	WritePos         int
	ReadPos          int
	ReadSubframe     int
	HPEnerAccum      float32
	Initialized      bool
	RNNState         [MaxNeurons]float32
	DownmixState     [3]float32
	Info             [DetectSize]AnalysisInfo

	// Scratch buffers for zero-allocation analysis
	scratchMono        []float32
	scratchDownsampled []float32
}

func NewTonalityAnalysisState(fs int) *TonalityAnalysisState {
	s := &TonalityAnalysisState{
		Fs: int32(fs),
	}
	s.Reset()
	return s
}

func (s *TonalityAnalysisState) Reset() {
	s.Count = 0
	s.ECount = 0
	s.MemFill = 0
	s.Initialized = true
	s.AnalysisOffset = 0
	s.WritePos = 0
	s.ReadPos = 0
	s.ReadSubframe = 0
	s.HPEnerAccum = 0
	for i := range s.RNNState {
		s.RNNState[i] = 0
	}
	for i := range s.InMem {
		s.InMem[i] = 0
	}
}

func (s *TonalityAnalysisState) tonalityAnalysis(pcm []float32, channels int) {
	if !s.Initialized {
		s.MemFill = 240
		s.Initialized = true
	}

	frameSize := len(pcm) / channels

	// Ensure we only process frames we can handle (standard Opus frame sizes)
	if frameSize < 120 {
		return
	}

	// 1. Downmix current frame to mono for analysis
	if cap(s.scratchMono) < frameSize {
		s.scratchMono = make([]float32, frameSize)
	}
	mono := s.scratchMono[:frameSize]
	if channels == 2 {
		for i := 0; i < frameSize; i++ {
			mono[i] = (pcm[2*i] + pcm[2*i+1]) * 0.5
		}
	} else {
		copy(mono, pcm)
	}

	// 2. Buffer mono samples into InMem
	// tonalityAnalysis is called with a new frame.
	// We need 480 samples at 24kHz for one analysis iteration.
	// But frames can be 2.5, 5, 10, 20ms.

	// libopus downsamples to 24kHz before buffering.
	// 48kHz mono -> 24kHz downsampled
	var downsampledBuf []float32
	var hpEner float32
	if s.Fs == 48000 {
		if cap(s.scratchDownsampled) < frameSize/2 {
			s.scratchDownsampled = make([]float32, frameSize/2)
		}
		downsampledBuf = s.scratchDownsampled[:frameSize/2]
		hpEner = silkResamplerDown2HP(s.DownmixState[:], downsampledBuf, mono)
	} else if s.Fs == 24000 {
		downsampledBuf = mono
	} else {
		// handle other rates...
		return
	}

	// Copy to circular InMem buffer
	for i := 0; i < len(downsampledBuf); i++ {
		if s.MemFill < AnalysisBufSize {
			s.InMem[s.MemFill] = downsampledBuf[i]
			s.MemFill++
		}
	}

	// Only proceed if we have 480 new samples (analysis frame size)
	if s.MemFill < 480 {
		return
	}

	alphaE := 1.0 / float32(min(25, 1+s.Count))

	var out [480]complex64
	// Use 480 samples from InMem for FFT
	for i := 0; i < 240; i++ {
		w := analysisWindow[i]
		out[i] = complex(w*s.InMem[i], w*s.InMem[240+i])
		out[480-i-1] = complex(w*s.InMem[480-i-1], w*s.InMem[480+240-i-1])
	}

	// Shift buffer left by 240 (libopus parity)
	copy(s.InMem[:], s.InMem[240:])
	s.MemFill -= 240

	var logE [NbTBands]float32
	var BFCC [8]float32
	var features [25]float32
	var bandEnergy [NbTBands]float32
	var maxBandEnergy float32

	// Band energies
	for b := 0; b < NbTBands; b++ {
		var bandE float32
		for i := tbands[b]; i < tbands[b+1]; i++ {
			magSq := real(out[i])*real(out[i]) + imag(out[i])*imag(out[i]) +
				real(out[480-i])*real(out[480-i]) + imag(out[480-i])*imag(out[480-i])
			bandE += magSq
		}
		s.E[s.ECount][b] = bandE
		bandEnergy[b] = bandE
		if bandE > maxBandEnergy {
			maxBandEnergy = bandE
		}
		logE[b] = float32(math.Log(float64(bandE) + 1e-10))
		s.LogE[s.ECount][b] = logE[b]
	}

	// BFCC extraction
	for i := 0; i < 8; i++ {
		var sum float32
		for b := 0; b < 16; b++ {
			sum += dctTable[i*16+b] * logE[b]
		}
		BFCC[i] = sum
	}

	// Feature assembly (simplified)
	for i := 0; i < 8; i++ {
		features[i] = BFCC[i]
		s.Mem[i] = BFCC[i]
	}

	// Run MLP
	var layerOut [32]float32
	var frameProbs [2]float32
	layer0.ComputeDense(layerOut[:], features[:])
	layer1.ComputeGRU(s.RNNState[:], layerOut[:])
	layer2.ComputeDense(frameProbs[:], s.RNNState[:])

	info := &s.Info[s.WritePos]
	info.Valid = true
	info.MusicProb = frameProbs[0]
	info.VADProb = frameProbs[1]
	info.Activity = alphaE*frameProbs[1] + (1-alphaE)*info.Activity
	info.BandwidthIndex = detectBandwidthIndex(bandEnergy[:], maxBandEnergy, s.PrevBandwidth, hpEner, int(s.Fs), 24, s.Count)
	info.Bandwidth = bandwidthTypeFromIndex(info.BandwidthIndex)
	s.PrevBandwidth = info.BandwidthIndex

	s.WritePos = (s.WritePos + 1) % DetectSize
	s.Count = min(s.Count+1, 10000)
	s.ECount = (s.ECount + 1) % NbFrames
}

func bandwidthTypeFromIndex(bandwidth int) types.Bandwidth {
	switch {
	case bandwidth <= 12:
		return types.BandwidthNarrowband
	case bandwidth <= 14:
		return types.BandwidthMediumband
	case bandwidth <= 16:
		return types.BandwidthWideband
	case bandwidth <= 18:
		return types.BandwidthSuperwideband
	default:
		return types.BandwidthFullband
	}
}

func detectBandwidthIndex(bandEnergy []float32, maxBandEnergy float32, prevBandwidth int, hpEner float32, fs int, lsbDepth int, frameCount int) int {
	if len(bandEnergy) == 0 {
		return 20
	}
	if lsbDepth < 8 {
		lsbDepth = 8
	}
	noiseFloor := float32(5.7e-4 / float64(uint(1)<<uint(lsbDepth-8)))
	noiseFloor *= noiseFloor

	bandwidthMask := float32(0)
	bandwidth := 0
	var masked [NbTBands + 1]bool

	for b := 0; b < len(bandEnergy); b++ {
		E := bandEnergy[b]
		width := tbands[b+1] - tbands[b]
		if width < 1 {
			width = 1
		}
		if E*1e9 > maxBandEnergy && E > noiseFloor*float32(width) {
			bandwidth = b + 1
		}
		maskThresh := float32(0.05)
		if prevBandwidth >= b+1 {
			maskThresh = 0.01
		}
		masked[b] = E < maskThresh*bandwidthMask
		bandwidthMask = maxf(0.05*bandwidthMask, E)
	}

	// High-band detector from the downsampler high-pass residual (48kHz path).
	if fs == 48000 {
		E := hpEner * (1.0 / (60.0 * 60.0))
		noiseRatio := float32(30.0)
		if prevBandwidth == 20 {
			noiseRatio = 10.0
		}
		if E > noiseRatio*noiseFloor*160 {
			bandwidth = 20
		}
		maskThresh := float32(0.05)
		if prevBandwidth == 20 {
			maskThresh = 0.01
		}
		masked[NbTBands] = E < maskThresh*bandwidthMask
	}

	if bandwidth == 20 && masked[NbTBands] {
		bandwidth -= 2
	} else if bandwidth > 0 && bandwidth <= NbTBands && masked[bandwidth-1] {
		bandwidth--
	}
	if frameCount <= 2 {
		bandwidth = 20
	}
	if bandwidth < 1 {
		bandwidth = 1
	}
	if bandwidth > 20 {
		bandwidth = 20
	}
	return bandwidth
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func (s *TonalityAnalysisState) RunAnalysis(pcm []float32, frameSize int, channels int) AnalysisInfo {
	s.tonalityAnalysis(pcm, channels)
	readPos := (s.WritePos + DetectSize - 1) % DetectSize
	return s.Info[readPos]
}

func (s *TonalityAnalysisState) GetInfo() AnalysisInfo {
	readPos := (s.WritePos + DetectSize - 1) % DetectSize
	return s.Info[readPos]
}
