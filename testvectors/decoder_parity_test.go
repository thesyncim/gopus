package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
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
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	libPackets, packetMeta, err := loadSILKWBFloatPacketFixturePackets()
	if err != nil {
		t.Fatalf("load SILK WB packet fixture: %v", err)
	}
	if packetMeta.Version != 1 ||
		packetMeta.SampleRate != sampleRate ||
		packetMeta.Channels != channels ||
		packetMeta.FrameSize != frameSize ||
		packetMeta.Bitrate != bitrate {
		t.Fatalf("invalid SILK WB packet fixture metadata: %+v", packetMeta)
	}
	if packetMeta.Frames != len(libPackets) {
		t.Fatalf("invalid SILK WB packet fixture frame count: header=%d packets=%d", packetMeta.Frames, len(libPackets))
	}

	packets := make([][]byte, len(libPackets))
	for i := range libPackets {
		packets[i] = libPackets[i].data
	}

	libDecoded, decodedMeta, err := loadSILKWBFloatDecodedFixtureSamples()
	if err != nil {
		t.Fatalf("load SILK WB decoded fixture: %v", err)
	}
	if decodedMeta.Version != 1 ||
		decodedMeta.SampleRate != sampleRate ||
		decodedMeta.Channels != channels ||
		decodedMeta.FrameSize != frameSize ||
		decodedMeta.Bitrate != bitrate {
		t.Fatalf("invalid SILK WB decoded fixture metadata: %+v", decodedMeta)
	}
	if decodedMeta.Frames != len(libPackets) {
		t.Fatalf("decoded fixture frame count mismatch: packets=%d decodedFrames=%d", len(libPackets), decodedMeta.Frames)
	}
	if len(libDecoded) == 0 {
		t.Fatal("decoded fixture contains no samples")
	}

	// Decode with internal decoder.
	internalDecoded := decodeWithInternalDecoder(t, packets, channels)
	if len(internalDecoded) == 0 {
		t.Fatal("internal decoder returned no samples")
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
