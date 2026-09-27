package silk

import "testing"

func TestDecodeFECRejectsUnsupportedOutputSize(t *testing.T) {
	d := NewDecoder()
	for _, tc := range []struct {
		frameSize int
		channels  int
	}{
		{-1, 1}, {0, 1}, {1, 1}, {960, 0}, {960, -1}, {960, 3},
		{int(^uint(0) >> 1), 2},
	} {
		if _, err := d.DecodeFEC(nil, BandwidthWideband, tc.frameSize, false, tc.channels); err != ErrDecodeFailed {
			t.Errorf("DecodeFEC size=%d channels=%d: error=%v want=%v", tc.frameSize, tc.channels, err, ErrDecodeFailed)
		}
		if _, err := d.DecodeFECInto(nil, BandwidthWideband, tc.frameSize, false, tc.channels, nil); err != ErrDecodeFailed {
			t.Errorf("DecodeFECInto size=%d channels=%d: error=%v want=%v", tc.frameSize, tc.channels, err, ErrDecodeFailed)
		}
	}
}
