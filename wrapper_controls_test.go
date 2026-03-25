package gopus

import (
	"errors"
	"testing"

	encodercore "github.com/thesyncim/gopus/encoder"
)

type optionalEncoderControl interface {
	SetDREDDuration(int) error
	DREDDuration() (int, error)
	SetDNNBlob([]byte) error
	SetQEXT(bool) error
	QEXT() (bool, error)
}

type optionalDecoderControl interface {
	SetOSCEBWE(bool) error
	OSCEBWE() (bool, error)
	SetDNNBlob([]byte) error
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

	if err := enc.SetDREDDuration(2); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetDREDDuration error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if got, err := enc.DREDDuration(); !errors.Is(err, ErrUnsupportedExtension) || got != 0 {
		t.Fatalf("DREDDuration()=(%d,%v) want=(0,%v)", got, err, ErrUnsupportedExtension)
	}
	if err := enc.SetDNNBlob([]byte{1, 2, 3}); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetDNNBlob error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if err := enc.SetQEXT(true); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetQEXT error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if got, err := enc.QEXT(); !errors.Is(err, ErrUnsupportedExtension) || got {
		t.Fatalf("QEXT()=(%v,%v) want=(false,%v)", got, err, ErrUnsupportedExtension)
	}
}

func assertOptionalDecoderControls(t *testing.T, dec optionalDecoderControl) {
	t.Helper()

	if err := dec.SetOSCEBWE(true); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetOSCEBWE error=%v want=%v", err, ErrUnsupportedExtension)
	}
	if got, err := dec.OSCEBWE(); !errors.Is(err, ErrUnsupportedExtension) || got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(false,%v)", got, err, ErrUnsupportedExtension)
	}
	if err := dec.SetDNNBlob([]byte{1, 2, 3}); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("SetDNNBlob error=%v want=%v", err, ErrUnsupportedExtension)
	}
}

func TestSupportsOptionalExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  OptionalExtension
	}{
		{name: "dred", ext: OptionalExtensionDRED},
		{name: "dnn_blob", ext: OptionalExtensionDNNBlob},
		{name: "qext", ext: OptionalExtensionQEXT},
		{name: "osce_bwe", ext: OptionalExtensionOSCEBWE},
		{name: "unknown", ext: OptionalExtension("future_ext")},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if SupportsOptionalExtension(tc.ext) {
				t.Fatalf("SupportsOptionalExtension(%q)=true want false in default build", tc.ext)
			}
		})
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
