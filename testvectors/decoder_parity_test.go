package testvectors

import (
	"bytes"
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func decodeWithInternalDecoder(t *testing.T, packets [][]byte, channels int) []float32 {
	t.Helper()
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}

	outBuf := make([]float32, 5760*channels)
	var decoded []float32
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, outBuf)
		if err != nil {
			t.Fatalf("decode frame %d failed: %v", i, err)
		}
		if n == 0 {
			continue
		}
		decoded = append(decoded, outBuf[:n*channels]...)
	}
	return decoded
}

func TestDecoderParitySILKWB(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	// Generate 1 second of test signal.
	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Encode with gopus.
	enc := encoder.NewEncoder(sampleRate, channels)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(bitrate)

	packets := make([][]byte, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("encode frame %d failed: %v", i, err)
		}
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		packets = append(packets, packetCopy)
	}

	// Decode with opusdec (libopus) via Ogg container.
	oggBuf, err := buildOggForPackets(packets, channels, sampleRate, frameSize)
	if err != nil {
		t.Fatalf("build Ogg: %v", err)
	}
	libDecoded, err := decodeWithOpusdec(oggBuf.Bytes())
	if err != nil {
		if err.Error() == "opusdec blocked by macOS provenance" {
			t.Skip("opusdec blocked by macOS provenance - skipping")
		}
		t.Fatalf("decode with opusdec: %v", err)
	}
	if len(libDecoded) == 0 {
		t.Fatal("opusdec returned no samples")
	}

	// Decode with internal decoder.
	internalDecoded := decodeWithInternalDecoder(t, packets, channels)
	if len(internalDecoded) == 0 {
		t.Fatal("internal decoder returned no samples")
	}

	// Apply the same pre-skip to internal decode for parity.
	preSkip := OpusPreSkip * channels
	if len(internalDecoded) > preSkip {
		internalDecoded = internalDecoded[preSkip:]
	}

	// Align lengths and compute quality between decoders.
	compareLen := len(libDecoded)
	if len(internalDecoded) < compareLen {
		compareLen = len(internalDecoded)
	}
	q, delay := ComputeQualityFloat32WithDelay(libDecoded[:compareLen], internalDecoded[:compareLen], sampleRate, 2000)
	t.Logf("decoder parity: Q=%.2f (SNR=%.2f dB), delay=%d samples", q, SNRFromQuality(q), delay)

	// Basic RMS and correlation diagnostics.
	var sumLib, sumInt, sumLibSq, sumIntSq, sumCross float64
	for i := 0; i < compareLen; i++ {
		a := float64(libDecoded[i])
		b := float64(internalDecoded[i])
		sumLib += a
		sumInt += b
		sumLibSq += a * a
		sumIntSq += b * b
		sumCross += a * b
	}
	n := float64(compareLen)
	if n > 0 {
		meanLib := sumLib / n
		meanInt := sumInt / n
		var cov, varLib, varInt float64
		for i := 0; i < compareLen; i++ {
			a := float64(libDecoded[i]) - meanLib
			b := float64(internalDecoded[i]) - meanInt
			cov += a * b
			varLib += a * a
			varInt += b * b
		}
		corr := 0.0
		if varLib > 0 && varInt > 0 {
			corr = cov / math.Sqrt(varLib*varInt)
		}
		rmsLib := math.Sqrt(sumLibSq / n)
		rmsInt := math.Sqrt(sumIntSq / n)
		ratio := 0.0
		if rmsLib > 0 {
			ratio = rmsInt / rmsLib
		}
		t.Logf("decoder parity stats: corr=%.4f rms(lib)=%.4f rms(int)=%.4f ratio=%.4f", corr, rmsLib, rmsInt, ratio)
	}
}

func buildOggForPackets(packets [][]byte, channels, sampleRate, frameSize int) (*bytes.Buffer, error) {
	var oggBuf bytes.Buffer
	if err := writeOggOpusEncoder(&oggBuf, packets, channels, sampleRate, frameSize); err != nil {
		return nil, err
	}
	return &oggBuf, nil
}
