package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting_user"
	StatusRetrying  Status = "retrying"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Job struct {
	ID           string
	Type         string
	Status       Status
	Input        json.RawMessage
	Result       json.RawMessage
	Progress     int
	Stage        string
	Message      string
	Attempt      int
	MaxAttempts  int
	ErrorCode    string
	ErrorMessage string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	UpdatedAt    time.Time
}

type Event struct {
	JobID     string
	Kind      string
	Stage     string
	Progress  int
	Message   string
	Data      json.RawMessage
	CreatedAt time.Time
}

type Result struct {
	Output       any
	Retryable    bool
	ErrorCode    string
	ErrorMessage string
}

type Reporter interface {
	Progress(ctx context.Context, stage string, percent int, message string) error
	Event(ctx context.Context, kind string, message string, data any) error
}

type Worker interface {
	ID() string
	Execute(ctx context.Context, job Job, reporter Reporter) Result
}

type Store interface {
	CreateJob(ctx context.Context, job Job) error
	GetJob(ctx context.Context, id string) (Job, error)
	MarkRunning(ctx context.Context, id string) (Job, error)
	UpdateProgress(ctx context.Context, id string, stage string, progress int, message string) error
	AppendEvent(ctx context.Context, event Event) error
	ListJobEvents(ctx context.Context, id string) ([]Event, error)
	CompleteJob(ctx context.Context, id string, output json.RawMessage) error
	RetryJob(ctx context.Context, id string, code string, message string) error
	FailJob(ctx context.Context, id string, code string, message string) error
	MarkCancelled(ctx context.Context, id string) error
	ReconcileInterrupted(ctx context.Context) ([]Job, error)
}

type Runtime struct {
	store       Store
	workers     map[string]Worker
	queue       chan string
	concurrency int
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.RWMutex
	cancels     map[string]context.CancelFunc
	inFlight    map[string]struct{}
}

func NewRuntime(store Store, concurrency int) (*Runtime, error) {
	if store == nil {
		return nil, errors.New("job store is required")
	}
	if concurrency < 1 {
		concurrency = 1
	}
	return &Runtime{
		store:       store,
		workers:     map[string]Worker{},
		queue:       make(chan string, concurrency*4),
		concurrency: concurrency,
		cancels:     map[string]context.CancelFunc{},
		inFlight:    map[string]struct{}{},
	}, nil
}

func (r *Runtime) Register(worker Worker) error {
	if worker == nil || worker.ID() == "" {
		return errors.New("worker and worker ID are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.workers[worker.ID()]; exists {
		return fmt.Errorf("worker %q is already registered", worker.ID())
	}
	r.workers[worker.ID()] = worker
	return nil
}

func (r *Runtime) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	r.ctx, r.cancel = context.WithCancel(parent)
	for i := 0; i < r.concurrency; i++ {
		r.wg.Add(1)
		go r.workerLoop()
	}
	r.recoverInterrupted()
}

func (r *Runtime) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

func (r *Runtime) Submit(ctx context.Context, workerID string, input any) (Job, error) {
	if r.ctx == nil {
		return Job{}, errors.New("job runtime is not running")
	}
	r.mu.RLock()
	_, registered := r.workers[workerID]
	r.mu.RUnlock()
	if !registered {
		return Job{}, fmt.Errorf("worker %q is not registered", workerID)
	}

	contents, err := json.Marshal(input)
	if err != nil {
		return Job{}, fmt.Errorf("marshal job input: %w", err)
	}
	now := time.Now().UTC()
	job := Job{
		ID:          newID(),
		Type:        workerID,
		Status:      StatusQueued,
		Input:       contents,
		Progress:    0,
		MaxAttempts: 3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := r.store.CreateJob(ctx, job); err != nil {
		return Job{}, err
	}

	select {
	case r.queue <- job.ID:
		return job, nil
	case <-ctx.Done():
		return Job{}, ctx.Err()
	case <-r.runtimeDone():
		return Job{}, errors.New("job runtime is not running")
	}
}

func (r *Runtime) workerLoop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.runtimeDone():
			return
		case id := <-r.queue:
			r.execute(id)
		}
	}
}

func (r *Runtime) Get(ctx context.Context, id string) (Job, error) {
	return r.store.GetJob(ctx, id)
}

