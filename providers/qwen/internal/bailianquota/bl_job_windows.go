package bailianquota

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

var (
	blKernel32      = syscall.NewLazyDLL("kernel32.dll")
	blCreateJob     = blKernel32.NewProc("CreateJobObjectW")
	blSetJob        = blKernel32.NewProc("SetInformationJobObject")
	blAssignJob     = blKernel32.NewProc("AssignProcessToJobObject")
	blTerminateJob  = blKernel32.NewProc("TerminateJobObject")
	blQueryJob      = blKernel32.NewProc("QueryInformationJobObject")
	blCompletion    = blKernel32.NewProc("GetQueuedCompletionStatus")
	blThreadFirst   = blKernel32.NewProc("Thread32First")
	blThreadNext    = blKernel32.NewProc("Thread32Next")
	blOpenThread    = blKernel32.NewProc("OpenThread")
	blResumeThread  = blKernel32.NewProc("ResumeThread")
	blThreadProcess = blKernel32.NewProc("GetProcessIdOfThread")
)

// The handles are unnamed and non-inheritable: neither concurrent invocations
// nor the child can hold this job open. Closing it also kills surviving members
// if the helper itself is externally terminated.
type blJob struct {
	handle    syscall.Handle
	port      syscall.Handle
	deadline  time.Time
	processes map[uint32]syscall.Handle
}

func newBLJob() (*blJob, error) {
	handle, _, err := blCreateJob.Call(0, 0)
	if handle == 0 {
		return nil, err
	}
	job := &blJob{handle: syscall.Handle(handle), processes: make(map[uint32]syscall.Handle)}
	fail := func(err error) (*blJob, error) { return nil, errors.Join(err, job.close()) }
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION is 144 bytes on Win64, 112 on
	// Win32; its embedded LimitFlags is always at offset 16. All other limits
	// stay zero. A byte buffer avoids Go/Win32 uint64 alignment differences.
	var limits [144]byte
	*(*uint32)(unsafe.Pointer(&limits[16])) = 0x00002000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	size := uintptr(len(limits))
	if unsafe.Sizeof(uintptr(0)) == 4 {
		size = 112
	}
	if ok, _, err := blSetJob.Call(handle, 9, uintptr(unsafe.Pointer(&limits[0])), size); ok == 0 {
		return fail(err)
	}
	port, err := syscall.CreateIoCompletionPort(syscall.InvalidHandle, 0, 0, 1)
	if err != nil {
		return fail(err)
	}
	job.port = port
	association := struct {
		key  uintptr
		port syscall.Handle
	}{port: port}
	if ok, _, err := blSetJob.Call(handle, 7, uintptr(unsafe.Pointer(&association)), unsafe.Sizeof(association)); ok == 0 {
		return fail(err)
	}
	return job, nil
}

func (job *blJob) close() error {
	err := syscall.CloseHandle(job.handle)
	if job.port != 0 {
		err = errors.Join(err, syscall.CloseHandle(job.port))
	}
	for _, process := range job.processes {
		if process != 0 {
			err = errors.Join(err, syscall.CloseHandle(process))
		}
	}
	return err
}

func (job *blJob) assign(pid uint32) error {
	process, err := syscall.OpenProcess(0x0100|syscall.PROCESS_TERMINATE, false, pid) // PROCESS_SET_QUOTA
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	if ok, _, err := blAssignJob.Call(uintptr(job.handle), uintptr(process)); ok == 0 {
		return err
	}
	return job.capture(pid)
}

func (job *blJob) terminate() error {
	if job.deadline.IsZero() {
		job.deadline = time.Now().Add(time.Second) // original cleanup budget
	}
	// Accounting can reach zero before a terminating process becomes signaled
	// and releases its executable mapping. Pin current members before requesting
	// termination, then wait on their actual process handles as well.
	captureErr := job.captureMembers()
	if ok, _, err := blTerminateJob.Call(uintptr(job.handle), 1); ok == 0 {
		return errors.Join(captureErr, err)
	}
	return captureErr
}

func (job *blJob) capture(pid uint32) error {
	if _, exists := job.processes[pid]; exists {
		return nil
	}
	process, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, pid)
	if err == syscall.Errno(87) { // ERROR_INVALID_PARAMETER: already exited
		job.processes[pid] = 0
		return nil
	}
	if err != nil {
		return err
	}
	job.processes[pid] = process
	return nil
}

