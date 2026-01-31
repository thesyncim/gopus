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
	"flag"
	"fmt"
	"log"
	"math"
	"os"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
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
	// Create encoder
	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		return fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return fmt.Errorf("set bitrate: %w", err)
	}

	// Create file
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	// Create Ogg writer
	oggWriter, err := ogg.NewWriter(f, sampleRate, channels)
	if err != nil {
		return fmt.Errorf("create ogg writer: %w", err)
	}

	// Generate and encode audio
	totalSamples := int(duration * sampleRate)
	totalFrames := totalSamples / frameSize
	encodedBytes := 0

	fmt.Printf("  Duration: %.1f seconds\n", duration)
	fmt.Printf("  Bitrate: %d kbps\n", bitrate/1000)
	fmt.Printf("  Frames: %d\n", totalFrames)

	for frame := 0; frame < totalFrames; frame++ {
		// Generate a pleasant test tone that varies over time
		pcm := generateFrame(frame, totalFrames)

		// Encode
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			return fmt.Errorf("encode frame %d: %w", frame, err)
		}

		// Write to Ogg
		if err := oggWriter.WritePacket(packet, frameSize); err != nil {
			return fmt.Errorf("write packet %d: %w", frame, err)
		}

		encodedBytes += len(packet)

		// Progress
		if frame > 0 && frame%(totalFrames/10) == 0 {
			fmt.Printf("  Progress: %d%%\n", 100*frame/totalFrames)
		}
	}

	// Close stream
	if err := oggWriter.Close(); err != nil {
		return fmt.Errorf("close ogg: %w", err)
	}

	// Report file stats
	stat, _ := f.Stat()
	fileSize := stat.Size()

	fmt.Printf("  Total samples: %d\n", totalSamples)
	fmt.Printf("  Encoded size: %d bytes\n", encodedBytes)
	fmt.Printf("  File size: %d bytes\n", fileSize)
	fmt.Printf("  Compression: %.1f:1\n",
		float64(totalSamples*channels*2)/float64(fileSize)) // 2 bytes per int16 sample
	fmt.Printf("  Effective bitrate: %.1f kbps\n",
		float64(fileSize*8)/duration/1000)

	return nil
}

// generateFrame creates an audio frame with pleasant test tones.
func generateFrame(frameNum, totalFrames int) []float32 {
	pcm := make([]float32, frameSize*channels)

	// Create a chord that evolves over time
	progress := float64(frameNum) / float64(totalFrames)

	// Base frequencies for a C major chord (C, E, G)
	freqs := []float64{261.63, 329.63, 392.00} // C4, E4, G4

	for i := 0; i < frameSize; i++ {
		sampleNum := frameNum*frameSize + i
		t := float64(sampleNum) / float64(sampleRate)

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
	defer f.Close()

	// Get file size
	stat, _ := f.Stat()
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
	dec, err := gopus.NewDecoderDefault(sampleRate, int(oggReader.Channels()))
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}

	// Read and decode all packets
	fmt.Println("\n=== Decoding ===")
	totalPackets := 0
	totalSamples := 0
	totalPacketBytes := 0
	var lastGranule uint64

	for {
		packet, granule, err := oggReader.ReadPacket()
		if err != nil {
			break // EOF
		}

		// Decode packet
		samples, err := dec.DecodeFloat32(packet)
		if err != nil {
			fmt.Printf("  Warning: decode error on packet %d: %v\n", totalPackets, err)
			continue
		}

		totalPackets++
		totalSamples += len(samples) / int(oggReader.Channels())
		totalPacketBytes += len(packet)
		lastGranule = granule
	}

	// Calculate duration
	duration := float64(totalSamples) / float64(sampleRate)
	granuleDuration := float64(lastGranule) / 48000.0 // Granule is always at 48kHz

	fmt.Printf("  Packets decoded: %d\n", totalPackets)
	fmt.Printf("  Total samples: %d\n", totalSamples)
	fmt.Printf("  Duration (samples): %.2f seconds\n", duration)
	fmt.Printf("  Duration (granule): %.2f seconds\n", granuleDuration)
	fmt.Printf("  Average packet size: %d bytes\n", totalPacketBytes/totalPackets)
	fmt.Printf("  Average bitrate: %.1f kbps\n", float64(totalPacketBytes*8)/duration/1000)

	// Seeking note
	fmt.Println("\n=== Seeking ===")
	fmt.Println("  Note: Ogg seeking requires bisection search (not implemented).")
	fmt.Println("  For seekable playback, use a player like ffplay or VLC.")

	return nil
}
