//go:build gopus_qext || gopus_dred || gopus_extra_controls

package encoder

func (e *Encoder) ensureExtensionPacketScratch() {
	if len(e.scratchPacket) >= extensionScratchPacketBytes {
		return
	}
	e.scratchPacket = make([]byte, extensionScratchPacketBytes)
}
