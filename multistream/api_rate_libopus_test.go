package multistream

import (
	"math"
	"strconv"
	"testing"

	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/types"
)

// These tests gate end-to-end decoded gopus multistream audio against a libopus
// reference with the canonical comparator (internal/qualitycompare). The trusted
// opus_compare Q only accepts 48 kHz mono/stereo PCM; every case here is either
// sub-48 kHz or 3+ channel multistream, so opus_compare cannot score them and
// they gate on the canonical waveform correlation / RMS-ratio diagnostics instead
// (the comparator itself exposes these as its secondary metrics). The decode is
// frame-aligned against the reference, so the diagnostics are computed at delay 0
// with no alignment search. Internal-state oracles, packet-duration checks,
// decoded length, and stream-mode assertions are exact hard gates.

// compareWaveformF32 builds a QualityComparison from the canonical waveform
// correlation / RMS-ratio diagnostics for decoded PCM that opus_compare cannot
// score (sub-48 kHz or >2 channels). The candidate and reference are already
// frame-aligned, so the metrics are taken at delay 0 and Q is left 0 (unchecked);
// the paired QualityBar sets MinQ:0 so only corr/RMS gate.
func compareWaveformF32(candidate, reference []float32) qualitycompare.QualityComparison {
	corr, rms := waveformCorrRMSF32(candidate, reference)
	return qualitycompare.QualityComparison{Corr: corr, RMSRatio: rms}
}

// waveformCorrRMSF32 computes Pearson correlation and RMS ratio over the common
// prefix, matching the canonical comparator's secondary-diagnostic definition.
func waveformCorrRMSF32(a, b []float32) (corr, rmsRatio float64) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0, 0
	}
	var sumA, sumB, sumASq, sumBSq float64
	for i := 0; i < n; i++ {
		fa, fb := float64(a[i]), float64(b[i])
		sumA += fa
		sumB += fb
		sumASq += fa * fa
		sumBSq += fb * fb
	}
	meanA, meanB := sumA/float64(n), sumB/float64(n)
	var varA, varB, cov float64
	for i := 0; i < n; i++ {
		da, db := float64(a[i])-meanA, float64(b[i])-meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA > 0 && varB > 0 {
		corr = cov / math.Sqrt(varA*varB)
	} else if varA == 0 && varB == 0 {
		corr = 1
	}
	rmsA := math.Sqrt(sumASq / float64(n))
	rmsB := math.Sqrt(sumBSq / float64(n))
	if rmsB > 0 {
		rmsRatio = rmsA / rmsB
	} else if rmsA == 0 {
		rmsRatio = 1
	}
	return corr, rmsRatio
}

// qualityBarWaveformNearExact is the trusted bar for multistream decode cases
// opus_compare cannot score (sub-48 kHz, or 3+ channels): Q is unchecked
// (MinQ:0) and the corr/RMS floors mirror QualityBarNearExact, holding these
// cases near-exact against the libopus reference.
var qualityBarWaveformNearExact = qualitycompare.QualityBar{
	MinQ:    0.0,
	MinCorr: 0.997,
	RMSLo:   0.98,
	RMSHi:   1.02,
	Desc:    "near-exact vs libopus (corr/RMS only; opus_compare needs 48k mono/stereo)",
}

// int16ToFloat32 converts interleaved int16 PCM to the float32 domain the
// canonical comparator scores in.
func int16ToFloat32(s []int16) []float32 {
	out := make([]float32, len(s))
	for i, v := range s {
		out[i] = float32(v) / 32768.0
	}
	return out
}

func TestLibopus_APIRateMultistreamDecodeMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encoderSampleRate = 48000
		sampleRate        = 16000
		channels          = 3
		encoderFrameSize  = encoderSampleRate / 50
		frameSize         = sampleRate / 50
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encoderSampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := make([]float32, encoderFrameSize*channels)
	for i := 0; i < encoderFrameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			freq := 330.0 + 170.0*float64(ch)
			pcm[i*channels+ch] = float32(0.25 * math.Sin(2*math.Pi*freq*float64(i)/encoderSampleRate))
		}
	}
	packet, err := enc.Encode(pcm, encoderFrameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, err := PacketDurationAtRate(packet, streams, sampleRate); err != nil || got != frameSize {
		t.Fatalf("PacketDurationAtRate()=(%d,%v) want (%d,nil)", got, err, frameSize)
	}
	if got, err := PacketDuration(packet, streams); err != nil || got != 960 {
		t.Fatalf("PacketDuration()=(%d,%v) want (960,nil)", got, err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	// sub-48k 3-channel: opus_compare unavailable, gate on corr/RMS diagnostics.
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "api-rate multistream decode (16k)")
}

func TestLibopus_APIRateMultistreamOutputGainMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate       = 48000
		channels         = 3
		frameSize        = sampleRate / 50
		gainQ8           = 8192
		encoderBitrate   = 256000
		encoderFrequency = 997
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(encoderBitrate)
	packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, encoderFrequency), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	want, err := decodeWithLibopusReferencePacketsGain(1, sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, nil, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode gain", err)
	}
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	// 48k CELT fullband + output gain, 3 channels: opus_compare needs mono/stereo,
	// gate on corr/RMS diagnostics.
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "api-rate multistream output gain (48k, 3ch)")
}

