//go:build !gopus_dred && !gopus_unsupported_controls
// +build !gopus_dred,!gopus_unsupported_controls

package main

type dredPacketProbe struct{}

func newDREDPacketProbe(_ []byte) (*dredPacketProbe, error) {
	return nil, nil
}

func (p *dredPacketProbe) packetHasDRED(_ []byte, _ int) bool {
	return false
}
