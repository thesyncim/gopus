//go:build !windows
// +build !windows

package cgo

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestCELTFirstDivergenceTrace captures gopus and libopus CELT traces
// for the first packet that falls below a target SNR threshold.
// Set GOPUS_TRACE_CELT=1 to enable. Optional:
//
//	GOPUS_TRACE_VECTOR (default: testvector01)
//	GOPUS_TRACE_PACKET (explicit packet index)
//	GOPUS_TRACE_SNR (default: 80)
//	GOPUS_TRACE_RMS (default: 1e-4)
func TestCELTFirstDivergenceTrace(t *testing.T) {
	if os.Getenv("GOPUS_TRACE_CELT") == "" {
		t.Skip("set GOPUS_TRACE_CELT=1 to run")
	}

	vector := getenvDefault("GOPUS_TRACE_VECTOR", "testvector01")
	targetSNR := getenvFloat("GOPUS_TRACE_SNR", 80.0)
	rmsFloor := getenvFloat("GOPUS_TRACE_RMS", 1e-4)

	bitFile := filepath.Join("..", "..", "..", "internal", "testvectors", "testdata", "opus_testvectors", vector+".bit")
	packets, err := loadPacketsSimple(bitFile, -1)
	if err != nil {
		t.Fatalf("load packets: %v", err)
	}
	if len(packets) == 0 {
		t.Fatalf("no packets in %s", bitFile)
	}

	packetIdx := -1
	if v := os.Getenv("GOPUS_TRACE_PACKET"); v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil {
			packetIdx = idx
		}
	}

	if packetIdx < 0 {
		foundSNR := 0.0
		packetIdx, foundSNR, _ = findFirstCELTDivergence(t, packets, targetSNR, rmsFloor)
		if packetIdx < 0 {
			t.Fatalf("no CELT packet below SNR threshold %.1f dB (rms>%.2g)", targetSNR, rmsFloor)
		}
		_ = foundSNR
	}

	if packetIdx >= len(packets) {
		t.Fatalf("packet index %d out of range (len=%d)", packetIdx, len(packets))
	}

	// Capture gopus trace.
	goTrace, err := captureGopusTraceForPacket(packets, packetIdx, 2)
	if err != nil {
		t.Fatalf("gopus trace: %v", err)
	}

	// Capture libopus trace.
	libTrace, err := captureLibopusTraceForPacket(packets, packetIdx, 2)
	if err != nil {
		t.Fatalf("libopus trace: %v", err)
	}

	outDir := t.TempDir()
	if baseDir := os.Getenv("GOPUS_TRACE_DIR"); baseDir != "" {
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			t.Fatalf("create GOPUS_TRACE_DIR: %v", err)
		}
		outDir = filepath.Join(baseDir, fmt.Sprintf("celt_%s_pkt_%d_%d", vector, packetIdx, time.Now().UnixNano()))
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("create trace dir: %v", err)
		}
	}
	goPath := filepath.Join(outDir, fmt.Sprintf("%s_pkt_%d_gopus.trace", vector, packetIdx))
	libPath := filepath.Join(outDir, fmt.Sprintf("%s_pkt_%d_libopus.trace", vector, packetIdx))

	if err := os.WriteFile(goPath, []byte(goTrace), 0o644); err != nil {
		t.Fatalf("write gopus trace: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libTrace), 0o644); err != nil {
		t.Fatalf("write libopus trace: %v", err)
	}

	toc := gopus.ParseTOC(packets[packetIdx][0])
	t.Logf("vector=%s packet=%d mode=%v stereo=%v frameSize=%d", vector, packetIdx, toc.Mode, toc.Stereo, toc.FrameSize)
	t.Logf("gopus trace: %s", goPath)
	t.Logf("libopus trace: %s", libPath)
}

func findFirstCELTDivergence(t *testing.T, packets [][]byte, snrThreshold, rmsFloor float64) (int, float64, float64) {
	goDec, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("gopus decoder: %v", err)
	}
	libDec, err := NewLibopusDecoder(48000, 2)
	if err != nil || libDec == nil {
		t.Fatalf("libopus decoder create failed")
	}
	defer libDec.Destroy()

	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeCELT {
			_, _ = goDec.DecodeFloat32(pkt)
			_, _ = libDec.DecodeFloat(pkt, 5760)
			continue
		}
		goOut, goErr := goDec.DecodeFloat32(pkt)
		if goErr != nil {
			continue
		}
		libOut, libN := libDec.DecodeFloat(pkt, 5760)
		if libN <= 0 {
			continue
		}
		n := libN * 2
		if len(goOut) < n {
			n = len(goOut)
		}
		snr, rms := snrAndRMS(goOut[:n], libOut[:n])
		if rms > rmsFloor && snr < snrThreshold {
			return i, snr, rms
		}
	}

	return -1, 0, 0
}

func captureGopusTraceForPacket(packets [][]byte, idx int, channels int) (string, error) {
	dec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		return "", err
	}

	for i := 0; i < idx; i++ {
		if _, err := dec.DecodeFloat32(packets[i]); err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	tracer := &diffTrace{w: &buf}
	orig := celt.DefaultTracer
	celt.SetTracer(tracer)
	defer celt.SetTracer(orig)

	if _, err := dec.DecodeFloat32(packets[idx]); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func captureLibopusTraceForPacket(packets [][]byte, idx int, channels int) (string, error) {
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		return "", fmt.Errorf("libopus decoder create failed")
	}
	defer libDec.Destroy()

	for i := 0; i < idx; i++ {
		libDec.DecodeFloat(packets[i], 5760)
	}

	SetLibopusDebugRange(true)
	defer SetLibopusDebugRange(false)

	trace, err := captureStderr(func() {
		libDec.DecodeFloat(packets[idx], 5760)
		FlushLibopusTrace()
	})
	if err != nil {
		return "", err
	}
	return trace, nil
}

