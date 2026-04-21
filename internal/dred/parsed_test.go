package dred

import (
	"reflect"
	"testing"
)

func TestParsePayload(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 8, -4)

	got, err := ParsePayload(payload, 8)
	if err != nil {
		t.Fatalf("ParsePayload error: %v", err)
	}
	if got.Header.Q0 != 6 || got.Header.DQ != 3 || got.Header.QMax != 9 {
		t.Fatalf("ParsePayload header q=(%d,%d,%d) want (6,3,9)", got.Header.Q0, got.Header.DQ, got.Header.QMax)
	}
	if got.Header.DredOffset != -4 || got.Header.DredFrameOffset != 8 {
		t.Fatalf("ParsePayload header offsets=(%d,%d) want (-4,8)", got.Header.DredOffset, got.Header.DredFrameOffset)
	}
	if got.PayloadLatents != 0 {
		t.Fatalf("ParsePayload PayloadLatents=%d want 0", got.PayloadLatents)
	}
	avail := got.Availability(960, 48000)
	if avail.FeatureFrames != 4 || avail.MaxLatents != 0 || avail.OffsetSamples != -480 || avail.EndSamples != 480 || avail.AvailableSamples != 480 {
		t.Fatalf("ParsePayload availability=%+v want {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", avail)
	}
	quant := make([]int, 4)
	n := got.FillQuantizerLevels(quant, 5760, 48000)
	if n != 0 {
		t.Fatalf("FillQuantizerLevels count=%d want 0", n)
	}
	want := []int{0, 0, 0, 0}
	if !reflect.DeepEqual(quant, want) {
		t.Fatalf("FillQuantizerLevels=%v want %v", quant, want)
	}
}
