package hybrid

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func (d *Decoder) finishSuccessfulDecode(frameSize, channels int) {
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, channels)
}

func (d *Decoder) requireStereoDecoder() error {
	if d.channels != 2 {
		return ErrDecodeFailed
	}
	return nil
}

func decodedInt16FromFloat32(samples []float32, err error) ([]int16, error) {
	if err != nil {
		return nil, err
	}
	return float32ToInt16(samples), nil
}

func (d *Decoder) decodeWithRangeDecoder(
	rd *rangecoding.Decoder,
	frameSize int,
	packetStereo bool,
	afterSilk func(*rangecoding.Decoder) error,
) ([]float32, error) {
	return d.decodeFrameWithHookFloat32(rd, frameSize, packetStereo, afterSilk, nil)
}

func (d *Decoder) decodeAndFinishPacket(
	data []byte,
	frameSize int,
	packetStereo bool,
	lastFrameChannels int,
) ([]float32, error) {
	if len(data) == 0 {
		return d.decodePLCToFloat32(frameSize, packetStereo)
	}
	if !ValidHybridFrameSize(d.frameSize48FromAPI(frameSize)) {
		return nil, ErrInvalidFrameSize
	}

	var rd rangecoding.Decoder
	rd.Init(data)

	samples, err := d.decodeWithRangeDecoder(&rd, frameSize, packetStereo, nil)
	if err != nil {
		return nil, err
	}

	d.finishSuccessfulDecode(frameSize, lastFrameChannels)
	return samples, nil
}

func (d *Decoder) decodeAndFinishWithRangeDecoder(
	rd *rangecoding.Decoder,
	frameSize int,
	packetStereo bool,
	lastFrameChannels int,
	afterSilk func(*rangecoding.Decoder) error,
) ([]float32, error) {
	samples, err := d.decodeWithRangeDecoder(rd, frameSize, packetStereo, afterSilk)
	if err != nil {
		return nil, err
	}

	d.finishSuccessfulDecode(frameSize, lastFrameChannels)
	return samples, nil
}

// Decode decodes a Hybrid mono frame and returns 48kHz PCM samples.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte), or nil for PLC
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float32 samples at 48kHz.
//
// Hybrid mode combines SILK (0-8kHz) and CELT (8-20kHz) for high-quality
// wideband speech at medium bitrates. Only 10ms and 20ms frames are supported.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, false, 1)
}

// DecodeWithPacketStereo decodes a Hybrid frame and honors the packet stereo flag.
// This is used when the output channels (decoder configuration) differ from the packet channels.
func (d *Decoder) DecodeWithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, packetStereo, int(d.channels))
}

// SetRawMonoFrameHook forwards the SILK lowband raw mono/mid-channel hook used
// by decoder-side neural PLC/DRED paths.
func (d *Decoder) SetRawMonoFrameHook(hook silk.RawMonoFrameHook) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.SetRawMonoFrameHook(hook)
}

// SetDeepPLCLossMonoHook forwards the SILK lowband loss hook used by
// decoder-side neural PLC/DRED paths.
func (d *Decoder) SetDeepPLCLossMonoHook(hook silk.DeepPLCLossMonoHook) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.SetDeepPLCLossMonoHook(hook)
}

// DecodeStereo decodes a Hybrid stereo frame and returns 48kHz PCM samples.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] at 48kHz.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte), or nil for PLC
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns interleaved float32 samples at 48kHz.
func (d *Decoder) DecodeStereo(data []byte, frameSize int) ([]float32, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}

	return d.decodeAndFinishPacket(data, frameSize, true, 2)
}

// DecodeToInt16 decodes and converts to int16 PCM.
// This is a convenience wrapper for common audio output formats.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte)
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns int16 samples at 48kHz in range [-32768, 32767].
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	return decodedInt16FromFloat32(d.decodeAndFinishPacket(data, frameSize, false, 1))
}

// DecodeStereoToInt16 decodes stereo and converts to int16 PCM.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] as int16.
func (d *Decoder) DecodeStereoToInt16(data []byte, frameSize int) ([]int16, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}

	return decodedInt16FromFloat32(d.decodeAndFinishPacket(data, frameSize, true, 2))
}

// DecodeToFloat32 decodes and converts to float32 PCM.
// This is a convenience wrapper for audio APIs expecting float32.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte)
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float32 samples at 48kHz in approximate range [-1, 1].
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, false, 1)
}

// DecodeToFloat32WithPacketStereo decodes with packet stereo flag and converts to float32.
func (d *Decoder) DecodeToFloat32WithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, packetStereo, int(d.channels))
}

// DecodeStereoToFloat32 decodes stereo and converts to float32 PCM.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] as float32.
func (d *Decoder) DecodeStereoToFloat32(data []byte, frameSize int) ([]float32, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}

	return d.decodeAndFinishPacket(data, frameSize, true, 2)
}

// DecodeWithDecoder decodes using a pre-initialized range decoder.
// This is useful when the range decoder state needs to be preserved or
// when decoding multiple frames from a single buffer.
//
// Parameters:
//   - rd: Pre-initialized range decoder
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float32 samples at 48kHz.
func (d *Decoder) DecodeWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	return d.decodeWithRangeDecoder(rd, frameSize, false, nil)
}

// DecodeWithDecoderHook decodes using a pre-initialized range decoder and an optional hook.
// The hook runs after SILK decode and before CELT decode, allowing Opus-layer parsing.
func (d *Decoder) DecodeWithDecoderHook(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) error) ([]float32, error) {
	return d.decodeAndFinishWithRangeDecoder(rd, frameSize, packetStereo, int(d.channels), afterSilk)
}

