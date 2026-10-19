//go:build !windows

package handlers

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lineStatsFileIdentity(f *os.File) (string, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d:%v:%v", stat.Dev, stat.Ino, stat.Size, stat.Mtim, stat.Ctim), nil
}
