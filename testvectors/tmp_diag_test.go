package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// Temporary diagnostic test; remove before commit.
func TestTmpSILKPolarityDiag(t *testing.T) {
	frameSize := 960
	channels := 1
	bitrate := 32000

	q, decoded := runEncoderComplianceTest(t, encoder.ModeSILK, types.BandwidthWideband, frameSize, channels, bitrate)
	t.Logf("baseline Q=%.2f SNR=%.2f", q, SNRFromQuality(q))

	samples := 48000
	original := generateEncoderTestSignal(samples*channels, channels)
	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}
	if compareLen <= 0 {
		t.Fatal("no samples for comparison")
	}
	q2, delay := ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, 4000)
	t.Logf("best-delay Q=%.2f SNR=%.2f delay=%d", q2, SNRFromQuality(q2), delay)

	// Correlation at best delay, both polarities.
	var d, r []float32
	if delay > 0 {
		if delay >= compareLen {
			t.Fatal("delay >= compareLen")
		}
		d = decoded[delay:compareLen]
		r = original[:len(d)]
	} else {
		d = decoded[:compareLen]
		r = original[:compareLen]
	}

	var dot, eD, eR float64
	var sumD, sumR float64
	for i := range d {
		dv := float64(d[i])
		rv := float64(r[i])
		dot += dv * rv
		eD += dv * dv
		eR += rv * rv
		sumD += dv
		sumR += rv
	}
	corr := dot / math.Sqrt((eD+1e-30)*(eR+1e-30))
	corrInv := -corr
	t.Logf("corr=%.6f corr_inverted=%.6f", corr, corrInv)

	// Least-squares scale and affine fit diagnostics.
	scale := dot / (eD + 1e-30)
	meanD := sumD / float64(len(d))
	meanR := sumR / float64(len(d))
	offset := meanR - scale*meanD

	var sigPow, noisePowScale, noisePowAffine float64
	for i := range d {
		dv := float64(d[i])
		rv := float64(r[i])
		ds := scale * dv
		da := ds + offset
		sigPow += rv * rv
		n1 := rv - ds
		n2 := rv - da
		noisePowScale += n1 * n1
		noisePowAffine += n2 * n2
	}
	snrScale := 10.0 * math.Log10(sigPow/(noisePowScale+1e-30))
	snrAffine := 10.0 * math.Log10(sigPow/(noisePowAffine+1e-30))
	t.Logf("scale=%.6f offset=%.6f snr_scale=%.2f snr_affine=%.2f", scale, offset, snrScale, snrAffine)

	// Also compute Q with inverted decoded polarity at same delay.
	inv := make([]float32, len(d))
	for i := range d {
		inv[i] = -d[i]
	}
	// Build int16-style quality via float helper expects full slices.
	qInv, _ := ComputeQualityFloat32WithDelay(inv, r, 48000, 0)
	t.Logf("fixed-delay inverted Q=%.2f SNR=%.2f", qInv, SNRFromQuality(qInv))

	// Per-frame (20ms) SNR after best delay alignment.
	frame := 960
	frames := len(r) / frame
	if frames > 0 {
		minSNR := math.Inf(1)
		maxSNR := math.Inf(-1)
		sumSNR := 0.0
		for f := 0; f < frames; f++ {
			s := f * frame
			e := s + frame
			rf := r[s:e]
			df := d[s:e]
			var sig, noi float64
			for i := range rf {
				rv := float64(rf[i])
				dv := float64(df[i])
				sig += rv * rv
				n := rv - dv
				noi += n * n
			}
			snr := 10.0 * math.Log10(sig/(noi+1e-30))
			if snr < minSNR {
				minSNR = snr
			}
			if snr > maxSNR {
				maxSNR = snr
			}
			sumSNR += snr
			if f < 8 {
				t.Logf("frame %02d snr=%.2f dB", f, snr)
			}
		}
		t.Logf("frame_snr avg=%.2f min=%.2f max=%.2f frames=%d", sumSNR/float64(frames), minSNR, maxSNR, frames)
	}
}

func TestTmpSILKComplexitySweep(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
		seconds    = 1
	)
	original := generateEncoderTestSignal(sampleRate*channels*seconds, channels)
	original64 := float32ToFloat64(original)

	for _, cx := range []int{0, 2, 4, 6, 8, 10} {
		enc := encoder.NewEncoder(sampleRate, channels)
		enc.SetMode(encoder.ModeSILK)
		enc.SetBandwidth(types.BandwidthWideband)
		enc.SetBitrate(bitrate)
		enc.SetComplexity(cx)

		var packets [][]byte
		for i := 0; i+frameSize <= len(original64); i += frameSize {
			pkt, err := enc.Encode(original64[i:i+frameSize], frameSize)
			if err != nil {
				t.Fatalf("cx=%d encode: %v", cx, err)
			}
			if pkt == nil {
				continue
			}
			c := make([]byte, len(pkt))
			copy(c, pkt)
			packets = append(packets, c)
		}
		decoded, err := decodeCompliancePackets(packets, channels, frameSize)
		if err != nil {
			t.Fatalf("cx=%d decode: %v", cx, err)
		}
		compareLen := len(original)
		if len(decoded) < compareLen {
			compareLen = len(decoded)
		}
		q, d := ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], sampleRate, 4000)
		t.Logf("cx=%d Q=%.2f SNR=%.2f delay=%d packets=%d", cx, q, SNRFromQuality(q), d, len(packets))
	}
}
