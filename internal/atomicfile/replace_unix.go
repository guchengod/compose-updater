//go:build linux || darwin

package atomicfile

import "os"

func replace(source, destination string) error {
	return os.Rename(source, destination)
}
