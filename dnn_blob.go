package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
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
	return blob, nil
}
