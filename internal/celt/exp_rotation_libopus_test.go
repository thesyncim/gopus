package celt

import (
	"fmt"
	"math"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	celtVQModeExpRotation       = uint32(0)
	celtVQModeRenormaliseVector = uint32(1)
	celtVQModeDenormaliseBands  = uint32(2)
	celtVQModeAlgUnquant        = uint32(3)
	celtVQModeEncodePulses      = uint32(4)
	celtVQModeTypeSizes         = uint32(5)
	celtVQModeLowbandOutScale   = uint32(6)
	celtVQModeMult32_32Q31      = uint32(7)
	celtVQModeStereoMerge       = uint32(8)
	celtVQModeHaar1             = uint32(9)
	celtVQModeOPPVQSearch       = uint32(10)
	celtVQModeAlgQuant          = uint32(11)
	celtVQModeThetaDist         = uint32(12)
	celtVQModeStereoItheta      = uint32(13)

	celtVQEncodePulsesStorage = uint32(4096)
)

const (
	celtQEXTVQModeAlgQuant   = uint32(0)
	celtQEXTVQModeAlgUnquant = uint32(1)
)

var libopusCELTVQHelper libopustest.HelperCache
var libopusCELTQEXTVQHelper libopustest.HelperCache

type expRotationOracleCase struct {
	x      []float32
	dir    int
	stride int
	k      int
	spread int
}

type renormaliseOracleCase struct {
	x    []float32
	gain float32
}

type denormaliseOracleCase struct {
	x          []float32
	energies   []float32
	frameSize  int
	start      int
	end        int
	lm         int
	downsample int
	silence    bool
}

type lowbandOutScaleOracleCase struct {
	x []float32
}

type mult32OracleCase struct {
	a float32
	b float32
}

type stereoMergeOracleCase struct {
	x   []float32
	y   []float32
	mid float32
}

type haar1OracleCase struct {
	name   string
	x      []float32
	n0     int
	stride int
}

type pvqSearchOracleCase struct {
	name string
	x    []float32
	k    int
}

type pvqSearchOracleResult struct {
	yy float32
	iy []int
}

type algUnquantOracleCase struct {
	payload []byte
	n       int
	k       int
	spread  int
	b       int
	gain    float32
}

type algUnquantOracleResult struct {
	collapse int
	x        []float32
}

type algUnquantQEXTOracleCase struct {
	name      string
	payload   []byte
	extPacket []byte
	n         int
	k         int
	spread    int
	b         int
	gain      float32
	extraBits int
}

type algUnquantQEXTOracleResult struct {
	collapse int
	x        []float32
}

type algQuantOracleCase struct {
	name    string
	x       []float32
	k       int
	spread  int
	b       int
	gain    float32
	resynth bool
}

type algQuantOracleResult struct {
	collapse int
	packet   []byte
	x        []float32
}

type algQuantQEXTOracleCase struct {
	name      string
	x         []float32
	k         int
	spread    int
	b         int
	gain      float32
	resynth   bool
	extraBits int
}

type algQuantQEXTOracleResult struct {
	collapse  int
	packet    []byte
	extPacket []byte
	x         []float32
}

type thetaDistOracleCase struct {
	ex, ey float32
	x0, x1 []float32
	y0, y1 []float32
}

type thetaDistOracleResult struct {
	w0, w1 float32
	p0, p1 float32
	dist   float32
}

type stereoIthetaOracleCase struct {
	name   string
	x      []float32
	y      []float32
	stereo bool
}

func requireDefaultLibopusPVQ(t *testing.T, n, k int) {
	t.Helper()
	if k > 0 && (!pvqUHasLookup(n, k) || !pvqUHasLookup(n, k+1)) {
		t.Fatalf("oracle case uses unsupported default libopus PVQ codebook N=%d K=%d", n, k)
	}
}

type libopusCELTTypeSizes struct {
	celtNorm  int
	celtSig   int
	celtEner  int
	celtGLog  int
	opusInt32 int
	opusVal16 int
	opusVal32 int
	opusRes   int
	analysis  int
}

