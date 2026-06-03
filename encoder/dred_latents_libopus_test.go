//go:build gopus_dred || gopus_extra_controls

package encoder

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var (
	encoderLibopusLatentsTraceOnce sync.Once
	encoderLibopusLatentsTracePath string
	encoderLibopusLatentsTraceErr  error

	encoderLibopusPitchBlobOnce sync.Once
	encoderLibopusPitchBlob     []byte
	encoderLibopusPitchBlobErr  error

	encoderLibopusDREDBlobOnce sync.Once
	encoderLibopusDREDBlob     []byte
	encoderLibopusDREDBlobErr  error
)

type encoderLibopusDREDFrameTrace struct {
	frameIdx    int
	latentsFill int32
	dredOffset  int32
	latentOff   int32
	latents     [][rdovae.LatentDim]float32
}

func TestEncoderDREDInitialLatentsTraceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{960, 1920, 2880} {
			channels := channels
			frameSize := frameSize
			t.Run(fmt.Sprintf("%dch_%d", channels, frameSize), func(t *testing.T) {
				const (
					sampleRate = 48000
					extraDelay = sampleRate / 250
				)

				want := probeEncoderLibopusDREDLatentsTrace(t, channels, frameSize)
				if len(want) == 0 {
					t.Fatal("libopus DRED latents trace is empty")
				}
				blob := requireEncoderLibopusNeuralModelBlob(t)
				parsed, err := dnnblob.Clone(blob)
				if err != nil {
					t.Fatalf("Clone libopus encoder model blob: %v", err)
				}

				enc := NewEncoder(sampleRate, channels)
				enc.SetDNNBlob(parsed)
				if err := enc.SetDREDDuration(4); err != nil {
					t.Fatalf("SetDREDDuration error: %v", err)
				}

				got := make([]encoderLibopusDREDFrameTrace, len(want))
				for frameIdx := range want {
					frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
					emitted := enc.processDREDLatents(frame, extraDelay)
					if emitted == 0 {
						t.Fatalf("frame %d processDREDLatents emitted 0", frameIdx)
					}
					got[frameIdx] = snapshotEncoderDREDTrace(t, enc, frameIdx)
				}

				compareEncoderDREDTraces(t, got, want)
			})
		}
	}
}

func TestEncoderDREDLongCELTLatentsUseLibopusSubframeCadence(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			const (
				sampleRate = 48000
				frameSize  = 2880
				chunkSize  = 960
				extraDelay = sampleRate / 250
			)

			want := probeEncoderLibopusDREDLatentsTraceWithChunkSize(t, channels, frameSize, chunkSize)
			if len(want) == 0 {
				t.Fatal("libopus DRED latents trace is empty")
			}
			blob := requireEncoderLibopusNeuralModelBlob(t)
			parsed, err := dnnblob.Clone(blob)
			if err != nil {
				t.Fatalf("Clone libopus encoder model blob: %v", err)
			}

			enc := NewEncoder(sampleRate, channels)
			enc.SetDNNBlob(parsed)
			if err := enc.SetDREDDuration(4); err != nil {
				t.Fatalf("SetDREDDuration error: %v", err)
			}

			got := make([]encoderLibopusDREDFrameTrace, len(want))
			for frameIdx := range want {
				frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
				emitted := enc.processDREDLatentsForPacket(frame, frameSize, extraDelay, ModeCELT)
				if emitted != frameSize/chunkSize {
					t.Fatalf("frame %d processDREDLatentsForPacket emitted %d want %d", frameIdx, emitted, frameSize/chunkSize)
				}
				got[frameIdx] = snapshotEncoderDREDTrace(t, enc, frameIdx)
			}

			compareEncoderDREDTraces(t, got, want)
		})
	}
}

