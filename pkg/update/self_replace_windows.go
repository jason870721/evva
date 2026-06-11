//go:build windows

package update

import (
	"fmt"
	"os"
)

// replaceBinary swaps dst with src on Windows. The running image cannot
// be deleted or overwritten, but it CAN be renamed — so move it aside and
// put the new binary in its place (the standard rename-aside dance; no
// helper script needed). The .old leftover is removed best-effort here
// and swept again by CleanupOld at the next start.
func replaceBinary(dst, src string) error {
	old := dst + ".old"
	// Leftover from an earlier update; gone unless some process still
	// holds it, in which case the rename below reports the real problem.
	_ = os.Remove(old)

	if err := os.Rename(dst, old); err != nil {
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		// Likely a cross-volume temp dir; stage a copy next to dst.
		if cpErr := copyAndRemove(src, dst); cpErr != nil {
			// Roll back so the install keeps working.
			_ = os.Rename(old, dst)
			return fmt.Errorf("install new binary: %w", cpErr)
		}
	}
	// Expected to fail while the old image is still the running process —
	// CleanupOld finishes the job at next start.
	_ = os.Remove(old)
	return nil
}
