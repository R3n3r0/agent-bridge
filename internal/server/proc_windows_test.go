//go:build windows

package server

import (
	"os"
	"time"
)

func processGone(pid int) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p, err := os.FindProcess(pid)
		if err != nil {
			return true
		}
		p.Release()
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