func TestEncoderDREDSetDNNBlobPreservesActiveRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	raw := requireEncoderLibopusNeuralModelBlob(t)
	newBlob := func() *dnnblob.Blob {
		t.Helper()
		blob, err := dnnblob.Clone(raw)
		if err != nil {
			t.Fatalf("Clone libopus encoder model blob: %v", err)
		}
		return blob
	}

	const (
		sampleRate  = 16000
		channels    = 1
		frameSize   = 320
		frameCount  = 7
		reloadFrame = 3
	)

	ref := NewEncoder(sampleRate, channels)
	ref.SetDNNBlob(newBlob())
	if err := ref.SetDREDDuration(4); err != nil {
		t.Fatalf("reference SetDREDDuration error: %v", err)
	}
	reloaded := NewEncoder(sampleRate, channels)
	reloaded.SetDNNBlob(newBlob())
	if err := reloaded.SetDREDDuration(4); err != nil {
		t.Fatalf("reloaded SetDREDDuration error: %v", err)
	}

	want := make([]encoderLibopusDREDFrameTrace, frameCount)
	got := make([]encoderLibopusDREDFrameTrace, frameCount)
	for frameIdx := 0; frameIdx < frameCount; frameIdx++ {
		frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
		if emitted := ref.processDREDLatents(frame, 0); emitted != 1 {
			t.Fatalf("reference frame %d emitted %d want 1", frameIdx, emitted)
		}
		want[frameIdx] = snapshotEncoderDREDTrace(t, ref, frameIdx)

		if frameIdx == reloadFrame {
			if reloaded.dred == nil || reloaded.dred.runtime == nil {
				t.Fatal("reloaded encoder has no active DRED runtime before reload")
			}
			runtimeBeforeReload := reloaded.dred.runtime
			reloaded.SetDNNBlob(newBlob())
			if reloaded.dred.runtime != runtimeBeforeReload {
				t.Fatal("SetDNNBlob replaced active DRED runtime")
			}
		}
		if emitted := reloaded.processDREDLatents(frame, 0); emitted != 1 {
			t.Fatalf("reloaded frame %d emitted %d want 1", frameIdx, emitted)
		}
		got[frameIdx] = snapshotEncoderDREDTrace(t, reloaded, frameIdx)
	}

	compareEncoderDREDTraces(t, got, want)
}

func snapshotEncoderDREDTrace(t *testing.T, enc *Encoder, frameIdx int) encoderLibopusDREDFrameTrace {
	t.Helper()
	if enc.dred == nil || enc.dred.runtime == nil {
		t.Fatal("DRED runtime is nil")
	}
	rt := enc.dred.runtime
	count := rt.latentsFill
	if count > 4 {
		count = 4
	}
	trace := encoderLibopusDREDFrameTrace{
		frameIdx:    frameIdx,
		latentsFill: rt.latentsFill,
		dredOffset:  rt.dredOffset,
		latentOff:   rt.latentOffset,
		latents:     make([][rdovae.LatentDim]float32, int(count)),
	}
	for pos := 0; pos < int(count); pos++ {
		copy(trace.latents[pos][:], rt.latentsBuffer[pos*rdovae.LatentDim:(pos+1)*rdovae.LatentDim])
	}
	return trace
}

// encoderDREDLatentTraceTolerance is the tight per-latent absolute tolerance
// for the DRED RDOVAE latent-trace parity comparison on every build except
// darwin/arm64 (see encoderDREDLatentTraceToleranceDarwinArm64).
//
// The libopus reference is always built and run NATIVELY on the same runner as
// gopus (the helper compiles the libopus C DRED encoder from the runner's own
// tree, configured --disable-asm/--disable-intrinsics so it uses the scalar DNN
// kernels), so the reduction ORDER is identical to gopus's sgemv on every
// platform. On linux/amd64 gcc leaves the scalar `acc += w*x` unfused by default
// and gopus's !arm64 sgemvSplit is likewise unfused, so the two agree to within
// this bound (latents are O(1)).
const encoderDREDLatentTraceTolerance = 5e-3

