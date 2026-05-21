//go:build gopus_dred || gopus_extra_controls

package main

import "github.com/thesyncim/gopus"

type dredPacketProbe struct {
	decoder       *gopus.DREDDecoder
	detectState   *gopus.DRED
	recoveryState *gopus.DRED
}

func newDREDPacketProbe(blob []byte) (*dredPacketProbe, error) {
	dec := gopus.NewDREDDecoder()
	if err := dec.SetDNNBlob(blob); err != nil {
		return nil, err
	}
	return &dredPacketProbe{
		decoder:       dec,
		detectState:   gopus.NewDRED(),
		recoveryState: gopus.NewDRED(),
	}, nil
}

func (p *dredPacketProbe) packetHasDRED(packet []byte, frameSamples int) bool {
	if p == nil || p.decoder == nil || p.detectState == nil || len(packet) == 0 {
		return false
	}
	available, _, err := p.decoder.Parse(p.detectState, packet, frameSamples, audioSampleRate, true)
	return err == nil && available > 0
}

func (p *dredPacketProbe) prepareRecovery(packet []byte, maxDREDSamples int) (int, bool) {
	if p == nil || p.decoder == nil || p.recoveryState == nil || len(packet) == 0 || maxDREDSamples <= 0 {
		return 0, false
	}
	available, _, err := p.decoder.Parse(p.recoveryState, packet, maxDREDSamples, audioSampleRate, false)
	return available, err == nil && available > 0 && p.recoveryState.Processed()
}

func (p *dredPacketProbe) decodeRecovery(dec *gopus.Decoder, dredOffsetSamples int, pcm []float32, frameSamples int) (int, error) {
	if p == nil || dec == nil || p.recoveryState == nil || !p.recoveryState.Processed() {
		return 0, gopus.ErrInvalidArgument
	}
	return dec.DecodeDRED(p.recoveryState, dredOffsetSamples, pcm, frameSamples)
}
