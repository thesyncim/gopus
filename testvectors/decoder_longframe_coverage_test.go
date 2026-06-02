package testvectors

import "testing"

// Frame-size constants (samples at 48 kHz) for the long-frame decoder coverage
// guards. The "lfc" prefix keeps these unique within the package.
const (
	lfcFrameSize40ms  = 1920
	lfcFrameSize60ms  = 2880
	lfcFrameSize80ms  = 3840
	lfcFrameSize100ms = 4800
	lfcFrameSize120ms = 5760
)

// TestDecoderMatrixLongFrameCoverage asserts that the decoder matrix fixture
// keeps the long/short-frame edge rows that close the PARITY_MATRIX
// "100 ms matrix row" and "Hybrid 60 ms decode" gaps: explicit 80/100/120 ms
// rows and at least one Hybrid row at >= 40 ms (mode histogram carries hybrid).
//
// This runs against whichever platform fixture is active (the darwin/arm64
// default or the linux/amd64 platform file), so both lanes are guarded against
// silently dropping these rows on a future regeneration.
func TestDecoderMatrixLongFrameCoverage(t *testing.T) {
	t.Parallel()
	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("decoder matrix fixture has no cases")
	}

	var (
		have80ms        bool
		have100ms       bool
		have120ms       bool
		haveLongHybrid  bool
		longHybridFrame int
	)
	for _, c := range fixture.Cases {
		switch c.FrameSize {
		case lfcFrameSize80ms:
			have80ms = true
		case lfcFrameSize100ms:
			have100ms = true
		case lfcFrameSize120ms:
			have120ms = true
		}
		if c.FrameSize >= lfcFrameSize40ms && c.ModeHistogram["hybrid"] > 0 {
			haveLongHybrid = true
			if c.FrameSize > longHybridFrame {
				longHybridFrame = c.FrameSize
			}
		}
	}

	if !have80ms {
		t.Errorf("decoder matrix missing 80 ms (frame_size=%d) row", lfcFrameSize80ms)
	}
	if !have100ms {
		t.Errorf("decoder matrix missing 100 ms (frame_size=%d) row", lfcFrameSize100ms)
	}
	if !have120ms {
		t.Errorf("decoder matrix missing 120 ms (frame_size=%d) row", lfcFrameSize120ms)
	}
	if !haveLongHybrid {
		t.Errorf("decoder matrix missing a Hybrid >= 40 ms (frame_size>=%d, mode_histogram[hybrid]>0) row", lfcFrameSize40ms)
	} else if longHybridFrame < lfcFrameSize60ms {
		t.Errorf("decoder matrix Hybrid long-frame coverage tops out at frame_size=%d; want a >= 60 ms (frame_size>=%d) Hybrid row", longHybridFrame, lfcFrameSize60ms)
	}
}

// TestDecoderLossLongFrameCoverage asserts that the loss/PLC fixture extends
// beyond 60 ms, closing the PARITY_MATRIX "loss fixtures beyond 60 ms" gap.
// It requires explicit 80/100/120 ms PLC cases (the libopus opus_decode(NULL)
// concealment path at long frame sizes), each carrying decoded results.
func TestDecoderLossLongFrameCoverage(t *testing.T) {
	t.Parallel()
	fixture, err := loadLibopusDecoderLossFixture()
	if err != nil {
		t.Fatalf("load decoder loss fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("decoder loss fixture has no cases")
	}

	var have80ms, have100ms, have120ms bool
	for _, c := range fixture.Cases {
		if c.FrameSize > lfcFrameSize60ms && len(c.Results) == 0 {
			t.Errorf("decoder loss case %q (frame_size=%d) has no loss results", c.Name, c.FrameSize)
		}
		switch c.FrameSize {
		case lfcFrameSize80ms:
			have80ms = true
		case lfcFrameSize100ms:
			have100ms = true
		case lfcFrameSize120ms:
			have120ms = true
		}
	}

	if !have80ms {
		t.Errorf("decoder loss fixture missing 80 ms (frame_size=%d) case", lfcFrameSize80ms)
	}
	if !have100ms {
		t.Errorf("decoder loss fixture missing 100 ms (frame_size=%d) case", lfcFrameSize100ms)
	}
	if !have120ms {
		t.Errorf("decoder loss fixture missing 120 ms (frame_size=%d) case", lfcFrameSize120ms)
	}
}
