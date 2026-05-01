package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob for future optional
// extension paths. A nil blob clears the retained main-decoder model state.
func (d *Decoder) SetDNNBlob(blob *dnnblob.Blob) {
	d.dnnBlob = blob
	var models dnnblob.DecoderModelState
	if blob != nil {
		models = blob.DecoderModels()
	}
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

// DNNBlobLoaded reports whether a validated model blob is retained.
func (d *Decoder) DNNBlobLoaded() bool {
	return d.dnnBlob != nil
}

// PitchDNNLoaded reports whether the retained blob contains libopus's shared
// decoder pitch model family.
func (d *Decoder) PitchDNNLoaded() bool {
	return d.pitchDNNLoaded
}

// PLCModelLoaded reports whether the retained blob contains the PLC model family.
func (d *Decoder) PLCModelLoaded() bool {
	return d.plcModelLoaded
}

// FARGANModelLoaded reports whether the retained blob contains the FARGAN model family.
func (d *Decoder) FARGANModelLoaded() bool {
	return d.farganModelLoaded
}
