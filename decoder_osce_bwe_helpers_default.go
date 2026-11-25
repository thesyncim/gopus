//go:build gopus_dred && !gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
)

func validateOSCEBWEModel(_ *dnnblob.Blob, _ bool) error {
	return nil
}

func (d *Decoder) bindOSCEBWEModel(_ *dnnblob.Blob, _ bool) error {
	return nil
}

func validateOSCELACEModel(_ *dnnblob.Blob, _ bool) error {
	return nil
}

func (d *Decoder) bindOSCELACEModel(_ *dnnblob.Blob, _ bool) error {
	return nil
}
