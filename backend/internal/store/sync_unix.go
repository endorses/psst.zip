//go:build !windows

package store

import "os"

func syncDiskFile(file *os.File) error { return file.Sync() }
