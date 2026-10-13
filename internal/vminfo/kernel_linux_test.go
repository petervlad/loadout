//go:build linux

package vminfo

import "testing"

func TestLinuxKernel(t *testing.T) {
	k := System()
	si, err := k.Sysinfo()
	if err != nil {
		t.Fatal(err)
	}
	if si.TotalRAM == 0 || si.Uptime <= 0 {
		t.Errorf("Sysinfo = %+v", si)
	}
	u, err := k.Uname()
	if err != nil {
		t.Fatal(err)
	}
	if u.Sysname != "Linux" || u.Release == "" || u.Machine == "" {
		t.Errorf("Uname = %+v", u)
	}
}
