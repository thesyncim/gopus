// Package main demonstrates gopus interoperability with ffmpeg.
//
// This example shows two use cases:
//  1. Encode audio with gopus and verify with ffmpeg/ffprobe
//  2. Decode audio encoded by ffmpeg with gopus
//
// Usage:
//
//	go run . -out output.opus
//	ffprobe output.opus  # Verify with ffmpeg
//	ffplay output.opus   # Play with ffmpeg
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
	sampleRate = 48000 // Opus native sample rate
	channels   = 2     // Stereo
	frameSize  = 960   // 20ms at 48kHz
)

func main() {
	// Parse flags
	outFile := flag.String("out", "output.opus", "Output Ogg Opus file path")
	inFile := flag.String("in", "", "Input Ogg Opus file to decode (optional)")
	duration := flag.Float64("duration", 2.0, "Duration of test signal in seconds")
	flag.Parse()

	// Part 1: Encode a test signal to Ogg Opus
	fmt.Println("=== Part 1: Encode with gopus ===")
	if err := encodeTestSignal(*outFile, *duration); err != nil {
		log.Fatalf("Encode failed: %v", err)
	}
	fmt.Printf("\nCreated: %s\n", *outFile)
	fmt.Println("\nVerify with ffprobe:")
	fmt.Printf("  ffprobe %s\n", *outFile)
	fmt.Println("\nPlay with ffplay:")
	fmt.Printf("  ffplay %s\n", *outFile)

	// Part 2: Decode an Ogg Opus file (if provided)
	if *inFile != "" {
		fmt.Println("\n=== Part 2: Decode with gopus ===")
		if err := decodeOpusFile(*inFile); err != nil {
			log.Fatalf("Decode failed: %v", err)
		}
	} else {
		fmt.Println("\n=== Part 2: Decode ffmpeg-encoded file ===")
		fmt.Println("To test decoding, create a file with ffmpeg:")
		fmt.Println("  ffmpeg -f lavfi -i \"sine=frequency=440:duration=2\" -c:a libopus test.opus")
		fmt.Println("Then run:")
		fmt.Printf("  %s -in test.opus\n", os.Args[0])
	}
}

// encodeTestSignal generates a 440Hz stereo sine wave and encodes it to Ogg Opus.
func encodeTestSignal(filename string, duration float64) error {
	totalSamples, err := sampleCountForDuration(duration)
	if err != nil {
		return err
	}

	// Create encoder
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return fmt.Errorf("create encoder: %w", err)
	}

	// Set a reasonable bitrate (64 kbps per channel = 128 kbps stereo)
	if err := enc.SetBitrate(128000); err != nil {
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

	// Create output file
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
		CoupledCount:  1,
	}
	oggWriter, err := ogg.NewWriterWithConfig(f, writerConfig)
	if err != nil {
		return fmt.Errorf("create ogg writer: %w", err)
	}

	// Generate and encode test signal
	encodedBytes := 0

	playableDuration := float64(totalSamples) / sampleRate
	fmt.Printf("Generating %.6fs stereo 440Hz sine wave...\n", duration)
	fmt.Printf("  Playable duration: %.6fs (%d samples)\n", playableDuration, totalSamples)
	fmt.Printf("  Sample rate: %d Hz\n", sampleRate)
	fmt.Printf("  Channels: %d\n", channels)
	fmt.Printf("  Frame size: %d samples (%.1f ms)\n", frameSize, float64(frameSize)/float64(sampleRate)*1000)
	fmt.Printf("  Encoded frames (including lookahead): %d\n", totalFrames)

	for frame := range totalFrames {
		// Generate stereo interleaved samples: [L0, R0, L1, R1, ...]
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			sampleIndex := frame*frameSize + i
			if sampleIndex >= totalSamples {
				continue
			}
			t := float64(sampleIndex) / float64(sampleRate)

			// 440Hz sine wave with slight stereo offset
			left := float32(0.5 * math.Sin(2*math.Pi*440*t))
			right := float32(0.5 * math.Sin(2*math.Pi*440*t+0.1)) // Slight phase shift

			pcm[i*2] = left
			pcm[i*2+1] = right
		}

		// Encode frame
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			return fmt.Errorf("encode frame %d: %w", frame, err)
		}

		// The final granule keeps the requested samples after pre-skip and excludes
		// zero padding after the last input sample.
		if frame == totalFrames-1 {
			finalSamples := encodedSamples - frame*frameSize
			if err := oggWriter.WriteFinalPacket(packet, finalSamples); err != nil {
				return fmt.Errorf("write final packet %d: %w", frame, err)
			}
		} else if err := oggWriter.WritePacket(packet, frameSize); err != nil {
			return fmt.Errorf("write packet %d: %w", frame, err)
		}

		encodedBytes += len(packet)
	}

	// Close Ogg stream (writes EOS page)
	if err := oggWriter.Close(); err != nil {
		return fmt.Errorf("close ogg: %w", err)
	}

	fmt.Printf("  Encoded %d frames, %d bytes total\n", totalFrames, encodedBytes)
	fmt.Printf("  Input samples: %d\n", totalSamples)
	fmt.Printf("  Average bitrate: %.1f kbps\n", float64(encodedBytes*8)/playableDuration/1000)

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

// decodeOpusFile reads and decodes an Ogg Opus file.
func decodeOpusFile(filename string) error {
	// Open file
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer examplecleanup.OnReturn("close input file", f.Close)

	// Create Ogg reader
	oggReader, err := ogg.NewReader(f)
	if err != nil {
		return fmt.Errorf("create ogg reader: %w", err)
	}

	fmt.Printf("File: %s\n", filename)
	fmt.Printf("  Channels: %d\n", oggReader.Channels())
	fmt.Printf("  Sample rate: %d Hz (informational)\n", oggReader.SampleRate())
	fmt.Printf("  Pre-skip: %d samples\n", oggReader.PreSkip())
	if oggReader.Tags != nil {
		fmt.Printf("  Vendor: %s\n", oggReader.Tags.Vendor)
		if len(oggReader.Tags.Comments) > 0 {
			fmt.Printf("  Comments: %v\n", oggReader.Tags.Comments)
		}
	}

	// Create decoder
	cfg := gopus.DefaultDecoderConfig(sampleRate, int(oggReader.Channels()))
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// Decode all packets
	totalSamples := 0
	totalPackets := 0
	var peakSample float32
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
		lastGranule = granule

		// Track peak sample
		for _, s := range pcmOut[:n*cfg.Channels] {
			if s > peakSample {
				peakSample = s
			} else if -s > peakSample {
				peakSample = -s
			}
		}
	}

	if totalPackets == 0 {
		return fmt.Errorf("input contains no decodable Opus packets")
	}

	duration := float64(totalSamples) / float64(sampleRate)
	fmt.Printf("\nDecoded:\n")
	fmt.Printf("  Packets: %d\n", totalPackets)
	fmt.Printf("  Raw packet samples (before pre-skip/EOS trimming): %d\n", totalSamples)
	fmt.Printf("  Raw packet decode duration (before trimming): %.2f seconds\n", duration)
	fmt.Printf("  Final Ogg granule: %d samples (%.2f seconds from stream start, including pre-skip)\n", lastGranule, float64(lastGranule)/sampleRate)
	fmt.Printf("  Peak level: %.4f (%.1f dBFS)\n", peakSample, 20*math.Log10(float64(peakSample)))

	return nil
}
