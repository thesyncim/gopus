package encoder

import (
	"math"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/types"
)

const (
	NbFrames               = 8
	NbTBands               = 18
	NbTonalSkipBands       = 9
	AnalysisBufSize        = 720 // 30ms at 24kHz
	DetectSize             = 100
	transitionPenalty      = float32(10.0)
	celtSigScale           = float32(32768.0)
	analysisFFTEnergyScale = float32(1.0 / (480.0 * 480.0))
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

	// In libopus float builds, SHR64() is identity, so hp_ener accumulates the
	// raw squared high-pass output (no /256 shift). Keep that behavior here.
	return float32(hpEner)
}

type AnalysisInfo struct {
	Valid            bool
	Tonality         float32
	TonalitySlope    float32
	NoisySpeech      float32
	StationarySpeech float32
	MusicProb        float32
	MusicProbMin     float32
	MusicProbMax     float32
	VADProb          float32
	Loudness         float32
	BandwidthIndex   int
	Bandwidth        types.Bandwidth
	Activity         float32
	MaxPitchRatio    float32
	LeakBoost        [19]uint8
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
	scratchResample3x  []float32
	scratchFFTKiss     []celt.KissCpx
}

func NewTonalityAnalysisState(fs int) *TonalityAnalysisState {
	s := &TonalityAnalysisState{
		Fs:             int32(fs),
		scratchFFTKiss: make([]celt.KissCpx, 480),
	}
	s.Reset()
	return s
}

