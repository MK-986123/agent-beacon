//go:build !unix

package config

import "os"

func privateOpenFlags() int { return 0 }

func ownedByCurrentUser(os.FileInfo) bool { return true }

func linkedElsewhere(os.FileInfo) bool { return false }
