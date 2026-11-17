package job

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Capability processes exit at EOF. A controlled worker owns job state;
// other invocations only read it or create a cancellation marker.
func (s *Store) PrepareBackground(kind, input, language, model string, request any) (*Record, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	logPath, err := s.logPath(id)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, id)
	if err = os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "input.json"), payload, 0o600); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	record := &Record{ID: id, Kind: kind, Input: input, Language: language, Model: model, Background: true, Status: StatusQueued, Stage: StatusQueued, LogPath: logPath, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	s.jobs[id] = record
	s.mu.Unlock()
	s.persist(record)
	return s.Get(id)
}

func (s *Store) BackgroundInput(id string, target any) error {
	if !safeID(id) {
		return fmt.Errorf("作业ID无效")
	}
	payload, err := os.ReadFile(filepath.Join(s.root, id, "input.json"))
	if err != nil {
		return err
	}
	if len(payload) > 64*1024 {
		return fmt.Errorf("作业输入过大")
	}
	return json.Unmarshal(payload, target)
}

func (s *Store) readRecord(id string) (*Record, error) {
	if !safeID(id) {
		return nil, fmt.Errorf("作业ID无效")
	}
	payload, err := os.ReadFile(filepath.Join(s.root, id, "job.json"))
	if err != nil {
		return nil, err
	}
	var record Record
	if err = json.Unmarshal(payload, &record); err != nil {
		return nil, err
	}
	if record.ID != id {
		return nil, fmt.Errorf("作业身份不一致")
	}
	recoverInterrupted(&record)
	return &record, nil
}

func recoverInterrupted(record *Record) {
	if record.Status != StatusRunning && record.Status != StatusQueued {
		return
	}
	updated, err := time.Parse(time.RFC3339, record.UpdatedAt)
	if record.Background && err == nil && time.Since(updated) < 30*time.Second {
		return
	}
	record.Status = StatusFailed
	record.Error = "识别工作进程已中断，请重新提交。"
}

func (s *Store) FailBackground(id string, err error) {
	current, readErr := s.Get(id)
	if readErr != nil || current.Status != StatusQueued {
		return
	}
	// Compete for the same ownership token as RunBackground. A queued record
	// can already have an owner that has not written "running" yet.
	lock, lockErr := os.OpenFile(filepath.Join(s.root, id, "worker.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if lockErr != nil {
		return
	}
	lock.Close()
	s.update(id, func(record *Record) {
		record.Status = StatusFailed
		record.Stage = StatusFailed
		record.Error = err.Error()
	})
}

func (s *Store) RunBackground(ctx context.Context, id string, run func(context.Context, *Reporter) (any, error)) error {
	record, err := s.Get(id)
	if err != nil {
		return err
	}
	if !record.Background || record.Status != StatusQueued {
		return fmt.Errorf("作业已开始或已结束")
	}
	lock, err := os.OpenFile(filepath.Join(s.root, id, "worker.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("作业已有工作进程: %w", err)
	}
	lock.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(s.root, id, "cancel")); err == nil {
					cancel()
					return
				}
				s.update(id, func(*Record) {})
			}
		}
	}()
	if _, err := os.Stat(filepath.Join(s.root, id, "cancel")); err == nil {
		cancel()
	}
	if ctx.Err() != nil {
		s.update(id, func(record *Record) {
			record.Status = StatusCancelled
			record.Stage = StatusCancelled
			record.Error = "已被取消"
		})
		close(done)
		<-stopped
		return nil
	}
	s.run(ctx, record, run)
	close(done)
	<-stopped
	return nil
}
