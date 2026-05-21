//go:build gopus_dred || gopus_extra_controls

package dred

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDREDRDOVAEEncInputMagic  = "GROI"
	libopusDREDRDOVAEEncOutputMagic = "GROO"
)

type libopusDREDRDOVAEEncInfo struct {
	Latents []float32
	State   []float32
}

var (
	libopusDREDEncoderModelBlobHelperOnce sync.Once
	libopusDREDEncoderModelBlobHelperPath string
	libopusDREDEncoderModelBlobHelperErr  error

	libopusDREDRDOVAEEncHelperOnce sync.Once
	libopusDREDRDOVAEEncHelperPath string
	libopusDREDRDOVAEEncHelperErr  error
)

func getLibopusDREDEncoderModelBlobHelperPath() (string, error) {
	libopusDREDEncoderModelBlobHelperOnce.Do(func() {
		libopusDREDEncoderModelBlobHelperPath, libopusDREDEncoderModelBlobHelperErr = buildLibopusDREDHelper("libopus_dred_encoder_model_blob.c", "gopus_libopus_dred_encoder_model_blob")
	})
	if libopusDREDEncoderModelBlobHelperErr != nil {
		return "", libopusDREDEncoderModelBlobHelperErr
	}
	return libopusDREDEncoderModelBlobHelperPath, nil
}

func getLibopusDREDRDOVAEEncHelperPath() (string, error) {
	libopusDREDRDOVAEEncHelperOnce.Do(func() {
		libopusDREDRDOVAEEncHelperPath, libopusDREDRDOVAEEncHelperErr = buildLibopusDREDHelper("libopus_dred_rdovae_enc_info.c", "gopus_libopus_dred_rdovae_enc")
	})
	if libopusDREDRDOVAEEncHelperErr != nil {
		return "", libopusDREDRDOVAEEncHelperErr
	}
	return libopusDREDRDOVAEEncHelperPath, nil
}

func probeLibopusDREDEncoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDEncoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run dred encoder model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDREDRDOVAEEnc(input []float32) (libopusDREDRDOVAEEncInfo, error) {
	binPath, err := getLibopusDREDRDOVAEEncHelperPath()
	if err != nil {
		return libopusDREDRDOVAEEncInfo{}, err
	}
	if len(input) == 0 || len(input)%(2*NumFeatures) != 0 {
		return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("input must contain a positive whole number of d-frames")
	}
	frameCount := len(input) / (2 * NumFeatures)

	payload := libopustest.NewOraclePayload(libopusDREDRDOVAEEncInputMagic, uint32(frameCount))
	for _, v := range input {
		payload.Float32(v)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("run rdovae helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("rdovae encoder", libopusDREDRDOVAEEncOutputMagic, data)
	if err != nil {
		return libopusDREDRDOVAEEncInfo{}, err
	}
	reader.Count(frameCount)
	reader.ExpectRemaining(frameCount * (LatentDim + StateDim) * 4)
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			values[i] = reader.Float32()
		}
		return values, nil
	}
	latents := make([]float32, 0, frameCount*LatentDim)
	state := make([]float32, 0, frameCount*StateDim)
	for i := 0; i < frameCount; i++ {
		frameLatents, err := readBits(LatentDim)
		if err != nil {
			return libopusDREDRDOVAEEncInfo{}, err
		}
		frameState, err := readBits(StateDim)
		if err != nil {
			return libopusDREDRDOVAEEncInfo{}, err
		}
		latents = append(latents, frameLatents...)
		state = append(state, frameState...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDRDOVAEEncInfo{}, err
	}
	return libopusDREDRDOVAEEncInfo{Latents: latents, State: state}, nil
}

func TestRDOVAEEncoderMatchesLibopusOnRealModel(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusDREDEncoderModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred encoder model blob", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real dred encoder blob) error: %v", err)
	}
	model, err := rdovae.LoadEncoder(blob)
	if err != nil {
		t.Fatalf("LoadEncoder(real model) error: %v", err)
	}

	cases := []struct {
		name  string
		input func(int) []float32
	}{
		{"trig", makeDREDRDOVAEInputFrames},
		{"zero", makeDREDRDOVAEZeroInputFrames},
		{"impulse", makeDREDRDOVAEImpulseInputFrames},
		{"alternating", makeDREDRDOVAEAlternatingInputFrames},
	}
	for _, tc := range cases {
		for _, frameCount := range []int{1, 2, 6, 9, 12} {
			t.Run(fmt.Sprintf("%s_frames_%d", tc.name, frameCount), func(t *testing.T) {
				input := tc.input(frameCount)
				want, err := probeLibopusDREDRDOVAEEnc(input)
				if err != nil {
					libopustest.HelperUnavailable(t, "rdovae encoder", err)
				}

				var processor rdovae.EncoderProcessor
				var latents [rdovae.LatentDim]float32
				var state [rdovae.StateDim]float32
				gotLatents := make([]float32, 0, len(want.Latents))
				gotState := make([]float32, 0, len(want.State))
				for i := 0; i < len(input); i += 2 * NumFeatures {
					if !model.EncodeDFrameWithProcessor(&processor, latents[:], state[:], input[i:i+2*NumFeatures]) {
						t.Fatalf("EncodeDFrameWithProcessor(frame=%d) returned false", i/(2*NumFeatures))
					}
					gotLatents = append(gotLatents, latents[:]...)
					gotState = append(gotState, state[:]...)
				}

				assertDREDFloat32Close(t, gotLatents, want.Latents, 0, "rdovae latents")
				assertDREDFloat32Close(t, gotState, want.State, 0, "rdovae state")
			})
		}
	}
}

func makeDREDRDOVAEInputFrames(frameCount int) []float32 {
	input := make([]float32, frameCount*2*NumFeatures)
	for i := range input {
		t := float64(i)
		frame := float64(i / (2 * NumFeatures))
		input[i] = float32(0.34*math.Sin(0.13*t+0.07*frame) + 0.21*math.Cos(0.29*t-0.11*frame))
	}
	return input
}

func makeDREDRDOVAEZeroInputFrames(frameCount int) []float32 {
	return make([]float32, frameCount*2*NumFeatures)
}

func makeDREDRDOVAEImpulseInputFrames(frameCount int) []float32 {
	input := make([]float32, frameCount*2*NumFeatures)
	for frame := 0; frame < frameCount; frame++ {
		base := frame * 2 * NumFeatures
		input[base+(frame%NumFeatures)] = 0.9
		input[base+NumFeatures+((frame*5+3)%NumFeatures)] = -0.8
	}
	return input
}

func makeDREDRDOVAEAlternatingInputFrames(frameCount int) []float32 {
	input := make([]float32, frameCount*2*NumFeatures)
	for i := range input {
		input[i] = float32(i%15-7) * 0.055
	}
	return input
}

func assertDREDFloat32Close(t *testing.T, got, want []float32, tol float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if tol == 0 {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("%s[%d]=%v want %v (bits differ)", label, i, got[i], want[i])
			}
			continue
		}
		if math.Abs(float64(got[i]-want[i])) > tol {
			t.Fatalf("%s[%d]=%v want %v (tol=%g)", label, i, got[i], want[i], tol)
		}
	}
}
