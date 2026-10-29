package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/true18/go-musthave-diploma-tpl/internal/accrual"
	"github.com/true18/go-musthave-diploma-tpl/internal/money"
	"github.com/true18/go-musthave-diploma-tpl/internal/repository"
)

const (
	defaultWorkerInterval = 2 * time.Second
	defaultWorkerBatch    = 20
	defaultWorkerCount    = 4
)

// AccrualClient gets order states from the external accrual system.
type AccrualClient interface {
	Fetch(ctx context.Context, number string) (accrual.Result, error)
}

// Worker periodically polls pending orders and updates their accrual state.
type Worker struct {
	repo       Repository
	client     AccrualClient
	logger     *zap.Logger
	interval   time.Duration
	batchLimit int
	workers    int

	mu         sync.Mutex
	inFlight   map[string]struct{}
	retryAfter time.Time
}

// NewWorker creates a fixed-size worker pool with conservative polling defaults.
func NewWorker(repo Repository, client AccrualClient, logger *zap.Logger) *Worker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Worker{
		repo:       repo,
		client:     client,
		logger:     logger,
		interval:   defaultWorkerInterval,
		batchLimit: defaultWorkerBatch,
		workers:    defaultWorkerCount,
		inFlight:   make(map[string]struct{}),
	}
}

// Run starts polling until ctx is cancelled and waits for all workers.
func (w *Worker) Run(ctx context.Context) {
	tasks := make(chan repository.Order, w.batchLimit)
	var group sync.WaitGroup
	for i := 0; i < w.workers; i++ {
		group.Add(1)
		go w.runWorker(ctx, tasks, &group)
	}

	w.poll(ctx, tasks)
	ticker := time.NewTicker(w.interval)
	for {
		select {
		case <-ctx.Done():
			ticker.Stop()
			close(tasks)
			group.Wait()
			return
		case <-ticker.C:
			w.poll(ctx, tasks)
		}
	}
}

// PollOnce processes one batch synchronously. It is useful for tests and maintenance jobs.
func (w *Worker) PollOnce(ctx context.Context) {
	if w.client == nil || w.rateLimited() {
		return
	}
	orders, err := w.repo.ListPendingOrders(ctx, w.batchLimit)
	if err != nil {
		w.logger.Error("list pending orders", zap.Error(err))
		return
	}
	for _, order := range orders {
		if !w.reserve(order.Number) {
			continue
		}
		w.process(ctx, order)
	}
}

func (w *Worker) poll(ctx context.Context, tasks chan<- repository.Order) {
	if w.client == nil || w.rateLimited() {
		return
	}
	orders, err := w.repo.ListPendingOrders(ctx, w.batchLimit)
	if err != nil {
		w.logger.Error("list pending orders", zap.Error(err))
		return
	}
	for _, order := range orders {
		if !w.reserve(order.Number) {
			continue
		}
		select {
		case tasks <- order:
		case <-ctx.Done():
			w.release(order.Number)
			return
		}
	}
}

func (w *Worker) runWorker(ctx context.Context, tasks <-chan repository.Order, group *sync.WaitGroup) {
	defer group.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case order, ok := <-tasks:
			if !ok {
				return
			}
			w.process(ctx, order)
		}
	}
}

func (w *Worker) process(ctx context.Context, order repository.Order) {
	defer w.release(order.Number)
	if w.rateLimited() {
		return
	}
	result, err := w.client.Fetch(ctx, order.Number)
	if errors.Is(err, accrual.ErrRateLimited) {
		w.setRateLimit(result.RetryAfter)
		if errors.Is(err, accrual.ErrInvalidRetryAfter) {
			w.logger.Warn("invalid accrual Retry-After", zap.Error(err))
		}
		return
	}
	if err != nil {
		w.logger.Warn("fetch accrual", zap.String("order", order.Number), zap.Error(err))
		return
	}
	if result.NoContent {
		return
	}
	status, accrualAmount, ok := mapAccrualResult(result)
	if !ok {
		w.logger.Warn("unknown accrual status", zap.String("status", result.Status))
		return
	}
	if err := w.repo.UpdateOrder(ctx, order.Number, status, accrualAmount); err != nil {
		w.logger.Warn("update order from accrual", zap.String("order", order.Number), zap.Error(err))
	}
}

func (w *Worker) reserve(number string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.inFlight[number]; exists {
		return false
	}
	w.inFlight[number] = struct{}{}
	return true
}

func (w *Worker) release(number string) {
	w.mu.Lock()
	delete(w.inFlight, number)
	w.mu.Unlock()
}

func (w *Worker) rateLimited() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Now().Before(w.retryAfter)
}

func (w *Worker) setRateLimit(delay time.Duration) {
	if delay <= 0 {
		delay = time.Second
	}
	w.mu.Lock()
	until := time.Now().Add(delay)
	if until.After(w.retryAfter) {
		w.retryAfter = until
	}
	w.mu.Unlock()
}

func mapAccrualResult(result accrual.Result) (string, *money.Amount, bool) {
	switch result.Status {
	case accrual.StatusRegistered, accrual.StatusProcessing:
		return repository.StatusProcessing, nil, true
	case accrual.StatusInvalid:
		return repository.StatusInvalid, nil, true
	case accrual.StatusProcessed:
		return repository.StatusProcessed, result.Accrual, true
	default:
		return "", nil, false
	}
}
