package gopus

import "testing"

func TestSettingsForApplicationInvalidReturnsError(t *testing.T) {
	if _, err := settingsForApplication(Application(-1)); err != ErrInvalidApplication {
		t.Fatalf("settingsForApplication(invalid) error = %v, want %v", err, ErrInvalidApplication)
	}
}
