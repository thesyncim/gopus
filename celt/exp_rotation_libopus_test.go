package celt

import (
	"fmt"
	"math"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
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
)

var libopusCELTVQHelper libopustest.HelperCache

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

type libopusCELTTypeSizes struct {
	celtNorm  int
	celtSig   int
	celtGLog  int
	opusVal16 int
	opusVal32 int
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
		payload.U32(64)
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
		celtGLog:  int(reader.U32()),
		opusVal16: int(reader.U32()),
		opusVal32: int(reader.U32()),
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
		got := make([]float64, len(tc.x))
		for i, sample := range tc.x {
			got[i] = float64(sample)
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
	if sizes.celtNorm != 4 || sizes.celtSig != 4 || sizes.celtGLog != 4 ||
		sizes.opusVal16 != 4 || sizes.opusVal32 != 4 {
		t.Fatalf("libopus CELT float sizes: celt_norm=%d celt_sig=%d celt_glog=%d opus_val16=%d opus_val32=%d, want all 4",
			sizes.celtNorm, sizes.celtSig, sizes.celtGLog, sizes.opusVal16, sizes.opusVal32)
	}
}

func TestDecoderGLogStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	dec := NewDecoder(2)
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
	dec := NewDecoder(2)
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
	enc := NewEncoder(2)
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
	enc := NewEncoder(2)
	enc.SetEnergyMask(make([]float64, MaxBands*2))
	got := []struct {
		name string
		size uintptr
	}{
		{"prevEnergy", unsafe.Sizeof(enc.prevEnergy[0])},
		{"prevEnergy2", unsafe.Sizeof(enc.prevEnergy2[0])},
		{"energyError", unsafe.Sizeof(enc.energyError[0])},
		{"energyMask", unsafe.Sizeof(enc.energyMask[0])},
		{"specAvg", unsafe.Sizeof(enc.specAvg)},
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
	enc := NewEncoder(2)
	if got := unsafe.Sizeof(enc.lastStereoSaving); got != uintptr(sizes.opusVal16) {
		t.Fatalf("lastStereoSaving element size=%d want libopus opus_val16 size %d", got, sizes.opusVal16)
	}
	got := []struct {
		name string
		size uintptr
	}{
		{"delayedIntra", unsafe.Sizeof(enc.delayedIntra)},
		{"overlapMax", unsafe.Sizeof(enc.overlapMax)},
	}
	for _, tc := range got {
		if tc.size != uintptr(sizes.opusVal32) {
			t.Fatalf("%s element size=%d want libopus opus_val32 size %d", tc.name, tc.size, sizes.opusVal32)
		}
	}
}

func TestDecoderPostfilterStateMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	dec := NewDecoder(2)
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

func TestRenormalizeVectorMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
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
		got := make([]float64, len(tc.x))
		for i, sample := range tc.x {
			got[i] = float64(sample)
		}
		renormalizeVector(got, float64(tc.gain))
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
		src := make([]float64, len(tc.x))
		got := make([]float64, len(tc.x))
		for i, sample := range tc.x {
			src[i] = float64(sample)
		}
		scaleLowbandOutForFolding(got, src, len(src))
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
		got := float32(celtMul32(float64(tc.a), float64(tc.b)))
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
		got := make([]float64, len(tc.x))
		energies := make([]float64, len(tc.energies))
		for i, sample := range tc.x {
			got[i] = float64(sample)
		}
		for i, energy := range tc.energies {
			energies[i] = float64(energy)
		}
		denormalizeCoeffsDownsample(got, energies, tc.end, tc.frameSize, tc.downsample)
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
		pulses []int
		spread int
		b      int
		gain   float32
	}
	pulseCases := []pulseCase{
		{[]int{2, -1, 0, 0, 0, 1, 0, -1}, spreadNormal, 1, 1},
		{[]int{0, 3, -2, 0, 1, 0, -1, 0, 2, 0, 0, -1}, spreadAggressive, 3, 0.75},
		{[]int{1, 0, -1, 1, 0, -1, 1, 0, 0, 0, -1, 0, 0, 0, 0, 0}, spreadLight, 4, 0.5},
		{[]int{1, 0, -1, 1, 0, -1, 1, 0, 2, -1, 2, 0, -1, 1, 0, 0}, spreadLight, 4, 0.5},
	}
	pulseVectors := make([][]int, len(pulseCases))
	for i, pc := range pulseCases {
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
		var dec rangecoding.Decoder
		dec.Init(tc.payload)
		got := make([]float64, tc.n)
		gotCollapse := algUnquantNoExtInto(got, &dec, tc.n, tc.k, tc.spread, tc.b, float64(tc.gain), nil)
		if gotCollapse != want[ci].collapse {
			t.Fatalf("case %d collapse=%d want %d", ci, gotCollapse, want[ci].collapse)
		}
		for i := range got {
			gotSample := float32(got[i])
			if math.Float32bits(gotSample) != math.Float32bits(want[ci].x[i]) {
				t.Fatalf("case %d x[%d]=%08x %.10g want %08x %.10g",
					ci, i,
					math.Float32bits(gotSample), gotSample,
					math.Float32bits(want[ci].x[i]), want[ci].x[i])
			}
		}
	}
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