func (s *TonalityAnalysisState) Reset() {
	s.Count = 0
	s.ECount = 0
	s.MemFill = 0
	// Match libopus tonality_analysis_reset(): initialized is cleared and the
	// first tonalityAnalysis() call bootstraps MemFill=240.
	s.Initialized = false
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

// fft480 computes a 480-point complex forward FFT using the shared CELT KISS FFT.
func fft480(out, in *[480]complex64, scratch []celt.KissCpx) {
	celt.KissFFT32ToWithScratch(out[:], in[:], scratch)
}

func (s *TonalityAnalysisState) tonalityAnalysis(pcm []float32, channels int) {
	if !s.Initialized {
		s.MemFill = 240
		s.Initialized = true
	}
	alphaE := float32(1.0 / float32(min(25, 1+s.Count)))
	alphaE2 := float32(1.0 / float32(min(100, 1+s.Count)))
	if s.Count <= 1 {
		alphaE2 = 1.0
	}

	frameSize := len(pcm) / channels

	// 1. Downmix current frame to mono for analysis
	if cap(s.scratchMono) < frameSize {
		s.scratchMono = make([]float32, frameSize)
	}
	mono := s.scratchMono[:frameSize]
	if channels == 2 {
		for i := 0; i < frameSize; i++ {
			mono[i] = (pcm[2*i] + pcm[2*i+1]) * (0.5 * celtSigScale)
		}
	} else {
		for i := 0; i < frameSize; i++ {
			mono[i] = pcm[i] * celtSigScale
		}
	}

	// 2. Buffer mono samples into InMem
	// tonalityAnalysis is called with a new frame.
	// We need 480 samples at 24kHz for one analysis iteration.
	// But frames can be 2.5, 5, 10, 20ms.

	var (
		analysisLen int
		firstCopy   int
		hpEner      float32
	)
	oldMemFill := s.MemFill
	space := AnalysisBufSize - oldMemFill
	if space < 0 {
		space = 0
	}

	// Match libopus downmix_and_resample split:
	// fill remaining analysis buffer first, and only then process residual.
	switch s.Fs {
	case 48000:
		analysisLen = frameSize / 2
		firstCopy = analysisLen
		if firstCopy > space {
			firstCopy = space
		}
		if firstCopy > 0 {
			if cap(s.scratchDownsampled) < firstCopy {
				s.scratchDownsampled = make([]float32, firstCopy)
			}
			first := s.scratchDownsampled[:firstCopy]
			firstSrc := firstCopy * 2
			hp := silkResamplerDown2HP(s.DownmixState[:], first, mono[:firstSrc])
			hp *= 1.0 / (celtSigScale * celtSigScale)
			s.HPEnerAccum += hp
			copy(s.InMem[oldMemFill:oldMemFill+firstCopy], first)
		}
	case 24000:
		analysisLen = frameSize
		firstCopy = analysisLen
		if firstCopy > space {
			firstCopy = space
		}
		if firstCopy > 0 {
			copy(s.InMem[oldMemFill:oldMemFill+firstCopy], mono[:firstCopy])
		}
	case 16000:
		analysisLen = (frameSize * 3) / 2
		firstCopy = analysisLen
		if firstCopy > space {
			firstCopy = space
		}
		if firstCopy > 0 {
			firstInput := (firstCopy * 2) / 3
			if firstInput > 0 {
				firstOutput := (firstInput * 3) / 2
				if cap(s.scratchDownsampled) < firstOutput {
					s.scratchDownsampled = make([]float32, firstOutput)
				}
				if cap(s.scratchResample3x) < firstInput*3 {
					s.scratchResample3x = make([]float32, firstInput*3)
				}
				first := s.scratchDownsampled[:firstOutput]
				tmp3x := s.scratchResample3x[:firstInput*3]
				for i := 0; i < firstInput; i++ {
					v := mono[i]
					j := 3 * i
					tmp3x[j] = v
					tmp3x[j+1] = v
					tmp3x[j+2] = v
				}
				hp := silkResamplerDown2HP(s.DownmixState[:], first, tmp3x)
				hp *= 1.0 / (celtSigScale * celtSigScale)
				s.HPEnerAccum += hp
				copy(s.InMem[oldMemFill:oldMemFill+firstOutput], first)
				firstCopy = firstOutput
			} else {
				firstCopy = 0
			}
		}
	default:
		// Handle supported float-analysis rates only.
		return
	}

	if oldMemFill+analysisLen < AnalysisBufSize {
		s.MemFill = oldMemFill + analysisLen
		return
	}

	hpEner = s.HPEnerAccum

	var in [480]complex64
	// Use 480 samples from InMem for FFT
	for i := 0; i < 240; i++ {
		w := analysisWindow[i]
		in[i] = complex(w*s.InMem[i], w*s.InMem[240+i])
		in[480-i-1] = complex(w*s.InMem[480-i-1], w*s.InMem[480+240-i-1])
	}
	if cap(s.scratchFFTKiss) < 480 {
		s.scratchFFTKiss = make([]celt.KissCpx, 480)
	}
	var out [480]complex64
	fft480(&out, &in, s.scratchFFTKiss[:480])

	// Shift buffer and keep the residual input for the next analysis step.
	copy(s.InMem[:240], s.InMem[AnalysisBufSize-240:AnalysisBufSize])
	remaining := analysisLen - firstCopy
	switch s.Fs {
	case 48000:
		if remaining > 0 {
			if cap(s.scratchDownsampled) < remaining {
				s.scratchDownsampled = make([]float32, remaining)
			}
			rest := s.scratchDownsampled[:remaining]
			restSrcStart := firstCopy * 2
			restSrcEnd := restSrcStart + remaining*2
			hp := silkResamplerDown2HP(s.DownmixState[:], rest, mono[restSrcStart:restSrcEnd])
			hp *= 1.0 / (celtSigScale * celtSigScale)
			s.HPEnerAccum = hp
			copy(s.InMem[240:240+remaining], rest)
		} else {
			s.HPEnerAccum = 0
		}
	case 24000:
		if remaining > 0 {
			copy(s.InMem[240:240+remaining], mono[firstCopy:firstCopy+remaining])
		}
		s.HPEnerAccum = 0
	case 16000:
		if remaining > 0 {
			restInput := (remaining * 2) / 3
			restSrcStart := (firstCopy * 2) / 3
			restSrcEnd := restSrcStart + restInput
			if restSrcEnd > frameSize {
				restSrcEnd = frameSize
			}
			restInput = restSrcEnd - restSrcStart
			if restInput > 0 {
				restOutput := (restInput * 3) / 2
				if cap(s.scratchDownsampled) < restOutput {
					s.scratchDownsampled = make([]float32, restOutput)
				}
				if cap(s.scratchResample3x) < restInput*3 {
					s.scratchResample3x = make([]float32, restInput*3)
				}
				rest := s.scratchDownsampled[:restOutput]
				tmp3x := s.scratchResample3x[:restInput*3]
				for i := 0; i < restInput; i++ {
					v := mono[restSrcStart+i]
					j := 3 * i
					tmp3x[j] = v
					tmp3x[j+1] = v
					tmp3x[j+2] = v
				}
				hp := silkResamplerDown2HP(s.DownmixState[:], rest, tmp3x)
				hp *= 1.0 / (celtSigScale * celtSigScale)
				s.HPEnerAccum = hp
				copy(s.InMem[240:240+restOutput], rest)
				remaining = restOutput
			} else {
				remaining = 0
				s.HPEnerAccum = 0
			}
		} else {
			s.HPEnerAccum = 0
		}
	}
	s.MemFill = 240 + remaining

	var logE [NbTBands]float32
	var bandLog2 [NbTBands + 1]float32
	var leakageFrom [NbTBands + 1]float32
	var leakageTo [NbTBands + 1]float32
	var BFCC [8]float32
	var features [25]float32
	var masked [NbTBands + 1]bool
	const (
		log2Scale      = float32(0.7213475) // 0.5*log2(e)
		leakageOffset  = float32(2.5)
		leakageSlope   = float32(2.0)
		leakageDivisor = float32(4.0)
	)

	var tonality [240]float32
	var tonality2 [240]float32
	var noisiness [240]float32
	const (
		atanScale = float32(0.5 / math.Pi)
		pi4       = float32(math.Pi * math.Pi * math.Pi * math.Pi)
	)
	for i := 1; i < 240; i++ {
		x1r := real(out[i]) + real(out[480-i])
		x1i := imag(out[i]) - imag(out[480-i])
		x2r := imag(out[i]) + imag(out[480-i])
		x2i := real(out[480-i]) - real(out[i])

		angle := atanScale * float32(math.Atan2(float64(x1i), float64(x1r)))
		dAngle := angle - s.Angle[i]
		d2Angle := dAngle - s.DAngle[i]

		angle2 := atanScale * float32(math.Atan2(float64(x2i), float64(x2r)))
		dAngle2 := angle2 - angle
		d2Angle2 := dAngle2 - dAngle

		mod1 := d2Angle - float32(math.Round(float64(d2Angle)))
		if mod1 < 0 {
			noisiness[i] = -mod1
		} else {
			noisiness[i] = mod1
		}
		mod1 *= mod1
		mod1 *= mod1

		mod2 := d2Angle2 - float32(math.Round(float64(d2Angle2)))
		if mod2 < 0 {
			noisiness[i] += -mod2
		} else {
			noisiness[i] += mod2
		}
		mod2 *= mod2
		mod2 *= mod2

		avgMod := 0.25 * (s.D2Angle[i] + mod1 + 2*mod2)
		tonality[i] = 1.0/(1.0+40.0*16.0*pi4*avgMod) - 0.015
		tonality2[i] = 1.0/(1.0+40.0*16.0*pi4*mod2) - 0.015

		s.Angle[i] = angle2
		s.DAngle[i] = dAngle2
		s.D2Angle[i] = mod2
	}
	for i := 2; i < 239; i++ {
		tt := minf(tonality2[i], maxf(tonality2[i-1], tonality2[i+1]))
		tonality[i] = 0.9 * maxf(tonality[i], tt-0.1)
	}

	frameNoisiness := float32(0)
	frameStationarity := float32(0)
	frameTonality := float32(0)
	maxFrameTonality := float32(0)
	relativeE := float32(0)
	frameLoudness := float32(0)
	slope := float32(0)
	bandwidthMask := float32(0)
	bandwidth := 0
	maxE := float32(0)
	noiseFloor := float32(5.7e-4 / float64(uint(1)<<uint(24-8)))
	belowMaxPitch := float32(0)
	aboveMaxPitch := float32(0)
	var bandTonality [NbTBands]float32
	maxPitchRatio := float32(1.0)

	if s.Count == 0 {
		for b := 0; b < NbTBands; b++ {
			s.LowE[b] = 1e10
			s.HighE[b] = -1e10
		}
	}
	noiseFloor *= noiseFloor

	// Match libopus special handling for the first band (DC/Nyquist bins).
	{
		x1r := 2 * real(out[0])
		x2r := 2 * imag(out[0])
		E := x1r*x1r + x2r*x2r
		for i := 1; i < 4; i++ {
			binE := real(out[i])*real(out[i]) + real(out[480-i])*real(out[480-i]) +
				imag(out[i])*imag(out[i]) + imag(out[480-i])*imag(out[480-i])
			E += binE
		}
		E *= (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale
		bandLog2[0] = log2Scale * float32(math.Log(float64(E)+1e-10))
	}

	// Band energies and tonal metrics.
	for b := 0; b < NbTBands; b++ {
		var bandE, tE, nE float32
		for i := tbands[b]; i < tbands[b+1]; i++ {
			binE := real(out[i])*real(out[i]) + real(out[480-i])*real(out[480-i]) +
				imag(out[i])*imag(out[i]) + imag(out[480-i])*imag(out[480-i])
			binE *= (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale
			bandE += binE
			tE += binE * maxf(0, tonality[i])
			nE += binE * 2.0 * (0.5 - noisiness[i])
		}

		s.E[s.ECount][b] = bandE
		logE[b] = float32(math.Log(float64(bandE) + 1e-10))
		bandLog2[b+1] = log2Scale * float32(math.Log(float64(bandE)+1e-10))
		s.LogE[s.ECount][b] = logE[b]

		frameNoisiness += nE / (1e-15 + bandE)
		frameLoudness += float32(math.Sqrt(float64(bandE + 1e-10)))

		if s.Count == 0 {
			s.HighE[b] = logE[b]
			s.LowE[b] = logE[b]
		}
		if s.HighE[b] > s.LowE[b]+7.5 {
			if s.HighE[b]-logE[b] > logE[b]-s.LowE[b] {
				s.HighE[b] -= 0.01
			} else {
				s.LowE[b] += 0.01
			}
		}
		if logE[b] > s.HighE[b] {
			s.HighE[b] = logE[b]
			s.LowE[b] = maxf(s.LowE[b], s.HighE[b]-15)
		} else if logE[b] < s.LowE[b] {
			s.LowE[b] = logE[b]
			s.HighE[b] = minf(s.HighE[b], s.LowE[b]+15)
		}
		relativeE += (logE[b] - s.LowE[b]) / (1e-5 + (s.HighE[b] - s.LowE[b]))

		var L1, L2 float32
		for i := 0; i < NbFrames; i++ {
			L1 += float32(math.Sqrt(float64(s.E[i][b])))
			L2 += s.E[i][b]
		}
		stationarity := minf(0.99, L1/float32(math.Sqrt(float64(1e-15+float32(NbFrames)*L2))))
		stationarity *= stationarity
		stationarity *= stationarity
		frameStationarity += stationarity

		bandTonality[b] = maxf(tE/(1e-15+bandE), stationarity*s.PrevBandTonality[b])
		frameTonality += bandTonality[b]
		if b >= NbTBands-NbTonalSkipBands {
			frameTonality -= bandTonality[b-NbTBands+NbTonalSkipBands]
		}
		maxFrameTonality = maxf(maxFrameTonality, (1.0+0.03*float32(b-NbTBands))*frameTonality)
		slope += bandTonality[b] * float32(b-8)
		s.PrevBandTonality[b] = bandTonality[b]
	}

	// Compute analysis leak_boost[] exactly as libopus analysis.c does.
	leakageFrom[0] = bandLog2[0]
	leakageTo[0] = bandLog2[0] - leakageOffset
	for b := 1; b < NbTBands+1; b++ {
		leakSlope := leakageSlope * float32(tbands[b]-tbands[b-1]) / leakageDivisor
		leakageFrom[b] = minf(leakageFrom[b-1]+leakSlope, bandLog2[b])
		leakageTo[b] = maxf(leakageTo[b-1]-leakSlope, bandLog2[b]-leakageOffset)
	}
	for b := NbTBands - 2; b >= 0; b-- {
		leakSlope := leakageSlope * float32(tbands[b+1]-tbands[b]) / leakageDivisor
		leakageFrom[b] = minf(leakageFrom[b], leakageFrom[b+1]+leakSlope)
		leakageTo[b] = maxf(leakageTo[b], leakageTo[b+1]-leakSlope)
	}

	for b := 0; b < NbTBands; b++ {
		bandStart := tbands[b]
		bandEnd := tbands[b+1]
		E := float32(0)
		for i := bandStart; i < bandEnd; i++ {
			binE := real(out[i])*real(out[i]) + real(out[480-i])*real(out[480-i]) +
				imag(out[i])*imag(out[i]) + imag(out[480-i])*imag(out[480-i])
			E += binE
		}
		E *= (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale
		maxE = maxf(maxE, E)
		if bandStart < 64 {
			belowMaxPitch += E
		} else {
			aboveMaxPitch += E
		}
		s.MeanE[b] = maxf((1.0-alphaE2)*s.MeanE[b], E)
		Em := maxf(E, s.MeanE[b])
		width := float32(bandEnd - bandStart)
		if E*1e9 > maxE && (Em > 3*noiseFloor*width || E > noiseFloor*width) {
			bandwidth = b + 1
		}
		maskThresh := float32(0.05)
		if s.PrevBandwidth >= b+1 {
			maskThresh = 0.01
		}
		masked[b] = E < maskThresh*bandwidthMask
		bandwidthMask = maxf(0.05*bandwidthMask, E)
	}
	if s.Fs == 48000 {
		E := hpEner * (1.0 / (60.0 * 60.0))
		noiseRatio := float32(30.0)
		if s.PrevBandwidth == 20 {
			noiseRatio = 10.0
		}
		aboveMaxPitch += E
		s.MeanE[NbTBands] = maxf((1.0-alphaE2)*s.MeanE[NbTBands], E)
		Em := maxf(E, s.MeanE[NbTBands])
		if Em > 3*noiseRatio*noiseFloor*160 || E > noiseRatio*noiseFloor*160 {
			bandwidth = 20
		}
		maskThresh := float32(0.05)
		if s.PrevBandwidth == 20 {
			maskThresh = 0.01
		}
		masked[NbTBands] = E < maskThresh*bandwidthMask
	}
	if aboveMaxPitch > belowMaxPitch {
		maxPitchRatio = belowMaxPitch / aboveMaxPitch
	}
	if bandwidth == 20 && masked[NbTBands] {
		bandwidth -= 2
	} else if bandwidth > 0 && bandwidth <= NbTBands && masked[bandwidth-1] {
		bandwidth--
	}
	if s.Count <= 2 {
		bandwidth = 20
	}

	frameLoudness = 20.0 * float32(math.Log10(float64(frameLoudness)))
	s.ETracker = maxf(s.ETracker-0.003, frameLoudness)
	s.LowECount *= 1.0 - alphaE
	if frameLoudness < s.ETracker-30.0 {
		s.LowECount += alphaE
	}

	// BFCC extraction
	for i := 0; i < 8; i++ {
		var sum float32
		for b := 0; b < 16; b++ {
			sum += dctTable[i*16+b] * logE[b]
		}
		BFCC[i] = sum
	}
	frameStationarity /= NbTBands
	relativeE /= NbTBands
	if s.Count < 10 {
		relativeE = 0.5
	}
	frameNoisiness /= NbTBands

	activity := frameNoisiness + (1.0-frameNoisiness)*relativeE
	frameTonality = maxFrameTonality / float32(NbTBands-NbTonalSkipBands)
	frameTonality = maxf(frameTonality, s.PrevTonality*0.8)
	s.PrevTonality = frameTonality
	slope /= 64.0

	s.ECount = (s.ECount + 1) % NbFrames
	s.Count = min(s.Count+1, 10000)

	info := &s.Info[s.WritePos]
	info.Valid = true
	info.Tonality = frameTonality
	info.TonalitySlope = slope
	info.NoisySpeech = frameNoisiness
	info.StationarySpeech = frameStationarity
	info.Activity = activity
	info.MaxPitchRatio = maxPitchRatio
	info.BandwidthIndex = bandwidth
	info.Bandwidth = bandwidthTypeFromIndex(bandwidth)
	info.Loudness = frameLoudness

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
	info.MusicProb = frameProbs[0]
	info.VADProb = frameProbs[1]
	for b := 0; b < NbTBands+1; b++ {
		boost := maxf(0, leakageTo[b]-bandLog2[b]) + maxf(0, bandLog2[b]-(leakageFrom[b]+leakageOffset))
		q6 := int(math.Floor(0.5 + 64.0*float64(boost)))
		if q6 > 255 {
			q6 = 255
		}
		if q6 < 0 {
			q6 = 0
		}
		info.LeakBoost[b] = uint8(q6)
	}
	s.PrevBandwidth = bandwidth
	s.WritePos = (s.WritePos + 1) % DetectSize
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

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// tonalityGetInfo mirrors libopus tonality_get_info() and derives the
// smoothed music-probability thresholds used for mode switching.
func (s *TonalityAnalysisState) tonalityGetInfo(frameSize int) AnalysisInfo {
	out := AnalysisInfo{}

	pos := s.ReadPos
	currLookahead := s.WritePos - s.ReadPos
	if currLookahead < 0 {
		currLookahead += DetectSize
	}

	subframe := int(s.Fs) / 400
	if subframe <= 0 {
		subframe = 1
	}
	s.ReadSubframe += frameSize / subframe
	for s.ReadSubframe >= 8 {
		s.ReadSubframe -= 8
		s.ReadPos++
	}
	if s.ReadPos >= DetectSize {
		s.ReadPos -= DetectSize
	}

	// On long frames, inspect the second analysis window.
	if frameSize > int(s.Fs)/50 && pos != s.WritePos {
		pos++
		if pos == DetectSize {
			pos = 0
		}
	}
	if pos == s.WritePos {
		pos--
	}
	if pos < 0 {
		pos = DetectSize - 1
	}
	pos0 := pos

	out = s.Info[pos]
	if !out.Valid {
		return out
	}

	tonalityMax := out.Tonality
	tonalityAvg := out.Tonality
	tonalityCount := 1
	bandwidthSpan := 6

	// Look ahead for tonality and safe bandwidth.
	for i := 0; i < 3; i++ {
		pos++
		if pos == DetectSize {
			pos = 0
		}
		if pos == s.WritePos {
			break
		}
		if s.Info[pos].Tonality > tonalityMax {
			tonalityMax = s.Info[pos].Tonality
		}
		tonalityAvg += s.Info[pos].Tonality
		tonalityCount++
		if s.Info[pos].BandwidthIndex > out.BandwidthIndex {
			out.BandwidthIndex = s.Info[pos].BandwidthIndex
		}
		bandwidthSpan--
	}

	// Look back for wider bandwidth evidence.
	pos = pos0
	for i := 0; i < bandwidthSpan; i++ {
		pos--
		if pos < 0 {
			pos = DetectSize - 1
		}
		if pos == s.WritePos {
			break
		}
		if s.Info[pos].BandwidthIndex > out.BandwidthIndex {
			out.BandwidthIndex = s.Info[pos].BandwidthIndex
		}
	}
	out.Bandwidth = bandwidthTypeFromIndex(out.BandwidthIndex)

	tonalityMean := tonalityAvg / float32(tonalityCount)
	out.Tonality = maxf(tonalityMean, tonalityMax-0.2)

	mpos := pos0
	vpos := pos0
	// Compensate music-prob (~5 frames) and VAD (~1 frame) delay when lookahead exists.
	if currLookahead > 15 {
		mpos += 5
		if mpos >= DetectSize {
			mpos -= DetectSize
		}
		vpos++
		if vpos >= DetectSize {
			vpos -= DetectSize
		}
	}

	probMin := float32(1.0)
	probMax := float32(0.0)
	vadProb := s.Info[vpos].VADProb
	activityWeight := maxf(0.1, vadProb)
	probCount := activityWeight
	probAvg := activityWeight * s.Info[mpos].MusicProb

	for {
		mpos++
		if mpos == DetectSize {
			mpos = 0
		}
		if mpos == s.WritePos {
			break
		}
		vpos++
		if vpos == DetectSize {
			vpos = 0
		}
		if vpos == s.WritePos {
			break
		}

		posVAD := s.Info[vpos].VADProb
		posWeight := maxf(0.1, posVAD)
		denom := probCount
		if denom < 1e-9 {
			denom = 1e-9
		}
		probMin = minf((probAvg-transitionPenalty*(vadProb-posVAD))/denom, probMin)
		probMax = maxf((probAvg+transitionPenalty*(vadProb-posVAD))/denom, probMax)

		probCount += posWeight
		probAvg += posWeight * s.Info[mpos].MusicProb
	}

	if probCount < 1e-9 {
		probCount = 1e-9
	}
	out.MusicProb = probAvg / probCount
	probMin = minf(out.MusicProb, probMin)
	probMax = maxf(out.MusicProb, probMax)
	probMin = maxf(probMin, 0.0)
	probMax = minf(probMax, 1.0)

	// With little/no lookahead, use recent history as fallback.
	if currLookahead < 10 {
		pmin := probMin
		pmax := probMax
		pos = pos0
		history := s.Count - 1
		if history > 15 {
			history = 15
		}
		if history < 0 {
			history = 0
		}
		for i := 0; i < history; i++ {
			pos--
			if pos < 0 {
				pos = DetectSize - 1
			}
			pmin = minf(pmin, s.Info[pos].MusicProb)
			pmax = maxf(pmax, s.Info[pos].MusicProb)
		}

		pmin = maxf(0.0, pmin-0.1*vadProb)
		pmax = minf(1.0, pmax+0.1*vadProb)
		blend := float32(1.0) - 0.1*float32(currLookahead)
		probMin += blend * (pmin - probMin)
		probMax += blend * (pmax - probMax)
	}

	out.MusicProbMin = probMin
	out.MusicProbMax = probMax

	return out
}

func (s *TonalityAnalysisState) RunAnalysis(pcm []float32, frameSize int, channels int) AnalysisInfo {
	if channels <= 0 {
		channels = 1
	}

	analysisFrameSize := 0
	if len(pcm) > 0 {
		analysisFrameSize = len(pcm) / channels
		analysisFrameSize -= analysisFrameSize & 1
		maxAnalysisFrameSize := (DetectSize - 5) * int(s.Fs) / 50
		if maxAnalysisFrameSize > 0 && analysisFrameSize > maxAnalysisFrameSize {
			analysisFrameSize = maxAnalysisFrameSize
		}
	}

	if analysisFrameSize > 0 {
		pcmLen := analysisFrameSize - s.AnalysisOffset
		offset := s.AnalysisOffset
		chunkSize := int(s.Fs) / 50
		if chunkSize <= 0 {
			chunkSize = analysisFrameSize
		}

		for pcmLen > 0 {
			// libopus can pass negative offsets when analysis uses external
			// lookahead. Skip unavailable prefix when only current PCM is present.
			if offset < 0 {
				advance := -offset
				if advance > pcmLen {
					advance = pcmLen
				}
				offset += advance
				pcmLen -= advance
				continue
			}

			chunk := chunkSize
			if chunk > pcmLen {
				chunk = pcmLen
			}
			if chunk <= 0 {
				break
			}

			start := offset * channels
			if start >= len(pcm) {
				break
			}
			end := start + chunk*channels
			if end > len(pcm) {
				end = len(pcm)
			}
			if end > start {
				s.tonalityAnalysis(pcm[start:end], channels)
			}

			offset += chunkSize
			pcmLen -= chunkSize
		}

		s.AnalysisOffset = analysisFrameSize - frameSize
	}

	return s.tonalityGetInfo(frameSize)
}

func (s *TonalityAnalysisState) GetInfo() AnalysisInfo {
	readPos := (s.WritePos + DetectSize - 1) % DetectSize
	return s.Info[readPos]
}
