package gopus

import (
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"testing"

	encodercore "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

type optionalEncoderControl interface {
	SetDNNBlob([]byte) error
}

type qextEncoderControl interface {
	SetQEXT(bool) error
	QEXT() (bool, error)
}

type optionalDecoderControl interface {
	SetDNNBlob([]byte) error
}

type unsupportedDREDControl interface {
	SetDREDDuration(int) error
	DREDDuration() (int, error)
}

type unsupportedOSCEBWEControl interface {
	SetOSCEBWE(bool) error
	OSCEBWE() (bool, error)
}

type ignoreExtensionsControl interface {
	SetIgnoreExtensions(bool)
	IgnoreExtensions() bool
	Reset()
}

type lookaheadCase struct {
	name        string
	sampleRate  int
	application Application
	want        int
}

type restrictedApplicationCase struct {
	name          string
	application   Application
	wantMode      encodercore.Mode
	wantLowDelay  bool
	wantBandwidth Bandwidth
	wantLookahead int
}

func assertOptionalEncoderControls(t *testing.T, enc optionalEncoderControl) {
	t.Helper()

	if err := enc.SetDNNBlob(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(nil) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob(makeFramedButIncompatibleTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(incompatible) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(decoder_blob) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(encoder_blob) error=%v want=nil", err)
	}
}

func assertUnsupportedDREDControl(t *testing.T, enc unsupportedDREDControl) {
	t.Helper()

	if err := enc.SetDREDDuration(2); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetDREDDuration error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if got, err := enc.DREDDuration(); !errors.Is(err, ErrUnsupportedExtension) || got != 0 {
		t.Fatalf("DREDDuration()=(%d,%v) want=(0,%v)", got, err, ErrUnsupportedExtension)
	}
}

func assertWorkingDREDControl(t *testing.T, enc unsupportedDREDControl) {
	t.Helper()

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration(4) error: %v", err)
	}
	if got, err := enc.DREDDuration(); err != nil || got != 4 {
		t.Fatalf("DREDDuration()=(%d,%v) want=(4,nil)", got, err)
	}
	if err := enc.SetDREDDuration(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDREDDuration(-1) error=%v want=%v", err, ErrInvalidArgument)
	}
}

func assertSupportedQEXTControl(t *testing.T, enc qextEncoderControl) {
	t.Helper()

	if err := enc.SetQEXT(true); err != nil {
		t.Fatalf("SetQEXT(true) error: %v", err)
	}
	if got, err := enc.QEXT(); err != nil || !got {
		t.Fatalf("QEXT()=(%v,%v) want=(true,nil)", got, err)
	}
	if err := enc.SetQEXT(false); err != nil {
		t.Fatalf("SetQEXT(false) error: %v", err)
	}
	if got, err := enc.QEXT(); err != nil || got {
		t.Fatalf("QEXT()=(%v,%v) want=(false,nil)", got, err)
	}
}

func assertUnsupportedQEXTControl(t *testing.T, enc qextEncoderControl) {
	t.Helper()

	if err := enc.SetQEXT(true); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetQEXT error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if got, err := enc.QEXT(); !errors.Is(err, ErrUnsupportedExtension) || got {
		t.Fatalf("QEXT()=(%v,%v) want=(false,%v)", got, err, ErrUnsupportedExtension)
	}
}

func assertOptionalDecoderControls(t *testing.T, dec optionalDecoderControl) {
	t.Helper()

	if err := dec.SetDNNBlob(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(nil) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob(makeFramedButIncompatibleTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(incompatible) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(encoder_blob) error=%v want=%v", err, ErrInvalidArgument)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob(decoder_blob) error=%v want=nil", err)
	}
}

func assertUnsupportedOSCEBWEControl(t *testing.T, dec unsupportedOSCEBWEControl) {
	t.Helper()

	if err := dec.SetOSCEBWE(true); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetOSCEBWE error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if got, err := dec.OSCEBWE(); !errors.Is(err, ErrUnsupportedExtension) || got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(false,%v)", got, err, ErrUnsupportedExtension)
	}
}

func assertWorkingOSCEBWEControl(t *testing.T, dec unsupportedOSCEBWEControl) {
	t.Helper()

	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true) error: %v", err)
	}
	if got, err := dec.OSCEBWE(); err != nil || !got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(true,nil)", got, err)
	}
	if err := dec.SetOSCEBWE(false); err != nil {
		t.Fatalf("SetOSCEBWE(false) error: %v", err)
	}
	if got, err := dec.OSCEBWE(); err != nil || got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(false,nil)", got, err)
	}
}

func TestSupportsOptionalExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  OptionalExtension
		want bool
	}{
		{name: "dred", ext: OptionalExtensionDRED, want: false},
		{name: "dnn_blob", ext: OptionalExtensionDNNBlob, want: true},
		{name: "qext", ext: OptionalExtensionQEXT, want: true},
		{name: "osce_bwe", ext: OptionalExtensionOSCEBWE, want: false},
		{name: "unknown", ext: OptionalExtension("future_ext"), want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsOptionalExtension(tc.ext); got != tc.want {
				t.Fatalf("SupportsOptionalExtension(%q)=%v want %v", tc.ext, got, tc.want)
			}
		})
	}
}

func appendTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	return append(dst, out...)
}

type testBlobRecordSpec struct {
	typ  int32
	size int
}

func addLinearLayerTestBlobSpec(specs map[string]testBlobRecordSpec, spec lpcnetplc.LinearLayerSpec) {
	if spec.Bias != "" {
		specs[spec.Bias] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Subias != "" {
		specs[spec.Subias] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Scale != "" {
		specs[spec.Scale] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbOutputs}
	}
	if spec.Weights != "" {
		specs[spec.Weights] = testBlobRecordSpec{typ: dnnblob.TypeInt8, size: spec.NbInputs * spec.NbOutputs}
		return
	}
	if spec.FloatWeights != "" {
		specs[spec.FloatWeights] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.NbInputs * spec.NbOutputs}
	}
}

