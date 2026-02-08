package testvectors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
)

func TestDecodeLibopusPacket(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping test: %v", err)
	}

	bitFile := filepath.Join(testVectorDir, "testvector11.bit")
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("parse %s: %v", bitFile, err)
	}
	if len(packets) == 0 {
		t.Fatal("no packets in testvector11")
	}

	decFile := filepath.Join(testVectorDir, "testvector11.dec")
	refData, err := os.ReadFile(decFile)
	if err != nil {
		t.Fatalf("read reference %s: %v", decFile, err)
	}
	refSamples := make([]float32, len(refData)/2)
	for i := range refSamples {
		refSamples[i] = float32(int16(binary.LittleEndian.Uint16(refData[i*2:]))) / 32768.0
	}

	var (
		maxDiff     float64
		signalPower float64
		errorPower  float64
	)

	dec := celt.NewDecoder(2)
	offset := 0
	totalFrames := 0
	maxFrames := 120
	if len(packets) < maxFrames {
		maxFrames = len(packets)
	}

	for i := 0; i < maxFrames; i++ {
		p := packets[i]
		if len(p.Data) < 2 {
			continue
		}

		info, err := gopus.ParsePacket(p.Data)
		if err != nil {
			t.Fatalf("parse packet %d: %v", i, err)
		}
		if info.TOC.Mode != gopus.ModeCELT || !info.TOC.Stereo {
			t.Fatalf("packet %d unsupported TOC for CELT parity test: mode=%v stereo=%v frameCode=%d", i, info.TOC.Mode, info.TOC.Stereo, info.TOC.FrameCode)
		}
		payloadBytes := 0
		for _, sz := range info.FrameSizes {
			payloadBytes += sz
		}
		payloadOffset := len(p.Data) - payloadBytes
		if payloadOffset < 0 || payloadOffset > len(p.Data) {
			t.Fatalf("packet %d invalid payload offset %d", i, payloadOffset)
		}

		for fi, sz := range info.FrameSizes {
			if sz < 0 || payloadOffset+sz > len(p.Data) {
				t.Fatalf("packet %d frame %d invalid size %d", i, fi, sz)
			}
			framePayload := p.Data[payloadOffset : payloadOffset+sz]
			payloadOffset += sz

			decoded, err := dec.DecodeFrame(framePayload, info.TOC.FrameSize)
			if err != nil {
				t.Fatalf("decode packet %d frame %d: %v", i, fi, err)
			}
			frameSamples := info.TOC.FrameSize * 2
			if len(decoded) != frameSamples {
				t.Fatalf("packet %d frame %d decoded sample count mismatch: got %d want %d", i, fi, len(decoded), frameSamples)
			}
			if offset+frameSamples > len(refSamples) {
				t.Fatalf("reference too short at packet %d frame %d: need %d samples, have %d", i, fi, offset+frameSamples, len(refSamples))
			}

			refFrame := refSamples[offset : offset+frameSamples]
			for j := 0; j < frameSamples; j++ {
				got := quantizeTo16(float32(decoded[j]))
				want := refFrame[j]
				diff := math.Abs(float64(got - want))
				if diff > maxDiff {
					maxDiff = diff
				}
				s := float64(want)
				signalPower += s * s
				errorPower += diff * diff
			}

			offset += frameSamples
			totalFrames++
		}
	}

	if totalFrames == 0 {
		t.Fatal("no frames compared")
	}

	snr := 0.0
	if errorPower > 0 {
		snr = 10 * math.Log10(signalPower/errorPower)
	}

	if maxDiff > 2.0/32768.0 {
		t.Fatalf("max diff too high: got %.2e want <= %.2e", maxDiff, 2.0/32768.0)
	}
	if snr < 60 {
		t.Fatalf("snr too low: got %.1f dB want >= 60 dB", snr)
	}

	t.Logf("frames=%d maxDiff=%.2e snr=%.1f dB", totalFrames, maxDiff, snr)
}
