//go:build gopus_dred || gopus_osce

package gopus

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var (
	defaultLibopusPitchDNNBlobHelper    libopustest.HelperCache
	defaultLibopusPLCBlobHelper         libopustest.HelperCache
	defaultLibopusFARGANBlobHelper      libopustest.HelperCache
	defaultLibopusDREDEncoderBlobHelper libopustest.HelperCache
)

func TestDNNBlobControlAcceptsLibopusModelBlobs(t *testing.T) {
	if !libopustest.StrictRefRequired() {
		t.Skip("requires GOPUS_STRICT_LIBOPUS_REF=1")
	}

	encoderBlob := requireDefaultLibopusEncoderDNNBlob(t)
	decoderBlob := requireDefaultLibopusDecoderDNNBlob(t)

	encoderParsed, err := dnnblob.Clone(encoderBlob)
	if err != nil {
		t.Fatalf("Clone(libopus encoder blob) error: %v", err)
	}
	if err := encoderParsed.ValidateEncoderControl(); err != nil {
		t.Fatalf("ValidateEncoderControl(libopus encoder blob) error: %v", err)
	}
	if !encoderParsed.SupportsPitchDNN() || !encoderParsed.SupportsDREDEncoder() {
		t.Fatal("libopus encoder blob missing expected PitchDNN or DRED encoder families")
	}

	decoderParsed, err := dnnblob.Clone(decoderBlob)
	if err != nil {
		t.Fatalf("Clone(libopus decoder blob) error: %v", err)
	}
	if err := decoderParsed.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(libopus decoder blob) error: %v", err)
	}
	models := decoderParsed.DecoderModels()
	if !models.PitchDNN || !models.PLC || !models.FARGAN {
		t.Fatalf("libopus decoder blob model families=%+v, want PitchDNN/PLC/FARGAN", models)
	}
	if models.DRED || models.OSCE || models.OSCEBWE {
		t.Fatalf("default libopus decoder blob unexpectedly reports extra-control-only families: %+v", models)
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := enc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("Encoder.SetDNNBlob(libopus encoder blob) error: %v", err)
	}
	if enc.dnnBlob == nil || !enc.enc.DNNBlobLoaded() {
		t.Fatal("encoder did not retain libopus encoder DNN blob")
	}
	rejectEnc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := rejectEnc.SetDNNBlob(decoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Encoder.SetDNNBlob(libopus decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	dec := newMonoTestDecoder(t)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("Decoder.SetDNNBlob(libopus decoder blob) error: %v", err)
	}
	if dec.dnnBlob == nil || !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder did not retain libopus decoder DNN blob model state")
	}
	if dec.dredState() != nil {
		t.Fatalf("default decoder SetDNNBlob allocated DRED sidecar: %+v", dec.dredState())
	}
	rejectDec := newMonoTestDecoder(t)
	if err := rejectDec.SetDNNBlob(encoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Decoder.SetDNNBlob(libopus encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := msEnc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("MultistreamEncoder.SetDNNBlob(libopus encoder blob) error: %v", err)
	}
	if msEnc.dnnBlob == nil || !msEnc.enc.DNNBlobLoaded() {
		t.Fatal("multistream encoder did not retain libopus encoder DNN blob")
	}
	rejectMSEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := rejectMSEnc.SetDNNBlob(decoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MultistreamEncoder.SetDNNBlob(libopus decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := msDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("MultistreamDecoder.SetDNNBlob(libopus decoder blob) error: %v", err)
	}
	if msDec.dnnBlob == nil || !msDec.dec.PitchDNNLoaded() || !msDec.dec.PLCModelLoaded() || !msDec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder did not retain libopus decoder DNN blob model state")
	}
	rejectMSDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := rejectMSDec.SetDNNBlob(encoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MultistreamDecoder.SetDNNBlob(libopus encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
}

func requireDefaultLibopusEncoderDNNBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeDefaultLibopusEncoderDNNBlob()
	if err != nil {
		t.Fatalf("libopus encoder DNN blob helper unavailable: %v", err)
	}
	return blob
}

func requireDefaultLibopusDecoderDNNBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeDefaultLibopusDecoderDNNBlob()
	if err != nil {
		t.Fatalf("libopus decoder DNN blob helper unavailable: %v", err)
	}
	return blob
}

func probeDefaultLibopusEncoderDNNBlob() ([]byte, error) {
	pitchBlob, err := runDefaultLibopusPitchDNNBlobHelper()
	if err != nil {
		return nil, err
	}
	dredEncoderBlob, err := runDefaultLibopusDREDEncoderBlobHelper()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredEncoderBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredEncoderBlob...)
	return blob, nil
}

func probeDefaultLibopusDecoderDNNBlob() ([]byte, error) {
	pitchBlob, err := runDefaultLibopusPitchDNNBlobHelper()
	if err != nil {
		return nil, err
	}
	plcBlob, err := runDefaultLibopusPLCBlobHelper()
	if err != nil {
		return nil, err
	}
	farganBlob, err := runDefaultLibopusFARGANBlobHelper()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func runDefaultLibopusPitchDNNBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusPitchDNNBlobHelper, "libopus_pitchdnn_model_blob.c", "gopus_default_libopus_pitchdnn_model_blob")
}

func runDefaultLibopusPLCBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusPLCBlobHelper, "libopus_plc_model_blob.c", "gopus_default_libopus_plc_model_blob")
}

func runDefaultLibopusFARGANBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusFARGANBlobHelper, "libopus_fargan_model_blob.c", "gopus_default_libopus_fargan_model_blob")
}

func runDefaultLibopusDREDEncoderBlobHelper() ([]byte, error) {
	return runCachedDefaultLibopusDNNBlobHelper(&defaultLibopusDREDEncoderBlobHelper, "libopus_dred_encoder_model_blob.c", "gopus_default_libopus_dred_encoder_model_blob")
}

func runCachedDefaultLibopusDNNBlobHelper(cache *libopustest.HelperCache, sourceFile, outputBase string) ([]byte, error) {
	binPath, err := cache.Path(func() (string, error) {
		return buildDefaultLibopusDNNHelper(sourceFile, outputBase)
	})
	if err != nil {
		return nil, err
	}
	return runDefaultLibopusDNNBlobHelper(binPath)
}

func runDefaultLibopusDNNBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
}

func buildDefaultLibopusDNNHelper(sourceFile, outputBase string) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, true)
}
