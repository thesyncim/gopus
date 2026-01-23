package celt

import (
	"bytes"
	"strings"
	"testing"
)

// TestLogTracerFormat verifies the LogTracer output format is correct.
func TestLogTracerFormat(t *testing.T) {
	var buf bytes.Buffer
	tracer := &LogTracer{W: &buf}

	// Test TraceHeader
	tracer.TraceHeader(960, 2, 3, 0, 1)
	output := buf.String()
	if !strings.Contains(output, "[CELT:header]") {
		t.Errorf("TraceHeader missing [CELT:header] prefix: %s", output)
	}
	if !strings.Contains(output, "frameSize=960") {
		t.Errorf("TraceHeader missing frameSize: %s", output)
	}
	if !strings.Contains(output, "channels=2") {
		t.Errorf("TraceHeader missing channels: %s", output)
	}
	if !strings.Contains(output, "lm=3") {
		t.Errorf("TraceHeader missing lm: %s", output)
	}
	if !strings.Contains(output, "intra=0") {
		t.Errorf("TraceHeader missing intra: %s", output)
	}
	if !strings.Contains(output, "transient=1") {
		t.Errorf("TraceHeader missing transient: %s", output)
	}
	buf.Reset()

	// Test TraceEnergy
	tracer.TraceEnergy(5, -12.3, 0.2, -12.1)
	output = buf.String()
	if !strings.Contains(output, "[CELT:energy]") {
		t.Errorf("TraceEnergy missing [CELT:energy] prefix: %s", output)
	}
	if !strings.Contains(output, "band=5") {
		t.Errorf("TraceEnergy missing band: %s", output)
	}
	if !strings.Contains(output, "coarse=-12.3") {
		t.Errorf("TraceEnergy missing coarse: %s", output)
	}
	buf.Reset()

	// Test TraceAllocation
	tracer.TraceAllocation(3, 24, 5)
	output = buf.String()
	if !strings.Contains(output, "[CELT:alloc]") {
		t.Errorf("TraceAllocation missing [CELT:alloc] prefix: %s", output)
	}
	if !strings.Contains(output, "band=3") {
		t.Errorf("TraceAllocation missing band: %s", output)
	}
	if !strings.Contains(output, "bits=24") {
		t.Errorf("TraceAllocation missing bits: %s", output)
	}
	if !strings.Contains(output, "k=5") {
		t.Errorf("TraceAllocation missing k: %s", output)
	}
	buf.Reset()

	// Test TracePVQ
	tracer.TracePVQ(2, 1234, 3, 8, []int{1, -1, 0, 1, 0, 0, 0, 0})
	output = buf.String()
	if !strings.Contains(output, "[CELT:pvq]") {
		t.Errorf("TracePVQ missing [CELT:pvq] prefix: %s", output)
	}
	if !strings.Contains(output, "band=2") {
		t.Errorf("TracePVQ missing band: %s", output)
	}
	if !strings.Contains(output, "index=1234") {
		t.Errorf("TracePVQ missing index: %s", output)
	}
	if !strings.Contains(output, "k=3") {
		t.Errorf("TracePVQ missing k: %s", output)
	}
	if !strings.Contains(output, "n=8") {
		t.Errorf("TracePVQ missing n: %s", output)
	}
	if !strings.Contains(output, "pulses=[") {
		t.Errorf("TracePVQ missing pulses: %s", output)
	}
	buf.Reset()

	// Test TraceCoeffs
	tracer.TraceCoeffs(0, []float64{0.12, -0.08, 0.03, 0.15})
	output = buf.String()
	if !strings.Contains(output, "[CELT:coeffs]") {
		t.Errorf("TraceCoeffs missing [CELT:coeffs] prefix: %s", output)
	}
	if !strings.Contains(output, "band=0") {
		t.Errorf("TraceCoeffs missing band: %s", output)
	}
	if !strings.Contains(output, "coeffs=[") {
		t.Errorf("TraceCoeffs missing coeffs: %s", output)
	}
	buf.Reset()

	// Test TraceSynthesis
	tracer.TraceSynthesis("imdct", []float64{0.001, -0.002, 0.003})
	output = buf.String()
	if !strings.Contains(output, "[CELT:synthesis]") {
		t.Errorf("TraceSynthesis missing [CELT:synthesis] prefix: %s", output)
	}
	if !strings.Contains(output, "stage=imdct") {
		t.Errorf("TraceSynthesis missing stage: %s", output)
	}
	if !strings.Contains(output, "samples=[") {
		t.Errorf("TraceSynthesis missing samples: %s", output)
	}
}

// TestLogTracerTruncation verifies arrays are truncated correctly.
func TestLogTracerTruncation(t *testing.T) {
	var buf bytes.Buffer
	tracer := &LogTracer{W: &buf}

	// Test with array longer than 8 elements
	longPulses := make([]int, 20)
	for i := range longPulses {
		longPulses[i] = i
	}
	tracer.TracePVQ(0, 100, 10, 20, longPulses)
	output := buf.String()

	// Should contain "..." to indicate truncation
	if !strings.Contains(output, "...") {
		t.Errorf("Long array should be truncated with '...': %s", output)
	}

	// Should contain first 8 values
	if !strings.Contains(output, "0,1,2,3,4,5,6,7") {
		t.Errorf("Truncated array should contain first 8 values: %s", output)
	}

	buf.Reset()

	// Test with exactly 8 elements (no truncation)
	exactPulses := []int{1, 2, 3, 4, 5, 6, 7, 8}
	tracer.TracePVQ(0, 100, 8, 8, exactPulses)
	output = buf.String()

	// Should NOT contain "..." since exactly 8 elements
	if strings.Contains(output, "...") {
		t.Errorf("Array with 8 elements should not be truncated: %s", output)
	}
}

