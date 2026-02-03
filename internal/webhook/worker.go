package webhook

import (
	"context"
	"errors"
	"log/slog"
	"pr-review-automation/internal/metrics"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrQueueFull is returned when the job queue is full
	ErrQueueFull = errors.New("worker pool queue is full")
	// ErrPoolClosed is returned when submitting to a closed pool
	ErrPoolClosed = errors.New("worker pool is closed")
)

// Job represents a task to be executed by a worker
type Job func(ctx context.Context, degradeLevel int) error

// Pool manages a pool of workers to execute jobs of type T
// Pool manages a pool of workers to execute jobs of type T
type Pool[T any] struct {
	queue           chan item[T]
	workers         int
	maxRetries      int
	retryDegrade    bool
	shutdownTimeout time.Duration
	backupHandler   func(context.Context, T, int) error

	// Lifecycle
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
}

type item[T any] struct {
	payload      T
	retryCount   int
	degradeLevel int
	usedBackup   bool
}

// NewWorkerPool creates a new generic Pool
func NewWorkerPool[T any](workers, queueSize, maxRetries int, retryDegrade bool, shutdownTimeout time.Duration) *Pool[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool[T]{
		queue:           make(chan item[T], queueSize),
		workers:         workers,
		maxRetries:      maxRetries,
		retryDegrade:    retryDegrade,
		shutdownTimeout: shutdownTimeout,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// Start launches the workers with the given handler
func (p *Pool[T]) Start(handler func(context.Context, T, int) error) {
	slog.Info("Starting worker pool", "workers", p.workers, "queue_size", cap(p.queue))
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i, handler)
	}
}

// Stop gracefully shuts down the worker pool
// Phase 1: Close queue (Drain)
// Phase 2: Cancel context after timeout (Force)
func (p *Pool[T]) Stop() {
	if !p.closed.CompareAndSwap(false, true) {
		return // Already closed
	}

	slog.Info("Stopping worker pool...")
	close(p.queue) // Signal workers to drain

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("Worker pool stopped gracefully")
	case <-time.After(p.shutdownTimeout):
		slog.Warn("Worker pool shutdown terminated by timeout, forcing exit", "timeout", p.shutdownTimeout)
		p.cancel() // Force cancel running jobs
		<-done     // Wait for workers to exit
	}
}

// SetBackupHandler sets the handler for backup LLM retries
func (p *Pool[T]) SetBackupHandler(handler func(context.Context, T, int) error) {
	p.backupHandler = handler
}

// Submit adds a job to the queue. Returns error if closed or full.
func (p *Pool[T]) Submit(payload T) error {
	if p.closed.Load() {
		return ErrPoolClosed
	}

	select {
	case p.queue <- item[T]{payload: payload, retryCount: 0, degradeLevel: 0}:
		return nil
	default:
		return ErrQueueFull
	}
}

func (p *Pool[T]) worker(id int, handler func(context.Context, T, int) error) {
	defer p.wg.Done()

	for task := range p.queue {
		// Use a separate function to handle panic recovery and logic per task
		p.processTask(id, task, handler)
	}
}

func (p *Pool[T]) processTask(workerID int, task item[T], handler func(context.Context, T, int) error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic in worker", "worker_id", workerID, "panic", r)
			// Don't retry on panic to avoid loops, unless desired
		}
	}()

	// Execute handler with degradeLevel
	var err error
	if task.usedBackup && p.backupHandler != nil {
		err = p.backupHandler(p.ctx, task.payload, task.degradeLevel)
	} else {
		err = handler(p.ctx, task.payload, task.degradeLevel)
	}
	if err == nil {
		return
	}

	// Handle Retry
	if task.retryCount >= p.maxRetries {
		// 尝试备用 LLM (如果配置了且尚未使用)
		if p.backupHandler != nil && !task.usedBackup {
			slog.Warn("Primary LLM exhausted, attempting backup LLM",
				"degrade_level", task.degradeLevel) // 记录当前降级级别
			task.usedBackup = true
			task.retryCount = 0

			// 关键：保持 degradeLevel 不变，复用已确定的降级策略
			// 配合 CachedStage1/2，备用 LLM 只执行 Stage 3
			go func() {
				// Use non-blocking send or drop if full, similar to retry logic
				// But since this is a "new" attempt mode, we try to enqueue.
				// NOTE: We launch a goroutine to avoid blocking the worker if queue is full.
				select {
				case p.queue <- task:
					slog.Info("Job requeued for backup LLM")
				default:
					slog.Error("Failed to requeue for backup LLM: queue full")
					metrics.WebhookRequests.WithLabelValues("dropped_backup_full").Inc()
				}
			}()
			return
		}

		slog.Error("Job failed after max retries (including backup)", "worker_id", workerID, "error", err)
		return
	}

	// Check if error is retryable (Context canceled usually means we are shutting down, so don't retry)
	isDeadline := errors.Is(err, context.DeadlineExceeded)
	if errors.Is(err, context.Canceled) || isDeadline {
		// If pool context is canceled, definitely stop.
		if p.ctx.Err() != nil {
			return
		}
	}

	// Degradation Logic
	if isDeadline && p.retryDegrade {
		task.degradeLevel++
		slog.Warn("Deadline exceeded, escalating degradation level", "level", task.degradeLevel)
	}

	// Non-blocking Retry Logic: Use time.AfterFunc to not block this worker
	// Backoff: 2^retry * 100ms (Simple exp backoff)
	backoff := time.Duration(1<<task.retryCount) * 100 * time.Millisecond

	slog.Warn("Job failed, scheduling retry",
		"worker_id", workerID,
		"retry", task.retryCount+1,
		"backoff", backoff,
		"error", err)

	time.AfterFunc(backoff, func() {
		// Attempt to re-submit
		if p.closed.Load() {
			return
		}
		task.retryCount++

		// Try to enqueue; if full, we drop it to avoid deadlock/goroutine leak
		select {
		case p.queue <- task:
			// Success
		default:
			slog.Error("Failed to requeue failed job: queue full", "retry", task.retryCount)
			metrics.WebhookRequests.WithLabelValues("dropped_retry_full").Inc() // Assuming metrics accessible
		}
	})
}
