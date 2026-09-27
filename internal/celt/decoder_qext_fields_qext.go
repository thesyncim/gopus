//go:build gopus_qext

package celt

type decoderQEXTFields struct {
	qext *decoderQEXTState
}

func (d *Decoder) qextState() *decoderQEXTState {
	return d.qext
}

func (d *Decoder) ensureQEXTState() *decoderQEXTState {
	if d.qext == nil {
		d.qext = &decoderQEXTState{}
	}
	return d.qext
}

func (d *Decoder) clearQEXTState() {
	if d.qext == nil {
		return
	}
	d.qext.pendingPayload = nil
	// These samples are decoder history, not scratch. opus_custom_decoder_init
	// clears the native decode memory along with the other persistent CELT
	// state; keep Reset equivalent for the native 96 kHz QEXT comb filter.
	clear(d.qext.hd96kPostMem)
	for i := range d.qext.oldBandE {
		d.qext.oldBandE[i] = 0
	}
}

func (d *Decoder) growQEXTOldBandE(needed int) {
	if d.qext == nil || len(d.qext.oldBandE) == 0 || len(d.qext.oldBandE) >= needed {
		return
	}
	prev := make([]celtGLog, needed)
	copy(prev, d.qext.oldBandE)
	d.qext.oldBandE = prev
}
