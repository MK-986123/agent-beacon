//go:build unix

package config

import (
	"os"
	"syscall"
)

func privateOpenFlags() int {
	return syscall.O_NOFOLLOW | syscall.O_NONBLOCK
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func linkedElsewhere(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}
