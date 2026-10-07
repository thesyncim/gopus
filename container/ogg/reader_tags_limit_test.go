package ogg

import "testing"

func TestOpusTagsSizeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		current    int
		additional int
		want       bool
	}{
		{name: "empty", want: true},
		{name: "exact limit", current: maxOpusTagsSize, want: true},
		{name: "reaches limit", current: maxOpusTagsSize - 1, additional: 1, want: true},
		{name: "exceeds limit", current: maxOpusTagsSize, additional: 1, want: false},
		{name: "already exceeds limit", current: maxOpusTagsSize + 1, want: false},
		{name: "negative current size", current: -1, want: false},
		{name: "negative addition", additional: -1, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := opusTagsSizeWithinLimit(tc.current, tc.additional); got != tc.want {
				t.Fatalf("opusTagsSizeWithinLimit(%d, %d) = %v, want %v", tc.current, tc.additional, got, tc.want)
			}
		})
	}
}
