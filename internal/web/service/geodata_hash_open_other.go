//go:build !windows

package service

import "os"

func openGeodataHashFile(path string) (*os.File, error) {
	return os.Open(path)
}
