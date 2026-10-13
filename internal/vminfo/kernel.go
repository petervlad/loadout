package vminfo

import (
	"errors"
	"strings"
	"time"
)

// errUnsupported is what the Kernel of a system without sysinfo(2) says.
var errUnsupported = errors.New("not available on this system")

// Sysinfo is what sysinfo(2) tells, with memory in bytes and the load
// already divided into the usual decimal form.
type Sysinfo struct {
	Uptime    time.Duration
	Load      [3]float64
	TotalRAM  uint64
	TotalSwap uint64
	FreeSwap  uint64
}

// Uname is what uname(2) tells.
type Uname struct {
	Sysname, Release, Machine string
}

// String is the form people know, "6.8.0-45-generic x86_64".
func (u Uname) String() string {
	return strings.TrimSpace(u.Release + " " + u.Machine)
}

// Kernel is what vminfo asks the kernel directly. The service's sandbox hides
// most of /proc, but these two system calls still work. Tests use a fake.
type Kernel interface {
	Sysinfo() (Sysinfo, error)
	Uname() (Uname, error)
}

// unsupported is the Kernel used when the caller gives none.
type unsupported struct{}

func (unsupported) Sysinfo() (Sysinfo, error) { return Sysinfo{}, errUnsupported }
func (unsupported) Uname() (Uname, error)     { return Uname{}, errUnsupported }
