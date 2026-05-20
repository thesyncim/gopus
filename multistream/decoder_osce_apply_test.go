//go:build gopus_extra_controls
// +build gopus_extra_controls

package multistream

import "testing"

func TestStreamOSCELACEComplexityMode(t *testing.T) {
	for _, tc := range []struct {
		complexity int
		want       streamOSCELACEMode
	}{
		{complexity: 5, want: streamOSCELACEModeNone},
		{complexity: 6, want: streamOSCELACEModeLACE},
		{complexity: 7, want: streamOSCELACEModeNoLACE},
		{complexity: 10, want: streamOSCELACEModeNoLACE},
	} {
		if got := pickStreamOSCELACEMode(tc.complexity); got != tc.want {
			t.Fatalf("pickStreamOSCELACEMode(%d)=%v want %v", tc.complexity, got, tc.want)
		}
	}
}
