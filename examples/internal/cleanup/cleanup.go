// Package cleanup reports errors from deferred example cleanup operations.
package cleanup

import "log"

// OnReturn runs fn and logs any error. It is intended for deferred cleanup.
func OnReturn(label string, fn func() error) {
	if err := fn(); err != nil {
		log.Printf("cleanup %s: %v", label, err)
	}
}
