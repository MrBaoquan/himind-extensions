//go:build !windows

package job

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func spawnWorker(jobDir string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("定位插件可执行文件失败: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(jobDir, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("创建 worker 日志失败: %w", err)
	}
	defer logFile.Close()
	command := exec.Command(executable, "--media-toolkit-job-worker", jobDir)
	command.Env = append(os.Environ(), "HIMIND_MEDIA_JOB_WORKER=1")
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("启动后台 worker 失败: %w", err)
	}
	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		return pid, fmt.Errorf("释放 worker 句柄失败: %w", err)
	}
	return pid, nil
}
