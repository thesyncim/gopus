package dred

import "math"

const (
	// ResamplingOrder mirrors libopus RESAMPLING_ORDER in dred_encoder.h.
	ResamplingOrder = 8
	// MaxConvert16kBuffer mirrors libopus MAX_DOWNMIX_BUFFER without QEXT.
	MaxConvert16kBuffer = 1920
)

const dredVerySmall float32 = 1e-30

type dred16kFilterSpec struct {
	b0 float32
	b  [ResamplingOrder]float32
	a  [ResamplingOrder]float32
}

var (
	dred48k24kTo16kFilter = dred16kFilterSpec{
		b0: 0.004523418224,
		b: [ResamplingOrder]float32{
			0.005873358047, 0.012980854831, 0.014531340042, 0.014531340042,
			0.012980854831, 0.005873358047, 0.004523418224, 0,
		},
		a: [ResamplingOrder]float32{
			-3.878718597768, 7.748834257468, -9.653651699533, 8.007342726666,
			-4.379450178552, 1.463182111810, -0.231720677804, 0,
		},
	}
	dred12kTo16kFilter = dred16kFilterSpec{
		b0: 0.002033596776,
		b: [ResamplingOrder]float32{
			-0.001017101081, 0.003673127243, 0.001009165267, 0.001009165267,
			0.003673127243, -0.001017101081, 0.002033596776, 0,
		},
		a: [ResamplingOrder]float32{
			-4.930414411612, 11.291643096504, -15.322037343815, 13.216403930898,
			-7.220409219553, 2.310550142771, -0.334338618782, 0,
		},
	}
	dred8kTo16kFilter = dred16kFilterSpec{
		b0: 0.020109185709,
		b: [ResamplingOrder]float32{
			0.081670120929, 0.180401598565, 0.259391051971, 0.259391051971,
			0.180401598565, 0.081670120929, 0.020109185709, 0,
		},
		a: [ResamplingOrder]float32{
			-1.393651933659, 2.609789872676, -2.403541968806, 2.056814957331,
			-1.148908574570, 0.473001413788, -0.110359852412, 0,
		},
	}
)

// ConvertTo16kMonoFloat64 mirrors libopus dred_convert_to_16k() for the
// public encoder float64 path. It writes 16 kHz mono PCM quantized to the same
// int16 grid libopus feeds into LPCNet feature extraction.
func ConvertTo16kMonoFloat64(dst []float32, mem *[ResamplingOrder + 1]float32, in []float64, sampleRate, channels int) int {
	if channels != 1 && channels != 2 {
		return 0
	}
	if len(in) == 0 || len(in)%channels != 0 {
		return 0
	}
	inLen := len(in) / channels
	outLen := inLen * 16000 / sampleRate
	if outLen <= 0 || outLen > len(dst) {
		return 0
	}

	up, ok := dred16kUpsampleFactor(sampleRate)
	if !ok {
		return 0
	}
	workLen := up * inLen
	if workLen > len(dst) {
		return 0
	}
	work := dst[:workLen]
	clear(work)

	if channels == 1 {
		for i := 0; i < inLen; i++ {
			work[up*i] = float32(dredFloatToInt16(float32(float64(up)*in[i]))) + dredVerySmall
		}
	} else {
		for i := 0; i < inLen; i++ {
			l := float32(in[2*i])
			r := float32(in[2*i+1])
			work[up*i] = float32(dredFloatToInt16(0.5*float32(up)*(l+r))) + dredVerySmall
		}
	}

	switch sampleRate {
	case 16000:
		return outLen
	case 48000, 24000:
		dredFilterDF2T(work, work, dred48k24kTo16kFilter, mem)
		for i := 0; i < outLen; i++ {
			dst[i] = work[3*i]
		}
		return outLen
	case 12000:
		dredFilterDF2T(work, work, dred12kTo16kFilter, mem)
		for i := 0; i < outLen; i++ {
			dst[i] = work[3*i]
		}
		return outLen
	case 8000:
		dredFilterDF2T(work, work[:outLen], dred8kTo16kFilter, mem)
		return outLen
	default:
		return 0
	}
}

func dred16kUpsampleFactor(sampleRate int) (int, bool) {
	switch sampleRate {
	case 8000:
		return 2, true
	case 12000:
		return 4, true
	case 16000:
		return 1, true
	case 24000:
		return 2, true
	case 48000:
		return 1, true
	default:
		return 0, false
	}
}

func dredFilterDF2T(in, out []float32, spec dred16kFilterSpec, mem *[ResamplingOrder + 1]float32) {
	for i := range out {
		xi := in[i]
		yi := xi*spec.b0 + mem[0]
		nyi := -yi
		for j := 0; j < ResamplingOrder; j++ {
			mem[j] = mem[j+1] + spec.b[j]*xi + spec.a[j]*nyi
		}
		out[i] = yi
	}
}

func dredFloatToInt16(v float32) int16 {
	scaled := v * 32768
	if scaled > 32767 {
		return 32767
	}
	if scaled < -32768 {
		return -32768
	}
	return int16(math.RoundToEven(float64(scaled)))
}
