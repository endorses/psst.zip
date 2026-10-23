//go:build !linux

package deployment

import (
	"fmt"
	"os"
)

func ownedByRoot(info os.FileInfo) bool     { return false }
func fileUID(info os.FileInfo) uint32       { return ^uint32(0) }
func freeBytes(path string) (uint64, error) { return 0, fmt.Errorf("deployment requires Linux") }
func lock(root string) (func(), error)      { return nil, fmt.Errorf("deployment requires Linux") }
func setPrivateUmask() error                { return fmt.Errorf("deployment requires Linux") }
