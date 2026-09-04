//go:build !windows

package process

import "fmt"

func assignToJob(pid int, jobOut *uintptr) error { return nil }
func closeJob(job *uintptr)                       {}
func killTree(pid int) error {
	return fmt.Errorf("not implemented")
}
func killPortOrphan(port int) error { return nil }
