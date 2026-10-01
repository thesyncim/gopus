// Package cleanup reports errors from deferred example cleanup operations.
package cleanup

import "log"

// OnReturn runs fn when its caller returns and logs any cleanup error.
func OnReturn(label string, fn func() error) {
	if err := fn(); err != nil {
		log.Printf("cleanup %s: %v", label, err)
	}
}
