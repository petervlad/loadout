//go:build !linux

package vminfo

// System is the kernel of this machine. Only Linux has the system calls the
// page needs, so a preview on another system says so on each item.
func System() Kernel { return unsupported{} }
