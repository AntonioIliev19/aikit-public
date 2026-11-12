//go:build windows

package packages

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"unsafe"
)

func runContained(ctx context.Context, cmd *exec.Cmd) error {
	return runContainedBeforeAttach(ctx, cmd, nil)
}
func resumePrimary(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = windows.Thread32First(snapshot, &entry)
	for err == nil {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			previous, resumeErr := windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			if resumeErr != nil {
				return resumeErr
			}
			if previous != 1 {
				return errors.New("unexpected suspended Git thread state")
			}
			return nil
		}
		err = windows.Thread32Next(snapshot, &entry)
	}
	return errors.New("suspended Git primary thread unavailable")
}
func runContainedBeforeAttach(ctx context.Context, cmd *exec.Cmd, beforeAttach func()) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return err
	}
	ready := make(chan struct{})
	// The primary thread cannot create descendants before membership in the job.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error { <-ready; return windows.TerminateJobObject(job, 1) }
	if err = cmd.Start(); err != nil {
		close(ready)
		return err
	}
	if beforeAttach != nil {
		beforeAttach()
	}
	process, openErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if openErr == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	} else {
		err = openErr
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = resumePrimary(uint32(cmd.Process.Pid))
	}
	close(ready)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	return cmd.Wait()
}
