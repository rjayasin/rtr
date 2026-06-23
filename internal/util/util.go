// Package util holds small, dependency-free helpers shared across rtr's
// packages: human-readable byte formatting and "~" path expansion.
package util

import (
	"fmt"
	"os"
	"strings"
)

// HumanBytes renders a byte count compactly: a bare "512B" below 1 KiB,
// otherwise a one-decimal value with a binary unit suffix, e.g. "4.2M" or
// "1.5G". Units are powers of 1024.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

// ExpandHome expands a leading "~" or "~/" to the user's home directory. The
// path is returned unchanged when it has no "~" prefix or the home directory
// cannot be resolved.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}