func makeFramedButIncompatibleTestDNNBlob() []byte {
	return appendTestBlobRecord(nil, "dummy_record", 0, 4)
}

func makeValidEncoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredEncoderControlRecordNames() {
		payloadSize := 4
		blob = appendTestBlobRecord(blob, name, 0, payloadSize)
	}
	return blob
}

func makeValidDecoderTestDNNBlob() []byte {
	specs := make(map[string]testBlobRecordSpec)
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		specs[name] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4}
	}
	for _, spec := range lpcnetplc.ModelLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.FARGANModelLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	var blob []byte
	for _, name := range names {
		spec := specs[name]
		blob = appendTestBlobRecord(blob, name, spec.typ, spec.size)
	}
	return blob
}

func TestValidEncoderTestDNNBlobShape(t *testing.T) {
	blob := makeValidEncoderTestDNNBlob()
	if string(blob[:4]) != "DNNw" {
		t.Fatalf("magic=%q want DNNw", string(blob[:4]))
	}
	for _, name := range dnnblob.RequiredEncoderControlRecordNames() {
		if !strings.Contains(string(blob), name) {
			t.Fatalf("missing record name %q", name)
		}
	}
}

func TestValidDecoderTestDNNBlobShape(t *testing.T) {
	blob := makeValidDecoderTestDNNBlob()
	if string(blob[:4]) != "DNNw" {
		t.Fatalf("magic=%q want DNNw", string(blob[:4]))
	}
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		if !strings.Contains(string(blob), name) {
			t.Fatalf("missing record name %q", name)
		}
	}
}

func TestEncoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	enc.Reset()
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func TestDecoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.dredPredictor.Loaded() || !dec.dredFARGAN.Loaded() {
		t.Fatal("decoder runtime models not loaded from retained DNN blob")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.dredPredictor.Loaded() || !dec.dredFARGAN.Loaded() {
		t.Fatal("decoder runtime models cleared by Reset")
	}
}

func TestMultistreamEncoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	enc.Reset()
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func TestMultistreamDecoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func assertIgnoreExtensionsControls(t *testing.T, dec ignoreExtensionsControl) {
	t.Helper()

	if dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=true want false by default")
	}
	dec.SetIgnoreExtensions(true)
	if !dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=false want true after SetIgnoreExtensions(true)")
	}
	dec.Reset()
	if !dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=false want true after Reset")
	}
	dec.SetIgnoreExtensions(false)
	if dec.IgnoreExtensions() {
		t.Fatal("IgnoreExtensions()=true want false after SetIgnoreExtensions(false)")
	}
}

func lookaheadTestCases() []lookaheadCase {
	return []lookaheadCase{
		{
			name:        "audio_48k",
			sampleRate:  48000,
			application: ApplicationAudio,
			want:        48000/400 + 48000/250,
		},
		{
			name:        "voip_48k",
			sampleRate:  48000,
			application: ApplicationVoIP,
			want:        48000/400 + 48000/250,
		},
		{
			name:        "lowdelay_48k",
			sampleRate:  48000,
			application: ApplicationLowDelay,
			want:        48000 / 400,
		},
		{
			name:        "audio_24k",
			sampleRate:  24000,
			application: ApplicationAudio,
			want:        24000/400 + 24000/250,
		},
		{
			name:        "lowdelay_24k",
			sampleRate:  24000,
			application: ApplicationLowDelay,
			want:        24000 / 400,
		},
	}
}

func restrictedApplicationTestCases() []restrictedApplicationCase {
	return []restrictedApplicationCase{
		{
			name:          "restricted_silk",
			application:   ApplicationRestrictedSilk,
			wantMode:      encodercore.ModeSILK,
			wantLowDelay:  false,
			wantBandwidth: BandwidthWideband,
			wantLookahead: 48000/400 + 48000/250,
		},
		{
			name:          "restricted_celt",
			application:   ApplicationRestrictedCelt,
			wantMode:      encodercore.ModeCELT,
			wantLowDelay:  true,
			wantBandwidth: BandwidthFullband,
			wantLookahead: 48000 / 400,
		},
	}
}

func assertApplicationLockAfterEncode(
	t *testing.T,
	currentApplication func() Application,
	setApplication func(Application) error,
	reset func(),
	encodeOnce func() error,
	changeTo Application,
	resetTo Application,
) {
	t.Helper()

	if err := encodeOnce(); err != nil {
		t.Fatalf("encode before application lock test error: %v", err)
	}
	if err := setApplication(changeTo); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(change after encode) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := setApplication(currentApplication()); err != nil {
		t.Fatalf("SetApplication(same after encode) error: %v", err)
	}
	reset()
	if err := setApplication(resetTo); err != nil {
		t.Fatalf("SetApplication(after reset) error: %v", err)
	}
}

func assertLookaheadUpdatesBeforeEncode(
	t *testing.T,
	lookahead func() int,
	setApplication func(Application) error,
) {
	t.Helper()

	if got, want := lookahead(), 48000/400+48000/250; got != want {
		t.Fatalf("Lookahead(audio) = %d, want %d", got, want)
	}
	if err := setApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(LowDelay) error: %v", err)
	}
	if got, want := lookahead(), 48000/400; got != want {
		t.Fatalf("Lookahead(lowdelay) = %d, want %d", got, want)
	}
	if err := setApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(Audio) error: %v", err)
	}
	if got, want := lookahead(), 48000/400+48000/250; got != want {
		t.Fatalf("Lookahead(audio after reset) = %d, want %d", got, want)
	}
}
