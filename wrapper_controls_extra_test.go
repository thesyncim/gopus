//go:build gopus_extra_controls

package gopus

import "testing"

func assertWorkingOSCELACEControl(t *testing.T, dec extraOSCELACEControl) {
	t.Helper()

	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true) error: %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || !got {
		t.Fatalf("OSCELACE()=(%v,%v) want=(true,nil)", got, err)
	}
	if err := dec.SetOSCELACE(false); err != nil {
		t.Fatalf("SetOSCELACE(false) error: %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || got {
		t.Fatalf("OSCELACE()=(%v,%v) want=(false,nil)", got, err)
	}
}
