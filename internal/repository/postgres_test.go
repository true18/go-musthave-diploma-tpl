package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/true18/go-musthave-diploma-tpl/internal/migrations"
	"github.com/true18/go-musthave-diploma-tpl/internal/money"
)

func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URI")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URI is not set")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE withdrawals, orders, users RESTART IDENTITY`); err != nil {
		t.Fatal(err)
	}

	repo := New(pool)
	userID, err := repo.CreateUser(context.Background(), "alice", []byte("$2a$04$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuuabcdefghijkl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindUserByLogin(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateOrder(context.Background(), userID, "12345678903"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateOrder(context.Background(), userID, "12345678903"); !errors.Is(err, ErrOrderExistsSameUser) {
		t.Fatalf("same-user duplicate error = %v", err)
	}
	amount := mustAmount(t, "500")
	if err := repo.UpdateOrder(context.Background(), "12345678903", StatusProcessed, &amount); err != nil {
		t.Fatal(err)
	}
	if err := repo.Withdraw(context.Background(), userID, "2377225624", mustAmount(t, "100")); err != nil {
		t.Fatal(err)
	}
	balance, err := repo.Balance(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Current != mustAmount(t, "400") || balance.Withdrawn != mustAmount(t, "100") {
		t.Fatalf("unexpected balance: %+v", balance)
	}
}

func mustAmount(t *testing.T, value string) money.Amount {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return amount
}

func TestRepositoryHelpers(t *testing.T) {
	amount := mustAmount(t, "12.50")
	if got := nullableAmount(&amount); got != "12.5" {
		t.Fatalf("nullableAmount = %v", got)
	}
	if nullableAmount(nil) != nil {
		t.Fatal("nil amount was not preserved")
	}
	if got, err := parseNullableAmount(pgtype.Text{String: "12.50", Valid: true}); err != nil || *got != amount {
		t.Fatalf("parse nullable amount = %v, %v", got, err)
	}
	if got, err := parseNullableAmount(pgtype.Text{}); err != nil || got != nil {
		t.Fatalf("null amount = %v, %v", got, err)
	}
	if !isUniqueViolation(&pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "orders_number_key"}, "orders_number_key") {
		t.Fatal("unique violation was not detected")
	}
	if isUniqueViolation(&pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "other"}, "orders_number_key") {
		t.Fatal("wrong constraint was detected")
	}
}

func TestBalanceAndScanHelpers(t *testing.T) {
	balance, err := (&Postgres{}).balance(context.Background(), fakeRowQuerier{row: fakeRow{values: []any{"600.50", "100.50"}}}, 1)
	if err != nil || balance.Current != mustAmount(t, "500") {
		t.Fatalf("balance = %+v, err = %v", balance, err)
	}
	row := fakeScanRows{values: []any{"12345678903", StatusProcessed, pgtype.Text{String: "500", Valid: true}, timeNow}}
	order, err := scanOrderRow(row)
	if err != nil || order.Number != "12345678903" || order.Accrual == nil {
		t.Fatalf("order = %+v, err = %v", order, err)
	}
}

func TestRepositoryWithPoolDouble(t *testing.T) {
	now := time.Now()
	p := &fakePool{
		rowResults: map[string]fakeRow{
			createUserQuery: {values: []any{int64(1)}},
			findUserQuery:   {values: []any{int64(1), "alice", []byte("hash"), now}},
			balanceQuery:    {values: []any{"600.50", "100.50"}},
		},
		queryResults: map[string]*fakePgxRows{
			listOrdersQuery:      {values: [][]any{{"1", StatusProcessed, pgtype.Text{String: "500", Valid: true}, now}}},
			listPendingQuery:     {values: [][]any{{"1", int64(1), StatusNew, pgtype.Text{}, now}}},
			listWithdrawalsQuery: {values: [][]any{{"2377225624", "100", now}}},
		},
	}
	repo := newWithPool(p)
	if id, err := repo.CreateUser(context.Background(), "alice", []byte("hash")); err != nil || id != 1 {
		t.Fatalf("CreateUser = %d, %v", id, err)
	}
	if user, err := repo.FindUserByLogin(context.Background(), "alice"); err != nil || user.Login != "alice" {
		t.Fatalf("FindUserByLogin = %+v, %v", user, err)
	}
	if err := repo.CreateOrder(context.Background(), 1, "1"); err != nil {
		t.Fatalf("CreateOrder = %v", err)
	}
	if orders, err := repo.ListUserOrders(context.Background(), 1); err != nil || len(orders) != 1 {
		t.Fatalf("ListUserOrders = %+v, %v", orders, err)
	}
	if orders, err := repo.ListPendingOrders(context.Background(), 20); err != nil || len(orders) != 1 {
		t.Fatalf("ListPendingOrders = %+v, %v", orders, err)
	}
	amount := mustAmount(t, "500")
	if err := repo.UpdateOrder(context.Background(), "1", StatusProcessed, &amount); err != nil {
		t.Fatalf("UpdateOrder = %v", err)
	}
	if balance, err := repo.Balance(context.Background(), 1); err != nil || balance.Current != mustAmount(t, "500") {
		t.Fatalf("Balance = %+v, %v", balance, err)
	}
	if withdrawals, err := repo.ListWithdrawals(context.Background(), 1); err != nil || len(withdrawals) != 1 {
		t.Fatalf("ListWithdrawals = %+v, %v", withdrawals, err)
	}
}

var timeNow = time.Unix(100, 0)

type fakeRowQuerier struct{ row fakeRow }

func (f fakeRowQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }

type fakeRow struct{ values []any }

func (f fakeRow) Scan(dest ...any) error {
	for i := range dest {
		switch value := dest[i].(type) {
		case *string:
			*value = f.values[i].(string)
		case *int64:
			*value = f.values[i].(int64)
		case *[]byte:
			*value = f.values[i].([]byte)
		case *time.Time:
			*value = f.values[i].(time.Time)
		}
	}
	return nil
}

type fakeScanRows struct{ values []any }

func (fakeScanRows) Next() bool { return true }
func (fakeScanRows) Err() error { return nil }
func (fakeScanRows) Close()     {}
func (f fakeScanRows) Scan(dest ...any) error {
	for i := range dest {
		switch value := dest[i].(type) {
		case *string:
			*value = f.values[i].(string)
		case *pgtype.Text:
			*value = f.values[i].(pgtype.Text)
		case interface{ Scan(any) error }:
			_ = value
		}
	}
	return nil
}

type fakePool struct {
	rowResults   map[string]fakeRow
	queryResults map[string]*fakePgxRows
}

func (f *fakePool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (f *fakePool) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	return f.queryResults[query], nil
}

func (f *fakePool) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	return f.rowResults[query]
}

func (f *fakePool) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("transaction not configured")
}

type fakePgxRows struct{ values [][]any }

func (f *fakePgxRows) Next() bool {
	return len(f.values) > 0
}

func (f *fakePgxRows) Scan(dest ...any) error {
	row := fakeRow{values: f.values[0]}
	f.values = f.values[1:]
	return row.Scan(dest...)
}

func (f *fakePgxRows) Err() error                                   { return nil }
func (f *fakePgxRows) Close()                                       {}
func (f *fakePgxRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 1") }
func (f *fakePgxRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakePgxRows) Values() ([]any, error)                       { return nil, nil }
func (f *fakePgxRows) RawValues() [][]byte                          { return nil }
func (f *fakePgxRows) Conn() *pgx.Conn                              { return nil }
