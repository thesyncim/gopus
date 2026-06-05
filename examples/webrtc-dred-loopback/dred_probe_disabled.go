//go:build !gopus_dred && !gopus_osce

package main

import "github.com/thesyncim/gopus"

type dredPacketProbe struct{}

func newDREDPacketProbe(_ []byte) (*dredPacketProbe, error) {
	return nil, nil
}

func (p *dredPacketProbe) packetHasDRED(_ []byte, _ int) bool {
	return false
}

func (p *dredPacketProbe) prepareRecovery(_ []byte, _ int) (int, bool, error) {
	return 0, false, nil
}

func (p *dredPacketProbe) decodeRecovery(_ *gopus.Decoder, _ int, _ []float32, _ int) (int, error) {
	return 0, gopus.ErrOptionalExtensionUnavailable
}
