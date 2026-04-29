//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package dred

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
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
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run dred encoder model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
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

	var payload bytes.Buffer
	payload.WriteString(libopusDREDRDOVAEEncInputMagic)
	for _, v := range []uint32{1, uint32(frameCount)} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("encode rdovae helper header: %w", err)
		}
	}
	for _, v := range input {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
			return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("encode rdovae helper input: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("run rdovae helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusDREDRDOVAEEncOutputMagic {
		return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("unexpected rdovae helper output")
	}
	gotFrameCount := int(binary.LittleEndian.Uint32(data[8:12]))
	if gotFrameCount != frameCount {
		return libopusDREDRDOVAEEncInfo{}, fmt.Errorf("rdovae helper frame count=%d want %d", gotFrameCount, frameCount)
	}
	offset := 12
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated rdovae helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
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
	return libopusDREDRDOVAEEncInfo{Latents: latents, State: state}, nil
}

func TestRDOVAEEncoderMatchesLibopusOnRealModel(t *testing.T) {
	raw, err := probeLibopusDREDEncoderModelBlob()
	if err != nil {
		t.Skipf("dred encoder model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real dred encoder blob) error: %v", err)
	}
	model, err := rdovae.LoadEncoder(blob)
	if err != nil {
		t.Fatalf("LoadEncoder(real model) error: %v", err)
	}

	input := makeDREDRDOVAEInputFrames(6)
	want, err := probeLibopusDREDRDOVAEEnc(input)
	if err != nil {
		t.Skipf("rdovae encoder helper unavailable: %v", err)
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

	assertDREDFloat32Close(t, gotLatents, want.Latents, 5e-3, "rdovae latents")
	assertDREDFloat32Close(t, gotState, want.State, 5e-3, "rdovae state")
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

func assertDREDFloat32Close(t *testing.T, got, want []float32, tol float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > tol {
			t.Fatalf("%s[%d]=%v want %v (tol=%g)", label, i, got[i], want[i], tol)
		}
	}
}
