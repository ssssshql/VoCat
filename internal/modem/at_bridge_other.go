//go:build !linux

package modem

// bridgedATPath has no meaning outside Linux; PTY bridging is a Linux-only
// workaround for the bam-dmux AT port.
func bridgedATPath(string) string { return "" }
