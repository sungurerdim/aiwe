package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	createSuspended          = 0x00000004 // CREATE_SUSPENDED
	processSetQuota          = 0x0100     // PROCESS_SET_QUOTA
	threadSuspendResume      = 0x0002     // THREAD_SUSPEND_RESUME
	jobBasicAccountingInfo   = 1          // JobObjectBasicAccountingInformation
	jobStoppedExitCode       = 1          // exit code of every process of a stopped job
	resumeThreadFailed       = 0xFFFFFFFF // (DWORD)-1 returned by ResumeThread
	systemDirectoryMaxLength = syscall.MAX_PATH
)

// kernel32 holds the kernel32 procedures that the standard library does not wrap. It is loaded
// per tree, so the package keeps no state.
type kernel32 struct {
	createJobObject           *syscall.LazyProc
	assignProcessToJobObject  *syscall.LazyProc
	terminateJobObject        *syscall.LazyProc
	queryInformationJobObject *syscall.LazyProc
	thread32First             *syscall.LazyProc
	thread32Next              *syscall.LazyProc
	openThread                *syscall.LazyProc
	resumeThread              *syscall.LazyProc
	getSystemDirectory        *syscall.LazyProc
}

func loadKernel32() (kernel32, error) {
	dll := syscall.NewLazyDLL("kernel32.dll")
	k := kernel32{
		createJobObject:           dll.NewProc("CreateJobObjectW"),
		assignProcessToJobObject:  dll.NewProc("AssignProcessToJobObject"),
		terminateJobObject:        dll.NewProc("TerminateJobObject"),
		queryInformationJobObject: dll.NewProc("QueryInformationJobObject"),
		thread32First:             dll.NewProc("Thread32First"),
		thread32Next:              dll.NewProc("Thread32Next"),
		openThread:                dll.NewProc("OpenThread"),
		resumeThread:              dll.NewProc("ResumeThread"),
		getSystemDirectory:        dll.NewProc("GetSystemDirectoryW"),
	}
	// Find each procedure now, because Call on a missing one panics.
	for _, p := range []*syscall.LazyProc{
		k.createJobObject, k.assignProcessToJobObject, k.terminateJobObject,
		k.queryInformationJobObject, k.thread32First, k.thread32Next, k.openThread,
		k.resumeThread, k.getSystemDirectory,
	} {
		if err := p.Find(); err != nil {
			return kernel32{}, fmt.Errorf("load kernel32 %s: %w", p.Name, err)
		}
	}
	return k, nil
}

// threadEntry is THREADENTRY32.
type threadEntry struct {
	size           uint32
	usage          uint32
	threadID       uint32
	ownerProcessID uint32
	basePriority   int32
	deltaPriority  int32
	flags          uint32
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION; its layout is the same on every
// Windows architecture.
type jobAccounting struct {
	totalUserTime             int64
	totalKernelTime           int64
	thisPeriodTotalUserTime   int64
	thisPeriodTotalKernelTime int64
	totalPageFaultCount       uint32
	totalProcesses            uint32
	activeProcesses           uint32
	totalTerminatedProcesses  uint32
}

// tree is the Job Object that holds the child and every process it starts.
type tree struct {
	k   kernel32
	job syscall.Handle // 0 once released
}

// newTree creates the job and sets the child to start suspended, so it joins the job before it
// can start a process of its own (U22). A batch launcher runs through cmd.exe with a command line
// built here (SPEC §5.20).
func newTree(cmd *exec.Cmd) (tree, error) {
	k, err := loadKernel32()
	if err != nil {
		return tree{}, err
	}
	attr := &syscall.SysProcAttr{CreationFlags: createSuspended}
	if isBatchLauncher(cmd.Path) {
		shell, err := k.commandShell()
		if err != nil {
			return tree{}, err
		}
		line, err := batchCommandLine(shell, cmd.Path, cmd.Args[1:])
		if err != nil {
			return tree{}, err
		}
		cmd.Path = shell
		attr.CmdLine = line
	}
	cmd.SysProcAttr = attr
	job, _, callErr := k.createJobObject.Call(0, 0)
	if job == 0 {
		return tree{}, fmt.Errorf("create job object: %w", callErr)
	}
	return tree{k: k, job: syscall.Handle(job)}, nil
}

// attach assigns the suspended child to the job, then resumes it.
func (t *tree) attach(p *os.Process) error {
	pid := uint32(p.Pid) // a Windows process id is a DWORD
	h, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	ok, _, callErr := t.k.assignProcessToJobObject.Call(uintptr(t.job), uintptr(h))
	_ = syscall.CloseHandle(h) // the job holds the process from here on
	if ok == 0 {
		return fmt.Errorf("assign process %d to its job: %w", pid, callErr)
	}
	return t.k.resumeProcess(pid)
}

// stop terminates the job and waits for it to be empty. Windows has no ask a console-less child
// is bound to obey, so the grace is not used (SPEC §5.20: StopTree terminates the job).
func (t *tree) stop(_ time.Duration) error {
	if t.job == 0 {
		return nil
	}
	ok, _, callErr := t.k.terminateJobObject.Call(uintptr(t.job), jobStoppedExitCode)
	if ok == 0 {
		return fmt.Errorf("terminate job: %w", callErr)
	}
	gone, err := waitGone(killBound, t.empty)
	if err != nil {
		return err
	}
	if !gone {
		return fmt.Errorf("job still runs a process %v after it was terminated", killBound)
	}
	return nil
}

// empty reports whether no process of the job is left.
func (t *tree) empty() (bool, error) {
	var info jobAccounting
	ok, _, callErr := t.k.queryInformationJobObject.Call(uintptr(t.job), jobBasicAccountingInfo,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
	if ok == 0 {
		return false, fmt.Errorf("query job: %w", callErr)
	}
	return info.activeProcesses == 0, nil
}

// release closes the job handle once the child has exited.
func (t *tree) release() {
	if t.job != 0 {
		_ = syscall.CloseHandle(t.job) // nothing is left to stop through it
		t.job = 0
	}
}

// resumeProcess resumes every thread of a process started suspended.
func (k kernel32) resumeProcess(pid uint32) error {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot threads: %w", err)
	}
	defer func() { _ = syscall.CloseHandle(snap) }() // a read-only snapshot
	entry := threadEntry{size: uint32(unsafe.Sizeof(threadEntry{}))}
	resumed := 0
	more, _, callErr := k.thread32First.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	for ; more != 0; more, _, callErr = k.thread32Next.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry))) {
		if entry.ownerProcessID != pid {
			continue
		}
		if err := k.resumeThreadByID(entry.threadID); err != nil {
			return err
		}
		resumed++
	}
	if !errors.Is(callErr, syscall.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("list threads of process %d: %w", pid, callErr)
	}
	if resumed == 0 {
		return fmt.Errorf("process %d has no thread to resume", pid)
	}
	return nil
}

func (k kernel32) resumeThreadByID(tid uint32) error {
	h, _, callErr := k.openThread.Call(threadSuspendResume, 0, uintptr(tid))
	if h == 0 {
		return fmt.Errorf("open thread %d: %w", tid, callErr)
	}
	defer func() { _ = syscall.CloseHandle(syscall.Handle(h)) }() // the thread runs on without it
	if r, _, callErr := k.resumeThread.Call(h); r == resumeThreadFailed {
		return fmt.Errorf("resume thread %d: %w", tid, callErr)
	}
	return nil
}

// commandShell returns the path of cmd.exe in the system directory, never one found on PATH.
func (k kernel32) commandShell() (string, error) {
	var buf [systemDirectoryMaxLength]uint16
	n, _, callErr := k.getSystemDirectory.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || n >= uintptr(len(buf)) {
		return "", fmt.Errorf("find the system directory: %w", callErr)
	}
	return filepath.Join(syscall.UTF16ToString(buf[:n]), "cmd.exe"), nil
}
