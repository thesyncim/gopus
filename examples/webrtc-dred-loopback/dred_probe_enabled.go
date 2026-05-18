//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package main

import "github.com/thesyncim/gopus"

type dredPacketProbe struct {
	decoder *gopus.DREDDecoder
	state   *gopus.DRED
}

func newDREDPacketProbe(blob []byte) (*dredPacketProbe, error) {
	dec := gopus.NewDREDDecoder()
	if err := dec.SetDNNBlob(blob); err != nil {
		return nil, err
	}
	return &dredPacketProbe{decoder: dec, state: gopus.NewDRED()}, nil
}

func (p *dredPacketProbe) packetHasDRED(packet []byte, frameSamples int) bool {
	if p == nil || p.decoder == nil || p.state == nil || len(packet) == 0 {
		return false
	}
	available, _, err := p.decoder.Parse(p.state, packet, frameSamples, audioSampleRate, true)
	return err == nil && available > 0
}
