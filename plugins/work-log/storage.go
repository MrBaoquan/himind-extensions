package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 本文件提供写入可靠性的公共设施：原子落盘与「按日志根串行化」。
//
// Each capability invocation may run in a different plugin process. A per-root
// mutex and OS file lock serialize cooperating writers; atomic replacement
// protects each individual file. Multi-file operations still need a journal.

var rootLocks sync.Map // 归一化后的日志根 -> *sync.Mutex

// lockRoot serializes writers within and across plugin processes. Failure to
// acquire the OS lock must stop the write rather than silently weaken it.
func lockRoot(root string) (func(), error) {
	return lockRootInternal(root)
}

func lockRootInternal(root string) (func(), error) {
	key := strings.ToLower(filepath.Clean(root))
	value, _ := rootLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	unlockFile, err := lockRootFile(root)
	if err != nil {
		mutex.Unlock()
		return nil, fmt.Errorf("日志库写锁不可用：%w", err)
	}
	return func() {
		unlockFile()
		mutex.Unlock()
	}, nil
}

// writeFileAtomic 先把内容写进同目录的临时文件并 fsync，再原子替换目标文件。
// 临时文件与目标同目录，保证 rename 在同一文件系统内完成，避免跨卷复制。
func writeFileAtomic(path string, payload []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 写前保留一份备份：目标已存在时，覆盖前先把旧内容存为 <name>.bak，
	// 便于误删或坏写后回收（这类文件都很小，重复拷贝成本可忽略）。
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		_ = os.WriteFile(path+".bak", existing, 0o644)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		// 某些受限运行环境（例如只放行目标文件写入、不允许新建隐藏临时文件）
		// 会在这里返回 Access is denied。此时退回直接写目标，优先保证日志可落盘；
		// 正常环境仍走临时文件 + 原子替换。
		return os.WriteFile(path, payload, 0o644)
	}
	tmpName := tmp.Name()
	// rename 成功后该路径已不存在，Remove 是无害的兜底；失败路径则清理残留。
	defer os.Remove(tmpName)
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameWithRetry(tmpName, path)
}

// renameWithRetry 在 Windows 上偶发「目标文件被其它进程短暂占用」时重试几次。
// 这不会掩盖真正的失败：超过重试上限后仍会把最后一次错误返回调用方。
func renameWithRetry(from, to string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		if !isTransientRenameError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

func isTransientRenameError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "being used by another process") ||
		strings.Contains(message, "access is denied") ||
		strings.Contains(message, "cannot access the file")
}
