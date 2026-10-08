//go:build !darwin

package scan

// fillVolumeDetails is a no-op outside macOS; only statfs data is available.
func fillVolumeDetails(*Volume) {}
