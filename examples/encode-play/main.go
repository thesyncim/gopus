// Package main demonstrates encoding audio with gopus and playing the result.
//
// Usage:
//
//	go run . -play
//	go run . -signal sweep -duration 3 -bitrate 96000 -play
//	go run . -out output.opus
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	examplecleanup "github.com/thesyncim/gopus/examples/internal/cleanup"
	"github.com/thesyncim/gopus/examples/internal/wav"
)

const (
	sampleRate = 48000
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("encode-play", flag.ContinueOnError)
	outPath := flags.String("out", "", "Output Ogg Opus file path (defaults to temp when -play is set)")
	duration := flags.Float64("duration", 2.0, "Duration in seconds")
	bitrate := flags.Int("bitrate", 128000, "Target bitrate in bps")
	channels := flags.Int("channels", 2, "Number of channels (1 or 2)")
	signal := flags.String("signal", "chord", "Signal type: sine, sweep, noise, chord, speech")
	frameSize := flags.Int("frame", 960, "Frame size in samples at 48kHz (e.g., 480, 960, 1920)")
	play := flags.Bool("play", false, "Play the encoded Opus file with ffplay if available")
	libopus := flags.Bool("libopus", false, "Use external libopus encoder (opusenc/ffmpeg) instead of gopus")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *channels < 1 {
		*channels = 1
	}
	if *channels > 2 {
		*channels = 2
	}

	app := gopus.ApplicationAudio

	output := strings.TrimSpace(*outPath)
	tempOutput := false
	cleanup := func() {}
	if output == "" {
		if *play {
			tmp, err := os.CreateTemp("", "gopus_encode_*.opus")
			if err != nil {
				return fmt.Errorf("create temp output: %w", err)
			}
			output = tmp.Name()
			tempOutput = true
			_ = tmp.Close()
			cleanup = func() { _ = os.Remove(output) }
		} else {
			output = "encoded.opus"
		}
	}
	if tempOutput {
		defer func() {
			if cleanup != nil {
				cleanup()
			}
		}()
	}

	var (
		stats encodeStats
		err   error
	)
	if *libopus {
		stats, err = encodeWithLibopus(output, *duration, *bitrate, *channels, *frameSize, *signal)
	} else {
		stats, err = encodeToOgg(output, *duration, *bitrate, *channels, *frameSize, app, *signal)
	}
	if err != nil {
		return fmt.Errorf("encode failed: %w", err)
	}

	fmt.Printf("Encoded: %s\n", output)
	fmt.Printf("  Encoder: %s\n", stats.encoder)
	fmt.Printf("  Duration: %.2fs (%.2fs rendered)\n", stats.requestedDuration, stats.actualDuration)
	fmt.Printf("  Frames: %d\n", stats.frames)
	fmt.Printf("  Channels: %d\n", stats.channels)
	fmt.Printf("  Bitrate: %d kbps\n", stats.bitrate/1000)
	fmt.Printf("  Signal: %s\n", stats.signal)
	fmt.Printf("  Encoded bytes: %d\n", stats.encodedBytes)
	fmt.Printf("  Avg bitrate: %.1f kbps\n", float64(stats.encodedBytes*8)/stats.actualDuration/1000)

	if *play {
		if err := playEncoded(output); err != nil {
			log.Printf("Playback failed: %v", err)
			fmt.Printf("Play the .opus file in a media player: %s\n", output)
			cleanup = nil
		}
	}

	return nil
}

type encodeStats struct {
	requestedDuration float64
	actualDuration    float64
	frames            int
	channels          int
	bitrate           int
	signal            string
	encodedBytes      int
	encoder           string
}

func sampleCountForDuration(duration float64) (int, error) {
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, fmt.Errorf("duration must be a positive finite number")
	}

	sampleCount := math.Round(duration * sampleRate)
	maxSampleCount := float64(int(^uint(0)>>1) - sampleRate*60)
	if math.IsInf(sampleCount, 0) || sampleCount >= maxSampleCount {
		return 0, fmt.Errorf("duration exceeds the supported sample count")
	}
	if sampleCount < 1 {
		return 1, nil
	}
	return int(sampleCount), nil
}

