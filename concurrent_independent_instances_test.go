package gopus

import (
	"hash"
	"hash/fnv"
	"math"
	"runtime"
	"sync"
	"testing"
)

// This file stress-tests the real-world server pattern: many parallel streams,
// each owning its OWN codec instances. Independent instances MUST be safe for
// concurrent use, and construction (which may trigger lazy global table init)
// MUST be race-free. A single instance shared across goroutines is intentionally
// out of scope (libopus is not safe in that mode either).
//
// Run with -race to surface accidental shared mutable state (package-level
// scratch buffers, unsynchronized lazy table init, shared caches).

// concurrentStreamSpec describes one independent worker stream. Distinct
// frequency + mode/bitrate per worker exercises SILK/CELT/Hybrid lazy tables.
type concurrentStreamSpec struct {
	sampleRate int
	channels   int
	app        Application
	mode       EncoderMode
	bitrate    int
	freq       float64
	frames     int
	frameSize  int // samples per channel per Encode call (internal-rate frames)
}

// concurrentStreamSpecs returns a deterministic mix of stream configurations
// that together cover SILK-only, CELT-only and Hybrid coding paths plus mono
// and stereo, so the lazily-built CELT tables (cwrs/pvq, pulse cache, FFT
// states) and SILK paths are all touched concurrently.
//
// frameSize is the libopus 48 kHz-equivalent frame-size unit the public API
// uses (one of 120/240/480/960/...); the encoder consumes frameSize*channels
// PCM samples at the API rate regardless of that rate.
func concurrentStreamSpecs() []concurrentStreamSpec {
	return []concurrentStreamSpec{
		{48000, 2, ApplicationAudio, EncoderModeCELT, 96000, 440, 24, 960},
		{48000, 1, ApplicationVoIP, EncoderModeSILK, 24000, 330, 24, 960},
		{48000, 2, ApplicationAudio, EncoderModeHybrid, 64000, 550, 24, 960},
		{16000, 1, ApplicationVoIP, EncoderModeSILK, 16000, 300, 24, 960},
		{24000, 2, ApplicationAudio, EncoderModeCELT, 48000, 700, 24, 480},
		{12000, 1, ApplicationVoIP, EncoderModeSILK, 12000, 250, 24, 960},
		{8000, 1, ApplicationVoIP, EncoderModeSILK, 10000, 200, 24, 960},
		{48000, 2, ApplicationLowDelay, EncoderModeCELT, 128000, 880, 24, 480},
	}
}

