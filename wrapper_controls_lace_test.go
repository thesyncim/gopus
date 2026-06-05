//go:build (!gopus_qext && !gopus_dred) || gopus_osce || (gopus_dred && gopus_qext)

package gopus

type extraOSCELACEControl interface {
	SetOSCELACE(bool) error
	OSCELACE() (bool, error)
}
