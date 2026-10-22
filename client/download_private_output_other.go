//go:build !windows

package client

import "os"

func createPrivateDownload(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}