// encoderDREDLatentTraceToleranceDarwinArm64 is the documented FMA-contraction
// tolerance for this trace on darwin/arm64. gopus's arm64 sgemvFused always
// emits a fused multiply-add (fma32 -> FMADD, single rounding per term), while
// whether the libopus scalar reduction `y[k] += w*xj` contracts to FMADD is left
// to the runner's clang `-ffp-contract` heuristic and so varies by Xcode version.
// Building the same pinned libopus source two ways on one darwin/arm64 host
// proves the gap is purely this contraction choice and not a gopus numerics
// error (all 1ch/2ch x 960/1920/2880 cases):
//
//	gopus vs default/-ffp-contract=on (fused) oracle: maxDiff = 0 (byte-exact)
//	gopus vs -ffp-contract=off (unfused) oracle:       maxDiff up to ~1.0
//	fused vs unfused oracle (same source):             same up-to-~1.0 self-variance
//
// So gopus reproduces the fused reference exactly; the divergence is the C
// reference disagreeing with itself across clang contraction modes, amplified
// through the 5-layer GRU/Conv RDOVAE stack. Two real darwin/arm64 default-clang
// data points bracket the realistic spread: Apple clang 21 fully contracts
// (maxDiff 0) and the CI macOS-arm64 runner partially contracts (observed
// first-violation 0.0068). `-ffp-contract=off` is an explicit non-default flag CI
// never passes (clang's standards default is contract=on), so its ~1.0 extreme is
// out of scope. The residual scales with latent magnitude — it peaks at the
// freshest, largest-magnitude latents of the later frames, where the GRU
// recurrence has amplified the contraction choice most (e.g. ~0.5 absolute at
// |latent|~53 on the CI runner) — so the darwin/arm64 bound is magnitude-relative:
// encoderDREDLatentTraceRelToleranceDarwinArm64 of |latent|, with an absolute
// floor for the small latents. That stays well under the O(1)..~50 latent scale,
// so it still catches any real (qualitative) latent shift while absorbing the
// contraction spread. amd64/linux keep the tight absolute
// encoderDREDLatentTraceTolerance. Mirrors the documented "Apple clang may
// contract the arm64 float accumulation inside libopus" residual already handled
// in celt/math_approx_libopus_test.go.
const encoderDREDLatentTraceToleranceDarwinArm64 = 1e-1    // absolute floor (small latents)
const encoderDREDLatentTraceRelToleranceDarwinArm64 = 0.05 // fraction of |latent| (large latents)

func compareEncoderDREDTraces(t *testing.T, got, want []encoderLibopusDREDFrameTrace) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("trace count=%d want %d", len(got), len(want))
	}
	tol := encoderDREDLatentTraceTolerance
	relTol := 0.0
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		tol = encoderDREDLatentTraceToleranceDarwinArm64
		relTol = encoderDREDLatentTraceRelToleranceDarwinArm64
	}
	for i := range want {
		if got[i].frameIdx != want[i].frameIdx {
			t.Fatalf("trace %d frameIdx=%d want %d", i, got[i].frameIdx, want[i].frameIdx)
		}
		if got[i].latentsFill != want[i].latentsFill {
			t.Fatalf("frame %d latentsFill=%d want %d", i, got[i].latentsFill, want[i].latentsFill)
		}
		if got[i].dredOffset != want[i].dredOffset {
			t.Fatalf("frame %d dredOffset=%d want %d", i, got[i].dredOffset, want[i].dredOffset)
		}
		if got[i].latentOff != want[i].latentOff {
			t.Fatalf("frame %d latentOff=%d want %d", i, got[i].latentOff, want[i].latentOff)
		}
		if len(got[i].latents) != len(want[i].latents) {
			t.Fatalf("frame %d latent rows=%d want %d", i, len(got[i].latents), len(want[i].latents))
		}
	}
	// Scan the whole trace and report the worst per-latent tolerance excess
	// rather than failing on the first violation; each latent is compared against
	// its magnitude-aware allowance (the FMA-contraction residual peaks at the
	// freshest, largest latents of the later frames, where GRU recurrence has
	// amplified it most).
	worstExcess := 0.0
	worstLoc := ""
	worstDiff := 0.0
	worstTol := 0.0
	for i := range want {
		for pos := range want[i].latents {
			for k := 0; k < rdovae.LatentDim; k++ {
				w := want[i].latents[pos][k]
				diff := math.Abs(float64(got[i].latents[pos][k] - w))
				allowed := tol
				if rel := relTol * math.Abs(float64(w)); rel > allowed {
					allowed = rel
				}
				if excess := diff - allowed; excess > worstExcess {
					worstExcess = excess
					worstDiff = diff
					worstTol = allowed
					worstLoc = fmt.Sprintf("frame %d row %d k=%d latent=%v want %v", i, pos, k, got[i].latents[pos][k], w)
				}
			}
		}
	}
	if worstExcess > 0 {
		t.Fatalf("%s diff=%v tol=%v", worstLoc, worstDiff, worstTol)
	}
}

func encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels int) []opusRes {
	pcm := make([]opusRes, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := opusRes(encoderLibopusDREDTraceSample(frameIdx, i, frameSize, sampleRate))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func encoderLibopusDREDTraceSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func requireEncoderLibopusNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	pitchBlob := probeEncoderLibopusPitchDNNBlob(t)
	dredBlob := probeEncoderLibopusDREDEncoderBlob(t)
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob
}

func probeEncoderLibopusPitchDNNBlob(t *testing.T) []byte {
	t.Helper()
	encoderLibopusPitchBlobOnce.Do(func() {
		binPath, err := buildEncoderLibopusDREDHelper("libopus_pitchdnn_model_blob.c", "gopus_encoder_libopus_pitchdnn_model_blob", true)
		if err != nil {
			encoderLibopusPitchBlobErr = err
			return
		}
		encoderLibopusPitchBlob, encoderLibopusPitchBlobErr = runEncoderLibopusModelBlobHelper(binPath)
	})
	if encoderLibopusPitchBlobErr != nil {
		libopustest.HelperUnavailable(t, "pitch model blob", encoderLibopusPitchBlobErr)
	}
	return encoderLibopusPitchBlob
}

func probeEncoderLibopusDREDEncoderBlob(t *testing.T) []byte {
	t.Helper()
	encoderLibopusDREDBlobOnce.Do(func() {
		binPath, err := buildEncoderLibopusDREDHelper("libopus_dred_encoder_model_blob.c", "gopus_encoder_libopus_dred_encoder_model_blob", true)
		if err != nil {
			encoderLibopusDREDBlobErr = err
			return
		}
		encoderLibopusDREDBlob, encoderLibopusDREDBlobErr = runEncoderLibopusModelBlobHelper(binPath)
	})
	if encoderLibopusDREDBlobErr != nil {
		libopustest.HelperUnavailable(t, "DRED encoder model blob", encoderLibopusDREDBlobErr)
	}
	return encoderLibopusDREDBlob
}

func probeEncoderLibopusDREDLatentsTrace(t *testing.T, channels, frameSize int) []encoderLibopusDREDFrameTrace {
	return probeEncoderLibopusDREDLatentsTraceWithChunkSize(t, channels, frameSize, frameSize)
}

func probeEncoderLibopusDREDLatentsTraceWithChunkSize(t *testing.T, channels, frameSize, chunkSize int) []encoderLibopusDREDFrameTrace {
	t.Helper()
	binPath, err := getEncoderLibopusDREDLatentsTracePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED latents trace", err)
	}
	data, err := libopustest.RunHelperArgs(binPath, nil, fmt.Sprintf("%d", channels), fmt.Sprintf("%d", frameSize), fmt.Sprintf("%d", chunkSize))
	if err != nil {
		t.Fatalf("run libopus DRED latents trace helper: %v", err)
	}
	return parseEncoderLibopusDREDLatentsTrace(t, data)
}

func parseEncoderLibopusDREDLatentsTrace(t *testing.T, data []byte) []encoderLibopusDREDFrameTrace {
	t.Helper()
	var traces []encoderLibopusDREDFrameTrace
	for offset := 0; offset < len(data); {
		if len(data)-offset < 24 || string(data[offset:offset+4]) != "GDLT" {
			t.Fatalf("unexpected libopus trace output at offset %d", offset)
		}
		trace := encoderLibopusDREDFrameTrace{
			frameIdx:    int(binary.LittleEndian.Uint32(data[offset+4 : offset+8])),
			latentsFill: int32(binary.LittleEndian.Uint32(data[offset+8 : offset+12])),
			dredOffset:  int32(binary.LittleEndian.Uint32(data[offset+12 : offset+16])),
			latentOff:   int32(binary.LittleEndian.Uint32(data[offset+16 : offset+20])),
		}
		positionCount := int(binary.LittleEndian.Uint32(data[offset+20 : offset+24]))
		pos := offset + 24
		trace.latents = make([][rdovae.LatentDim]float32, positionCount)
		for i := 0; i < positionCount; i++ {
			if len(data)-pos < 4*rdovae.LatentDim {
				t.Fatalf("truncated libopus trace at offset %d", pos)
			}
			for k := 0; k < rdovae.LatentDim; k++ {
				trace.latents[i][k] = math.Float32frombits(binary.LittleEndian.Uint32(data[pos+4*k : pos+4*(k+1)]))
			}
			pos += 4 * rdovae.LatentDim
		}
		traces = append(traces, trace)
		offset = pos
	}
	return traces
}

func getEncoderLibopusDREDLatentsTracePath() (string, error) {
	encoderLibopusLatentsTraceOnce.Do(func() {
		encoderLibopusLatentsTracePath, encoderLibopusLatentsTraceErr = buildEncoderLibopusDREDHelper("libopus_dred_latents_trace.c", "gopus_encoder_libopus_dred_latents_trace", true)
	})
	if encoderLibopusLatentsTraceErr != nil {
		return "", encoderLibopusLatentsTraceErr
	}
	return encoderLibopusLatentsTracePath, nil
}

func runEncoderLibopusModelBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
}

func buildEncoderLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, ".."))
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, includeInternal)
}
