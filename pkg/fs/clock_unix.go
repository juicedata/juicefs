// +build !windows

package fs

import "time"

var started = time.Now()

func Now() time.Time {
	return time.Now()
}

func Clock() time.Duration {
	return time.Since(started)
}
