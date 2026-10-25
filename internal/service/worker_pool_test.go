package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/true18/go-musthave-diploma-tpl/internal/accrual"
	"github.com/true18/go-musthave-diploma-tpl/internal/money"
	"github.com/true18/go-musthave-diploma-tpl/internal/repository"
)

func TestWorkerPoolLimitsConcurrencyAndStops(t *testing.T) {
	repo := &poolRepo{orders: []repository.Order{
		{Number: "1", Status: repository.StatusNew},
		{Number: "2", Status: repository.StatusNew},
		{Number: "3", Status: repository.StatusNew},
		{Number: "4", Status: repository.StatusNew},
	}}
	client := &poolClient{started: make(chan struct{}, 4)}
	worker := NewWorker(repo, client, zap.NewNop())
	worker.interval = time.Hour
	worker.batchLimit = 4
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	for range 4 {
		select {
		case <-client.started:
		case <-time.After(time.Second):
			t.Fatal("worker pool did not start all tasks")
		}
	}
	if got := atomic.LoadInt32(&client.maxActive); got != 4 {
		t.Fatalf("max active workers = %d, want 4", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker pool did not stop")
	}
}

type poolClient struct {
	started   chan struct{}
	active    int32
	maxActive int32
}

func (c *poolClient) Fetch(ctx context.Context, number string) (accrual.Result, error) {
	active := atomic.AddInt32(&c.active, 1)
	for {
		old := atomic.LoadInt32(&c.maxActive)
		if active <= old || atomic.CompareAndSwapInt32(&c.maxActive, old, active) {
			break
		}
	}
	c.started <- struct{}{}
	<-ctx.Done()
	atomic.AddInt32(&c.active, -1)
	return accrual.Result{}, ctx.Err()
}

type poolRepo struct {
	orders []repository.Order
}

func (r *poolRepo) ListPendingOrders(context.Context, int) ([]repository.Order, error) {
	return r.orders, nil
}

func (r *poolRepo) UpdateOrder(context.Context, string, string, *money.Amount) error { return nil }
func (r *poolRepo) CreateUser(context.Context, string, []byte) (int64, error)        { return 0, nil }
func (r *poolRepo) FindUserByLogin(context.Context, string) (repository.User, error) {
	return repository.User{}, nil
}
func (r *poolRepo) CreateOrder(context.Context, int64, string) error { return nil }
func (r *poolRepo) ListUserOrders(context.Context, int64) ([]repository.Order, error) {
	return nil, nil
}
func (r *poolRepo) Balance(context.Context, int64) (repository.Balance, error) {
	return repository.Balance{}, nil
}
func (r *poolRepo) Withdraw(context.Context, int64, string, money.Amount) error { return nil }
func (r *poolRepo) ListWithdrawals(context.Context, int64) ([]repository.Withdrawal, error) {
	return nil, nil
}
