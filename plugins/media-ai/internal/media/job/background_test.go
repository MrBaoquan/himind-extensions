package job

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, *Record) {
	t.Helper()
	t.Setenv("HIMIND_PLUGIN_DATA_ROOT", t.TempDir())
	s, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.PrepareBackground("transcribe", "input.wav", "zh", "small", map[string]string{"path": "input.wav"})
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func TestBackgroundReloadIsReadonlyAndKeepsLiveJob(t *testing.T) {
	s, r := fixture(t)
	path := filepath.Join(s.root, r.ID, "job.json")
	before, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	for i := 0; i < 3; i++ {
		fresh, err := NewStore()
		if err != nil {
			t.Fatal(err)
		}
		got, err := fresh.Get(r.ID)
		if err != nil || got.Status != StatusQueued {
			t.Fatalf("%+v %v", got, err)
		}
	}
	after, _ := os.ReadFile(path)
	latest, _ := os.Stat(path)
	if !bytes.Equal(before, after) || !latest.ModTime().Equal(info.ModTime()) {
		t.Fatal("status rewrote job")
	}
	s.update(r.ID, func(r *Record) { r.UpdatedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339) })
	// update normally refreshes heartbeat; deliberately write an old record.
	r.UpdatedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	s.persist(r)
	fresh, _ := NewStore()
	got, _ := fresh.Get(r.ID)
	if got.Status != StatusFailed {
		t.Fatal("stale worker remained live")
	}
}

func TestBackgroundCancelBeforeStartDoesNotRunAndRejectsEscape(t *testing.T) {
	s, r := fixture(t)
	other, _ := NewStore()
	if err := other.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := s.RunBackground(context.Background(), r.ID, func(context.Context, *Reporter) (any, error) { called = true; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if called || got.Status != StatusCancelled {
		t.Fatalf("called=%v %+v", called, got)
	}
	if err := other.BackgroundInput("../outside", &map[string]any{}); err == nil {
		t.Fatal("escaped job path")
	}
}

func TestBackgroundFailureCannotOverwriteQueuedOwner(t *testing.T) {
	s, r := fixture(t)
	lock, err := os.OpenFile(filepath.Join(s.root, r.ID, "worker.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	path := filepath.Join(s.root, r.ID, "job.json")
	before, _ := os.ReadFile(path)
	other, _ := NewStore()
	other.FailBackground(r.ID, errors.New("duplicate before running"))
	after, _ := os.ReadFile(path)
	got, err := s.Get(r.ID)
	if err != nil || got.Status != StatusQueued || !bytes.Equal(before, after) {
		t.Fatalf("queued owner overwritten: %+v %v", got, err)
	}
}

func TestBackgroundLaunchFailureClaimsOwnership(t *testing.T) {
	s, r := fixture(t)
	other, _ := NewStore()
	other.FailBackground(r.ID, errors.New("launch failed"))
	called := false
	err := s.RunBackground(context.Background(), r.ID, func(context.Context, *Reporter) (any, error) {
		called = true
		return nil, nil
	})
	got, _ := s.Get(r.ID)
	if err == nil || called || got.Status != StatusFailed || got.Error != "launch failed" {
		t.Fatalf("failure allowed worker: %+v called=%v err=%v", got, called, err)
	}
}

func TestBackgroundStatusSurvivesWorkerProcessAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancel"}[cancel], func(t *testing.T) {
			_, r := fixture(t)
			command := exec.Command(os.Args[0], "-test.run=^TestBackgroundHelperProcess$", "--", r.ID)
			command.Env = append(os.Environ(), "HIMIND_JOB_TEST_HELPER=1")
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			deadline := time.Now().Add(8 * time.Second)
			for {
				fresh, _ := NewStore()
				got, err := fresh.Get(r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status == StatusRunning {
					if cancel {
						if err := fresh.Cancel(r.ID); err != nil {
							t.Fatal(err)
						}
					}
					// A second worker may not run or poison the original job.
					err = fresh.RunBackground(context.Background(), r.ID, func(context.Context, *Reporter) (any, error) { t.Fatal("duplicate ran"); return nil, nil })
					if err == nil {
						t.Fatal("duplicate worker allowed")
					}
					fresh.FailBackground(r.ID, errors.New("duplicate"))
				}
				if got.Status == StatusCompleted || got.Status == StatusCancelled {
					if cancel && got.Status != StatusCancelled || !cancel && got.Status != StatusCompleted {
						t.Fatalf("%+v", got)
					}
					if !cancel {
						var result map[string]bool
						if got.Result == nil || json.Unmarshal(*got.Result, &result) != nil || !result["actual_worker"] {
							t.Fatalf("missing result: %+v", got)
						}
					}
					break
				}
				if got.Status == StatusFailed || time.Now().After(deadline) {
					t.Fatalf("worker did not finish: %+v", got)
				}
				time.Sleep(60 * time.Millisecond)
			}
		})
	}
}

func TestBackgroundHelperProcess(t *testing.T) {
	if os.Getenv("HIMIND_JOB_TEST_HELPER") != "1" {
		return
	}
	s, err := NewStore()
	if err != nil {
		os.Exit(2)
	}
	err = s.RunBackground(context.Background(), os.Args[len(os.Args)-1], func(ctx context.Context, r *Reporter) (any, error) {
		r.Stage("transcribing")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1700 * time.Millisecond):
			return map[string]bool{"actual_worker": true}, nil
		}
	})
	if err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
