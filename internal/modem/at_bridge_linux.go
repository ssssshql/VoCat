//go:build linux

package modem

import (
	"os"
	"path/filepath"
	"strings"
)

// bridgedATPath returns the PTY a local bridge exposes for a native WWAN AT
// port, or "" when no bridge is running.
//
// The UFI003 / OpenStick 410 bam-dmux AT port only delivers a response to a
// read that is already blocked in the driver, so it cannot be used through the
// poll plus non-blocking model. A small helper process keeps the blocking read
// outstanding and re-exports the port as a PTY; when that PTY exists it is a
// drop-in replacement for every AT consumer.
func bridgedATPath(wwanPath string) string {
	base := filepath.Base(wwanPath)
	if !strings.HasPrefix(base, "wwan") {
		return ""
	}
	// /dev/wwan0at0 -> /dev/vocat-at (single-device bridge) and
	// /dev/vocat-<wwan>at<n> for multi-device setups.
	candidates := []string{
		"/dev/vocat-" + base,
		"/dev/vocat-at",
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return candidate
		}
	}
	return ""
}
