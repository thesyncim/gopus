package libopustest

const (
	celtMathInputMagic  = "GCMI"
	celtMathOutputMagic = "GCMO"

	CELTMathModeLog2               = uint32(0)
	CELTMathModeExp2               = uint32(1)
	CELTMathModeFracMul16          = uint32(2)
	CELTMathModeBitexactCos        = uint32(3)
	CELTMathModeBitexactLog2Tan    = uint32(4)
	CELTMathModeISqrt32            = uint32(5)
	CELTMathModeUdiv               = uint32(6)
	CELTMathModeSudiv              = uint32(7)
	CELTMathModeLog2TanTheta       = uint32(8)
	CELTMathModeAtanNorm           = uint32(9)
	CELTMathModeAtan2pNorm         = uint32(10)
	CELTMathModeCosNorm2           = uint32(11)
	CELTMathModeStereoIthetaQ30    = uint32(12)
	CELTMathModeLog                = uint32(13)
	CELTMathModeSin                = uint32(14)
	CELTMathModeBitexactThetaPair  = uint32(15)
	CELTMathModeDynallocImportance = uint32(16)
)

type CELTBitexactThetaPair struct {
	Mid   int
	Side  int
	Delta int
}

type CELTStereoIthetaCase struct {
	Stereo bool
	X      []float32
	Y      []float32
}

var celtMathHelper HelperCache

func buildCELTMathHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt math",
		OutputBase:  "gopus_libopus_celt_math",
		SourceFile:  "libopus_celt_math_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTMathHelperPath() (string, error) {
	return celtMathHelper.Path(buildCELTMathHelper)
}

func ProbeCELTMath(mode uint32, samples []float32) ([]float32, error) {
	binPath, err := getCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtMathInputMagic, mode, uint32(len(samples)))
	for _, sample := range samples {
		payload.Float32(sample)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt math", celtMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
	reader.ExpectRemaining(4 * count)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func ProbeCELTMathWords(mode uint32, count int, words []uint32) ([]uint32, error) {
	binPath, err := getCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtMathInputMagic, mode, uint32(count))
	for _, word := range words {
		payload.U32(word)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt math", celtMathOutputMagic)
	if err != nil {
		return nil, err
	}
	gotCount := reader.Count(count)
	reader.ExpectRemaining(4 * gotCount)
	out := make([]uint32, gotCount)
	for i := range out {
		out[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func ProbeCELTBitexactThetaPairs(inputs []uint32) ([]CELTBitexactThetaPair, error) {
	binPath, err := getCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtMathInputMagic, CELTMathModeBitexactThetaPair, uint32(len(inputs)))
	for _, input := range inputs {
		payload.U32(input)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt math", celtMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(inputs))
	reader.ExpectRemaining(12 * count)
	out := make([]CELTBitexactThetaPair, count)
	for i := range out {
		out[i] = CELTBitexactThetaPair{
			Mid:   int(int32(reader.U32())),
			Side:  int(int32(reader.U32())),
			Delta: int(int32(reader.U32())),
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func ProbeCELTStereoIthetaQ30(cases []CELTStereoIthetaCase) ([]uint32, error) {
	binPath, err := getCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtMathInputMagic, CELTMathModeStereoIthetaQ30, uint32(len(cases)))
	for _, tc := range cases {
		if tc.Stereo {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(tc.X)))
		for _, v := range tc.X {
			payload.Float32(v)
		}
		for _, v := range tc.Y {
			payload.Float32(v)
		}
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt math", celtMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(4 * count)
	out := make([]uint32, count)
	for i := range out {
		out[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
