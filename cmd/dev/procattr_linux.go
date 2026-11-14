package main

import (
	"os"
	"syscall"
)

// goir restarts by SIGKILLing this process, which can't be trapped; Pdeathsig
// makes the kernel kill the children too so the API port is freed.
func childProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// dieWithParent asks for SIGTERM when goir exits, so the launcher can't be orphaned.
func dieWithParent() {
	const prSetPdeathsig = 1
	ppid := os.Getppid()
	_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prSetPdeathsig, uintptr(syscall.SIGTERM), 0)
	if os.Getppid() != ppid {
		os.Exit(0)
	}
}
