package webhook

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Job represents a task to be executed by a worker
type Job func(ctx context.Context) error

// WorkerPool manages a pool of workers to execute jobs
type WorkerPool struct {
	Queue   chan Job
	Workers int
	wg      sync.WaitGroup
	quit    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

// ErrQueueFull is returned when the job queue is full
var ErrQueueFull = errors.New("worker pool queue is full")

// NewWorkerPool creates a new WorkerPool
func NewWorkerPool(workers, queueSize int) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		Queue:   make(chan Job, queueSize),
		Workers: workers,
		quit:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start launches the workers
func (p *WorkerPool) Start() {
	slog.Info("Starting worker pool", "workers", p.Workers, "queue_size", cap(p.Queue))
	for i := 0; i < p.Workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Stop gracefully shuts down the worker pool
func (p *WorkerPool) Stop() {
	slog.Info("Stopping worker pool...")

	// Signal workers to stop accepting new jobs from closed channel
	close(p.quit) // Wait, we can just close Queue if we want to drain.
	// But usually standard pattern is:
	// 1. Stop accepting new submissions (caller responsibility usually, or we close a submission channel)
	// 2. Wait for workers to drain Queue.

	// In this implementation:
	// We close Queue to signal no more jobs.
	close(p.Queue)

	// Wait for all workers to finish
	p.wg.Wait()
	p.cancel()
	slog.Info("Worker pool stopped")
}

// Submit adds a job to the queue. Returns ErrQueueFull if the queue is full.
func (p *WorkerPool) Submit(job Job) error {
	select {
	case p.Queue <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()
	for job := range p.Queue {
		// Prepare a context for the job that is cancelled if the pool stops forceully?
		// or just pass background?
		// Usually we want the job to respect the pool's context or a per-request context?
		// Pr-processor creates its own timeout context.

		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic in worker", "worker_id", id, "panic", r)
				}
			}()

			if err := job(p.ctx); err != nil {
				slog.Error("Job execution failed", "worker_id", id, "error", err)
			}
		}()
	}
}
