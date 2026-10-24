//go:build !linux

package deployment

import (
	"context"
	"fmt"
	"os"
)

func isTerminal(f *os.File) bool { return false }
func readSecret(context.Context, *os.File) (string, error) {
	return "", fmt.Errorf("deployment requires Linux")
}
func readLine(context.Context, *os.File) (string, error) {
	return "", fmt.Errorf("deployment requires Linux")
}
