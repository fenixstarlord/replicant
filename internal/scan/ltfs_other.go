//go:build !darwin

package scan

// On Linux LTFS publishes its attributes in the user namespace.
const ltfsAttrPrefix = "user."
