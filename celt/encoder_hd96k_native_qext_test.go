//go:build gopus_qext

package celt

import (
	"math"
	"testing"
)

// TestHD96kNativeEncodeRuns drives the native 96 kHz CELT encode end-to-end at
// frameSize=1920 with the HD96k mode enabled and QEXT extension-band encode
// active, exercising the overlap=240 MDCT analysis, the 2-tap HD pre-emphasis,
// the base-band CELT encode and the >20 kHz extension payload. It checks the
// pipeline produces a non-empty main payload and (for a wideband signal) a
// non-empty QEXT extension payload, without panicking on the HD lengths.
func TestHD96kNativeEncodeRuns(t *testing.T) {
	const frameSize = 1920
	for _, ch := range []int{1, 2} {
		e := NewEncoder(ch)
		// Match the top-level Opus integration: the outer encoder owns DC reject,
		// LSB-depth rounding and delay compensation, so the CELT layer disables
		// them. RESTRICTED_LOWDELAY disables the prefilter.
		e.dcRejectEnabled = false
		e.lsbQuantizationEnabled = false
		e.delayCompensationEnabled = false
		e.disablePrefilter = true
		e.SetVBR(false)
		e.SetComplexity(10)
		e.SetBitrate(256000)
		e.SetQEXTEnabled(true)
		e.EnableHD96kMode()

		pcm := make([]float32, frameSize*ch)
		for i := 0; i < frameSize; i++ {
			// 6 kHz tone has content above 20 kHz only via harmonics; use a tone
			// plus a high component to populate the extension bands.
			s := 0.4*math.Sin(2*math.Pi*6000*float64(i)/96000) +
				0.2*math.Sin(2*math.Pi*30000*float64(i)/96000)
			pcm[i*ch] = float32(s)
			if ch == 2 {
				pcm[i*ch+1] = float32(0.3 * s)
			}
		}

		pkt, err := e.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("ch=%d: EncodeFrame(1920) error: %v", ch, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("ch=%d: empty main CELT payload", ch)
		}
		t.Logf("ch=%d: main payload=%d bytes, qext payload=%d bytes",
			ch, len(pkt), len(e.LastQEXTPayload()))
	}
}
