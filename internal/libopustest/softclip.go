package libopustest

import "fmt"

const (
	softClipInputMagic  = "GSCI"
	softClipOutputMagic = "GSCO"
)

var softClipHelper HelperCache

// ProbeSoftClip runs libopus opus_pcm_soft_clip for the provided interleaved PCM.
func ProbeSoftClip(n, channels int, samples, mem []float32) ([]float32, []float32, error) {
	binPath, err := softClipHelper.CHelperPath(CHelperConfig{
		Label:      "softclip",
		OutputBase: "gopus_shared_softclip",
		SourceFile: "libopus_softclip_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H"},
		Libs:       []string{RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		return nil, nil, err
	}

	payload := NewOraclePayload(softClipInputMagic, uint32(n), uint32(channels))
	for _, v := range mem {
		payload.Float32(v)
	}
	for _, v := range samples {
		payload.Float32(v)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "softclip", softClipOutputMagic)
	if err != nil {
		return nil, nil, err
	}
	countN := int(reader.U32())
	countC := int(reader.U32())
	if countN != n || countC != channels {
		return nil, nil, fmt.Errorf("helper shape=%dx%d want %dx%d", countN, countC, n, channels)
	}
	total := countN * countC
	reader.ExpectRemaining(4*countC + 4*total)
	outMem := make([]float32, countC)
	for i := range outMem {
		outMem[i] = reader.Float32()
	}
	out := make([]float32, total)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return out, outMem, nil
}
