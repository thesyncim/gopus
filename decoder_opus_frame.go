package gopus

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

var celtSilenceFrame2B = [...]byte{0xFF, 0xFF}

// smoothFade applies a libopus-style crossfade using the CELT window.
func smoothFade(in1, in2, out []float32, overlap, channels, sampleRate int) {
	if overlap <= 0 || channels <= 0 || sampleRate <= 0 {
		return
	}
	inc := 48000 / sampleRate
	if inc <= 0 {
		inc = 1
	}
	win := celt.GetWindowBuffer(overlap * inc)
	if len(win) == 0 {
		return
	}
	maxSamples := overlap * channels
	if len(out) < maxSamples || len(in1) < maxSamples || len(in2) < maxSamples {
		maxSamples = min(len(out), min(len(in1), len(in2)))
		overlap = maxSamples / channels
	}
	for c := 0; c < channels; c++ {
		for i := 0; i < overlap; i++ {
			w := win[i*inc]
			w *= w
			idx := i*channels + c
			if idx >= len(out) || idx >= len(in1) || idx >= len(in2) {
				break
			}
			out[idx] = float32(float64(in2[idx])*w + float64(in1[idx])*(1.0-w))
		}
	}
}

func copyFloat32(dst []float32, src []float32) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	copy(dst, src[:n])
	if n < len(dst) {
		clear(dst[n:])
	}
}

func copyFloat64ToFloat32(dst []float32, src []float64) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	if n > 0 {
		dst = dst[:n:n]
		src = src[:n:n]
		_ = src[n-1]
	}
	i := 0
	for ; i+3 < n; i += 4 {
		dst[i] = float32(src[i])
		dst[i+1] = float32(src[i+1])
		dst[i+2] = float32(src[i+2])
		dst[i+3] = float32(src[i+3])
	}
	for ; i < n; i++ {
		dst[i] = float32(src[i])
	}
	if n < len(dst) {
		clear(dst[n:])
	}
}

func addFloat64ToFloat32(dst []float32, src []float64) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	for i := 0; i < n; i++ {
		dst[i] += float32(src[i])
	}
}

