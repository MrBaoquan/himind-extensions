package main

import (
	"archive/zip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRootLockProcessHelper(t *testing.T) {
	root := os.Getenv("WORK_LOG_TEST_LOCK_ROOT")
	if root == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(root, "ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.WriteFile(filepath.Join(root, "acquired"), []byte("acquired"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRootLockSerializesSeparateProcesses(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRootLockProcessHelper$")
	cmd.Env = append(os.Environ(), "WORK_LOG_TEST_LOCK_ROOT="+root)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := filepath.Join(root, "ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not reach lock attempt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "acquired")); !os.IsNotExist(err) {
		t.Fatal("second process acquired a held lock")
	}
	unlock()
	locked = false
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child could not acquire released lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "acquired")); err != nil {
		t.Fatal("child never acquired released lock")
	}
}

func TestRootLockFailureDoesNotLeaveMutexHeld(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := lockRoot(root); err == nil {
		t.Fatal("missing directory should fail lock acquisition")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestConcurrentWriteProcessHelper(t *testing.T) {
	root := os.Getenv("WORK_LOG_TEST_WRITE_ROOT")
	id := os.Getenv("WORK_LOG_TEST_WRITE_ID")
	if root == "" || id == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(root, "ready-"+id), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "start")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer start signal missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := upsert(input{LogRoot: root, Date: "2026-11-19", Entry: &entry{Source: "codex", SessionID: id, Title: id}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentProcessesPreserveBothLogEntries(t *testing.T) {
	withDataRoot(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 0, 2)
	defer func() {
		for _, cmd := range commands {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}()
	for _, id := range []string{"a", "b"} {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentWriteProcessHelper$")
		cmd.Env = append(os.Environ(), "WORK_LOG_TEST_WRITE_ROOT="+root, "WORK_LOG_TEST_WRITE_ID="+id)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, errA := os.Stat(filepath.Join(root, "ready-a"))
		_, errB := os.Stat(filepath.Join(root, "ready-b"))
		if errA == nil && errB == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writers did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(root, "start"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer failed: %v", err)
		}
	}
	day, err := readDay(root, "2026-11-19")
	if err != nil || len(day.Entries) != 2 {
		t.Fatalf("concurrent writes lost entries: %+v, %v", day.Entries, err)
	}
}

func TestBackupRejectsUnexpectedBusinessDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(workspaceBusinessPath(root), 0700); err != nil {
		t.Fatal(err)
	}
	if path, _, _, err := writeBackupArchive(root); err == nil || path != "" {
		t.Fatal("backup reported success without the project registry")
	}
}

func TestBackupIncludesWorkspaceBusinessState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(workspaceBusinessPath(root), []byte(`{"schema_version":"work-log-workspace-business.v1","projects":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	path, _, _, err := writeBackupArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == "workspace-business.json" {
			if !isRestorablePath(file.Name) {
				t.Fatal("business state is not restorable")
			}
			return
		}
	}
	t.Fatal("backup omitted workspace business state")
}