func TestLibopus_APIRateMultistreamDecodeToInt16MatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate       = 48000
		channels         = 3
		frameSize        = sampleRate / 50
		gainQ8           = 8192
		encoderBitrate   = 256000
		encoderFrequency = 997
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(encoderBitrate)
	packets := make([][]byte, 2)
	for i := range packets {
		packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, encoderFrequency+float64(i)*113), frameSize)
		if err != nil {
			t.Fatalf("Encode packet %d: %v", i, err)
		}
		packets[i] = packet
	}

	want, err := decodeWithLibopusReferencePacketsInt16Gain(1, sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, nil, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference int16 decode gain", err)
	}
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got := make([]int16, 0, len(want))
	for i, packet := range packets {
		frame, err := dec.DecodeToInt16(packet, frameSize)
		if err != nil {
			t.Fatalf("DecodeToInt16 packet %d: %v", i, err)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("DecodeToInt16 sample count=%d want %d", len(got), len(want))
	}
	// 48k CELT fullband int16 output + output gain, 3 channels: opus_compare needs
	// mono/stereo, so score the int16 PCM in the float32 domain on the canonical
	// corr/RMS diagnostics vs the libopus int16 reference.
	cmp := compareWaveformF32(int16ToFloat32(got), int16ToFloat32(want))
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "api-rate multistream int16 output gain (48k, 3ch)")
}

func TestLibopus_APIRateMultistreamCELTDecodeAndPLCMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encoderSampleRate = 48000
		channels          = 3
		encoderFrameSize  = encoderSampleRate / 50
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encoderSampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)
	pcm := generateTestSignal(channels, encoderFrameSize, encoderSampleRate, 997)
	packet, err := enc.Encode(pcm, encoderFrameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	streamPackets, err := parseMultistreamPacket(packet, streams)
	if err != nil {
		t.Fatalf("parseMultistreamPacket: %v", err)
	}
	for i, streamPacket := range streamPackets {
		if got := parseStreamTOC(streamPacket[0]).mode; got != streamModeCELT {
			t.Fatalf("stream %d mode=%d want CELT", i, got)
		}
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000} {
		frameSize := encoderFrameSize * sampleRate / encoderSampleRate
		t.Run("fs_"+strconv.Itoa(sampleRate), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "reference decode", err)
			}

			dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 0, len(want))
			for i, pkt := range sequence {
				frame, err := dec.Decode(pkt, frameSize)
				if err != nil {
					t.Fatalf("Decode sequence[%d]: %v", i, err)
				}
				if len(frame) != frameSize*channels {
					t.Fatalf("Decode sequence[%d] samples=%d want %d", i, len(frame)/channels, frameSize)
				}
				got = append(got, frame...)
			}
			if len(got) != len(want) {
				t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
			}
			// sub-48k 3-channel CELT decode + PLC: opus_compare unavailable, gate on corr/RMS.
			cmp := compareWaveformF32(got, want)
			qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "api-rate CELT multistream + PLC fs="+strconv.Itoa(sampleRate))
		})
	}
}

func TestLibopus_APIRateMultistreamSILKRequestedPLCMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encoderSampleRate = 48000
		sampleRate        = 16000
		channels          = 1
		encoderFrameSize  = encoderSampleRate / 50
		packetFrameSize   = sampleRate / 50
		requestFrameSize  = sampleRate / 25
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encoderSampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(32000)
	packet, err := enc.Encode(generateTestSignal(channels, encoderFrameSize, encoderSampleRate, 440), encoderFrameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	streamPackets, err := parseMultistreamPacket(packet, streams)
	if err != nil {
		t.Fatalf("parseMultistreamPacket: %v", err)
	}
	if got := parseStreamTOC(streamPackets[0][0]).mode; got != streamModeSILK {
		t.Fatalf("stream mode=%d want SILK", got)
	}

	sequence := [][]byte{packet, nil}
	want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, requestFrameSize, mapping, nil, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference requested PLC decode", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	frame, err := dec.Decode(packet, requestFrameSize)
	if err != nil {
		t.Fatalf("Decode packet: %v", err)
	}
	if len(frame) != packetFrameSize*channels {
		t.Fatalf("Decode packet samples=%d want %d", len(frame)/channels, packetFrameSize)
	}
	got = append(got, frame...)
	frame, err = dec.Decode(nil, requestFrameSize)
	if err != nil {
		t.Fatalf("Decode nil: %v", err)
	}
	if len(frame) != requestFrameSize*channels {
		t.Fatalf("Decode nil samples=%d want %d", len(frame)/channels, requestFrameSize)
	}
	got = append(got, frame...)
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	// sub-48k mono SILK requested PLC: opus_compare needs 48k, gate on corr/RMS.
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "api-rate SILK requested PLC (16k)")
}
