//go:build gopus_qext
// +build gopus_qext

package gopus

type decoderQEXTPayloads struct {
	payloads [maxRepacketizerFrames][]byte
}

func (p *decoderQEXTPayloads) frame(frameIndex int) []byte {
	if p == nil || frameIndex < 0 || frameIndex >= len(p.payloads) {
		return nil
	}
	return p.payloads[frameIndex]
}

func (p *decoderQEXTPayloads) collect(data []byte, nbFrames, id int) {
	if p == nil {
		return
	}
	collectQEXTPacketExtensions(data, nbFrames, id, &p.payloads)
}
