//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// decodeFixedQEXTCELTFrame records output from the selected fixed-point
// ENABLE_QEXT CELT decoder. This received-frame path supports API rates from
// 8 kHz through 96 kHz when the packet does not cross a mode transition. Rates
// below 48 kHz use the 48 kHz core geometry with integer output downsampling.
// The float decoder still runs first to retain shared public state used by
// other codec paths; the fixed result replaces its public samples and range.
func (d *Decoder) decodeFixedQEXTCELTFrame(main *rangecoding.Decoder, dataLen, frameSize int, packetStereo bool, bandwidth celt.CELTBandwidth, qextPayload []byte, transition bool) (bool, error) {
	if !d.fixedPacketActive {
		return false, nil
	}
	if frameSize <= 0 || transition {
		d.invalidateFixedQEXTCELT()
		return false, nil
	}
	if d.fixedQEXT.invalid {
		return false, nil
	}
	if d.fixedQEXT.decoder == nil {
		decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
		if err != nil {
			return false, err
		}
		d.fixedQEXT.decoder = decoder
	}
	core := d.fixedQEXT.decoder
	core.SetPhaseInversionDisabled(d.celtDecoder.PhaseInversionDisabled())
	core.SetBandRange(0, bandwidth.EffectiveBands())

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSize * downsample
	needed := frameSize * int(d.channels)
	if cap(d.fixedQEXT.res) < needed {
		d.fixedQEXT.res = make([]int32, needed)
	}
	res := d.fixedQEXT.res[:needed]
	codedChannels := 1
	if packetStereo {
		codedChannels = 2
	}
	decoded := core.DecodeFrameWithEC(main, dataLen, coreFrameSize, codedChannels, qextPayload, res)
	if decoded < 0 {
		return false, ErrInvalidPacket
	}
	if decoded != frameSize {
		return false, ErrInvalidPacket
	}
	int16Out := d.fixedCELTScratch(needed)
	for i, sample := range res {
		int16Out[i] = fixedpoint.Res2Int16(sample)
	}
	d.appendFixedOutput(int16Out, res)
	d.mainDecodeRng = core.FinalRange()
	return true, nil
}

func (d *Decoder) decodeFixedQEXTCELTLostFrame(frameSize int) bool {
	if !d.fixedPacketActive || d.fixedQEXT.invalid || d.fixedQEXT.decoder == nil || frameSize <= 0 {
		return false
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSize * downsample
	needed := frameSize * int(d.channels)
	if cap(d.fixedQEXT.res) < needed {
		d.fixedQEXT.res = make([]int32, needed)
	}
	res := d.fixedQEXT.res[:needed]
	if decoded := d.fixedQEXT.decoder.DecodeLost(coreFrameSize, res); decoded != frameSize {
		d.invalidateFixedQEXTCELT()
		return false
	}
	int16Out := d.fixedCELTScratch(needed)
	for i, sample := range res {
		int16Out[i] = fixedpoint.Res2Int16(sample)
	}
	d.appendFixedOutput(int16Out, res)
	d.mainDecodeRng = d.fixedQEXT.decoder.FinalRange()
	return true
}

// resetFixedQEXTCELT resets received-frame history while preserving the
// public phase-inversion control in the native decoder.
func (d *Decoder) resetFixedQEXTCELT() {
	if d.fixedQEXT.decoder != nil {
		d.fixedQEXT.decoder.Reset()
	}
	d.fixedQEXT.invalid = false
}

// invalidateFixedQEXTCELT prevents later frames from claiming exact output
// after a packet-loss or mode-transition frame that the native QEXT sidecar
// does not advance yet.
func (d *Decoder) invalidateFixedQEXTCELT() {
	d.fixedQEXT.invalid = true
}

func (d *Decoder) setFixedQEXTPhaseInversionDisabled(disabled bool) {
	if d.fixedQEXT.decoder != nil {
		d.fixedQEXT.decoder.SetPhaseInversionDisabled(disabled)
	}
}
