//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The fixed opus_decode_frame gain stage saturates opus_res before converting
// it to float. Check the same high-amplitude packet used by the control test,
// including positive and negative gains that exercise its saturation boundary.
func TestFixedQEXTDecodeGainClampMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := minimalHybridTestPacket20ms()
	for _, gain := range []int{-256, 0, 256, 8 * 256} {
		t.Run(fmt.Sprintf("gain_%d", gain), func(t *testing.T) {
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(48000, 1, 960, gain,
				[]libopusAPIRateDecodeStep{{packet: packet}})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed-QEXT gain", err)
			}
			d, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetGain(gain); err != nil {
				t.Fatal(err)
			}
			got := make([]float32, 960)
			n, err := d.Decode(packet, got)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(want) {
				t.Fatalf("samples=%d want %d", n, len(want))
			}
			for i, sample := range got[:n] {
				if math.Float32bits(sample) != math.Float32bits(want[i]) {
					t.Fatalf("sample %d: Go=%08x C=%08x", i, math.Float32bits(sample), math.Float32bits(want[i]))
				}
			}
		})
	}
}
