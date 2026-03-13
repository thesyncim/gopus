//go:build !darwin && !linux

package main

func sampleRSSBytes() (uint64, bool) {
	return 0, false
}
