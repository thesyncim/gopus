package libopustest

import "github.com/thesyncim/gopus/internal/extsupport"

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

type CELTStereoIthetaOracle struct {
	Values       []uint32
	SelectedArch uint32
	RTCDEnabled  bool
	PresumeNEON  bool
}

var celtMathHelper HelperCache

func buildCELTMathHelper() (string, error) {
	cfg := CHelperConfig{
		Label:       "celt math",
		OutputBase:  "gopus_libopus_celt_math",
		SourceFile:  "libopus_celt_math_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	// These probes exercise CELT's floating-point math primitives, including
	// in fixed-point public builds. Select the matching float QEXT archive when
	// the helper's code is compiled with ENABLE_QEXT; fixed-point archives use
	// a different celt_norm domain and do not match this helper's float inputs.
	if extsupport.QEXT {
		if dredQEXTReferenceEnabled && !customModesReferenceEnabled {
			cfg.DREDQEXTRef = true
			cfg.RefIncludes = append(cfg.RefIncludes, "dnn")
			cfg.Libs = []string{DREDQEXTRefPath(".libs", "libopus.a"), "-lm"}
		} else if customModesReferenceEnabled {
			cfg.CustomQEXTRef = true
			cfg.Libs = []string{CustomQEXTRefPath(".libs", "libopus.a"), "-lm"}
		} else {
			cfg.QEXTRef = true
			cfg.Libs = []string{QEXTRefPath(".libs", "libopus.a"), "-lm"}
		}
	}
	return BuildCHelper(cfg)
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

func ProbeCELTStereoIthetaQ30(cases []CELTStereoIthetaCase) (CELTStereoIthetaOracle, error) {
	binPath, err := getCELTMathHelperPath()
	if err != nil {
		return CELTStereoIthetaOracle{}, err
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

	reader, err := RunOracleVersion(binPath, payload.Bytes(), "celt math stereo itheta", celtMathOutputMagic, 2)
	if err != nil {
		return CELTStereoIthetaOracle{}, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(12 + 4*count)
	out := CELTStereoIthetaOracle{
		SelectedArch: reader.U32(),
		RTCDEnabled:  reader.U32() != 0,
		PresumeNEON:  reader.U32() != 0,
		Values:       make([]uint32, count),
	}
	for i := range out.Values {
		out.Values[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return CELTStereoIthetaOracle{}, err
	}
	return out, nil
}