// TestNoopTracerZeroOverhead verifies NoopTracer methods are empty.
func TestNoopTracerZeroOverhead(t *testing.T) {
	tracer := &NoopTracer{}

	// All methods should do nothing (no panics)
	tracer.TraceHeader(960, 2, 3, 0, 1)
	tracer.TraceEnergy(0, 0, 0, 0)
	tracer.TraceAllocation(0, 0, 0)
	tracer.TracePVQ(0, 0, 0, 0, nil)
	tracer.TraceCoeffs(0, nil)
	tracer.TraceSynthesis("", nil)

	// If we get here without panic, test passes
	t.Log("NoopTracer all methods executed without panic")
}

// TestSetTracerGlobal verifies SetTracer changes the global tracer.
func TestSetTracerGlobal(t *testing.T) {
	// Save original
	original := DefaultTracer
	defer SetTracer(original)

	// Verify we can set a LogTracer
	var buf bytes.Buffer
	logTracer := &LogTracer{W: &buf}
	SetTracer(logTracer)

	if DefaultTracer != logTracer {
		t.Error("SetTracer did not set DefaultTracer to LogTracer")
	}

	// Verify we can set nil (becomes NoopTracer)
	SetTracer(nil)
	if _, ok := DefaultTracer.(*NoopTracer); !ok {
		t.Error("SetTracer(nil) should set DefaultTracer to NoopTracer")
	}
}

// TestTracerInterfaceCalledDuringDecode verifies Tracer methods are called.
func TestTracerInterfaceCalledDuringDecode(t *testing.T) {
	// Save original tracer
	original := DefaultTracer
	defer SetTracer(original)

	// Create tracking tracer
	tracker := &trackingTracer{}
	SetTracer(tracker)

	// Create decoder and decode a frame
	d := NewDecoder(1)
	// Use 0xFF bytes to avoid silence flag in range decoder
	frameData := make([]byte, 32)
	for i := range frameData {
		frameData[i] = 0xFF
	}

	_, err := d.DecodeFrame(frameData, 480)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Verify methods were called
	if !tracker.headerCalled {
		t.Error("TraceHeader was not called during decode")
	}
	if !tracker.energyCalled {
		t.Error("TraceEnergy was not called during decode")
	}
	if !tracker.synthesisCalled {
		t.Error("TraceSynthesis was not called during decode")
	}
	// Note: allocation/pvq/coeffs may not be called if all bands are silence/folded

	t.Logf("Tracer calls: header=%v energy=%v alloc=%v pvq=%v coeffs=%v synthesis=%v",
		tracker.headerCalled, tracker.energyCalled, tracker.allocCalled,
		tracker.pvqCalled, tracker.coeffsCalled, tracker.synthesisCalled)
}

// trackingTracer tracks which methods were called.
type trackingTracer struct {
	headerCalled    bool
	energyCalled    bool
	allocCalled     bool
	pvqCalled       bool
	coeffsCalled    bool
	synthesisCalled bool
}

func (t *trackingTracer) TraceHeader(frameSize, channels, lm, intra, transient int) {
	t.headerCalled = true
}

func (t *trackingTracer) TraceEnergy(band int, coarse, fine, total float64) {
	t.energyCalled = true
}

func (t *trackingTracer) TraceAllocation(band, bits, k int) {
	t.allocCalled = true
}

func (t *trackingTracer) TracePVQ(band int, index uint32, k, n int, pulses []int) {
	t.pvqCalled = true
}

func (t *trackingTracer) TraceCoeffs(band int, coeffs []float64) {
	t.coeffsCalled = true
}

func (t *trackingTracer) TraceSynthesis(stage string, samples []float64) {
	t.synthesisCalled = true
}

// TestFormatIntSlice tests the int slice formatter.
func TestFormatIntSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []int
		maxLen   int
		contains []string
	}{
		{"empty", []int{}, 8, []string{"[]"}},
		{"single", []int{42}, 8, []string{"[42]"}},
		{"under_limit", []int{1, 2, 3}, 8, []string{"[1,2,3]"}},
		{"at_limit", []int{1, 2, 3, 4, 5, 6, 7, 8}, 8, []string{"[1,2,3,4,5,6,7,8]"}},
		{"over_limit", []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, 8, []string{"[1,2,3,4,5,6,7,8...", "..."}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := formatIntSlice(tc.input, tc.maxLen)
			for _, want := range tc.contains {
				if !strings.Contains(result, want) {
					t.Errorf("formatIntSlice(%v, %d) = %q, want contains %q",
						tc.input, tc.maxLen, result, want)
				}
			}
		})
	}
}

// TestFormatFloatSlice tests the float slice formatter.
func TestFormatFloatSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []float64
		maxLen   int
		contains []string
	}{
		{"empty", []float64{}, 8, []string{"[]"}},
		{"single", []float64{1.5}, 8, []string{"[1.5000]"}},
		{"negative", []float64{-0.5}, 8, []string{"[-0.5000]"}},
		{"over_limit", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}, 8, []string{"..."}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := formatFloatSlice(tc.input, tc.maxLen)
			for _, want := range tc.contains {
				if !strings.Contains(result, want) {
					t.Errorf("formatFloatSlice(%v, %d) = %q, want contains %q",
						tc.input, tc.maxLen, result, want)
				}
			}
		})
	}
}
