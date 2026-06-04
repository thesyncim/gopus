package gopus

import (
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	encodercore "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
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

type extraDREDControl interface {
	SetDREDDuration(int) error
	DREDDuration() (int, error)
}

type extraOSCEBWEControl interface {
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

	if !extsupport.DNNBlob {
		// Default builds gate USE_WEIGHTS_FILE model loading exactly like
		// libopus (no ENABLE_DRED/OSCE/DEEP_PLC): SetDNNBlob is a zero-cost
		// no-op that reports the optional extension as unavailable.
		if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
			t.Fatalf("default SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
		}
		return
	}

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

func assertWorkingDREDControl(t *testing.T, enc extraDREDControl) {
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

func assertOptionalDecoderControls(t *testing.T, dec optionalDecoderControl) {
	t.Helper()

	if !extsupport.DNNBlob {
		// Default builds gate USE_WEIGHTS_FILE model loading exactly like
		// libopus (no ENABLE_DRED/OSCE/DEEP_PLC): SetDNNBlob is a zero-cost
		// no-op that reports the optional extension as unavailable.
		if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
			t.Fatalf("default SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
		}
		return
	}

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

func assertWorkingOSCEBWEControl(t *testing.T, dec extraOSCEBWEControl) {
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
		{name: "dred", ext: OptionalExtensionDRED, want: extsupport.DRED},
		{name: "dnn_blob", ext: OptionalExtensionDNNBlob, want: extsupport.DNNBlob},
		{name: "qext", ext: OptionalExtensionQEXT, want: extsupport.QEXT},
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

func appendTestBlobRecordWithPayload(dst []byte, name string, typ int32, payload []byte) []byte {
	const headerSize = 64
	blockSize := ((len(payload) + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	copy(out[headerSize:], payload)
	return append(dst, out...)
}

func encodeInt32s(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], uint32(v))
	}
	return out
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

func addConv2DLayerTestBlobSpec(specs map[string]testBlobRecordSpec, spec lpcnetplc.Conv2DLayerSpec) {
	if spec.Bias != "" {
		specs[spec.Bias] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4 * spec.OutChannels}
	}
	if spec.FloatWeights != "" {
		size := 4 * spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
		specs[spec.FloatWeights] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: size}
	}
}

func makeFramedButIncompatibleTestDNNBlob() []byte {
	return appendTestBlobRecord(nil, "dummy_record", 0, 4)
}

func makeNameCompleteEncoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredEncoderControlRecordNames() {
		blob = appendTestBlobRecord(blob, name, dnnblob.TypeFloat, 4)
	}
	return blob
}

func makeNameCompleteDecoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		blob = appendTestBlobRecord(blob, name, dnnblob.TypeFloat, 4)
	}
	return blob
}

func makeNameCompleteDREDDecoderTestDNNBlob() []byte {
	var blob []byte
	for _, name := range dnnblob.RequiredDREDDecoderRecordNames() {
		blob = appendTestBlobRecord(blob, name, dnnblob.TypeFloat, 4)
	}
	return blob
}

func makeValidEncoderTestDNNBlob() []byte {
	specs := make(map[string]testBlobRecordSpec)
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		addConv2DLayerTestBlobSpec(specs, spec)
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
	for _, spec := range rdovae.EncoderLayerSpecs() {
		blob = appendRDOVAEEncoderLayerRecords(blob, spec)
	}
	return blob
}

func appendRDOVAEEncoderLayerRecords(dst []byte, spec rdovae.LinearLayerSpec) []byte {
	totalBlocks := 0
	if spec.Bias != "" {
		dst = appendTestBlobRecord(dst, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.Subias != "" {
		dst = appendTestBlobRecord(dst, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.WeightsIdx != "" {
		idx := make([]int32, 0, 2*(spec.NbOutputs/8))
		for i := 0; i < spec.NbOutputs; i += 8 {
			idx = append(idx, 1, 0)
			totalBlocks++
		}
		dst = appendTestBlobRecordWithPayload(dst, spec.WeightsIdx, dnnblob.TypeInt, encodeInt32s(idx))
	}
	if spec.Weights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendTestBlobRecord(dst, spec.Weights, dnnblob.TypeInt8, size)
		dst = appendTestBlobRecord(dst, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
	}
	if spec.FloatWeights != "" {
		size := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			size = rdovae.SparseBlockSize * totalBlocks
		}
		dst = appendTestBlobRecord(dst, spec.FloatWeights, dnnblob.TypeFloat, 4*size)
	}
	return dst
}

func makeValidDecoderTestDNNBlob() []byte {
	specs := make(map[string]testBlobRecordSpec)
	for _, name := range dnnblob.RequiredDecoderControlRecordNames(false) {
		specs[name] = testBlobRecordSpec{typ: dnnblob.TypeFloat, size: 4}
	}
	for _, spec := range lpcnetplc.PitchDNNLinearLayerSpecs() {
		addLinearLayerTestBlobSpec(specs, spec)
	}
	for _, spec := range lpcnetplc.PitchDNNConv2DLayerSpecs() {
		addConv2DLayerTestBlobSpec(specs, spec)
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
			name:         "restricted_silk",
			application:  ApplicationRestrictedSilk,
			wantMode:     encodercore.ModeSILK,
			wantLowDelay: false,
			// Bandwidth() reports st->bandwidth (FULLBAND init default) before any
			// encode regardless of application; the per-application bandwidth bias
			// lives in the user request and is applied at encode time.
			wantBandwidth: BandwidthFullband,
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