func (job *blJob) captureMembers() error {
	for count := 16; count <= 65536; count *= 2 {
		// JOBOBJECT_BASIC_PROCESS_ID_LIST: two DWORD counts, then ULONG_PTR IDs.
		buffer := make([]uintptr, count+2)
		size := uintptr(len(buffer)) * unsafe.Sizeof(buffer[0])
		ok, _, err := blQueryJob.Call(uintptr(job.handle), 3, uintptr(unsafe.Pointer(&buffer[0])), size, 0)
		if ok == 0 {
			if err == syscall.ERROR_MORE_DATA && time.Now().Before(job.deadline) {
				continue
			}
			return err
		}
		listed := *(*uint32)(unsafe.Add(unsafe.Pointer(&buffer[0]), 4))
		if uintptr(listed) > (size-8)/unsafe.Sizeof(buffer[0]) {
			return errors.New("invalid bl process tree member count")
		}
		ids := unsafe.Slice((*uintptr)(unsafe.Add(unsafe.Pointer(&buffer[0]), 8)), int(listed))
		var captureErr error
		for _, pid := range ids {
			captureErr = errors.Join(captureErr, job.capture(uint32(pid)))
		}
		return captureErr
	}
	return errors.New("bl process tree exceeds cleanup tracking limit")
}

func (job *blJob) notification(milliseconds uint32) error {
	var message uint32
	var key, value uintptr
	ok, _, err := blCompletion.Call(uintptr(job.port), uintptr(unsafe.Pointer(&message)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value)), uintptr(milliseconds))
	if ok == 0 {
		return err
	}
	if message == 6 { // JOB_OBJECT_MSG_NEW_PROCESS: include last-moment children
		return job.capture(uint32(value))
	}
	return nil
}

func (job *blJob) reap() error {
	for {
		var accounting struct {
			times                                 [4]int64
			pageFaults, total, active, terminated uint32
		}
		if ok, _, err := blQueryJob.Call(uintptr(job.handle), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0); ok == 0 {
			return err
		}
		if accounting.active == 0 {
			// Drain new-process notifications too: a child may have been born
			// between the member snapshot and TerminateJobObject.
			for {
				err := job.notification(0)
				if err == syscall.Errno(syscall.WAIT_TIMEOUT) {
					break
				}
				if err != nil {
					return err
				}
			}
			// Notifications are best effort. Do not certify reaping if a
			// short-lived/racing member was missed: wait within the same budget
			// for its notification, or fail closed on the accounting mismatch.
			if uint64(accounting.total) == uint64(len(job.processes)) {
				for _, process := range job.processes {
					if process == 0 {
						continue // observed PID already ceased to exist
					}
					remaining := max(time.Duration(0), time.Until(job.deadline))
					state, err := syscall.WaitForSingleObject(process, uint32((remaining+time.Millisecond-1)/time.Millisecond))
					if err != nil {
						return err
					}
					if state != syscall.WAIT_OBJECT_0 {
						return errors.New("bl process did not exit within cleanup deadline")
					}
				}
				return nil
			}
		}
		remaining := time.Until(job.deadline)
		if remaining <= 0 {
			return errors.New("bl process tree did not exit within cleanup deadline")
		}
		// Wait for real job notifications, not a sleep that can hide an orphan.
		// Always requery accounting, including after a lost notification/timeout.
		if err := job.notification(uint32((remaining + time.Millisecond - 1) / time.Millisecond)); err != nil && err != syscall.Errno(syscall.WAIT_TIMEOUT) {
			return err
		}
	}
}

// Go closes CreateProcess's primary thread handle inside Cmd.Start. Since the
// process is still suspended, require exactly one owned thread and reopen that
// thread only. Ambiguity fails closed instead of resuming arbitrary code.
func resumeBLProcess(pid uint32) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(0x00000004, 0) // TH32CS_SNAPTHREAD
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)
	var entry struct {
		size, usage, id, owner      uint32
		basePriority, deltaPriority int32
		flags                       uint32
	}
	entry.size = uint32(unsafe.Sizeof(entry))
	var threadID uint32
	for proc := blThreadFirst; ; proc = blThreadNext {
		ok, _, err := proc.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
		if ok == 0 {
			if err != syscall.ERROR_NO_MORE_FILES {
				return err
			}
			break
		}
		if entry.owner == pid {
			if threadID != 0 {
				return errors.New("ambiguous suspended bl process threads")
			}
			threadID = entry.id
		}
		entry.size = uint32(unsafe.Sizeof(entry))
	}
	if threadID == 0 {
		return errors.New("missing suspended bl process thread")
	}
	thread, _, err := blOpenThread.Call(0x0002|0x0800, 0, uintptr(threadID)) // SUSPEND_RESUME | QUERY_LIMITED_INFORMATION
	if thread == 0 {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(thread))
	owner, _, err := blThreadProcess.Call(thread)
	if owner == 0 {
		return err
	}
	if uint32(owner) != pid {
		return errors.New("suspended bl process thread identity changed")
	}
	previous, _, err := blResumeThread.Call(thread)
	if uint32(previous) == 0xffffffff {
		return err
	}
	if previous != 1 {
		return errors.New("unexpected bl process suspension count")
	}
	return nil
}
