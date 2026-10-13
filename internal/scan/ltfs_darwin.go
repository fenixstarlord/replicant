//go:build darwin

package scan

// On macOS LTFS publishes its attributes without a namespace prefix.
const ltfsAttrPrefix = ""
