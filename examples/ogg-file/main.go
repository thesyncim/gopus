// Package main demonstrates Ogg Opus file creation and reading.
//
// This example shows how to create podcast-style Ogg Opus files and read them back.
//
// Usage:
//
//	go run . -out podcast.opus -duration 5
//	go run . -in podcast.opus
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	examplecleanup "github.com/thesyncim/gopus/examples/internal/cleanup"
)

const (
	sampleRate = 48000
	channels   = 2
	frameSize  = 960 // 20ms at 48kHz
)

func main() {
	// Parse flags
	outFile := flag.String("out", "", "Output Ogg Opus file to create")
	inFile := flag.String("in", "", "Input Ogg Opus file to read")
	duration := flag.Float64("duration", 5.0, "Duration in seconds (for output)")
	bitrate := flag.Int("bitrate", 64000, "Target bitrate in bps")
	flag.Parse()

	if *outFile == "" && *inFile == "" {
		fmt.Println("Usage: ogg-file -out <file.opus> [-duration N] [-bitrate N]")
		fmt.Println("       ogg-file -in <file.opus>")
		flag.PrintDefaults()
		return
	}

	// Create file
	if *outFile != "" {
		fmt.Printf("Creating Ogg Opus file: %s\n", *outFile)
		if err := createOggFile(*outFile, *duration, *bitrate); err != nil {
			log.Fatalf("Create failed: %v", err)
		}
		fmt.Println("Done!")
	}

	// Read file
	if *inFile != "" {
		fmt.Printf("\nReading Ogg Opus file: %s\n", *inFile)
		if err := readOggFile(*inFile); err != nil {
			log.Fatalf("Read failed: %v", err)
		}
	}
}

// createOggFile creates an Ogg Opus file with a test audio signal.
func createOggFile(filename string, duration float64, bitrate int) error {
	totalSamples, err := sampleCountForDuration(duration)
	if err != nil {
		return err
	}

	// Create encoder
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return fmt.Errorf("set bitrate: %w", err)
	}
	lookahead := enc.Lookahead()
	if lookahead < 0 || lookahead > int(^uint16(0)) {
		return fmt.Errorf("encoder lookahead %d cannot be represented in OpusHead", lookahead)
	}
	maxInt := int(^uint(0) >> 1)
	if totalSamples > maxInt-lookahead {
		return fmt.Errorf("duration exceeds the supported encoded sample count")
	}
	encodedSamples := totalSamples + lookahead
	totalFrames := encodedSamples / frameSize
	if encodedSamples%frameSize != 0 {
		totalFrames++
	}

	// Create file
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer examplecleanup.OnReturn("close output file", f.Close)

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
		return fmt.Errorf("create ogg writer: %w", err)
	}

	// Generate and encode audio
	encodedBytes := 0
	progressInterval := max(1, totalFrames/10)

	playableDuration := float64(totalSamples) / sampleRate
	fmt.Printf("  Requested duration: %.6f seconds\n", duration)
	fmt.Printf("  Playable duration: %.6f seconds (%d samples)\n", playableDuration, totalSamples)
	fmt.Printf("  Bitrate: %d kbps\n", bitrate/1000)
	fmt.Printf("  Encoded frames (including lookahead): %d\n", totalFrames)

	for frame := range totalFrames {
		// Generate a pleasant test tone that varies over time
		pcm := generateFrame(frame, totalSamples)

		// Encode
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			return fmt.Errorf("encode frame %d: %w", frame, err)
		}

		// The final granule retains the requested input and encoder lookahead,
		// while excluding the zero padding after the final input sample.
		if frame == totalFrames-1 {
			finalSamples := encodedSamples - frame*frameSize
			if err := oggWriter.WriteFinalPacket(packet, finalSamples); err != nil {
				return fmt.Errorf("write final packet %d: %w", frame, err)
			}
		} else if err := oggWriter.WritePacket(packet, frameSize); err != nil {
			return fmt.Errorf("write packet %d: %w", frame, err)
		}

		encodedBytes += len(packet)

		// Progress
		if (frame+1)%progressInterval == 0 || frame+1 == totalFrames {
			fmt.Printf("  Progress: %d%%\n", 100*(frame+1)/totalFrames)
		}
	}

	// Close stream
	if err := oggWriter.Close(); err != nil {
		return fmt.Errorf("close ogg: %w", err)
	}

	// Report file stats
	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat output: %w", err)
	}
	fileSize := stat.Size()

	fmt.Printf("  Input samples: %d\n", totalSamples)
	fmt.Printf("  Encoded size: %d bytes\n", encodedBytes)
	fmt.Printf("  File size: %d bytes\n", fileSize)
	fmt.Printf("  Compression: %.1f:1\n",
		float64(totalSamples)*float64(channels)*2/float64(fileSize)) // 2 bytes per int16 sample
	fmt.Printf("  Effective bitrate: %.1f kbps\n",
		float64(fileSize)*8/playableDuration/1000)

	return nil
}