func encodeToOgg(path string, duration float64, bitrate int, channels int, frameSize int, app gopus.Application, signal string) (encodeStats, error) {
	stats := encodeStats{
		requestedDuration: duration,
		channels:          channels,
		bitrate:           bitrate,
		signal:            signal,
		encoder:           "gopus",
	}
	totalSamples, err := sampleCountForDuration(duration)
	if err != nil {
		return stats, err
	}

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: app})
	if err != nil {
		return stats, fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return stats, fmt.Errorf("set bitrate: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return stats, fmt.Errorf("set frame size: %w", err)
	}

	lookahead := enc.Lookahead()
	if lookahead < 0 || lookahead > int(^uint16(0)) {
		return stats, fmt.Errorf("encoder lookahead %d cannot be represented in OpusHead", lookahead)
	}
	frames := 1 + (totalSamples+lookahead-1)/frameSize
	stats.frames = frames
	stats.actualDuration = float64(totalSamples) / sampleRate

	f, err := os.Create(path)
	if err != nil {
		return stats, fmt.Errorf("create output: %w", err)
	}
	defer examplecleanup.OnReturn("close encoded file", f.Close)

	writerConfig := ogg.WriterConfig{
		SampleRate:    uint32(sampleRate),
		Channels:      uint8(channels),
		PreSkip:       uint16(lookahead),
		MappingFamily: ogg.MappingFamilyRTP,
		StreamCount:   1,
	}
	if channels == 2 {
		writerConfig.CoupledCount = 1
	}
	oggWriter, err := ogg.NewWriterWithConfig(f, writerConfig)
	if err != nil {
		return stats, fmt.Errorf("create ogg writer: %w", err)
	}

	pcm := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)
	gen := newSignalGenerator(signal, totalSamples, channels)

	for frame := range frames {
		startSample := frame * frameSize
		gen.fillFrame(pcm, startSample, frameSize)

		n, err := enc.Encode(pcm, packet)
		if err != nil {
			return stats, fmt.Errorf("encode frame %d: %w", frame, err)
		}
		if n == 0 {
			return stats, fmt.Errorf("encode frame %d: empty Opus packet", frame)
		}

		if frame == frames-1 {
			finalSamples := totalSamples + lookahead - startSample
			if finalSamples < 0 || finalSamples > frameSize {
				return stats, fmt.Errorf("final packet duration %d is outside frame size %d", finalSamples, frameSize)
			}
			err = oggWriter.WriteFinalPacket(packet[:n], finalSamples)
		} else {
			err = oggWriter.WritePacket(packet[:n], frameSize)
		}
		if err != nil {
			return stats, fmt.Errorf("write packet %d: %w", frame, err)
		}
		stats.encodedBytes += n
	}

	return stats, nil
}

