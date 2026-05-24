package encoder

import (
	"fmt"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTVQInputMagic = "GVCI"
	celtVQModeTypeSizes     = uint32(5)
)

var encoderLibopusCELTVQHelper libopustest.HelperCache

func probeLibopusOpusResSize() (int, error) {
	binPath, err := encoderLibopusCELTVQHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "celt vq",
		OutputBase:  "gopus_encoder_libopus_celt_vq",
		SourceFile:  "libopus_celt_vq_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
	if err != nil {
		return 0, err
	}
	payload := libopustest.NewOraclePayload(libopusCELTVQInputMagic, celtVQModeTypeSizes, 1)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt vq", "GVCO")
	if err != nil {
		return 0, err
	}
	if mode := reader.U32(); mode != celtVQModeTypeSizes {
		return 0, fmt.Errorf("mode=%d want %d", mode, celtVQModeTypeSizes)
	}
	reader.Count(1)
	for i := 0; i < 5; i++ {
		reader.U32()
	}
	opusResSize := int(reader.U32())
	reader.U32()
	if err := reader.ExpectConsumed(); err != nil {
		return 0, err
	}
	return opusResSize, nil
}

func TestEncoderDelayBufferMatchesLibopusOpusResSize(t *testing.T) {
	libopustest.RequireOracle(t)
	want, err := probeLibopusOpusResSize()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt type sizes", err)
	}
	enc := NewEncoder(48000, 1)
	enc.delayBuffer = make([]opusRes, 1)
	if got := unsafe.Sizeof(enc.delayBuffer[0]); got != uintptr(want) {
		t.Fatalf("delayBuffer element size=%d want libopus opus_res size %d", got, want)
	}
	delayState := enc.ensureDelayState(1)
	if len(delayState) == 0 {
		t.Fatal("delayState unexpectedly empty")
	}
	if got := unsafe.Sizeof(delayState[0]); got != uintptr(want) {
		t.Fatalf("delayState element size=%d want libopus opus_res size %d", got, want)
	}
}

func TestEncoderDelayBufferStoresOpusResRoundedSamples(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.delayBuffer = make([]opusRes, 2)
	enc.updateDelayBufferInternal([]float64{1.0 / 3.0, 1.0 / 7.0}, 2, 2)
	for i, sample := range []float64{1.0 / 3.0, 1.0 / 7.0} {
		if got, want := float64(enc.delayBuffer[i]), float64(opusRes(sample)); got != want {
			t.Fatalf("delayBuffer[%d]=%.17g want %.17g", i, got, want)
		}
	}
}