func sampleCountForDuration(duration float64) (int, error) {
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, fmt.Errorf("duration must be a positive finite number")
	}

	sampleCount := math.Round(duration * float64(sampleRate))
	if math.IsInf(sampleCount, 0) || sampleCount >= float64(int(^uint(0)>>1)) {
		return 0, fmt.Errorf("duration exceeds the supported sample count")
	}
	if sampleCount < 1 {
		return 0, fmt.Errorf("duration is shorter than one sample at %d Hz", sampleRate)
	}
	return int(sampleCount), nil
}

// generateFrame creates an audio frame with pleasant test tones and pads after the input ends.
func generateFrame(frameNum, totalSamples int) []float32 {
	pcm := make([]float32, frameSize*channels)

	// Base frequencies for a C major chord (C, E, G)
	freqs := []float64{261.63, 329.63, 392.00} // C4, E4, G4

	for i := range frameSize {
		sampleNum := frameNum*frameSize + i
		if sampleNum >= totalSamples {
			continue
		}
		t := float64(sampleNum) / float64(sampleRate)
		progress := float64(sampleNum) / float64(totalSamples)

		// Mix chord tones with decreasing amplitude over time
		var sample float64
		for j, freq := range freqs {
			// Slight detuning for richness
			detune := 1.0 + 0.002*math.Sin(2*math.Pi*0.1*t+float64(j))
			// Amplitude envelope: fade in then sustain
			amp := 0.15 * math.Min(1.0, progress*5)
			sample += amp * math.Sin(2*math.Pi*freq*detune*t)
		}

		// Add gentle vibrato
		sample *= 1.0 + 0.05*math.Sin(2*math.Pi*5*t)

		// Stereo: slight panning effect
		left := float32(sample * (0.6 + 0.4*math.Sin(2*math.Pi*0.2*t)))
		right := float32(sample * (0.6 - 0.4*math.Sin(2*math.Pi*0.2*t)))

		pcm[i*2] = left
		pcm[i*2+1] = right
	}

	return pcm
}

// readOggFile reads and analyzes an Ogg Opus file.
func readOggFile(filename string) error {
	// Open file
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer examplecleanup.OnReturn("close input file", f.Close)

	// Get file size
	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat input: %w", err)
	}
	fileSize := stat.Size()

	// Create reader
	oggReader, err := ogg.NewReader(f)
	if err != nil {
		return fmt.Errorf("create ogg reader: %w", err)
	}

	// Print header info
	fmt.Println("=== Ogg Opus File Info ===")
	fmt.Printf("  File size: %d bytes\n", fileSize)
	fmt.Printf("  Channels: %d\n", oggReader.Channels())
	fmt.Printf("  Sample rate: %d Hz (informational)\n", oggReader.SampleRate())
	fmt.Printf("  Pre-skip: %d samples\n", oggReader.PreSkip())

	if oggReader.Tags != nil {
		fmt.Printf("  Vendor: %s\n", oggReader.Tags.Vendor)
		if len(oggReader.Tags.Comments) > 0 {
			fmt.Println("  Comments:")
			for _, c := range oggReader.Tags.Comments {
				fmt.Printf("    %s\n", c)
			}
		}
	}

	// Create decoder
	cfg := gopus.DefaultDecoderConfig(sampleRate, int(oggReader.Channels()))
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// Read and decode all packets
	fmt.Println("\n=== Decoding ===")
	totalPackets := 0
	totalSamples := 0
	totalPacketBytes := 0
	var lastGranule uint64

	for {
		packet, granule, err := oggReader.ReadPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read packet %d: %w", totalPackets, err)
		}

		// Decode packet
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			return fmt.Errorf("decode packet %d: %w", totalPackets, err)
		}

		totalPackets++
		totalSamples += n
		totalPacketBytes += len(packet)
		lastGranule = granule
	}

	if totalPackets == 0 {
		return fmt.Errorf("input contains no decodable Opus packets")
	}

	// Calculate duration
	duration := float64(totalSamples) / float64(sampleRate)
	granuleDuration := float64(lastGranule) / 48000.0 // Granule is always at 48kHz

	fmt.Printf("  Packets decoded: %d\n", totalPackets)
	fmt.Printf("  Raw packet samples (before pre-skip/EOS trimming): %d\n", totalSamples)
	fmt.Printf("  Raw packet decode duration (before trimming): %.2f seconds\n", duration)
	fmt.Printf("  Final Ogg granule: %d samples (%.2f seconds from stream start, including pre-skip)\n", lastGranule, granuleDuration)
	fmt.Printf("  Average packet size: %d bytes\n", totalPacketBytes/totalPackets)
	fmt.Printf("  Average packet payload rate (using raw decode duration): %.1f kbps\n", float64(totalPacketBytes*8)/duration/1000)

	fmt.Println("\n=== Seeking ===")
	targetGranule := lastGranule / 2
	if err := oggReader.SeekGranule(targetGranule); err != nil {
		fmt.Printf("  Seek failed: %v\n", err)
	} else {
		packet, granule, err := oggReader.ReadPacket()
		if err != nil {
			fmt.Printf("  Read after seek failed: %v\n", err)
		} else {
			fmt.Printf("  Seek target granule: %d\n", targetGranule)
			fmt.Printf("  First packet after seek: %d bytes at granule %d\n", len(packet), granule)
		}
	}

	return nil
}