func captureStderr(fn func()) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer r.Close()

	oldFD, err := syscall.Dup(2)
	if err != nil {
		_ = w.Close()
		return "", err
	}
	if err := syscall.Dup2(int(w.Fd()), 2); err != nil {
		_ = syscall.Close(oldFD)
		_ = w.Close()
		return "", err
	}
	_ = w.Close()

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()

	_ = syscall.Dup2(oldFD, 2)
	_ = syscall.Close(oldFD)
	_ = r.Close()
	<-done

	return buf.String(), nil
}

type diffTrace struct {
	w *bytes.Buffer
}

func (t *diffTrace) TraceHeader(frameSize, channels, lm, intra, transient int) {
	fmt.Fprintf(t.w, "[CELT:header] frameSize=%d channels=%d lm=%d intra=%d transient=%d\n", frameSize, channels, lm, intra, transient)
}

func (t *diffTrace) TraceEnergy(band int, coarse, fine, total float64) {
	fmt.Fprintf(t.w, "[CELT:energy] band=%d coarse=%.4f fine=%.4f total=%.4f\n", band, coarse, fine, total)
}

func (t *diffTrace) TraceAllocation(band, bits, k int) {
	fmt.Fprintf(t.w, "[CELT:alloc] band=%d bits=%d k=%d\n", band, bits, k)
}

func (t *diffTrace) TracePVQ(band int, index uint32, k, n int, pulses []int) {
	fmt.Fprintf(t.w, "[CELT:pvq] band=%d index=%d k=%d n=%d pulses=%s\n", band, index, k, n, formatIntSlice(pulses, 8))
}

func (t *diffTrace) TraceCoeffs(band int, coeffs []float64) {
	fmt.Fprintf(t.w, "[CELT:coeffs] band=%d vals=%s\n", band, formatFloatSlice(coeffs, 8))
}

func (t *diffTrace) TraceSynthesis(stage string, samples []float64) {
	if stage == "synth_pre" {
		fmt.Fprintf(t.w, "[CELT:synth_pre] vals=%s\n", formatFloatSlice(samples, 8))
		return
	}
	fmt.Fprintf(t.w, "[CELT:%s] vals=%s\n", stage, formatFloatSlice(samples, 8))
}

func (t *diffTrace) TraceRange(stage string, rng uint32, tell, tellFrac int) {
	fmt.Fprintf(t.w, "[%s] rng=0x%08X tell=%d tell_frac=%d\n", stage, rng, tell, tellFrac)
}

func (t *diffTrace) TraceTF(band int, val int) {
	fmt.Fprintf(t.w, "[CELT:tf] band=%d val=%d\n", band, val)
}

func (t *diffTrace) TraceFlag(name string, value int) {
	if name == "anticollapse_on" {
		fmt.Fprintf(t.w, "[CELT:anticollapse_on] val=%d\n", value)
	}
}

func (t *diffTrace) TraceLowband(band int, lowbandOffset int, effectiveLowband int, lowband []float64) {
	fmt.Fprintf(t.w, "[CELT:lowband] band=%d lowband_offset=%d effective_lowband=%d vals=%s\n",
		band, lowbandOffset, effectiveLowband, formatFloatSlice(lowband, 8))
}

func (t *diffTrace) TraceEnergyFine(band int, channel int, energy float64) {
	fmt.Fprintf(t.w, "[CELT:energy_fine] band=%d ch=%d val=%.4f\n", band, channel, energy)
}

func (t *diffTrace) TraceEnergyFinal(band int, channel int, energy float64) {
	fmt.Fprintf(t.w, "[CELT:energy_final] band=%d ch=%d val=%.4f\n", band, channel, energy)
}

func formatIntSlice(v []int, maxLen int) string {
	if len(v) == 0 {
		return "[]"
	}
	n := len(v)
	if n > maxLen {
		n = maxLen
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(v[i]))
	}
	if len(v) > maxLen {
		sb.WriteString("...")
	}
	sb.WriteByte(']')
	return sb.String()
}

func formatFloatSlice(v []float64, maxLen int) string {
	if len(v) == 0 {
		return "[]"
	}
	n := len(v)
	if n > maxLen {
		n = maxLen
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%.4f", v[i])
	}
	if len(v) > maxLen {
		sb.WriteString("...")
	}
	sb.WriteByte(']')
	return sb.String()
}

func snrAndRMS(goOut, libOut []float32) (float64, float64) {
	n := len(goOut)
	if len(libOut) < n {
		n = len(libOut)
	}
	if n == 0 {
		return 0, 0
	}
	var signal, noise float64
	for i := 0; i < n; i++ {
		diff := float64(goOut[i]) - float64(libOut[i])
		noise += diff * diff
		signal += float64(libOut[i]) * float64(libOut[i])
	}
	rms := math.Sqrt(signal / float64(n))
	if noise == 0 {
		return 999.0, rms
	}
	return 10 * math.Log10(signal/noise), rms
}

func getenvDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func getenvFloat(name string, def float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
