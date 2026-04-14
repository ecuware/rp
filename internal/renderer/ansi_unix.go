//go:build !windows

package renderer

// enableANSI is a no-op on Unix; terminals support ANSI natively.
func enableANSI() {}