func probeLibopusLowbandOutScale(cases []lowbandOutScaleOracleCase) ([][]float32, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeLowbandOutScale, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeLowbandOutScale {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]float32, n)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusStereoMerge(cases []stereoMergeOracleCase) ([]stereoMergeOracleCase, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeStereoMerge, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(math.Float32bits(tc.mid))
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
		for _, sample := range tc.y {
			payload.U32(math.Float32bits(sample))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeStereoMerge {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]stereoMergeOracleCase, count)
	for i := range out {
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		out[i].y = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
		for j := range out[i].y {
			out[i].y[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusHaar1(cases []haar1OracleCase) ([][]float32, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeHaar1, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.n0))
		payload.U32(uint32(tc.stride))
		payload.U32(uint32(len(tc.x)))
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeHaar1 {
		return nil, fmt.Errorf("celt vq helper mode=%d want %d", mode, celtVQModeHaar1)
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]float32, n)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusMult32_32Q31(cases []mult32OracleCase) ([]float32, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeMult32_32Q31, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(math.Float32bits(tc.a))
		payload.U32(math.Float32bits(tc.b))
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeMult32_32Q31 {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildLibopusCELTVQHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt vq",
		OutputBase:  "gopus_libopus_celt_vq",
		SourceFile:  "libopus_celt_vq_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func buildLibopusCELTQEXTVQHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext vq",
		OutputBase:  "gopus_libopus_celt_qext_vq",
		SourceFile:  "libopus_celt_qext_vq_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusAlgUnquant(cases []algUnquantOracleCase) ([]algUnquantOracleResult, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeAlgUnquant, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.n))
		payload.U32(uint32(tc.k))
		payload.U32(uint32(tc.spread))
		payload.U32(uint32(tc.b))
		payload.U32(math.Float32bits(tc.gain))
		payload.U32(uint32(len(tc.payload)))
		payload.Raw(tc.payload)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeAlgUnquant {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]algUnquantOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusAlgQuantQEXT(cases []algQuantQEXTOracleCase) ([]algQuantQEXTOracleResult, error) {
	binPath, err := libopusCELTQEXTVQHelper.Path(buildLibopusCELTQEXTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GQVI", celtQEXTVQModeAlgQuant, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.k))
		payload.U32(uint32(tc.spread))
		payload.U32(uint32(tc.b))
		if tc.resynth {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(tc.extraBits))
		payload.U32(math.Float32bits(tc.gain))
		payload.U32(128)
		payload.U32(128)
		for _, v := range tc.x {
			payload.U32(math.Float32bits(v))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext vq", "GQVO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtQEXTVQModeAlgQuant {
		return nil, fmt.Errorf("celt qext vq mode=%d want %d", mode, celtQEXTVQModeAlgQuant)
	}
	count := reader.Count(len(cases))
	out := make([]algQuantQEXTOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		packetLen := int(reader.U32())
		out[i].packet = append([]byte(nil), reader.Bytes(packetLen)...)
		extPacketLen := int(reader.U32())
		out[i].extPacket = append([]byte(nil), reader.Bytes(extPacketLen)...)
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusAlgUnquantQEXT(cases []algUnquantQEXTOracleCase) ([]algUnquantQEXTOracleResult, error) {
	binPath, err := libopusCELTQEXTVQHelper.Path(buildLibopusCELTQEXTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GQVI", celtQEXTVQModeAlgUnquant, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.n))
		payload.U32(uint32(tc.k))
		payload.U32(uint32(tc.spread))
		payload.U32(uint32(tc.b))
		payload.U32(uint32(tc.extraBits))
		payload.U32(math.Float32bits(tc.gain))
		payload.U32(uint32(len(tc.payload)))
		payload.U32(uint32(len(tc.extPacket)))
		payload.Raw(tc.payload)
		payload.Raw(tc.extPacket)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext vq", "GQVO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtQEXTVQModeAlgUnquant {
		return nil, fmt.Errorf("celt qext vq mode=%d want %d", mode, celtQEXTVQModeAlgUnquant)
	}
	count := reader.Count(len(cases))
	out := make([]algUnquantQEXTOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusAlgQuant(cases []algQuantOracleCase) ([]algQuantOracleResult, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeAlgQuant, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.k))
		payload.U32(uint32(tc.spread))
		payload.U32(uint32(tc.b))
		if tc.resynth {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(math.Float32bits(tc.gain))
		payload.U32(128)
		for _, v := range tc.x {
			payload.U32(math.Float32bits(v))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeAlgQuant {
		return nil, fmt.Errorf("celt vq mode=%d want %d", mode, celtVQModeAlgQuant)
	}
	count := reader.Count(len(cases))
	out := make([]algQuantOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		packetLen := int(reader.U32())
		out[i].packet = append([]byte(nil), reader.Bytes(packetLen)...)
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusStereoItheta(cases []stereoIthetaOracleCase) ([]int, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeStereoItheta, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		if tc.stereo {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
		for _, sample := range tc.y {
			payload.U32(math.Float32bits(sample))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeStereoItheta {
		return nil, fmt.Errorf("celt vq mode=%d want %d", mode, celtVQModeStereoItheta)
	}
	count := reader.Count(len(cases))
	out := make([]int, count)
	for i := range out {
		out[i] = int(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusThetaDist(cases []thetaDistOracleCase) ([]thetaDistOracleResult, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeThetaDist, uint32(len(cases)))
	for _, tc := range cases {
		n := len(tc.x0)
		if len(tc.x1) != n || len(tc.y0) != n || len(tc.y1) != n {
			return nil, fmt.Errorf("theta dist vector length mismatch")
		}
		payload.U32(math.Float32bits(tc.ex))
		payload.U32(math.Float32bits(tc.ey))
		payload.U32(uint32(n))
		for i := 0; i < n; i++ {
			payload.U32(math.Float32bits(tc.x0[i]))
			payload.U32(math.Float32bits(tc.x1[i]))
			payload.U32(math.Float32bits(tc.y0[i]))
			payload.U32(math.Float32bits(tc.y1[i]))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeThetaDist {
		return nil, fmt.Errorf("celt vq mode=%d want %d", mode, celtVQModeThetaDist)
	}
	count := reader.Count(len(cases))
	out := make([]thetaDistOracleResult, count)
	for i := range out {
		out[i] = thetaDistOracleResult{
			w0:   reader.Float32(),
			w1:   reader.Float32(),
			p0:   reader.Float32(),
			p1:   reader.Float32(),
			dist: reader.Float32(),
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusEncodePulses(pulseVectors [][]int) ([][]byte, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeEncodePulses, uint32(len(pulseVectors)))
	for _, pulses := range pulseVectors {
		k := 0
		for _, pulse := range pulses {
			if pulse < 0 {
				k -= pulse
			} else {
				k += pulse
			}
		}
		payload.U32(uint32(len(pulses)))
		payload.U32(uint32(k))
		payload.U32(celtVQEncodePulsesStorage)
		for _, pulse := range pulses {
			payload.U32(uint32(int32(pulse)))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeEncodePulses {
		return nil, err
	}
	count := reader.Count(len(pulseVectors))
	out := make([][]byte, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusOPPVQSearch(cases []pvqSearchOracleCase) ([]pvqSearchOracleResult, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeOPPVQSearch, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.k))
		for _, v := range tc.x {
			payload.U32(math.Float32bits(v))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeOPPVQSearch {
		return nil, fmt.Errorf("celt vq mode=%d want %d", mode, celtVQModeOPPVQSearch)
	}
	count := reader.Count(len(cases))
	out := make([]pvqSearchOracleResult, count)
	for i := range out {
		out[i].yy = reader.Float32()
		n := int(reader.U32())
		out[i].iy = make([]int, n)
		for j := range out[i].iy {
			out[i].iy[j] = int(reader.I32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusCELTTypeSizes() (libopusCELTTypeSizes, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return libopusCELTTypeSizes{}, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeTypeSizes, 1)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return libopusCELTTypeSizes{}, err
	}
	mode := reader.U32()
	if mode != celtVQModeTypeSizes {
		return libopusCELTTypeSizes{}, err
	}
	reader.Count(1)
	sizes := libopusCELTTypeSizes{
		celtNorm:  int(reader.U32()),
		celtSig:   int(reader.U32()),
		celtEner:  int(reader.U32()),
		celtGLog:  int(reader.U32()),
		opusInt32: int(reader.U32()),
		opusVal16: int(reader.U32()),
		opusVal32: int(reader.U32()),
		opusRes:   int(reader.U32()),
		analysis:  int(reader.U32()),
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusCELTTypeSizes{}, err
	}
	return sizes, nil
}

func probeLibopusDenormaliseBands(cases []denormaliseOracleCase) ([][]float32, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeDenormaliseBands, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(tc.start))
		payload.U32(uint32(tc.end))
		payload.U32(uint32(tc.lm))
		payload.U32(uint32(tc.downsample))
		if tc.silence {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
		for _, energy := range tc.energies {
			payload.U32(math.Float32bits(energy))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeDenormaliseBands {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]float32, n)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusExpRotation(cases []expRotationOracleCase) ([][]float32, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeExpRotation, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(int32(tc.dir)))
		payload.U32(uint32(tc.stride))
		payload.U32(uint32(tc.k))
		payload.U32(uint32(tc.spread))
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeExpRotation {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]float32, n)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusRenormaliseVector(cases []renormaliseOracleCase) ([][]float32, error) {
	binPath, err := libopusCELTVQHelper.Path(buildLibopusCELTVQHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVCI", celtVQModeRenormaliseVector, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(math.Float32bits(tc.gain))
		for _, sample := range tc.x {
			payload.U32(math.Float32bits(sample))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return nil, err
	}
	mode := reader.U32()
	if mode != celtVQModeRenormaliseVector {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		n := int(reader.U32())
		out[i] = make([]float32, n)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestExpRotationMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []expRotationOracleCase{
		{fixtureExpRotationVector(16, 0x12345678), -1, 1, 2, spreadNormal},
		{fixtureExpRotationVector(32, 0x31415926), 1, 1, 5, spreadAggressive},
		{fixtureExpRotationVector(48, 0xabcdef01), -1, 3, 4, spreadLight},
		{fixtureExpRotationVector(96, 0xdecafbad), 1, 4, 7, spreadNormal},
		{fixtureExpRotationVector(176, 0x0badf00d), -1, 8, 11, spreadAggressive},
	}
	want, err := probeLibopusExpRotation(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		got := make([]celtNorm, len(tc.x))
		for i, sample := range tc.x {
			got[i] = celtNorm(sample)
		}
		expRotation(got, len(got), tc.dir, tc.stride, tc.k, tc.spread)
		for i := range got {
			gotSample := float32(got[i])
			if math.Float32bits(gotSample) != math.Float32bits(want[ci][i]) {
				t.Fatalf("case %d x[%d]=%08x %.10g want %08x %.10g",
					ci, i,
					math.Float32bits(gotSample), gotSample,
					math.Float32bits(want[ci][i]), want[ci][i])
			}
		}
	}
}

func TestLibopusCELTFloatTypeSizes(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	if sizes.celtNorm != 4 || sizes.celtSig != 4 || sizes.celtEner != 4 || sizes.celtGLog != 4 ||
		sizes.opusVal16 != 4 || sizes.opusVal32 != 4 || sizes.opusRes != 4 || sizes.analysis != 4 {
		t.Fatalf("libopus CELT float sizes: celt_norm=%d celt_sig=%d celt_ener=%d celt_glog=%d opus_val16=%d opus_val32=%d opus_res=%d analysis=%d, want all 4",
			sizes.celtNorm, sizes.celtSig, sizes.celtEner, sizes.celtGLog, sizes.opusVal16, sizes.opusVal32, sizes.opusRes, sizes.analysis)
	}
}

func TestDecoderGLogStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var dec Decoder
	got := []struct {
		name string
		size uintptr
	}{
		{"prevEnergy", unsafe.Sizeof(dec.prevEnergy[0])},
		{"prevEnergy2", unsafe.Sizeof(dec.prevEnergy2[0])},
		{"prevLogE", unsafe.Sizeof(dec.prevLogE[0])},
		{"prevLogE2", unsafe.Sizeof(dec.prevLogE2[0])},
		{"backgroundEnergy", unsafe.Sizeof(dec.backgroundEnergy[0])},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.celtGLog) {
			t.Fatalf("%s element size=%d want libopus celt_glog size %d", tc.name, tc.size, sizes.celtGLog)
		}
	}
}

func TestDecoderSigStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var dec Decoder
	got := []struct {
		name string
		size uintptr
	}{
		{"overlapBuffer", unsafe.Sizeof(dec.overlapBuffer[0])},
		{"preemphState", unsafe.Sizeof(dec.preemphState[0])},
		{"postfilterMem", unsafe.Sizeof(dec.postfilterMem[0])},
		{"plcDecodeMem", unsafe.Sizeof(dec.plcDecodeMem[0])},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.celtSig) {
			t.Fatalf("%s element size=%d want libopus celt_sig size %d", tc.name, tc.size, sizes.celtSig)
		}
	}
}

func TestEncoderSigStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var enc Encoder
	got := []struct {
		name string
		size uintptr
	}{
		{"overlapBuffer", unsafe.Sizeof(enc.overlapBuffer[0])},
		{"preemphState", unsafe.Sizeof(enc.preemphState[0])},
		{"prefilterMem", unsafe.Sizeof(enc.prefilterMem[0])},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.celtSig) {
			t.Fatalf("%s element size=%d want libopus celt_sig size %d", tc.name, tc.size, sizes.celtSig)
		}
	}
}

func TestEncoderGLogStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var enc Encoder
	var scratch encoderScratch
	scratch.coarseOldStart = make([]celtGLog, 1)
	got := []struct {
		name string
		size uintptr
	}{
		{"prevEnergy", unsafe.Sizeof(enc.prevEnergy[0])},
		{"prevEnergy2", unsafe.Sizeof(enc.prevEnergy2[0])},
		{"prevBandLogEnergy", unsafe.Sizeof(enc.prevBandLogEnergy[0])},
		{"energyError", unsafe.Sizeof(enc.energyError[0])},
		{"energyMask", unsafe.Sizeof(enc.energyMask[0])},
		{"specAvg", unsafe.Sizeof(enc.specAvg)},
		{"surroundTrim", unsafe.Sizeof(enc.surroundTrim)},
		{"lastTemporalVBR", unsafe.Sizeof(enc.lastTemporalVBR)},
		{"lastBandLogE", unsafe.Sizeof(enc.lastBandLogE[0])},
		{"lastBandLogE2", unsafe.Sizeof(enc.lastBandLogE2[0])},
		{"lastDynalloc.MaxDepth", unsafe.Sizeof(enc.lastDynalloc.MaxDepth)},
		{"scratch.coarseOldStart", unsafe.Sizeof(scratch.coarseOldStart[0])},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.celtGLog) {
			t.Fatalf("%s element size=%d want libopus celt_glog size %d", tc.name, tc.size, sizes.celtGLog)
		}
	}
}

func TestEncoderOpusValStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var enc Encoder
	if got := unsafe.Sizeof(enc.lastStereoSaving); got != uintptr(sizes.opusVal16) {
		t.Fatalf("lastStereoSaving element size=%d want libopus opus_val16 size %d", got, sizes.opusVal16)
	}
	if got := unsafe.Sizeof(enc.lastTonality); got != uintptr(sizes.analysis) {
		t.Fatalf("lastTonality element size=%d want libopus AnalysisInfo float size %d", got, sizes.analysis)
	}
	analysisFields := []struct {
		name string
		size uintptr
	}{
		{"analysisActivity", unsafe.Sizeof(enc.analysisActivity)},
		{"analysisTonality", unsafe.Sizeof(enc.analysisTonality)},
		{"analysisTonalitySlope", unsafe.Sizeof(enc.analysisTonalitySlope)},
		{"analysisMaxPitchRatio", unsafe.Sizeof(enc.analysisMaxPitchRatio)},
	}
	for _, tc := range analysisFields {
		if tc.size != uintptr(sizes.analysis) {
			t.Fatalf("%s element size=%d want libopus AnalysisInfo float size %d", tc.name, tc.size, sizes.analysis)
		}
	}
	got := []struct {
		name string
		size uintptr
	}{
		{"delayedIntra", unsafe.Sizeof(enc.delayedIntra)},
		{"overlapMax", unsafe.Sizeof(enc.overlapMax)},
		{"hpMem", unsafe.Sizeof(enc.hpMem[0])},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.opusVal32) {
			t.Fatalf("%s element size=%d want libopus opus_val32 size %d", tc.name, tc.size, sizes.opusVal32)
		}
	}
	if got := unsafe.Sizeof(enc.delayBuffer[0]); got != uintptr(sizes.opusRes) {
		t.Fatalf("delayBuffer element size=%d want libopus opus_res size %d", got, sizes.opusRes)
	}
}

func TestEncoderVBRStateMatchesLibopusInt32Size(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var enc Encoder
	got := []struct {
		name string
		size uintptr
	}{
		{"vbrReservoir", unsafe.Sizeof(enc.vbrReservoir)},
		{"vbrDrift", unsafe.Sizeof(enc.vbrDrift)},
		{"vbrOffset", unsafe.Sizeof(enc.vbrOffset)},
		{"vbrCount", unsafe.Sizeof(enc.vbrCount)},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.opusInt32) {
			t.Fatalf("%s size=%d want libopus opus_int32 size %d", tc.name, tc.size, sizes.opusInt32)
		}
	}
}

func TestDecoderPostfilterStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var dec Decoder
	got := []struct {
		name string
		size uintptr
	}{
		{"postfilterGain", unsafe.Sizeof(dec.postfilterGain)},
		{"postfilterGainOld", unsafe.Sizeof(dec.postfilterGainOld)},
		{"plcLPC", unsafe.Sizeof(dec.plcLPC[0])},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.opusVal16) {
			t.Fatalf("%s element size=%d want libopus opus_val16 size %d", tc.name, tc.size, sizes.opusVal16)
		}
	}
}

func TestEncodePulsesPayloadMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	pulseVectors := [][]int{
		{2, -1, 0, 0, 0, 1, 0, -1},
		{0, 3, -2, 0, 1, 0, -1, 0, 2, 0, 0, -1},
		{1, 0, -1, 1, 0, -1, 1, 0, 2, -1, 2, 0, -1, 1, 0, 0},
		{1, -1, 0, 1, -1, 0, 0, 0, 1, 0, 0, -1, -1, 0, 0, 0, 0, 0, 0, -1, 0, 0, -1, 0},
	}
	want, err := probeLibopusEncodePulses(pulseVectors)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, pulses := range pulseVectors {
		k := 0
		for _, pulse := range pulses {
			if pulse < 0 {
				k -= pulse
			} else {
				k += pulse
			}
		}
		requireDefaultLibopusPVQ(t, len(pulses), k)
		idx := EncodePulses(pulses, len(pulses), k)
		var enc rangecoding.Encoder
		buf := make([]byte, 64)
		enc.Init(buf)
		enc.EncodeUniform(idx, PVQ_V(len(pulses), k))
		got := enc.Done()
		if string(got) != string(want[ci]) {
			t.Fatalf("case %d payload=%x want %x", ci, got, want[ci])
		}
	}
}

func TestOPPVQSearchMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []pvqSearchOracleCase{
		{name: "low_k", x: []float32{0.42, -0.31, 0.17, -0.09, 0.53, -0.23, 0.08, -0.44}, k: 3},
		{name: "high_k", x: []float32{0.12, -0.71, 0.38, 0.19, -0.27, 0.55, -0.06, 0.44}, k: 9},
		{name: "silence_high_k", x: []float32{0, 0, 0, 0, 0, 0}, k: 7},
		{name: "near_zero", x: []float32{1e-20, -2e-20, 3e-20, -4e-20, 5e-20}, k: 4},
		{name: "near_tie", x: []float32{0.50000006, -0.5, 0.24999997, -0.25, 0.12500001, -0.125}, k: 5},
		{name: "wide_band", x: fixtureExpRotationVector(32, 0x70767173), k: 18},
		{name: "large_k", x: fixtureExpRotationVector(48, 0x80818283), k: 35},
	}
	want, err := probeLibopusOPPVQSearch(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		x := make([]celtNorm, len(tc.x))
		for i, sample := range tc.x {
			x[i] = celtNorm(sample)
		}
		gotPulses, gotYY := opPVQSearchNorm(x, tc.k)
		if len(gotPulses) != len(want[ci].iy) {
			t.Fatalf("%s pulses len=%d want %d", tc.name, len(gotPulses), len(want[ci].iy))
		}
		for i := range gotPulses {
			if gotPulses[i] != int32(want[ci].iy[i]) {
				t.Fatalf("%s pulse[%d]=%d want %d", tc.name, i, gotPulses[i], want[ci].iy[i])
			}
		}
		if math.Float32bits(gotYY) != math.Float32bits(want[ci].yy) {
			t.Fatalf("%s yy=%08x %.10g want %08x %.10g",
				tc.name,
				math.Float32bits(gotYY), gotYY,
				math.Float32bits(want[ci].yy), want[ci].yy)
		}
	}
}

func TestAlgQuantMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []algQuantOracleCase{
		{name: "normal_no_resynth", x: []float32{0.42, -0.31, 0.17, -0.09, 0.53, -0.23, 0.08, -0.44}, k: 3, spread: spreadNormal, b: 1, gain: 1, resynth: false},
		{name: "normal_resynth", x: []float32{0.12, -0.71, 0.38, 0.19, -0.27, 0.55, -0.06, 0.44}, k: 5, spread: spreadNormal, b: 1, gain: 1, resynth: true},
		{name: "aggressive_resynth", x: fixtureExpRotationVector(16, 0x90919293), k: 6, spread: spreadAggressive, b: 1, gain: 0.75, resynth: true},
		{name: "light_blocks", x: fixtureExpRotationVector(24, 0xa0a1a2a3), k: 9, spread: spreadLight, b: 2, gain: 1.25, resynth: true},
		{name: "normal_stride2_resynth", x: fixtureExpRotationVector(32, 0xa4a5a6a7), k: 6, spread: spreadNormal, b: 4, gain: 0.9, resynth: true},
	}
	for _, tc := range cases {
		requireDefaultLibopusPVQ(t, len(tc.x), tc.k)
	}
	want, err := probeLibopusAlgQuant(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		x := make([]celtNorm, len(tc.x))
		for i, sample := range tc.x {
			x[i] = celtNorm(sample)
		}
		var enc rangecoding.Encoder
		buf := make([]byte, 128)
		enc.Init(buf)
		gotCollapse := algQuantScratch(&enc, 0, x, len(x), tc.k, tc.spread, tc.b, opusVal16(tc.gain), tc.resynth, nil, 0, nil)
		gotPacket := enc.Done()
		if gotCollapse != want[ci].collapse {
			t.Fatalf("%s collapse=%d want %d", tc.name, gotCollapse, want[ci].collapse)
		}
		if string(gotPacket) != string(want[ci].packet) {
			t.Fatalf("%s packet=%x want %x", tc.name, gotPacket, want[ci].packet)
		}
		if len(x) != len(want[ci].x) {
			t.Fatalf("%s x len=%d want %d", tc.name, len(x), len(want[ci].x))
		}
		for i := range x {
			got := float32(x[i])
			if math.Float32bits(got) != math.Float32bits(want[ci].x[i]) {
				t.Fatalf("%s x[%d]=%08x %.10g want %08x %.10g",
					tc.name, i,
					math.Float32bits(got), got,
					math.Float32bits(want[ci].x[i]), want[ci].x[i])
			}
		}
	}
}

func TestAlgQuantQEXTMatchesLibopusSource(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []algQuantQEXTOracleCase{
		{name: "n2_resynth", x: []float32{0.68, -0.37}, k: 7, spread: spreadNone, b: 1, gain: 0.85, resynth: true, extraBits: 4},
		{name: "n4_no_resynth", x: []float32{0.51, -0.28, 0.42, -0.19}, k: 8, spread: spreadLight, b: 1, gain: 1, resynth: false, extraBits: 3},
		{name: "n8_resynth", x: []float32{0.12, -0.71, 0.38, 0.19, -0.27, 0.55, -0.06, 0.44}, k: 9, spread: spreadNormal, b: 2, gain: 1.15, resynth: true, extraBits: 5},
		{name: "wide_extra_bits", x: fixtureExpRotationVector(16, 0xd0d1d2d3), k: 12, spread: spreadAggressive, b: 4, gain: 0.75, resynth: true, extraBits: 8},
	}
	for _, tc := range cases {
		requireDefaultLibopusPVQ(t, len(tc.x), tc.k)
	}
	want, err := probeLibopusAlgQuantQEXT(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext vq", err)
	}
	for ci, tc := range cases {
		x := make([]celtNorm, len(tc.x))
		for i, sample := range tc.x {
			x[i] = celtNorm(sample)
		}
		var enc rangecoding.Encoder
		buf := make([]byte, 128)
		enc.Init(buf)
		var extEnc rangecoding.Encoder
		extBuf := make([]byte, 128)
		extEnc.Init(extBuf)
		gotCollapse := algQuantScratch(&enc, 0, x, len(x), tc.k, tc.spread, tc.b, opusVal16(tc.gain), tc.resynth, &extEnc, tc.extraBits, nil)
		gotPacket := enc.Done()
		gotExtPacket := extEnc.Done()
		if gotCollapse != want[ci].collapse {
			t.Fatalf("%s collapse=%d want %d", tc.name, gotCollapse, want[ci].collapse)
		}
		if string(gotPacket) != string(want[ci].packet) {
			t.Fatalf("%s packet=%x want %x", tc.name, gotPacket, want[ci].packet)
		}
		if string(gotExtPacket) != string(want[ci].extPacket) {
			t.Fatalf("%s ext packet=%x want %x", tc.name, gotExtPacket, want[ci].extPacket)
		}
		if len(x) != len(want[ci].x) {
			t.Fatalf("%s x len=%d want %d", tc.name, len(x), len(want[ci].x))
		}
		for i := range x {
			got := float32(x[i])
			if math.Float32bits(got) != math.Float32bits(want[ci].x[i]) {
				t.Fatalf("%s x[%d]=%08x %.10g want %08x %.10g",
					tc.name, i,
					math.Float32bits(got), got,
					math.Float32bits(want[ci].x[i]), want[ci].x[i])
			}
		}
	}
}

func TestAlgUnquantQEXTMatchesLibopusSource(t *testing.T) {
	libopustest.RequireOracle(t)
	quantCases := []algQuantQEXTOracleCase{
		{name: "n2", x: []float32{0.68, -0.37}, k: 7, spread: spreadNone, b: 1, gain: 0.85, resynth: true, extraBits: 4},
		{name: "n4", x: []float32{0.51, -0.28, 0.42, -0.19}, k: 8, spread: spreadLight, b: 1, gain: 1, resynth: true, extraBits: 3},
		{name: "n8", x: []float32{0.12, -0.71, 0.38, 0.19, -0.27, 0.55, -0.06, 0.44}, k: 9, spread: spreadNormal, b: 2, gain: 1.15, resynth: true, extraBits: 5},
		{name: "wide_extra_bits", x: fixtureExpRotationVector(16, 0xd0d1d2d3), k: 12, spread: spreadAggressive, b: 4, gain: 0.75, resynth: true, extraBits: 8},
	}
	for _, tc := range quantCases {
		requireDefaultLibopusPVQ(t, len(tc.x), tc.k)
	}
	encoded, err := probeLibopusAlgQuantQEXT(quantCases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext vq", err)
	}
	cases := make([]algUnquantQEXTOracleCase, len(quantCases))
	for i, tc := range quantCases {
		cases[i] = algUnquantQEXTOracleCase{
			name:      tc.name,
			payload:   encoded[i].packet,
			extPacket: encoded[i].extPacket,
			n:         len(tc.x),
			k:         tc.k,
			spread:    tc.spread,
			b:         tc.b,
			gain:      tc.gain,
			extraBits: tc.extraBits,
		}
	}
	want, err := probeLibopusAlgUnquantQEXT(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext vq", err)
	}
	for ci, tc := range cases {
		var dec rangecoding.Decoder
		dec.Init(tc.payload)
		var extDec rangecoding.Decoder
		extDec.Init(tc.extPacket)
		got := make([]celtNorm, tc.n)
		gotCollapse := algUnquantInto(got, &dec, 0, tc.n, tc.k, tc.spread, tc.b, opusVal16(tc.gain), &extDec, tc.extraBits, nil)
		if gotCollapse != want[ci].collapse {
			t.Fatalf("%s collapse=%d want %d", tc.name, gotCollapse, want[ci].collapse)
		}
		if len(got) != len(want[ci].x) {
			t.Fatalf("%s x len=%d want %d", tc.name, len(got), len(want[ci].x))
		}
		for i := range got {
			gotSample := float32(got[i])
			if math.Float32bits(gotSample) != math.Float32bits(want[ci].x[i]) {
				t.Fatalf("%s x[%d]=%08x %.10g want %08x %.10g",
					tc.name, i,
					math.Float32bits(gotSample), gotSample,
					math.Float32bits(want[ci].x[i]), want[ci].x[i])
			}
		}
	}
}

func TestStereoIthetaMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []stereoIthetaOracleCase{
		{name: "n2_stereo", x: []float32{0.68, -0.37}, y: []float32{-0.23, 0.51}, stereo: true},
		{name: "n8_stereo", x: []float32{0.12, -0.71, 0.38, 0.19, -0.27, 0.55, -0.06, 0.44}, y: []float32{-0.18, 0.63, -0.31, 0.22, 0.14, -0.49, 0.08, -0.35}, stereo: true},
		{name: "wide_stereo", x: fixtureExpRotationVector(32, 0xe0e1e2e3), y: fixtureExpRotationVector(32, 0xe4e5e6e7), stereo: true},
	}
	want, err := probeLibopusStereoItheta(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		x := make([]celtNorm, len(tc.x))
		y := make([]celtNorm, len(tc.y))
		for i := range tc.x {
			x[i] = celtNorm(tc.x[i])
			y[i] = celtNorm(tc.y[i])
		}
		got := stereoIthetaQ30(x, y, tc.stereo)
		if got != want[ci] {
			t.Fatalf("%s itheta=%d want %d", tc.name, got, want[ci])
		}
	}
}

func TestThetaRDODistortionMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)
	cases := []thetaDistOracleCase{
		{
			ex: 1, ey: 1,
			x0: []float32{0.50, -0.25, 0.125, -0.75},
			x1: []float32{0.49, -0.24, 0.120, -0.70},
			y0: []float32{0.10, 0.40, -0.30, 0.20},
			y1: []float32{0.08, 0.42, -0.28, 0.18},
		},
		{
			ex: 0.8, ey: 0.2,
			x0: fixtureExpRotationVector(16, 0xb0b1b2b3),
			x1: fixtureExpRotationVector(16, 0xb4b5b6b7),
			y0: fixtureExpRotationVector(16, 0xb8b9babb),
			y1: fixtureExpRotationVector(16, 0xbcbdbebf),
		},
		{
			ex: 1.0e-10, ey: 0.75,
			x0: fixtureExpRotationVector(24, 0xc0c1c2c3),
			x1: fixtureExpRotationVector(24, 0xc4c5c6c7),
			y0: fixtureExpRotationVector(24, 0xc8c9cacb),
			y1: fixtureExpRotationVector(24, 0xcccdcecf),
		},
		{
			ex: 0.00618804805, ey: 0.00706463633,
			x0: []float32{1},
			x1: []float32{0.982530773},
			y0: []float32{1},
			y1: []float32{0.979359686},
		},
	}
	want, err := probeLibopusThetaDist(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		w0, w1 := computeChannelWeights(celtEner(tc.ex), celtEner(tc.ey))
		if math.Float32bits(w0) != math.Float32bits(want[ci].w0) ||
			math.Float32bits(w1) != math.Float32bits(want[ci].w1) {
			t.Fatalf("case %d weights=(%08x,%08x) want (%08x,%08x)",
				ci, math.Float32bits(w0), math.Float32bits(w1),
				math.Float32bits(want[ci].w0), math.Float32bits(want[ci].w1))
		}
		x0n := float32SliceToNorm(tc.x0)
		x1n := float32SliceToNorm(tc.x1)
		y0n := float32SliceToNorm(tc.y0)
		y1n := float32SliceToNorm(tc.y1)
		p0 := innerProductNorm(x0n, x1n)
		p1 := innerProductNorm(y0n, y1n)
		dist := thetaRDODistortion(w0, w1, x0n, x1n, y0n, y1n)
		if math.Float32bits(p0) != math.Float32bits(want[ci].p0) ||
			math.Float32bits(p1) != math.Float32bits(want[ci].p1) ||
			math.Float32bits(dist) != math.Float32bits(want[ci].dist) {
			t.Fatalf("case %d p=(%08x,%08x) dist=%08x want p=(%08x,%08x) dist=%08x",
				ci,
				math.Float32bits(p0), math.Float32bits(p1), math.Float32bits(dist),
				math.Float32bits(want[ci].p0), math.Float32bits(want[ci].p1), math.Float32bits(want[ci].dist))
		}
	}
}

func TestRenormalizeVectorMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)
	cases := []renormaliseOracleCase{
		{fixtureExpRotationVector(8, 0x10203040), 1},
		{fixtureExpRotationVector(21, 0x50607080), 0.5},
		{fixtureExpRotationVector(64, 0x90abcdef), 1.75},
		{fixtureExpRotationVector(176, 0x13579bdf), 0.125},
	}
	want, err := probeLibopusRenormaliseVector(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		got := make([]celtNorm, len(tc.x))
		for i, sample := range tc.x {
			got[i] = celtNorm(sample)
		}
		renormalizeVector(got, opusVal16(tc.gain))
		for i := range got {
			gotSample := float32(got[i])
			if math.Float32bits(gotSample) != math.Float32bits(want[ci][i]) {
				t.Fatalf("case %d x[%d]=%08x %.10g want %08x %.10g",
					ci, i,
					math.Float32bits(gotSample), gotSample,
					math.Float32bits(want[ci][i]), want[ci][i])
			}
		}
	}
}

func TestLowbandOutScaleMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []lowbandOutScaleOracleCase{
		{fixtureExpRotationVector(8, 0x7a5a0001)},
		{fixtureExpRotationVector(21, 0x7a5a0002)},
		{fixtureExpRotationVector(48, 0x7a5a0003)},
		{fixtureExpRotationVector(176, 0x7a5a0004)},
	}
	want, err := probeLibopusLowbandOutScale(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		src := make([]celtNorm, len(tc.x))
		got := make([]celtNorm, len(tc.x))
		for i, sample := range tc.x {
			src[i] = celtNorm(sample)
		}
		scaleLowbandOutForFoldingNorm(got, src, len(src))
		for i := range got {
			gotSample := float32(got[i])
			if math.Float32bits(gotSample) != math.Float32bits(want[ci][i]) {
				t.Fatalf("case %d x[%d]=%08x %.10g want %08x %.10g",
					ci, i,
					math.Float32bits(gotSample), gotSample,
					math.Float32bits(want[ci][i]), want[ci][i])
			}
		}
	}
}

func TestCELTMult32_32Q31MatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []mult32OracleCase{
		{a: 1, b: 1},
		{a: 0.70710677, b: 0.70710677},
		{a: 0.99999994, b: 0.50000006},
		{a: -0.31250003, b: 0.81249994},
		{a: 1.4142135, b: 0.17677669},
		{a: 0.00024414062, b: 4095.9375},
	}
	want, err := probeLibopusMult32_32Q31(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for i, tc := range cases {
		got := float32(celtMul32(opusVal16(tc.a), opusVal16(tc.b)))
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("case %d mult=%08x %.10g want %08x %.10g",
				i,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i])
		}
	}
}

func TestDenormalizeBandsMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := make([]denormaliseOracleCase, 0, 4)
	for _, lm := range []int{0, 1, 2, 3} {
		frameSize := 120 << lm
		x := fixtureExpRotationVector(frameSize, uint32(0x44aa0000+lm))
		energies := make([]float32, MaxBands)
		for i := range energies {
			energies[i] = float32((i%9)-4)*0.375 + float32(lm)*0.125
		}
		cases = append(cases, denormaliseOracleCase{
			x: x, energies: energies, frameSize: frameSize,
			start: 0, end: MaxBands, lm: lm, downsample: 1,
		})
	}
	want, err := probeLibopusDenormaliseBands(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		got := make([]celtNorm, len(tc.x))
		energies := make([]celtGLog, len(tc.energies))
		for i, sample := range tc.x {
			got[i] = celtNorm(sample)
		}
		for i, energy := range tc.energies {
			energies[i] = celtGLog(energy)
		}
		denormalizeNormCoeffsDownsample(got, energies, tc.end, tc.frameSize, tc.downsample)
		for i := range got {
			gotSample := float32(got[i])
			if math.Float32bits(gotSample) != math.Float32bits(want[ci][i]) {
				t.Fatalf("case %d freq[%d]=%08x %.10g want %08x %.10g",
					ci, i,
					math.Float32bits(gotSample), gotSample,
					math.Float32bits(want[ci][i]), want[ci][i])
			}
		}
	}
}

func TestAlgUnquantMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	type pulseCase struct {
		name   string
		pulses []int
		spread int
		b      int
		gain   float32
	}
	pulseCases := []pulseCase{
		{name: "legacy_n8", pulses: []int{2, -1, 0, 0, 0, 1, 0, -1}, spread: spreadNormal, b: 1, gain: 1},
		{name: "legacy_n12", pulses: []int{0, 3, -2, 0, 1, 0, -1, 0, 2, 0, 0, -1}, spread: spreadAggressive, b: 3, gain: 0.75},
		{name: "legacy_n16_sparse", pulses: []int{1, 0, -1, 1, 0, -1, 1, 0, 0, 0, -1, 0, 0, 0, 0, 0}, spread: spreadLight, b: 4, gain: 0.5},
		{name: "legacy_n16_dense", pulses: []int{1, 0, -1, 1, 0, -1, 1, 0, 2, -1, 2, 0, -1, 1, 0, 0}, spread: spreadLight, b: 4, gain: 0.5},
		{name: "n2_none", pulses: []int{1, -1}, spread: spreadNone, b: 1, gain: 1},
		{name: "n24_light", pulses: makeAlgUnquantPulseVector(24, 9, 0x31415926), spread: spreadLight, b: 3, gain: 0.875},
		{name: "n48_normal", pulses: makeAlgUnquantPulseVector(48, 6, 0xabcdef01), spread: spreadNormal, b: 6, gain: 0.625},
		{name: "n176_aggressive", pulses: makeAlgUnquantPulseVector(176, 4, 0x0badf00d), spread: spreadAggressive, b: 8, gain: 1.25},
	}
	pulseVectors := make([][]int, len(pulseCases))
	for i, pc := range pulseCases {
		k := 0
		for _, p := range pc.pulses {
			if p < 0 {
				k -= p
			} else {
				k += p
			}
		}
		requireDefaultLibopusPVQ(t, len(pc.pulses), k)
		pulseVectors[i] = pc.pulses
	}
	payloads, err := probeLibopusEncodePulses(pulseVectors)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	cases := make([]algUnquantOracleCase, 0, len(pulseCases))
	for i, pc := range pulseCases {
		n := len(pc.pulses)
		k := 0
		for _, p := range pc.pulses {
			if p < 0 {
				k -= p
			} else {
				k += p
			}
		}
		cases = append(cases, algUnquantOracleCase{
			payload: payloads[i],
			n:       n, k: k, spread: pc.spread, b: pc.b, gain: pc.gain,
		})
	}
	want, err := probeLibopusAlgUnquant(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		pc := pulseCases[ci]
		t.Run(pc.name, func(t *testing.T) {
			scratchCases := []struct {
				name    string
				scratch *bandDecodeScratch
			}{
				{name: "nil_scratch"},
				{name: "scratch", scratch: &bandDecodeScratch{}},
			}
			for _, sc := range scratchCases {
				t.Run(sc.name, func(t *testing.T) {
					var dec rangecoding.Decoder
					dec.Init(tc.payload)
					got := make([]celtNorm, tc.n)
					gotCollapse := algUnquantNoExtInto(got, &dec, tc.n, tc.k, tc.spread, tc.b, opusVal16(tc.gain), sc.scratch)
					if gotCollapse != want[ci].collapse {
						t.Fatalf("collapse=%d want %d", gotCollapse, want[ci].collapse)
					}
					for i := range got {
						gotSample := float32(got[i])
						if math.Float32bits(gotSample) != math.Float32bits(want[ci].x[i]) {
							t.Fatalf("x[%d]=%08x %.10g want %08x %.10g",
								i,
								math.Float32bits(gotSample), gotSample,
								math.Float32bits(want[ci].x[i]), want[ci].x[i])
						}
					}
				})
			}
		})
	}
}

func makeAlgUnquantPulseVector(n, count int, seed uint32) []int {
	pulses := make([]int, n)
	if n == 0 {
		return pulses
	}
	for i := 0; i < count; i++ {
		seed = seed*1664525 + 1013904223
		pos := int(seed % uint32(n))
		if seed&0x80000000 != 0 {
			pulses[pos]--
		} else {
			pulses[pos]++
		}
	}
	return pulses
}

func fixtureExpRotationVector(n int, seed uint32) []float32 {
	out := make([]float32, n)
	for i := range out {
		seed = seed*1664525 + 1013904223
		v := int32(seed>>8)%4097 - 2048
		out[i] = float32(v) * (1.0 / 1024.0)
		if i%11 == 0 {
			out[i] *= 1.0 / 257.0
		}
	}
	return out
}