// DecodeStereoWithDecoder decodes stereo using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}
	return d.decodeWithRangeDecoder(rd, frameSize, true, nil)
}

func float32ToInt16(samples []float32) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(s)
	}
	return output
}

func (d *Decoder) decodePLCToFloat32(frameSize int, stereo bool) ([]float32, error) {
	if frameSize < 0 {
		return nil, ErrInvalidFrameSize
	}
	out := make([]float32, frameSize*int(d.channels))
	if err := d.DecodePLCToFloat32WithPacketStereoInto(frameSize, stereo, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DecodePLCToFloat32WithPacketStereoInto conceals a Hybrid frame into
// caller-owned PCM while advancing the same SILK and CELT PLC state.
func (d *Decoder) DecodePLCToFloat32WithPacketStereoInto(frameSize int, stereo bool, output []float32) error {
	frameSizeAPI := frameSize
	frameSize48 := d.frameSize48FromAPI(frameSizeAPI)
	if !ValidHybridFrameSize(frameSize48) && frameSize48 != 120 && frameSize48 != 240 {
		return ErrInvalidFrameSize
	}
	channels := int(d.channels)
	totalSamples := frameSizeAPI * channels
	if len(output) < totalSamples {
		return ErrDecodeFailed
	}
	output = output[:totalSamples]

	// Advance the PLC loss-fade cadence. libopus has no fade-exhausted
	// shortcut: silk_PLC and celt_decode_lost run unconditionally on every lost
	// hybrid frame. The SILK lowband fades through silk_PLC and the CELT highband
	// energy floors at the background estimate, so the concealed frame decays
	// without ever being short-circuited to silence. Critically, celt_decode_lost
	// always advances the CELT range-coder state (st->rng, the noise LCG) on each
	// lost frame, and that state is what an in-band FEC (decode_fec=1) recovery
	// reports as the final range. Returning early here would freeze st->rng and
	// desync the range coder on the next FEC step, so the CELT PLC must run on
	// every lost frame regardless of how decayed the energy is.
	fadeFactor := d.plcState.RecordLoss()

	// SILK PLC cannot produce less than 10ms; use 10ms and trim if needed.
	plcSilkFrameSize := frameSizeAPI
	apiSampleRate := int(d.apiSampleRate)
	minSilkFrameSize := apiSampleRate / 100
	if minSilkFrameSize <= 0 {
		minSilkFrameSize = 480
	}
	if plcSilkFrameSize < minSilkFrameSize {
		plcSilkFrameSize = minSilkFrameSize
	}

	// Generate SILK PLC through the SILK decoder's native nil-packet path.
	// This keeps concealment cadence/state aligned with SILK-mode PLC.
	silkChannels := channels
	if stereo {
		silkChannels = 2
	}
	silkUpsampled := d.ensureSilkUpsampled(plcSilkFrameSize * silkChannels)
	clear(silkUpsampled)
	d.silkDecoder.NotifyBandwidthChange(silk.BandwidthWideband)
	if stereo {
		n, err := d.silkDecoder.DecodePLCStereoInto(silk.BandwidthWideband, plcSilkFrameSize, silkUpsampled)
		if err != nil {
			return err
		}
		silkUpsampled = silkUpsampled[:n]
	} else {
		mono := silkUpsampled
		if channels == 2 {
			if cap(d.plcMonoScratch) < plcSilkFrameSize {
				d.plcMonoScratch = make([]float32, plcSilkFrameSize)
			}
			mono = d.plcMonoScratch[:plcSilkFrameSize]
		}
		n, err := d.silkDecoder.DecodePLCInto(silk.BandwidthWideband, plcSilkFrameSize, mono)
		if err != nil {
			return err
		}
		if d.channels == 2 {
			for i := range n {
				val := mono[i]
				silkUpsampled[i*2] = val
				silkUpsampled[i*2+1] = val
			}
			silkUpsampled = silkUpsampled[:n*2]
		} else {
			silkUpsampled = silkUpsampled[:n]
		}
	}
	if len(silkUpsampled) > totalSamples {
		silkUpsampled = silkUpsampled[:totalSamples]
	}

	// The SILK decoder/resampler path already provides API-rate alignment.
	clear(output[copy(output, silkUpsampled):])

	// Conceal the CELT highband (bands 17-21) and accumulate it onto the SILK
	// lowband, as opus_decode_frame's celt_decode_with_ec(NULL, celt_accum=1)
	// does for a lost Hybrid frame.
	if frameSize48 == 240 || frameSize48 == 480 || frameSize48 == 960 {
		celtFrameSize := frameSize48
		if apiSampleRate == 96000 {
			celtFrameSize = frameSizeAPI
		}
		if err := d.celtDecoder.DecodeHybridFECPLC(celtFrameSize, output); err != nil {
			return err
		}
	} else {
		// Fallback for non-hybrid frame sizes used by internal cadence paths.
		// Pass celtDecoder as both state and synthesizer (implements both interfaces).
		celtConcealed := plc.ConcealCELTHybrid(d.celtDecoder, d.celtDecoder, frameSize48, fadeFactor)
		factor := 1
		if apiSampleRate > 0 {
			factor = max(48000/apiSampleRate, 1)
		}
		for i := range frameSizeAPI {
			for c := range channels {
				if celtIdx := i*factor*channels + c; celtIdx < len(celtConcealed) {
					output[i*channels+c] += celtConcealed[celtIdx] * (1.0 / 32768.0)
				}
			}
		}
	}

	return nil
}
