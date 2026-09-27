//go:build !unix

package libopustest

// lockDNNBuild is a no-op where the C oracle builds do not run.
func lockDNNBuild(string) (unlock func(), err error) {
	return func() {}, nil
}
