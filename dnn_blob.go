package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func cloneEncoderDNNBlobForControl(data []byte) (*dnnblob.Blob, error) {
	if data == nil {
		return nil, ErrInvalidArgument
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if err := blob.ValidateEncoderControl(); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := rdovae.LoadEncoder(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadPitchDNNModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	return blob, nil
}

func cloneDecoderDNNBlobForControl(data []byte) (*dnnblob.Blob, error) {
	if data == nil {
		return nil, ErrInvalidArgument
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if err := blob.ValidateDecoderControl(false); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadPitchDNNModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := lpcnetplc.LoadFARGANModel(blob); err != nil {
		return nil, ErrInvalidArgument
	}
	if blob.SupportsDREDDecoder() {
		if _, err := rdovae.LoadDecoder(blob); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	return blob, nil
}