// decodeOpusFrameInto mirrors libopus opus_decode_frame behavior for a single frame.
// out must have room for frameSize * channels samples.
func (d *Decoder) decodeOpusFrameInto(
	out []float32,
	data []byte,
	frameSize int,
	packetFrameSize int,
	packetMode Mode,
	packetBandwidth Bandwidth,
	packetStereo bool,
) (int, error) {
	fs := 48000
	F20 := fs / 50
	F10 := F20 >> 1
	F5 := F10 >> 1
	F2_5 := F5 >> 1

	if frameSize < F2_5 {
		return 0, ErrBufferTooSmall
	}

	maxFrame := fs / 25 * 3
	frameSize = min(frameSize, maxFrame)

	if len(data) <= 1 {
		data = nil
		if packetFrameSize > 0 {
			frameSize = min(frameSize, packetFrameSize)
		}
	}

	audiosize := frameSize
	mode := packetMode
	bandwidth := packetBandwidth
	packetStereoLocal := packetStereo

	if data == nil {
		audiosize = frameSize
		if !d.haveDecoded {
			needed := audiosize * d.channels
			if len(out) < needed {
				return 0, ErrBufferTooSmall
			}
			clear(out[:needed])
			return audiosize, nil
		}

		if d.prevRedundancy {
			mode = ModeCELT
		} else {
			mode = d.prevMode
		}
		bandwidth = d.lastBandwidth
		packetStereoLocal = d.prevPacketStereo

		if audiosize > F20 {
			remaining := audiosize
			offset := 0
			for remaining > 0 {
				chunk := min(remaining, F20)
				n, err := d.decodeOpusFrameInto(out[offset*d.channels:], nil, chunk, packetFrameSize, mode, bandwidth, packetStereoLocal)
				if err != nil {
					return 0, err
				}
				if n == 0 {
					break
				}
				offset += n
				remaining -= n
			}
			return audiosize, nil
		} else if audiosize < F20 {
			if audiosize > F10 {
				audiosize = F10
			} else if mode != ModeSILK && audiosize > F5 && audiosize < F10 {
				audiosize = F5
			}
		}
	} else {
		audiosize = packetFrameSize
	}

	if audiosize > frameSize {
		return 0, ErrBufferTooSmall
	}
	frameSize = audiosize

	needed := frameSize * d.channels
	if len(out) < needed {
		return 0, ErrBufferTooSmall
	}
	out = out[:needed]

	transition := false
	var pcmTransition []float32
	// Keep transition smoothing for CELT<->(SILK/Hybrid), but skip the 10ms
	// Hybrid->CELT transition fade. For 10ms this fade can create a single-frame
	// artifact on the first CELT frame after Hybrid.
	if data != nil && d.haveDecoded && ((mode == ModeCELT && d.prevMode != ModeCELT &&
		!d.prevRedundancy && !(d.prevMode == ModeHybrid && audiosize == F10)) ||
		(mode != ModeCELT && d.prevMode == ModeCELT)) {
		transition = true
		if mode == ModeCELT {
			transSize := min(F5, audiosize)
			if len(d.scratchTransition) < transSize*d.channels {
				return 0, ErrBufferTooSmall
			}
			n, err := d.decodeOpusFrameInto(d.scratchTransition, nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
			if err != nil {
				return 0, err
			}
			pcmTransition = d.scratchTransition[:n*d.channels]
		}
	}

	rd := &d.scratchRangeDecoder
	if data != nil {
		rd.Init(data)
	}

	celtBW := celt.CELTFullband
	if data != nil {
		celtBW = celt.BandwidthFromOpusConfig(int(bandwidth))
		d.celtDecoder.SetBandwidth(celtBW)
	}

	redundancy := false
	celtToSilk := false
	redundancyBytes := 0
	mainLen := len(data)
	var redundantAudio []float32
	var redundantRng uint32 // Captured final range from redundancy decoding

	needCeltReset := d.haveDecoded && mode != d.prevMode && !d.prevRedundancy

	decodeRedundantCELT := func(redundancyData []byte) ([]float32, error) {
		samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(redundancyData, F5, packetStereoLocal)
		if err != nil {
			return nil, err
		}
		// Capture the final range from decoding the redundancy frame
		redundantRng = d.celtDecoder.FinalRange()
		if len(d.scratchRedundant) < len(samples) {
			return nil, ErrBufferTooSmall
		}
		for i := 0; i < len(samples); i++ {
			d.scratchRedundant[i] = float32(samples[i])
		}
		if len(samples) < len(d.scratchRedundant) {
			clear(d.scratchRedundant[len(samples):])
		}
		return d.scratchRedundant[:len(samples)], nil
	}

	switch mode {
	case ModeHybrid:
		if data == nil {
			samples, err := d.hybridDecoder.DecodeWithPacketStereo(nil, frameSize, packetStereoLocal)
			if err != nil {
				return 0, err
			}
			copyFloat64ToFloat32(out, samples)
			// Capture FinalRange for PLC
			d.mainDecodeRng = d.hybridDecoder.FinalRange()
		} else {
			d.hybridDecoder.SetPrevPacketStereo(d.prevPacketStereo)
			afterSilk := func(rd *rangecoding.Decoder) error {
				if rd == nil {
					return nil
				}
				if rd.Tell()+17+20 <= 8*len(data) {
					redundancy = rd.DecodeBit(12) == 1
					if redundancy {
						celtToSilk = rd.DecodeBit(1) == 1
						redundancyBytes = int(rd.DecodeUniform(256)) + 2
						mainLen = len(data) - redundancyBytes
						if mainLen*8 < rd.Tell() {
							mainLen = 0
							redundancyBytes = 0
							redundancy = false
							celtToSilk = false
						} else {
							rd.ShrinkStorage(redundancyBytes)
						}
					}
				}

				if redundancy && celtToSilk && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
					redundantData := data[mainLen : mainLen+redundancyBytes]
					decoded, err := decodeRedundantCELT(redundantData)
					if err != nil {
						return err
					}
					redundantAudio = decoded
				}

				if transition && !redundancy && len(pcmTransition) == 0 {
					transSize := min(F5, audiosize)
					n, err := d.decodeOpusFrameInto(d.scratchTransition, nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
					if err != nil {
						return err
					}
					pcmTransition = d.scratchTransition[:n*d.channels]
				}

				if needCeltReset {
					d.celtDecoder.Reset()
					d.celtDecoder.SetBandwidth(celtBW)
				}
				return nil
			}

			samples, err := d.hybridDecoder.DecodeWithDecoderHook(rd, frameSize, packetStereoLocal, afterSilk)
			if err != nil {
				return 0, err
			}
			copyFloat64ToFloat32(out, samples)
			// Capture the main decode's FinalRange before any redundancy post-processing
			d.mainDecodeRng = d.hybridDecoder.FinalRange()
		}

	case ModeSILK:
		if d.haveDecoded && d.prevMode == ModeCELT {
			d.silkDecoder.Reset()
		}

		silkBW, ok := silk.BandwidthFromOpus(int(bandwidth))
		if !ok {
			silkBW = silk.BandwidthWideband
		}

		silkDecodeSize := frameSize
		if silkDecodeSize < F10 {
			silkDecodeSize = F10
		}

		var silkSamples int
		var err error
		if data != nil {
			if packetStereoLocal && d.channels == 2 && !d.prevPacketStereo {
				d.silkDecoder.ResetSideChannel()
				leftResampler := d.silkDecoder.GetResampler(silkBW)
				rightResampler := d.silkDecoder.GetResamplerRightChannel(silkBW)
				if rightResampler != nil && leftResampler != nil {
					rightResampler.CopyFrom(leftResampler)
				}
			}
			switch {
			case packetStereoLocal && d.channels == 2:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeStereoWithDecoder(rd, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			case packetStereoLocal && d.channels == 1:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeStereoToMonoWithDecoder(rd, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			case !packetStereoLocal && d.channels == 2:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeMonoToStereoWithDecoder(rd, silkBW, silkDecodeSize, true, d.prevPacketStereo)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			default:
				// Zero-allocation path for mono SILK decode
				silkSamples, err = d.silkDecoder.DecodeWithDecoderInto(rd, silkBW, silkDecodeSize, true, out)
				if err == nil && frameSize < silkDecodeSize {
					silkSamples = frameSize
				}
			}
		} else {
			if packetStereoLocal && d.channels == 2 && !d.prevPacketStereo {
				d.silkDecoder.ResetSideChannel()
				leftResampler := d.silkDecoder.GetResampler(silkBW)
				rightResampler := d.silkDecoder.GetResamplerRightChannel(silkBW)
				if rightResampler != nil && leftResampler != nil {
					rightResampler.CopyFrom(leftResampler)
				}
			}
			switch {
			case packetStereoLocal && d.channels == 2:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeStereo(nil, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			case packetStereoLocal && d.channels == 1:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeStereoToMono(nil, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			case !packetStereoLocal && d.channels == 2:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeMonoToStereo(nil, silkBW, silkDecodeSize, true, d.prevPacketStereo)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			default:
				var silkOut []float32
				silkOut, err = d.silkDecoder.Decode(nil, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / d.channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*d.channels])
				}
			}
		}
		if err != nil {
			return 0, err
		}
		_ = silkSamples // Used for tracking decode size

		if data != nil && rd.Tell()+17 <= 8*len(data) {
			redundancy = true
			celtToSilk = rd.DecodeBit(1) == 1
			redundancyBytes = len(data) - ((rd.Tell() + 7) >> 3)
			mainLen = len(data) - redundancyBytes
			if mainLen*8 < rd.Tell() {
				mainLen = 0
				redundancyBytes = 0
				redundancy = false
				celtToSilk = false
			} else {
				rd.ShrinkStorage(redundancyBytes)
			}
		}

		// Capture the main decode's FinalRange AFTER redundancy flag reads but BEFORE any CELT redundancy decode.
		// For SILK-only mode, the final range includes all bits read from the range decoder.
		d.mainDecodeRng = rd.Range()

		if transition && !redundancy && len(pcmTransition) == 0 {
			transSize := min(F5, audiosize)
			n, err := d.decodeOpusFrameInto(d.scratchTransition, nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
			if err != nil {
				return 0, err
			}
			pcmTransition = d.scratchTransition[:n*d.channels]
		}

	case ModeCELT:
		if needCeltReset {
			d.celtDecoder.Reset()
			if data != nil {
				d.celtDecoder.SetBandwidth(celtBW)
			}
		}
		samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(data, min(F20, frameSize), packetStereoLocal)
		if err != nil {
			return 0, err
		}
		copyFloat64ToFloat32(out, samples)
		// Capture the main decode's FinalRange (no redundancy post-processing for CELT-only)
		d.mainDecodeRng = d.celtDecoder.FinalRange()
	}

	if redundancy {
		transition = false
		pcmTransition = nil
	}

	if redundancy && celtToSilk && len(redundantAudio) == 0 && data != nil && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
		redundantData := data[mainLen : mainLen+redundancyBytes]
		decoded, err := decodeRedundantCELT(redundantData)
		if err != nil {
			return 0, err
		}
		redundantAudio = decoded
	}

	if mode != ModeSILK && data == nil {
		// No extra work for PLC in CELT/Hybrid modes.
	} else if mode == ModeSILK && d.prevMode == ModeHybrid && !(redundancy && celtToSilk && d.prevRedundancy) {
		samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(celtSilenceFrame2B[:], F2_5, packetStereoLocal)
		if err != nil {
			return 0, err
		}
		addFloat64ToFloat32(out, samples)
	}

	if redundancy && !celtToSilk && data != nil && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
		d.celtDecoder.Reset()
		d.celtDecoder.SetBandwidth(celtBW)
		redundantData := data[mainLen : mainLen+redundancyBytes]
		decoded, err := decodeRedundantCELT(redundantData)
		if err != nil {
			return 0, err
		}
		redundantAudio = decoded
		start := (frameSize - F2_5) * d.channels
		if start >= 0 && start < len(out) && len(redundantAudio) >= F5*d.channels {
			smoothFade(out[start:], redundantAudio[F2_5*d.channels:], out[start:], F2_5, d.channels, fs)
		}
	}

	if redundancy && celtToSilk && (d.prevMode != ModeSILK || d.prevRedundancy) && len(redundantAudio) >= F5*d.channels {
		copy(out[:F2_5*d.channels], redundantAudio[:F2_5*d.channels])
		smoothFade(redundantAudio[F2_5*d.channels:], out[F2_5*d.channels:], out[F2_5*d.channels:], F2_5, d.channels, fs)
	}

	if transition && len(pcmTransition) > 0 {
		if audiosize >= F5 {
			copy(out[:F2_5*d.channels], pcmTransition[:F2_5*d.channels])
			smoothFade(pcmTransition[F2_5*d.channels:], out[F2_5*d.channels:], out[F2_5*d.channels:], F2_5, d.channels, fs)
		} else {
			smoothFade(pcmTransition, out, out, F2_5, d.channels, fs)
		}
	}

	d.prevMode = mode
	d.prevRedundancy = redundancy && !celtToSilk
	d.haveDecoded = true
	d.redundantRng = redundantRng
	// Note: d.lastDataLen is set at packet level in Decode(), not here

	return audiosize, nil
}
