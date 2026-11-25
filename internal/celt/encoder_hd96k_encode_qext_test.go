//go:build gopus_qext

package celt

import (
	"math"
	"testing"
)

func TestEncoderEnableHD96kMode(t *testing.T) {
	for _, ch := range []int{1, 2} {
		e := NewEncoder(ch)
		if e.HD96kEncodeEnabled() {
			t.Fatalf("ch=%d: HD96k unexpectedly enabled on fresh encoder", ch)
		}
		if got := e.analysisOverlap(); got != Overlap {
			t.Fatalf("ch=%d: 48k analysis overlap = %d, want %d", ch, got, Overlap)
		}

		e.EnableHD96kMode()
		if !e.HD96kEncodeEnabled() {
			t.Fatalf("ch=%d: HD96k not enabled after EnableHD96kMode", ch)
		}
		m := NewHD96kMode()
		if got := e.analysisOverlap(); got != m.Overlap {
			t.Fatalf("ch=%d: HD analysis overlap = %d, want %d", ch, got, m.Overlap)
		}
		if e.sampleRate != int32(m.Fs) {
			t.Fatalf("ch=%d: sampleRate = %d, want %d", ch, e.sampleRate, m.Fs)
		}
		if e.hd96kPreemph != m.Preemph {
			t.Fatalf("ch=%d: HD preemph = %v, want %v", ch, e.hd96kPreemph, m.Preemph)
		}
		if len(e.overlapBuffer) < m.Overlap*ch {
			t.Fatalf("ch=%d: overlap history len = %d, want >= %d", ch, len(e.overlapBuffer), m.Overlap*ch)
		}

		// Idempotent: a second call must not change state.
		e.EnableHD96kMode()
		if !e.HD96kEncodeEnabled() || e.analysisOverlap() != m.Overlap {
			t.Fatalf("ch=%d: EnableHD96kMode not idempotent", ch)
		}
	}
}

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

func TestQEXTEnergyHistorySurvivesControlToggleAndResetsAllChannels(t *testing.T) {
	e := NewEncoder(2)
	e.SetQEXTEnabled(true)
	history := e.ensureQEXTOldBandE(2)
	for i := range history {
		history[i] = celtGLog(float32(i+1) * 0.125)
	}

	e.SetQEXTEnabled(false)
	mono := e.ensureQEXTOldBandE(1)
	if len(mono) != MaxBands || len(e.qext.oldBandE) != 2*MaxBands {
		t.Fatalf("mono view=%d stored history=%d; want %d and %d", len(mono), len(e.qext.oldBandE), MaxBands, 2*MaxBands)
	}
	e.SetQEXTEnabled(true)
	for i, got := range e.ensureQEXTOldBandE(2) {
		want := celtGLog(float32(i+1) * 0.125)
		if got != want {
			t.Fatalf("control toggle changed history[%d]: got %v want %v", i, got, want)
		}
	}
	if allocs := testing.AllocsPerRun(1000, func() { e.ensureQEXTOldBandE(1) }); allocs != 0 {
		t.Fatalf("warmed QEXT history view allocated %.1f times", allocs)
	}

	e.Reset()
	if !e.QEXTEnabled() {
		t.Fatal("reset changed the QEXT control")
	}
	for i, got := range e.ensureQEXTOldBandE(2) {
		if got != 0 {
			t.Fatalf("reset retained history[%d]=%v", i, got)
		}
	}
}
