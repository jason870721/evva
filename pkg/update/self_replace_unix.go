//go:build unix

package update

import (
	"fmt"
	"os"
	"strings"
)

// replaceBinary atomically replaces dst with src. os.Rename is atomic on
// Linux/macOS within the same mount point, which the temp dir and the
// binary dir almost always are; a cross-device rename falls back to
// staging a copy next to dst.
func replaceBinary(dst, src string) error {
	if err := os.Rename(src, dst); err != nil {
		if isCrossDevice(err) {
			return copyAndRemove(src, dst)
		}
		return fmt.Errorf("rename %s → %s: %w", src, dst, err)
	}
	return nil
}

func isCrossDevice(err error) bool {
	return strings.Contains(err.Error(), "cross-device") ||
		strings.Contains(err.Error(), "invalid cross-device link")
}
