package libopustest

import "fmt"

const (
	celtAlgQuantInputMagic  = "GAQI"
	celtAlgQuantOutputMagic = "GAQO"

	// CELTAlgQuantModeQuant drives FIXED_POINT alg_quant with a real ec_enc.
	CELTAlgQuantModeQuant = uint32(0)
	// CELTAlgQuantModeUnquant drives FIXED_POINT alg_unquant with a real ec_dec.
	CELTAlgQuantModeUnquant = uint32(1)
)

var celtAlgQuantHelper HelperCache

func buildCELTAlgQuantHelper() (string, error) {
	// Build against the --enable-fixed-point reference so the integer
	// alg_quant/alg_unquant path (exp_rotation, op_pvq_search, encode_pulses
	// via the range coder, normalise_residual) is exercised. The libopus static
	// library is linked to resolve those symbols plus the entropy coder.
	return BuildCHelper(CHelperConfig{
		Label:       "CELT fixed alg_quant",
		OutputBase:  "gopus_libopus_celt_alg_quant_fixed",
		SourceFile:  "libopus_celt_alg_quant_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeCELTAlgQuant runs the FIXED_POINT alg_quant kernel against the real
// libopus reference. X is the celt_norm (int32) input vector of length N; gain
// is the opus_val32 (int32) resynthesis gain. resynth is forced on, so X is
// reconstructed in place. It returns the anti-collapse mask, the finalised
// range-coder bytes, and the resynthesised X of length N.
func ProbeCELTAlgQuant(x []int32, k, spread, b int, gain int32, bufBytes int) (collapseMask uint32, bytes []byte, resynth []int32, err error) {
	binPath, buildErr := celtAlgQuantHelper.Path(buildCELTAlgQuantHelper)
	if buildErr != nil {
		return 0, nil, nil, buildErr
	}
	n := len(x)
	payload := NewOraclePayload(celtAlgQuantInputMagic, CELTAlgQuantModeQuant)
	payload.U32(uint32(n))
	payload.U32(uint32(k))
	payload.U32(uint32(spread))
	payload.U32(uint32(b))
	payload.I32(gain)
	payload.U32(uint32(bufBytes))
	payload.I32s(x...)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed alg_quant", celtAlgQuantOutputMagic)
	if runErr != nil {
		return 0, nil, nil, runErr
	}
	if mode := reader.U32(); mode != CELTAlgQuantModeQuant {
		return 0, nil, nil, fmt.Errorf("celt alg_quant oracle: unexpected mode %d want %d", mode, CELTAlgQuantModeQuant)
	}
	collapseMask = reader.U32()
	nbytes := int(reader.U32())
	bytes = append([]byte(nil), reader.Bytes(nbytes)...)
	resynth = make([]int32, n)
	for i := range resynth {
		resynth[i] = reader.I32()
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return 0, nil, nil, cErr
	}
	return collapseMask, bytes, resynth, nil
}

// ProbeCELTAlgUnquant runs the FIXED_POINT alg_unquant kernel against the real
// libopus reference, decoding from the supplied range-coder bytes. It returns
// the anti-collapse mask and the decoded celt_norm X of length n.
func ProbeCELTAlgUnquant(n, k, spread, b int, gain int32, bytes []byte) (collapseMask uint32, x []int32, err error) {
	binPath, buildErr := celtAlgQuantHelper.Path(buildCELTAlgQuantHelper)
	if buildErr != nil {
		return 0, nil, buildErr
	}
	payload := NewOraclePayload(celtAlgQuantInputMagic, CELTAlgQuantModeUnquant)
	payload.U32(uint32(n))
	payload.U32(uint32(k))
	payload.U32(uint32(spread))
	payload.U32(uint32(b))
	payload.I32(gain)
	payload.U32(uint32(len(bytes)))
	payload.Raw(bytes)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed alg_quant", celtAlgQuantOutputMagic)
	if runErr != nil {
		return 0, nil, runErr
	}
	if mode := reader.U32(); mode != CELTAlgQuantModeUnquant {
		return 0, nil, fmt.Errorf("celt alg_quant oracle: unexpected mode %d want %d", mode, CELTAlgQuantModeUnquant)
	}
	collapseMask = reader.U32()
	x = make([]int32, n)
	for i := range x {
		x[i] = reader.I32()
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return 0, nil, cErr
	}
	return collapseMask, x, nil
}
