//go:build linux

package vminfo

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// loadScale is how sysinfo(2) scales the load averages (SI_LOAD_SHIFT is 16).
const loadScale = 1 << 16

type linuxKernel struct{}

// System is the real kernel of this machine.
func System() Kernel { return linuxKernel{} }

func (linuxKernel) Sysinfo() (Sysinfo, error) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return Sysinfo{}, fmt.Errorf("sysinfo: %w", err)
	}
	unit := uint64(si.Unit)
	if unit == 0 {
		unit = 1
	}
	out := Sysinfo{
		Uptime:    time.Duration(si.Uptime) * time.Second,
		TotalRAM:  uint64(si.Totalram) * unit,  //nolint:unconvert // uint32 on 32-bit Linux
		TotalSwap: uint64(si.Totalswap) * unit, //nolint:unconvert // uint32 on 32-bit Linux
		FreeSwap:  uint64(si.Freeswap) * unit,  //nolint:unconvert // uint32 on 32-bit Linux
	}
	for i, l := range si.Loads {
		out.Load[i] = float64(l) / loadScale
	}
	return out, nil
}

func (linuxKernel) Uname() (Uname, error) {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return Uname{}, fmt.Errorf("uname: %w", err)
	}
	return Uname{
		Sysname: unix.ByteSliceToString(u.Sysname[:]),
		Release: unix.ByteSliceToString(u.Release[:]),
		Machine: unix.ByteSliceToString(u.Machine[:]),
	}, nil
}
