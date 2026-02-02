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
type Job func(ctx context.Context) error

// Pool manages a pool of workers to execute jobs of type T
type Pool[T any] struct {
	queue           chan item[T]
	workers         int
	maxRetries      int
	shutdownTimeout time.Duration

	// Lifecycle
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
}

type item[T any] struct {
	payload    T
	retryCount int
}

// NewWorkerPool creates a new generic Pool
func NewWorkerPool[T any](workers, queueSize, maxRetries int, shutdownTimeout time.Duration) *Pool[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool[T]{
		queue:           make(chan item[T], queueSize),
		workers:         workers,
		maxRetries:      maxRetries,
		shutdownTimeout: shutdownTimeout,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// Start launches the workers with the given handler
func (p *Pool[T]) Start(handler func(context.Context, T) error) {
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

// Submit adds a job to the queue. Returns error if closed or full.
func (p *Pool[T]) Submit(payload T) error {
	if p.closed.Load() {
		return ErrPoolClosed
	}

	select {
	case p.queue <- item[T]{payload: payload, retryCount: 0}:
		return nil
	default:
		return ErrQueueFull
	}
}

func (p *Pool[T]) worker(id int, handler func(context.Context, T) error) {
	defer p.wg.Done()

	for task := range p.queue {
		// Use a separate function to handle panic recovery and logic per task
		p.processTask(id, task, handler)
	}
}

func (p *Pool[T]) processTask(workerID int, task item[T], handler func(context.Context, T) error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic in worker", "worker_id", workerID, "panic", r)
			// Don't retry on panic to avoid loops, unless desired
		}
	}()

	// Execute handler
	err := handler(p.ctx, task.payload)
	if err == nil {
		return
	}

	// Handle Retry
	if task.retryCount >= p.maxRetries {
		slog.Error("Job failed after max retries", "worker_id", workerID, "error", err)
		return
	}

	// Check if error is retryable (Context canceled usually means we are shutting down, so don't retry)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// If pool context is canceled, definitely stop.
		if p.ctx.Err() != nil {
			return
		}
		// If just job timeout, maybe retry?
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
