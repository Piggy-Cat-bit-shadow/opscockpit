//go:build linux

package host

import (
	"syscall"
	"time"
)

type statfsT = syscall.Statfs_t

func statfs(mount string) (statfsT, error) {
	var fs statfsT
	err := syscall.Statfs(mount, &fs)
	return fs, err
}

func timeSleep(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }
