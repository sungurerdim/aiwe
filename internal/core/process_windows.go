package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

const (
	createSuspended          = 0x00000004 // CREATE_SUSPENDED
	processSetQuota          = 0x0100     // PROCESS_SET_QUOTA
	processQueryLimitedInfo  = 0x1000     // PROCESS_QUERY_LIMITED_INFORMATION
	threadSuspendResume      = 0x0002     // THREAD_SUSPEND_RESUME
	jobBasicAccountingInfo   = 1          // JobObjectBasicAccountingInformation
	jobExtendedLimitInfo     = 9          // JobObjectExtendedLimitInformation
	jobLimitKillOnJobClose   = 0x00002000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	jobStoppedExitCode       = 1          // exit code of every process of a stopped job
	resumeThreadFailed       = 0xFFFFFFFF // (DWORD)-1 returned by ResumeThread
	systemDirectoryMaxLength = syscall.MAX_PATH
)

// kernel32 holds the kernel32 procedures that the standard library does not wrap. It is loaded
// per tree, so the package keeps no state.
type kernel32 struct {
	createJobObject           *syscall.LazyProc
	assignProcessToJobObject  *syscall.LazyProc
	isProcessInJob            *syscall.LazyProc
	terminateJobObject        *syscall.LazyProc
	queryInformationJobObject *syscall.LazyProc
	setInformationJobObject   *syscall.LazyProc
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
		isProcessInJob:            dll.NewProc("IsProcessInJob"),
		terminateJobObject:        dll.NewProc("TerminateJobObject"),
		queryInformationJobObject: dll.NewProc("QueryInformationJobObject"),
		setInformationJobObject:   dll.NewProc("SetInformationJobObject"),
		thread32First:             dll.NewProc("Thread32First"),
		thread32Next:              dll.NewProc("Thread32Next"),
		openThread:                dll.NewProc("OpenThread"),
		resumeThread:              dll.NewProc("ResumeThread"),
		getSystemDirectory:        dll.NewProc("GetSystemDirectoryW"),
	}
	// Find each procedure now, because Call on a missing one panics.
	for _, p := range []*syscall.LazyProc{
		k.createJobObject, k.assignProcessToJobObject, k.isProcessInJob, k.terminateJobObject,
		k.queryInformationJobObject, k.setInformationJobObject, k.thread32First, k.thread32Next, k.openThread,
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

// jobExtendedLimit is JOBOBJECT_EXTENDED_LIMIT_INFORMATION; only LimitFlags is set. C aligns its
// 64-bit fields to 8 bytes on every architecture, which Go does not on 32-bit Windows, so the
// padding C adds after JOBOBJECT_BASIC_LIMIT_INFORMATION there is written out. A wrong size makes
// SetInformationJobObject fail, so Start fails instead of running a tree without the limit.
type jobExtendedLimit struct {
	_          [2]int64                            // PerProcessUserTimeLimit, PerJobUserTimeLimit
	limitFlags uint32                              // LimitFlags
	_          [2]uintptr                          // MinimumWorkingSetSize, MaximumWorkingSetSize
	_          uint32                              // ActiveProcessLimit
	_          uintptr                             // Affinity
	_          [2]uint32                           // PriorityClass, SchedulingClass
	_          [8 - unsafe.Sizeof(uintptr(0))]byte // tail padding of the basic limits on 32-bit Windows
	_          [6]uint64                           // IoInfo (IO_COUNTERS)
	_          [4]uintptr                          // ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed
}

// tree is the Job Object that holds the child and every process it starts.
type tree struct {
	k   kernel32
	job syscall.Handle // 0 once released
	// steps records each start step that took effect, in order (U22): create_suspended when the
	// tree is made, assign_job once the job answers that it holds the child, resume once a thread
	// of the child is resumed. The call that sees an effect records it, so a resume that runs before
	// the job holds the child is refused, not recorded in the wrong place.
	steps []string
}

// newTree creates the job and sets the child to start suspended, so it joins the job before it
// can start a process of its own (U22). Closing the last handle of the job kills every process
// left in it, so a tree never outlives the engine (DR-15). A batch launcher runs through cmd.exe
// with a command line built here (SPEC §5.20).
func newTree(cmd *exec.Cmd) (tree, error) {
	k, err := loadKernel32()
	if err != nil {
		return tree{}, err
	}
	attr := &syscall.SysProcAttr{CreationFlags: createSuspended}
	if err := k.prepareLauncher(cmd, attr); err != nil {
		return tree{}, err
	}
	cmd.SysProcAttr = attr
	job, _, callErr := k.createJobObject.Call(0, 0)
	if job == 0 {
		return tree{}, fmt.Errorf("create job object: %w", callErr)
	}
	t := tree{k: k, job: syscall.Handle(job), steps: []string{"create_suspended"}}
	limit := jobExtendedLimit{limitFlags: jobLimitKillOnJobClose}
	ok, _, callErr := k.setInformationJobObject.Call(job, jobExtendedLimitInfo,
		uintptr(unsafe.Pointer(&limit)), unsafe.Sizeof(limit))
	if ok == 0 {
		t.release()
		return tree{}, fmt.Errorf("set the job to kill its processes on close: %w", callErr)
	}
	return t, nil
}

// prepareLauncher resolves the program of cmd once, to the absolute path of the file Start runs,
// so the batch decision is made on that file and not on the name as given: Go adds a PATHEXT
// extension, so "solo" may run solo.cmd through cmd.exe. Start finds an absolute path with a
// PATHEXT extension as given, so it runs that file and joins no Dir to it again. A relative Dir, a
// name that resolves to no file and a file outside PATHEXT are refused. A batch file is set to run
// through cmd.exe with a command line built here (SPEC §5.20).
func (k kernel32) prepareLauncher(cmd *exec.Cmd, attr *syscall.SysProcAttr) error {
	path, err := resolveLauncher(cmd.Path, cmd.Dir)
	if err != nil {
		return err
	}
	cmd.Path = path // Start runs the file the decision below is made on
	if !isBatchLauncher(path) {
		return nil
	}
	shell, err := k.commandShell()
	if err != nil {
		return err
	}
	line, err := batchCommandLine(shell, path, cmd.Args[1:])
	if err != nil {
		return err
	}
	cmd.Path = shell
	attr.CmdLine = line
	return nil
}

// attach assigns the suspended child to the job, then resumes it once the job reports that it
// holds the child.
func (t *tree) attach(p *os.Process) error {
	pid, err := windowsPID(p.Pid)
	if err != nil {
		return err
	}
	h, err := syscall.OpenProcess(processSetQuota|processQueryLimitedInfo|syscall.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = syscall.CloseHandle(h) }() // the job holds the process; h only checks and ends it
	ok, _, callErr := t.k.assignProcessToJobObject.Call(uintptr(t.job), uintptr(h))
	if ok == 0 {
		return fmt.Errorf("assign process %d to its job: %w", pid, callErr)
	}
	return t.resumeProcess(h, pid)
}

// stop terminates the job and waits for it to be empty. Windows has no ask a console-less child
// is bound to obey, so the grace is not used (SPEC §5.20: StopTree terminates the job).
func (t *tree) stop(_ time.Duration) error {
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

// release closes the job handle once the tree is stopped or empty, or the child did not start.
// The job kills on close what it still holds, so a handle closed too early kills, never leaks.
func (t *tree) release() {
	if t.job != 0 {
		_ = syscall.CloseHandle(t.job) // nothing is left to stop through it
		t.job = 0
	}
}

// resumeProcess resumes every thread of a process started suspended, after the job has answered
// that it holds the process (h is a handle to it). The check is here, ahead of the first thread to
// run, so no call can resume the process without it (U22).
func (t *tree) resumeProcess(h syscall.Handle, pid uint32) error {
	if err := t.requireInJob(h, pid); err != nil {
		return err
	}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot threads: %w", err)
	}
	defer func() { _ = syscall.CloseHandle(snap) }() // a read-only snapshot
	entry := threadEntry{size: uint32(unsafe.Sizeof(threadEntry{}))}
	resumed := 0
	more, _, callErr := t.k.thread32First.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	for ; more != 0; more, _, callErr = t.k.thread32Next.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry))) {
		if entry.ownerProcessID != pid {
			continue
		}
		if err := t.k.resumeThreadByID(entry.threadID); err != nil {
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
	t.steps = append(t.steps, "resume")
	return nil
}

// requireInJob asks the job whether it holds the suspended process of h, and records assign_job
// only from a true answer. When the question fails or the answer is false, it terminates the
// process, which has run nothing yet, and returns an error: the process is never resumed outside
// the tree (U22).
func (t *tree) requireInJob(h syscall.Handle, pid uint32) error {
	var in int32 // a BOOL: IsProcessInJob writes four bytes
	ok, _, callErr := t.k.isProcessInJob.Call(uintptr(h), uintptr(t.job), uintptr(unsafe.Pointer(&in)))
	if ok != 0 && in != 0 {
		t.steps = append(t.steps, "assign_job")
		return nil
	}
	cause := fmt.Errorf("process %d is not in its job", pid)
	if ok == 0 {
		cause = fmt.Errorf("ask the job whether it holds process %d: %w", pid, callErr)
	}
	if err := syscall.TerminateProcess(h, jobStoppedExitCode); err != nil {
		return errors.Join(cause, fmt.Errorf("terminate process %d: %w", pid, err))
	}
	return cause
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