func (r *Runtime) Events(ctx context.Context, id string) ([]Event, error) {
	return r.store.ListJobEvents(ctx, id)
}

func (r *Runtime) Cancel(ctx context.Context, id string) error {
	r.mu.RLock()
	cancel := r.cancels[id]
	r.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if err := r.store.MarkCancelled(ctx, id); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) execute(id string) {
	r.mu.Lock()
	if _, busy := r.inFlight[id]; busy {
		r.mu.Unlock()
		return
	}
	r.inFlight[id] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.inFlight, id)
		r.mu.Unlock()
	}()

	job, err := r.store.GetJob(r.ctx, id)
	if err != nil {
		return
	}
	if job.Status == StatusCancelled || job.Status == StatusCompleted || job.Status == StatusFailed {
		return
	}
	worker := r.worker(job.Type)
	if worker == nil {
		_ = r.store.FailJob(r.ctx, id, "worker_not_found", "worker no registrado")
		return
	}
	job, err = r.store.MarkRunning(r.ctx, id)
	if err != nil {
		return
	}

	workerCtx, cancel := context.WithCancel(r.ctx)
	r.mu.Lock()
	r.cancels[job.ID] = cancel
	r.mu.Unlock()
	reporter := &jobReporter{store: r.store, jobID: job.ID}
	result := executeWorker(workerCtx, worker, job, reporter)
	r.mu.Lock()
	delete(r.cancels, job.ID)
	r.mu.Unlock()
	wasCancelled := workerCtx.Err() != nil
	cancel()
	if wasCancelled {
		return
	}
	if result.ErrorMessage != "" {
		if result.Retryable && job.Attempt < job.MaxAttempts {
			if err := r.store.RetryJob(r.ctx, id, result.ErrorCode, result.ErrorMessage); errors.Is(err, ErrInvalidTransition) {
				return
			}
			r.enqueueRetry(id, time.Duration(job.Attempt)*500*time.Millisecond)
			return
		}
		_ = r.store.FailJob(r.ctx, id, result.ErrorCode, result.ErrorMessage)
		return
	}

	output, err := json.Marshal(result.Output)
	if err != nil {
		_ = r.store.FailJob(r.ctx, id, "result_encode_failed", err.Error())
		return
	}
	if err := r.store.CompleteJob(r.ctx, id, output); err != nil && !errors.Is(err, ErrInvalidTransition) {
		_ = r.store.FailJob(r.ctx, id, "result_persist_failed", err.Error())
	}
}

func (r *Runtime) recoverInterrupted() {
	jobsToResume, err := r.store.ReconcileInterrupted(r.ctx)
	if err != nil {
		return
	}
	for _, job := range jobsToResume {
		select {
		case r.queue <- job.ID:
		case <-r.runtimeDone():
			return
		}
	}
}

func (r *Runtime) enqueueRetry(id string, delay time.Duration) {
	time.AfterFunc(delay, func() {
		select {
		case r.queue <- id:
		case <-r.runtimeDone():
		}
	})
}

func (r *Runtime) worker(id string) Worker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workers[id]
}

func (r *Runtime) runtimeDone() <-chan struct{} {
	if r.ctx == nil {
		return neverDone()
	}
	return r.ctx.Done()
}

func newID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	return "job-" + hex.EncodeToString(bytes)
}

type jobReporter struct {
	store Store
	jobID string
}

func (r *jobReporter) Progress(ctx context.Context, stage string, percent int, message string) error {
	return r.store.UpdateProgress(ctx, r.jobID, stage, percent, message)
}

func (r *jobReporter) Event(ctx context.Context, kind string, message string, data any) error {
	contents, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return r.store.AppendEvent(ctx, Event{
		JobID:     r.jobID,
		Kind:      kind,
		Message:   message,
		Data:      contents,
		CreatedAt: time.Now().UTC(),
	})
}

func executeWorker(ctx context.Context, worker Worker, job Job, reporter Reporter) (result Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = Result{ErrorCode: "worker_panic", ErrorMessage: fmt.Sprintf("worker panic: %v", recovered)}
		}
	}()
	return worker.Execute(ctx, job, reporter)
}

func neverDone() <-chan struct{} {
	return make(chan struct{})
}
