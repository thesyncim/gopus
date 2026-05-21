//go:build gopus_qext

package multistream

type streamQEXTPayloads struct {
	payloads [maxPacketExtensionFrames][]byte
}

func (p *streamQEXTPayloads) frame(frameIndex int) []byte {
	if p == nil || frameIndex < 0 || frameIndex >= len(p.payloads) {
		return nil
	}
	return p.payloads[frameIndex]
}

func (p *streamQEXTPayloads) collect(data []byte, nbFrames, id int) {
	if p == nil {
		return
	}
	collectQEXTPacketExtensions(data, nbFrames, id, &p.payloads)
}
