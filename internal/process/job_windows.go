//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modKernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = modKernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modKernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modKernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess              = modKernel32.NewProc("OpenProcess")
	procCloseHandle              = modKernel32.NewProc("CloseHandle")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInfo struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func assignToJob(pid int, jobOut *uintptr) error {
	hJob, _, err := procCreateJobObjectW.Call(0, 0)
	if hJob == 0 {
		return fmt.Errorf("CreateJobObjectW: %v", err)
	}

	var info jobObjectExtendedLimitInfo
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r1, _, err2 := procSetInformationJobObject.Call(
		hJob,
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r1 == 0 {
		procCloseHandle.Call(hJob)
		return fmt.Errorf("SetInformationJobObject: %v", err2)
	}

	access := uintptr(processSetQuota | processTerminate)
	hProc, _, err3 := procOpenProcess.Call(access, 0, uintptr(pid))
	if hProc == 0 {
		procCloseHandle.Call(hJob)
		return fmt.Errorf("OpenProcess: %v", err3)
	}
	defer procCloseHandle.Call(hProc)

	r2, _, err4 := procAssignProcessToJobObject.Call(hJob, hProc)
	if r2 == 0 {
		procCloseHandle.Call(hJob)
		return fmt.Errorf("AssignProcessToJobObject: %v", err4)
	}

	*jobOut = hJob
	return nil
}

func closeJob(job *uintptr) {
	if job == nil || *job == 0 {
		return
	}
	procCloseHandle.Call(*job)
	*job = 0
}

func killTree(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

// findPortListenerPid returns the PID listening on port, or 0.
func findPortListenerPid(port int) (int, error) {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return 0, err
	}
	suffix := fmt.Sprintf(":%d", port)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.EqualFold(f[len(f)-2], "LISTENING") {
			continue
		}
		if strings.HasSuffix(f[1], suffix) {
			pid, err := strconv.Atoi(f[len(f)-1])
			if err != nil {
				continue
			}
			return pid, nil
		}
	}
	return 0, nil
}

// killPortOrphan force-kills the node.exe listening on port. The dsh web this
// app manages can outlive the app process after a crash; without this, tray
// 停止/退出 would leave the service running.
func killPortOrphan(port int) error {
	pid, err := findPortListenerPid(port)
	if err != nil || pid == 0 {
		return err
	}
	img, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err == nil && !strings.Contains(strings.ToLower(string(img)), "node.exe") {
		// 端口被非 node 程序占用时不动它，只报告。
		return fmt.Errorf("端口 %d 被非 node 进程 (pid %d) 占用，跳过清理", port, pid)
	}
	return killTree(pid)
}