func encodeWithLibopus(path string, duration float64, bitrate int, channels int, frameSize int, signal string) (encodeStats, error) {
	stats := encodeStats{
		requestedDuration: duration,
		channels:          channels,
		bitrate:           bitrate,
		signal:            signal,
		encoder:           "libopus",
	}
	if _, ok := frameSizeToMs(frameSize); !ok {
		return stats, fmt.Errorf("unsupported external encoder frame size %d samples", frameSize)
	}
	totalSamples, err := sampleCountForDuration(duration)
	if err != nil {
		return stats, err
	}

	tmp, err := os.CreateTemp("", "gopus_encode_src_*.wav")
	if err != nil {
		return stats, fmt.Errorf("create temp wav: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer examplecleanup.OnReturn("remove temporary WAV input", func() error { return os.Remove(tmpPath) })

	writer, err := wav.NewWriter(tmpPath, sampleRate, channels)
	if err != nil {
		return stats, fmt.Errorf("create wav: %w", err)
	}

	frames := 1 + (totalSamples-1)/frameSize
	stats.actualDuration = float64(totalSamples) / sampleRate

	pcm := make([]float32, frameSize*channels)
	gen := newSignalGenerator(signal, totalSamples, channels)
	for frame := range frames {
		startSample := frame * frameSize
		gen.fillFrame(pcm, startSample, frameSize)
		samples := min(frameSize, totalSamples-startSample)
		if err := writer.WriteSamples(pcm[:samples*channels]); err != nil {
			_ = writer.Close()
			return stats, fmt.Errorf("write wav: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return stats, fmt.Errorf("finalize wav: %w", err)
	}

	if err := runLibopusEncoder(tmpPath, path, bitrate, frameSize); err != nil {
		return stats, err
	}

	stats.frames, stats.encodedBytes, err = countEncodedOggPackets(path)
	if err != nil {
		return stats, err
	}

	return stats, nil
}

func countEncodedOggPackets(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open encoded Ogg file: %w", err)
	}
	defer examplecleanup.OnReturn("close encoded Opus file", f.Close)

	reader, err := ogg.NewReader(f)
	if err != nil {
		return 0, 0, fmt.Errorf("read encoded Ogg headers: %w", err)
	}

	var packets, payloadBytes int
	for {
		packet, _, err := reader.ReadPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, 0, fmt.Errorf("read encoded packet %d: %w", packets, err)
		}
		packets++
		payloadBytes += len(packet)
	}
	if packets == 0 {
		return 0, 0, errors.New("encoded Ogg file contains no audio packets")
	}
	return packets, payloadBytes, nil
}

func runLibopusEncoder(inputWav, outputOpus string, bitrate int, frameSize int) error {
	frameMs, ok := frameSizeToMs(frameSize)
	if !ok {
		return fmt.Errorf("unsupported external encoder frame size %d samples", frameSize)
	}

	if opusenc := lookup("opusenc"); opusenc != "" {
		args := []string{"--bitrate", fmt.Sprintf("%d", bitrate/1000), "--framesize", frameMs}
		args = append(args, inputWav, outputOpus)
		return runCommand(opusenc, args)
	}

	if ffmpeg := lookup("ffmpeg"); ffmpeg != "" {
		args := []string{
			"-y", "-loglevel", "error", "-i", inputWav,
			"-c:a", "libopus", "-b:a", fmt.Sprintf("%dk", bitrate/1000),
			"-frame_duration", frameMs, outputOpus,
		}
		return runCommand(ffmpeg, args)
	}

	return errors.New("libopus encoder not found (install opusenc or ffmpeg)")
}

func frameSizeToMs(frameSize int) (string, bool) {
	switch frameSize {
	case 120:
		return "2.5", true
	case 240:
		return "5", true
	case 480:
		return "10", true
	case 960:
		return "20", true
	case 1920:
		return "40", true
	case 2880:
		return "60", true
	default:
		return "", false
	}
}

type signalGenerator struct {
	signal       string
	totalSamples int
	channels     int
	seed         uint32

	// Source-filter speech state.
	glottalPhase float64        // glottal oscillator phase [0,1)
	biquads      [5]biquadState // formant resonator states
	radHPState   [2]float64     // radiation HP filter state
	prevF0       float64        // previous pitch for smoothing
}

// biquadState holds the delay-line state for a second-order IIR resonator.
type biquadState struct {
	y1, y2 float64 // output history
}

// biquadResonator applies a single biquad formant resonator.
// H(z) = 1 / (1 - 2r·cos(θ)z⁻¹ + r²z⁻²)
// Unconditionally stable for bandwidth > 0.
func (b *biquadState) process(x float64, freq, bw float64) float64 {
	r := math.Exp(-math.Pi * bw / float64(sampleRate))
	theta := 2.0 * math.Pi * freq / float64(sampleRate)
	a1 := -2.0 * r * math.Cos(theta)
	a2 := r * r
	y := x - a1*b.y1 - a2*b.y2
	b.y2 = b.y1
	b.y1 = y
	return y
}

func newSignalGenerator(signal string, totalSamples int, channels int) *signalGenerator {
	return &signalGenerator{
		signal:       strings.ToLower(strings.TrimSpace(signal)),
		totalSamples: totalSamples,
		channels:     channels,
		seed:         12345,
	}
}

func (g *signalGenerator) fillFrame(pcm []float32, startSample int, frameSize int) {
	if len(pcm) == 0 {
		return
	}
	if g.channels < 1 {
		g.channels = 1
	}

	for i := range frameSize {
		sampleIndex := startSample + i
		if sampleIndex >= g.totalSamples {
			for ch := 0; ch < g.channels; ch++ {
				pcm[i*g.channels+ch] = 0
			}
			continue
		}

		t := float64(sampleIndex) / float64(sampleRate)
		progress := float64(sampleIndex) / float64(g.totalSamples)

		var left, right float32

		switch g.signal {
		case "sine":
			left = float32(0.5 * math.Sin(2*math.Pi*440*t))
			right = float32(0.5 * math.Sin(2*math.Pi*554.37*t+0.1))
		case "sweep":
			startHz := 100.0
			endHz := 8000.0
			freq := startHz + (endHz-startHz)*progress
			left = float32(0.5 * math.Sin(2*math.Pi*freq*t))
			right = float32(0.5 * math.Sin(2*math.Pi*(freq*1.05)*t))
		case "noise":
			left = g.nextNoiseSample(0.4)
			right = g.nextNoiseSample(0.4)
		case "speech":
			left = g.speechSample(t, sampleIndex)
			right = left
		case "chord":
			left, right = g.chordSample(t, progress)
		default:
			left = float32(0.5 * math.Sin(2*math.Pi*440*t))
			right = left
		}

		if g.channels == 1 {
			pcm[i] = left
			continue
		}

		pcm[i*g.channels] = left
		pcm[i*g.channels+1] = right
	}
}

func (g *signalGenerator) chordSample(t float64, progress float64) (float32, float32) {
	freqs := []float64{261.63, 329.63, 392.0}
	amp := 0.15 * math.Min(1.0, progress*5)
	vibrato := 1.0 + 0.05*math.Sin(2*math.Pi*5*t)
	var sample float64
	for i, freq := range freqs {
		detune := 1.0 + 0.002*math.Sin(2*math.Pi*0.1*t+float64(i))
		sample += amp * math.Sin(2*math.Pi*freq*detune*t)
	}
	sample *= vibrato
	pan := 0.5 + 0.4*math.Sin(2*math.Pi*0.2*t)
	left := float32(sample * pan)
	right := float32(sample * (1.0 - pan))
	return left, right
}

func (g *signalGenerator) nextNoiseSample(scale float32) float32 {
	g.seed = g.seed*1103515245 + 12345
	val := float32((g.seed>>16)&0x7FFF)/32768.0 - 0.5
	return val * scale
}

// speechSample generates speech using a source-filter model:
//  1. Glottal source: Rosenberg pulse (asymmetric open/close phases)
//  2. Formant filter: cascade of 5 biquad resonators (unconditionally stable)
//  3. Radiation: first-order high-pass (lip radiation effect)
func (g *signalGenerator) speechSample(t float64, _ int) float32 {
	// --- Pitch contour ---
	f0 := 120.0 + 15.0*math.Sin(2*math.Pi*0.35*t) + // intonation
		4.0*math.Sin(2*math.Pi*5.5*t) // vibrato
	if f0 < 60 {
		f0 = 60
	}
	// Smooth pitch to avoid clicks.
	if g.prevF0 == 0 {
		g.prevF0 = f0
	}
	f0 = g.prevF0 + 0.1*(f0-g.prevF0)
	g.prevF0 = f0

	// --- Syllable envelope (~3 syl/sec) ---
	syllableRate := 3.0
	syllablePhase := math.Mod(t*syllableRate, 1.0)
	var syllableAmp float64
	switch {
	case syllablePhase < 0.10:
		syllableAmp = 0.5 - 0.5*math.Cos(math.Pi*syllablePhase/0.10)
	case syllablePhase < 0.55:
		syllableAmp = 1.0
	case syllablePhase < 0.75:
		syllableAmp = 0.5 + 0.5*math.Cos(math.Pi*(syllablePhase-0.55)/0.20)
	default:
		syllableAmp = 0.0
	}

	// --- Glottal source (Rosenberg pulse model) ---
	// Advance glottal phase.
	g.glottalPhase += f0 / float64(sampleRate)
	if g.glottalPhase >= 1.0 {
		g.glottalPhase -= math.Floor(g.glottalPhase)
	}

	var source float64
	tp := 0.40 // open phase ratio
	tn := 0.16 // closing phase ratio
	phase := g.glottalPhase
	if phase < tp {
		// Opening phase: half-cosine rise.
		source = 0.5 - 0.5*math.Cos(math.Pi*phase/tp)
	} else if phase < tp+tn {
		// Closing phase: cosine fall.
		source = math.Cos(0.5 * math.Pi * (phase - tp) / tn)
	} else {
		// Closed phase.
		source = 0.0
	}

	// Add aspiration noise modulated by glottal open phase.
	if syllableAmp > 0.05 {
		aspiration := float64(g.nextNoiseSample(1.0))
		if phase < tp+tn {
			source += 0.04 * aspiration // more noise during open phase
		} else {
			source += 0.01 * aspiration
		}
	}

	source *= syllableAmp

	// --- Vowel formants (smooth interpolation) ---
	type fmtSet struct{ f, bw [5]float64 }
	vowels := [5]fmtSet{
		{f: [5]float64{730, 1090, 2440, 3300, 3750}, bw: [5]float64{90, 110, 170, 250, 300}}, // /a/
		{f: [5]float64{270, 2290, 3010, 3500, 4100}, bw: [5]float64{60, 100, 150, 200, 280}}, // /i/
		{f: [5]float64{300, 870, 2240, 3200, 3700}, bw: [5]float64{65, 100, 140, 220, 280}},  // /u/
		{f: [5]float64{530, 1840, 2480, 3300, 3900}, bw: [5]float64{70, 110, 150, 230, 290}}, // /e/
		{f: [5]float64{570, 840, 2410, 3250, 3750}, bw: [5]float64{80, 105, 155, 240, 300}},  // /o/
	}

	vowelPos := math.Mod(t*syllableRate, 5.0)
	idx0 := int(vowelPos) % 5
	idx1 := (idx0 + 1) % 5
	frac := vowelPos - math.Floor(vowelPos)
	alpha := 0.5 - 0.5*math.Cos(math.Pi*frac) // cosine interpolation

	// --- Apply cascade of 5 biquad resonators ---
	sample := source
	for i := range 5 {
		freq := vowels[idx0].f[i] + alpha*(vowels[idx1].f[i]-vowels[idx0].f[i])
		bw := vowels[idx0].bw[i] + alpha*(vowels[idx1].bw[i]-vowels[idx0].bw[i])
		// Gain reduction for higher formants.
		gain := 1.0
		switch i {
		case 1:
			gain = 0.5
		case 2:
			gain = 0.25
		case 3:
			gain = 0.12
		case 4:
			gain = 0.06
		}
		sample = gain * g.biquads[i].process(sample, freq, bw)
	}

	// --- Radiation filter (first-order high-pass: y[n] = x[n] - x[n-1]) ---
	radiated := sample - g.radHPState[0]
	g.radHPState[0] = sample

	// Scale output.
	radiated *= 0.0003

	// Soft-clip.
	radiated = math.Tanh(radiated)
	return float32(radiated)
}

func playEncoded(path string) error {
	if player := lookup("ffplay"); player != "" {
		if err := runPlayer(player, []string{"-autoexit", "-nodisp", "-hide_banner", "-loglevel", "error", path}); err == nil {
			return nil
		}
	}

	tmp, err := os.CreateTemp("", "gopus_encode_*.wav")
	if err != nil {
		return fmt.Errorf("create temp wav: %w", err)
	}
	wavPath := tmp.Name()
	_ = tmp.Close()
	defer examplecleanup.OnReturn("remove temporary WAV", func() error { return os.Remove(wavPath) })

	if err := decodeOpusToWav(path, wavPath); err != nil {
		return fmt.Errorf("decode to wav: %w", err)
	}

	return playWav(wavPath)
}

func decodeOpusToWav(opusPath, wavPath string) error {
	f, err := os.Open(opusPath)
	if err != nil {
		return err
	}
	defer examplecleanup.OnReturn("close Opus input", f.Close)

	oggReader, err := ogg.NewReader(f)
	if err != nil {
		return fmt.Errorf("create ogg reader: %w", err)
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return errors.New("invalid channel count in OpusHead")
	}

	decCfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(decCfg)
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}

	pcmOut := make([]float32, decCfg.MaxPacketSamples*channels)
	preSkip := int(oggReader.PreSkip())

	writer, err := wav.NewWriter(wavPath, sampleRate, channels)
	if err != nil {
		return fmt.Errorf("create wav: %w", err)
	}
	defer examplecleanup.OnReturn("close WAV writer", writer.Close)

	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}

		start := 0
		if preSkip > 0 {
			if preSkip >= n {
				preSkip -= n
				continue
			}
			start = preSkip
			preSkip = 0
		}

		if start < n {
			if err := writer.WriteSamples(pcmOut[start*channels : n*channels]); err != nil {
				return err
			}
		}
	}

	return writer.Close()
}

func playWav(path string) error {
	if player := lookup("ffplay"); player != "" {
		return runPlayer(player, []string{"-autoexit", "-nodisp", path})
	}

	switch runtime.GOOS {
	case "darwin":
		if player := lookup("afplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("open"); player != "" {
			return runPlayer(player, []string{path})
		}
	case "linux":
		if player := lookup("aplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("paplay"); player != "" {
			return runPlayer(player, []string{path})
		}
		if player := lookup("xdg-open"); player != "" {
			return runPlayer(player, []string{path})
		}
	case "windows":
		if player := lookup("powershell"); player != "" {
			escaped := strings.ReplaceAll(path, "'", "''")
			script := fmt.Sprintf("(New-Object Media.SoundPlayer '%s').PlaySync()", escaped)
			return runPlayer(player, []string{"-NoProfile", "-Command", script})
		}
		if player := lookup("cmd"); player != "" {
			return runPlayer(player, []string{"/c", "start", "", path})
		}
	}

	return errors.New("no audio player found in PATH")
}

func lookup(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func runPlayer(binary string, args []string) error {
	return runCommand(binary, args)
}

func runCommand(binary string, args []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
