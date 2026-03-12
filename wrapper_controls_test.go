package gopus

import "testing"

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

func assertOptionalEncoderControls(t *testing.T, enc optionalEncoderControl) {
	t.Helper()

	if err := enc.SetDREDDuration(2); err != ErrUnimplemented {
		t.Fatalf("SetDREDDuration error=%v want=%v", err, ErrUnimplemented)
	}
	if got, err := enc.DREDDuration(); err != ErrUnimplemented || got != 0 {
		t.Fatalf("DREDDuration()=(%d,%v) want=(0,%v)", got, err, ErrUnimplemented)
	}
	if err := enc.SetDNNBlob([]byte{1, 2, 3}); err != ErrUnimplemented {
		t.Fatalf("SetDNNBlob error=%v want=%v", err, ErrUnimplemented)
	}
	if err := enc.SetQEXT(true); err != ErrUnimplemented {
		t.Fatalf("SetQEXT error=%v want=%v", err, ErrUnimplemented)
	}
	if got, err := enc.QEXT(); err != ErrUnimplemented || got {
		t.Fatalf("QEXT()=(%v,%v) want=(false,%v)", got, err, ErrUnimplemented)
	}
}

func assertOptionalDecoderControls(t *testing.T, dec optionalDecoderControl) {
	t.Helper()

	if err := dec.SetOSCEBWE(true); err != ErrUnimplemented {
		t.Fatalf("SetOSCEBWE error=%v want=%v", err, ErrUnimplemented)
	}
	if got, err := dec.OSCEBWE(); err != ErrUnimplemented || got {
		t.Fatalf("OSCEBWE()=(%v,%v) want=(false,%v)", got, err, ErrUnimplemented)
	}
	if err := dec.SetDNNBlob([]byte{1, 2, 3}); err != ErrUnimplemented {
		t.Fatalf("SetDNNBlob error=%v want=%v", err, ErrUnimplemented)
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