// concurrentRunStream encodes+decodes one full stream end-to-end through a
// freshly constructed Encoder/Decoder pair and returns a digest of every
// encoded packet plus the full decoded PCM. Running the identical spec serially
// and concurrently must yield the identical digest: any divergence means
// cross-instance state leaked through a shared global.
func concurrentRunStream(t *testing.T, spec concurrentStreamSpec) uint64 {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  spec.sampleRate,
		Channels:    spec.channels,
		Application: spec.app,
	})
	if err != nil {
		t.Fatalf("NewEncoder(%+v): %v", spec, err)
	}
	if err := enc.SetMode(spec.mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetBitrate(spec.bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetFrameSize(spec.frameSize); err != nil {
		t.Fatalf("SetFrameSize(%d): %v", spec.frameSize, err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(spec.sampleRate, spec.channels))
	if err != nil {
		t.Fatalf("NewDecoder(%+v): %v", spec, err)
	}

	pcm := generateSineWaveFloat32(spec.sampleRate, spec.freq, spec.frameSize, spec.channels)

	h := fnv.New64a()
	encBuf := make([]byte, defaultMaxPacketBytes)
	decBuf := make([]float32, spec.frameSize*spec.channels)
	var scratch [8]byte

	for f := 0; f < spec.frames; f++ {
		// Vary the input slightly per frame so the encoder state evolves and
		// stale shared scratch would corrupt the output deterministically.
		concurrentPerturb(pcm, f)

		n, err := enc.Encode(pcm, encBuf)
		if err != nil {
			t.Fatalf("Encode frame %d (%+v): %v", f, spec, err)
		}
		concurrentHashInt(h, scratch[:], n)
		h.Write(encBuf[:n])

		got, err := dec.Decode(encBuf[:n], decBuf)
		if err != nil {
			t.Fatalf("Decode frame %d (%+v): %v", f, spec, err)
		}
		concurrentHashInt(h, scratch[:], got)
		concurrentHashFloats(h, scratch[:], decBuf[:got*spec.channels])
	}
	return h.Sum64()
}

// concurrentPerturb applies a deterministic, frame-dependent offset to the PCM
// so each Encode call sees distinct input. Pure function of (pcm contents, f).
func concurrentPerturb(pcm []float32, f int) {
	delta := float32((f%7)-3) * 0.0009765625 // small, deterministic
	for i := range pcm {
		v := pcm[i] + delta
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		pcm[i] = v
	}
}

func concurrentHashInt(h hash.Hash64, scratch []byte, v int) {
	u := uint64(v)
	for i := range 8 {
		scratch[i] = byte(u >> (8 * i))
	}
	h.Write(scratch[:8])
}

func concurrentHashFloats(h hash.Hash64, scratch []byte, vs []float32) {
	for _, v := range vs {
		bits := math.Float32bits(v)
		scratch[0] = byte(bits)
		scratch[1] = byte(bits >> 8)
		scratch[2] = byte(bits >> 16)
		scratch[3] = byte(bits >> 24)
		h.Write(scratch[:4])
	}
}

// TestConcurrentIndependentInstancesRoundTrip runs every stream spec serially to
// capture reference digests, then runs many copies of the same specs
// concurrently (each goroutine owning its own Encoder+Decoder) and asserts the
// concurrent digests match the serial references. Combined with -race, this
// proves independent instances neither race nor leak state across goroutines.
func TestConcurrentIndependentInstancesRoundTrip(t *testing.T) {
	specs := concurrentStreamSpecs()

	// Serial reference digests (single-threaded, definitively no contention).
	want := make([]uint64, len(specs))
	for i, spec := range specs {
		want[i] = concurrentRunStream(t, spec)
	}

	// Concurrent burst: replicate the spec set across many goroutines so each
	// spec runs on several independent instances in parallel.
	const replicas = 6
	type result struct {
		specIdx int
		digest  uint64
	}
	results := make(chan result, len(specs)*replicas)

	var wg sync.WaitGroup
	for range replicas {
		for i, spec := range specs {
			wg.Add(1)
			go func(i int, spec concurrentStreamSpec) {
				defer wg.Done()
				results <- result{specIdx: i, digest: concurrentRunStream(t, spec)}
			}(i, spec)
		}
	}
	wg.Wait()
	close(results)

	for res := range results {
		if res.digest != want[res.specIdx] {
			t.Errorf("concurrent stream %d digest %x != serial %x (cross-instance state leak)",
				res.specIdx, res.digest, want[res.specIdx])
		}
	}
}

// TestConcurrentIndependentInstancesConstructionStorm hammers all public
// constructors in parallel to surface races in lazily-initialized package
// globals (mode/window/cwrs/pvq tables, FFT states, pulse caches). Each
// goroutine constructs and lightly exercises a fresh instance.
func TestConcurrentIndependentInstancesConstructionStorm(t *testing.T) {
	workers := max(runtime.GOMAXPROCS(0)*4, 8)
	const iterations = 40

	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start // release all goroutines together to maximize contention
			for it := range iterations {
				concurrentConstructAndProbe(t, w, it)
			}
		}(w)
	}
	close(start)
	wg.Wait()
}

// concurrentConstructAndProbe builds one of each public codec type (rotating by
// worker/iteration) and runs a single encode/decode so any lazy init triggered
// by first use executes under the race detector with maximal concurrency.
func concurrentConstructAndProbe(t *testing.T, w, it int) {
	t.Helper()
	channels := 1 + ((w + it) & 1) // alternate mono/stereo
	const sampleRate = 48000

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Errorf("NewEncoder: %v", err)
		return
	}
	// Rotate the forced mode so CELT/SILK/Hybrid lazy paths all initialize.
	switch (w + it) % 3 {
	case 0:
		_ = enc.SetMode(EncoderModeCELT)
	case 1:
		_ = enc.SetMode(EncoderModeSILK)
	default:
		_ = enc.SetMode(EncoderModeHybrid)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Errorf("NewDecoder: %v", err)
		return
	}

	pcm := generateSineWaveFloat32(sampleRate, 440, 960, channels)
	encBuf := make([]byte, defaultMaxPacketBytes)
	n, err := enc.Encode(pcm, encBuf)
	if err != nil {
		t.Errorf("Encode: %v", err)
		return
	}
	decBuf := make([]float32, 960*channels)
	if _, err := dec.Decode(encBuf[:n], decBuf); err != nil {
		t.Errorf("Decode: %v", err)
		return
	}

	// Multistream constructors share the same underlying mode tables; build a
	// surround pair too. Use the default mapping helpers to keep it valid.
	msEnc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Errorf("NewMultistreamEncoderDefault: %v", err)
		return
	}
	msDec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Errorf("NewMultistreamDecoderDefault: %v", err)
		return
	}
	msPCM := generateSineWaveFloat32(sampleRate, 440, 960, channels)
	msBuf := make([]byte, defaultMaxPacketBytes*channels)
	mn, err := msEnc.Encode(msPCM, msBuf)
	if err != nil {
		t.Errorf("Multistream Encode: %v", err)
		return
	}
	msOut := make([]float32, 960*channels)
	if _, err := msDec.Decode(msBuf[:mn], msOut); err != nil {
		t.Errorf("Multistream Decode: %v", err)
		return
	}
}
