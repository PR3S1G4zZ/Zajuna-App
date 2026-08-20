package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu     sync.Mutex
	jobs   map[string]Job
	events []Event
}

func newMemoryStore() *memoryStore { return &memoryStore{jobs: map[string]Job{}} }

func (s *memoryStore) CreateJob(_ context.Context, job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
}
func (s *memoryStore) GetJob(_ context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id], nil
}
func (s *memoryStore) MarkRunning(_ context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.lockedTransition(id, AllowedSources(StatusRunning), func(job *Job) {
		job.Status = StatusRunning
		job.Attempt++
		now := time.Now().UTC()
		job.StartedAt = &now
		job.FinishedAt = nil
		job.UpdatedAt = now
	})
	return job, err
}
func (s *memoryStore) UpdateProgress(_ context.Context, id, stage string, progress int, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	job.Stage, job.Progress, job.Message = stage, progress, message
	s.jobs[id] = job
	return nil
}
func (s *memoryStore) AppendEvent(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}
func (s *memoryStore) ListJobEvents(_ context.Context, id string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Event, 0)
	for _, event := range s.events {
		if event.JobID == id {
			result = append(result, event)
		}
	}
	return result, nil
}
func (s *memoryStore) CompleteJob(_ context.Context, id string, output json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.lockedTransition(id, AllowedSources(StatusCompleted), func(job *Job) {
		job.Status = StatusCompleted
		job.Result = output
		job.Progress = 100
		now := time.Now().UTC()
		job.FinishedAt = &now
		job.UpdatedAt = now
	})
	return err
}
func (s *memoryStore) RetryJob(_ context.Context, id, code, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.lockedTransition(id, AllowedSources(StatusRetrying), func(job *Job) {
		job.Status = StatusRetrying
		job.ErrorCode = code
		job.ErrorMessage = message
		job.UpdatedAt = time.Now().UTC()
	})
	return err
}
func (s *memoryStore) FailJob(_ context.Context, id, code, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.lockedTransition(id, AllowedSources(StatusFailed), func(job *Job) {
		job.Status = StatusFailed
		job.ErrorCode = code
		job.ErrorMessage = message
		now := time.Now().UTC()
		job.FinishedAt = &now
		job.UpdatedAt = now
	})
	return err
}
func (s *memoryStore) MarkCancelled(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.lockedTransition(id, AllowedSources(StatusCancelled), func(job *Job) {
		job.Status = StatusCancelled
		now := time.Now().UTC()
		job.FinishedAt = &now
		job.UpdatedAt = now
	})
	return err
}
func (s *memoryStore) ReconcileInterrupted(_ context.Context) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ready := make([]Job, 0)
	for id, job := range s.jobs {
		switch job.Status {
		case StatusRunning:
			if job.Attempt < job.MaxAttempts {
				job.Status = StatusRetrying
				job.ErrorCode = "interrupted"
				job.ErrorMessage = "el core se reinició mientras el trabajo estaba en ejecución"
				s.jobs[id] = job
				ready = append(ready, job)
			} else {
				job.Status = StatusFailed
				job.ErrorCode = "interrupted"
				job.ErrorMessage = "el trabajo quedó huérfano tras reiniciar el core"
				s.jobs[id] = job
			}
		case StatusQueued, StatusRetrying:
			ready = append(ready, job)
		}
	}
	return ready, nil
}

func (s *memoryStore) lockedTransition(id string, from []Status, apply func(*Job)) (Job, error) {
	job, ok := s.jobs[id]
	if !ok || !statusIn(job.Status, from) {
		return Job{}, ErrInvalidTransition
	}
	apply(&job)
	s.jobs[id] = job
	return job, nil
}

type demoWorker struct{}

func (demoWorker) ID() string { return "demo" }
func (demoWorker) Execute(ctx context.Context, job Job, reporter Reporter) Result {
	if err := reporter.Progress(ctx, "processing", 50, "Procesando fixture"); err != nil {
		return Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	return Result{Output: map[string]any{"job": job.ID, "ok": true}}
}

func TestRuntimeExecutesWorkerAndPersistsResult(t *testing.T) {
	store := newMemoryStore()
	runtime, err := NewRuntime(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(demoWorker{}); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()

	job, err := runtime.Submit(context.Background(), "demo", map[string]string{"fixture": "ok"})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, _ := store.GetJob(context.Background(), job.ID)
		if stored.Status == StatusCompleted {
			if stored.Progress != 100 {
				t.Fatalf("expected completed progress, got %d", stored.Progress)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not complete before timeout")
}

func TestCanTransitionRejectsTerminalMutations(t *testing.T) {
	if CanTransition(StatusCompleted, StatusRunning) || CanTransition(StatusFailed, StatusCancelled) || CanTransition(StatusCancelled, StatusRetrying) {
		t.Fatal("terminal states must not transition")
	}
	if !CanTransition(StatusQueued, StatusRunning) || !CanTransition(StatusRunning, StatusCompleted) || !CanTransition(StatusRunning, StatusCancelled) {
		t.Fatal("expected legal transitions to be allowed")
	}
}

func TestRuntimeRejectsCancelAfterCompletion(t *testing.T) {
	store := newMemoryStore()
	runtime, err := NewRuntime(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(demoWorker{}); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	job, err := runtime.Submit(context.Background(), "demo", map[string]string{"fixture": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, _ := store.GetJob(context.Background(), job.ID)
		if stored.Status == StatusCompleted {
			if err := runtime.Cancel(context.Background(), job.ID); err == nil {
				t.Fatal("expected cancel of a completed job to be rejected")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not complete before timeout")
}

func TestRuntimeRecoversInterruptedJobsAfterRestart(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	store.jobs["job-orphan"] = Job{
		ID: "job-orphan", Type: "demo", Status: StatusRunning, Input: []byte(`{}`),
		Attempt: 1, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
	store.jobs["job-queued"] = Job{
		ID: "job-queued", Type: "demo", Status: StatusQueued, Input: []byte(`{}`),
		MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
	runtime, err := NewRuntime(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(demoWorker{}); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		orphan, _ := store.GetJob(context.Background(), "job-orphan")
		queued, _ := store.GetJob(context.Background(), "job-queued")
		if orphan.Status == StatusCompleted && queued.Status == StatusCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	orphan, _ := store.GetJob(context.Background(), "job-orphan")
	queued, _ := store.GetJob(context.Background(), "job-queued")
	t.Fatalf("interrupted jobs were not recovered: orphan=%s queued=%s", orphan.Status, queued.Status)
}
