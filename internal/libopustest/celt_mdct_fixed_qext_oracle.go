//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type CELTFixedQEXTMDCTParams struct {
	Mode, Shift, Stride int
	Input               []int32
}

type CELTFixedQEXTMDCTRecord struct {
	Fold     []int32
	PreFFT   []int32 // interleaved complex values in KISS bit-reversed order
	FFT      []int32
	Post     []int32 // t0, t1, mul(i,t1), mul(r,t0), mul(r,t1), mul(i,t0)
	Headroom int
	Output   []int32
}

var celtFixedQEXTMDCTHelper HelperCache

func buildCELTFixedQEXTMDCTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT MDCT",
		OutputBase:   "gopus_libopus_celt_mdct_fixed_qext",
		SourceFile:   "libopus_celt_mdct_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func ProbeCELTFixedQEXTMDCT(p CELTFixedQEXTMDCTParams) (CELTFixedQEXTMDCTRecord, error) {
	var out CELTFixedQEXTMDCTRecord
	var n int
	var overlap int
	switch p.Mode {
	case 0:
		n, overlap = 1920, 120
	case 1:
		n, overlap = 3840, 240
	default:
		return out, fmt.Errorf("invalid fixed-QEXT mode %d", p.Mode)
	}
	if p.Shift < 0 || p.Shift > 3 || p.Stride < 1 || p.Stride > 8 || len(p.Input) != n {
		return out, fmt.Errorf("invalid fixed-QEXT MDCT dimensions")
	}
	payload := NewOraclePayloadVersion("GQMI", 1, uint32(p.Mode), uint32(p.Shift), uint32(p.Stride))
	payload.I32s(p.Input...)
	bin, err := celtFixedQEXTMDCTHelper.Path(buildCELTFixedQEXTMDCTHelper)
	if err != nil {
		return out, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT MDCT", "GQMO")
	if err != nil {
		return out, err
	}
	foldCount := int(reader.U32())
	preFFTCount := int(reader.U32())
	fftCount := int(reader.U32())
	postCount := int(reader.U32())
	out.Headroom = int(reader.U32())
	count := int(reader.U32())
	want := p.Stride*((n>>p.Shift>>1)-1) + 1
	if foldCount != n>>p.Shift>>1 || preFFTCount != n>>p.Shift>>2 || fftCount != preFFTCount || postCount != 6*fftCount || count != want {
		return out, fmt.Errorf("fixed-QEXT MDCT output counts fold=%d preFFT=%d fft=%d post=%d output=%d, want %d, %d, %d, %d and %d (overlap=%d)", foldCount, preFFTCount, fftCount, postCount, count, n>>p.Shift>>1, n>>p.Shift>>2, n>>p.Shift>>2, 6*(n>>p.Shift>>2), want, overlap)
	}
	out.Fold = make([]int32, foldCount)
	for i := range out.Fold {
		out.Fold[i] = reader.I32()
	}
	out.PreFFT = make([]int32, 2*preFFTCount)
	for i := range out.PreFFT {
		out.PreFFT[i] = reader.I32()
	}
	out.FFT = make([]int32, 2*fftCount)
	for i := range out.FFT {
		out.FFT[i] = reader.I32()
	}
	out.Post = make([]int32, postCount)
	for i := range out.Post {
		out.Post[i] = reader.I32()
	}
	out.Output = make([]int32, count)
	for i := range out.Output {
		out.Output[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return out, err
	}
	return out, nil
}
