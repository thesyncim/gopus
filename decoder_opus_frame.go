package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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
		maxSamples = minInt(len(out), minInt(len(in1), len(in2)))
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

// decodeOpusFrame mirrors libopus opus_decode_frame behavior for a single frame.
// frameSize is the maximum output size for this call (samples per channel).
// packetFrameSize is the current packet's frame size (used for PLC clamping).
func (d *Decoder) decodeOpusFrame(
	data []byte,
	frameSize int,
	packetFrameSize int,
	packetMode Mode,
	packetBandwidth Bandwidth,
	packetStereo bool,
) ([]float32, int, error) {
	fs := 48000
	F20 := fs / 50
	F10 := F20 >> 1
	F5 := F10 >> 1
	F2_5 := F5 >> 1

	if frameSize < F2_5 {
		return nil, 0, ErrBufferTooSmall
	}

	maxFrame := fs / 25 * 3
	frameSize = minInt(frameSize, maxFrame)

	if len(data) <= 1 {
		data = nil
		if packetFrameSize > 0 {
			frameSize = minInt(frameSize, packetFrameSize)
		}
	}

	audiosize := frameSize
	mode := packetMode
	bandwidth := packetBandwidth
	packetStereoLocal := packetStereo

	if data == nil {
		audiosize = frameSize
		if !d.haveDecoded {
			return make([]float32, audiosize*d.channels), audiosize, nil
		}

		if d.prevRedundancy {
			mode = ModeCELT
		} else {
			mode = d.prevMode
		}
		bandwidth = d.lastBandwidth
		packetStereoLocal = d.prevPacketStereo

		if audiosize > F20 {
			out := make([]float32, frameSize*d.channels)
			remaining := audiosize
			offset := 0
			for remaining > 0 {
				chunk := minInt(remaining, F20)
				chunkOut, chunkN, err := d.decodeOpusFrame(nil, chunk, packetFrameSize, mode, bandwidth, packetStereoLocal)
				if err != nil {
					return nil, 0, err
				}
				if chunkN == 0 {
					break
				}
				copy(out[offset*d.channels:], chunkOut[:chunkN*d.channels])
				offset += chunkN
				remaining -= chunkN
			}
			return out, frameSize, nil
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
		return nil, 0, ErrBufferTooSmall
	}
	frameSize = audiosize

	transition := false
	var pcmTransition []float32
	if data != nil && d.haveDecoded && ((mode == ModeCELT && d.prevMode != ModeCELT && !d.prevRedundancy) ||
		(mode != ModeCELT && d.prevMode == ModeCELT)) {
		transition = true
		if mode == ModeCELT {
			transSize := minInt(F5, audiosize)
			trans, transN, err := d.decodeOpusFrame(nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
			if err != nil {
				return nil, 0, err
			}
			pcmTransition = trans[:transN*d.channels]
		}
	}

	var rd rangecoding.Decoder
	if data != nil {
		rd.Init(data)
	}

	celtBW := celt.CELTFullband
	if data != nil {
		celtBW = celt.BandwidthFromOpusConfig(int(bandwidth))
		d.celtDecoder.SetBandwidth(celtBW)
	}

	pcm := make([]float32, frameSize*d.channels)

	redundancy := false
	celtToSilk := false
	redundancyBytes := 0
	mainLen := len(data)
	var redundantAudio []float32

	needCeltReset := d.haveDecoded && mode != d.prevMode && !d.prevRedundancy

	decodeRedundantCELT := func(redundancyData []byte) ([]float32, error) {
		samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(redundancyData, F5, packetStereoLocal)
		if err != nil {
			return nil, err
		}
		out := make([]float32, len(samples))
		for i, s := range samples {
			out[i] = float32(s)
		}
		return out, nil
	}

	switch mode {
	case ModeHybrid:
		if data == nil {
			samples, err := d.hybridDecoder.DecodeWithPacketStereo(nil, frameSize, packetStereoLocal)
			if err != nil {
				return nil, 0, err
			}
			for i := range pcm {
				if i < len(samples) {
					pcm[i] = float32(samples[i])
				}
			}
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
					transSize := minInt(F5, audiosize)
					trans, transN, err := d.decodeOpusFrame(nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
					if err != nil {
						return err
					}
					pcmTransition = trans[:transN*d.channels]
				}

				if needCeltReset {
					d.celtDecoder.Reset()
					d.celtDecoder.SetBandwidth(celtBW)
				}
				return nil
			}

			samples, err := d.hybridDecoder.DecodeWithDecoderHook(&rd, frameSize, packetStereoLocal, afterSilk)
			if err != nil {
				return nil, 0, err
			}
			for i := range pcm {
				if i < len(samples) {
					pcm[i] = float32(samples[i])
				}
			}
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

		var silkOut []float32
		var err error
		if data != nil {
			if packetStereoLocal && d.channels == 2 && !d.prevPacketStereo {
				d.silkDecoder.ResetSideChannel()
			}
			switch {
			case packetStereoLocal && d.channels == 2:
				silkOut, err = d.silkDecoder.DecodeStereoWithDecoder(&rd, silkBW, silkDecodeSize, true)
			case packetStereoLocal && d.channels == 1:
				silkOut, err = d.silkDecoder.DecodeStereoToMonoWithDecoder(&rd, silkBW, silkDecodeSize, true)
			case !packetStereoLocal && d.channels == 2:
				silkOut, err = d.silkDecoder.DecodeMonoToStereoWithDecoder(&rd, silkBW, silkDecodeSize, true, d.prevPacketStereo)
			default:
				silkOut, err = d.silkDecoder.DecodeWithDecoder(&rd, silkBW, silkDecodeSize, true)
			}
		} else {
			if packetStereoLocal && d.channels == 2 && !d.prevPacketStereo {
				d.silkDecoder.ResetSideChannel()
			}
			switch {
			case packetStereoLocal && d.channels == 2:
				silkOut, err = d.silkDecoder.DecodeStereo(nil, silkBW, silkDecodeSize, true)
			case packetStereoLocal && d.channels == 1:
				silkOut, err = d.silkDecoder.DecodeStereoToMono(nil, silkBW, silkDecodeSize, true)
			case !packetStereoLocal && d.channels == 2:
				silkOut, err = d.silkDecoder.DecodeMonoToStereo(nil, silkBW, silkDecodeSize, true, d.prevPacketStereo)
			default:
				silkOut, err = d.silkDecoder.Decode(nil, silkBW, silkDecodeSize, true)
			}
		}
		if err != nil {
			return nil, 0, err
		}
		if frameSize < silkDecodeSize {
			silkOut = silkOut[:frameSize*d.channels]
		}
		copy(pcm, silkOut)

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

		if transition && !redundancy && len(pcmTransition) == 0 {
			transSize := minInt(F5, audiosize)
			trans, transN, err := d.decodeOpusFrame(nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
			if err != nil {
				return nil, 0, err
			}
			pcmTransition = trans[:transN*d.channels]
		}

	case ModeCELT:
		if needCeltReset {
			d.celtDecoder.Reset()
			if data != nil {
				d.celtDecoder.SetBandwidth(celtBW)
			}
		}
		samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(data, minInt(F20, frameSize), packetStereoLocal)
		if err != nil {
			return nil, 0, err
		}
		for i := range pcm {
			if i < len(samples) {
				pcm[i] = float32(samples[i])
			}
		}
	}

	if redundancy {
		transition = false
		pcmTransition = nil
	}

	if redundancy && celtToSilk && len(redundantAudio) == 0 && data != nil && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
		redundantData := data[mainLen : mainLen+redundancyBytes]
		decoded, err := decodeRedundantCELT(redundantData)
		if err != nil {
			return nil, 0, err
		}
		redundantAudio = decoded
	}

	if mode != ModeSILK && data == nil {
		// No extra work for PLC in CELT/Hybrid modes.
	} else if mode == ModeSILK && d.prevMode == ModeHybrid && !(redundancy && celtToSilk && d.prevRedundancy) {
		silence := []byte{0xFF, 0xFF}
		samples, err := d.celtDecoder.DecodeFrameWithPacketStereo(silence, F2_5, packetStereoLocal)
		if err != nil {
			return nil, 0, err
		}
		for i := 0; i < minInt(len(pcm), len(samples)); i++ {
			pcm[i] += float32(samples[i])
		}
	}

	if redundancy && !celtToSilk && data != nil && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
		d.celtDecoder.Reset()
		d.celtDecoder.SetBandwidth(celtBW)
		redundantData := data[mainLen : mainLen+redundancyBytes]
		decoded, err := decodeRedundantCELT(redundantData)
		if err != nil {
			return nil, 0, err
		}
		redundantAudio = decoded
		start := (frameSize - F2_5) * d.channels
		if start >= 0 && start < len(pcm) && len(redundantAudio) >= F5*d.channels {
			smoothFade(pcm[start:], redundantAudio[F2_5*d.channels:], pcm[start:], F2_5, d.channels, fs)
		}
	}

	if redundancy && celtToSilk && (d.prevMode != ModeSILK || d.prevRedundancy) && len(redundantAudio) >= F5*d.channels {
		copy(pcm[:F2_5*d.channels], redundantAudio[:F2_5*d.channels])
		smoothFade(redundantAudio[F2_5*d.channels:], pcm[F2_5*d.channels:], pcm[F2_5*d.channels:], F2_5, d.channels, fs)
	}

	if transition && len(pcmTransition) > 0 {
		if audiosize >= F5 {
			copy(pcm[:F2_5*d.channels], pcmTransition[:F2_5*d.channels])
			smoothFade(pcmTransition[F2_5*d.channels:], pcm[F2_5*d.channels:], pcm[F2_5*d.channels:], F2_5, d.channels, fs)
		} else {
			smoothFade(pcmTransition, pcm, pcm, F2_5, d.channels, fs)
		}
	}

	d.prevMode = mode
	d.prevRedundancy = redundancy && !celtToSilk
	d.haveDecoded = true

	return pcm, audiosize, nil
}
