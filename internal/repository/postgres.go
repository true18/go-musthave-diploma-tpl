package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/true18/go-musthave-diploma-tpl/internal/money"
)

const pgUniqueViolation = "23505"

const (
	createUserQuery = `INSERT INTO users (login, password_hash) VALUES ($1, $2) RETURNING id`
	findUserQuery   = `SELECT id, login, password_hash, created_at FROM users WHERE login = $1`

	findOrderOwnerQuery = `SELECT user_id FROM orders WHERE number = $1`
	createOrderQuery    = `INSERT INTO orders (number, user_id, status) VALUES ($1, $2, $3)`
	listOrdersQuery     = `SELECT number, status, accrual::text, uploaded_at FROM orders WHERE user_id = $1 ORDER BY uploaded_at DESC`
	listPendingQuery    = `SELECT number, user_id, status, accrual::text, uploaded_at FROM orders WHERE status IN ($1, $2) ORDER BY uploaded_at ASC LIMIT $3`
	updateOrderQuery    = `UPDATE orders SET status = $2, accrual = $3, updated_at = NOW() WHERE number = $1 AND status <> $4 AND status <> $5`

	balanceQuery = `SELECT COALESCE((SELECT SUM(accrual) FROM orders WHERE user_id = $1 AND status = $2), 0)::text, COALESCE((SELECT SUM("sum") FROM withdrawals WHERE user_id = $1), 0)::text`

	lockUserQuery         = `SELECT id FROM users WHERE id = $1 FOR UPDATE`
	insertWithdrawalQuery = `INSERT INTO withdrawals (user_id, order_number, "sum") VALUES ($1, $2, $3)`
	listWithdrawalsQuery  = `SELECT order_number, "sum"::text, processed_at FROM withdrawals WHERE user_id = $1 ORDER BY processed_at DESC`
)

// Postgres is a PostgreSQL-backed repository.
type Postgres struct {
	pool pool
}

type pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

// New creates a PostgreSQL repository using an already opened pool.
func New(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func newWithPool(pool pool) *Postgres {
	return &Postgres{pool: pool}
}

// CreateUser creates a user with a bcrypt password hash.
func (p *Postgres) CreateUser(ctx context.Context, login string, passwordHash []byte) (int64, error) {
	var id int64
	if err := p.pool.QueryRow(ctx, createUserQuery, login, string(passwordHash)).Scan(&id); err != nil {
		if isUniqueViolation(err, "users_login_key") {
			return 0, ErrUserExists
		}
		return 0, fmt.Errorf("create user: %w", err)
	}
	return id, nil
}

// FindUserByLogin returns a user by login.
func (p *Postgres) FindUserByLogin(ctx context.Context, login string) (User, error) {
	var user User
	if err := p.pool.QueryRow(ctx, findUserQuery, login).Scan(&user.ID, &user.Login, &user.PasswordHash, &user.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("find user by login: %w", err)
	}
	return user, nil
}

// CreateOrder stores an order for a user.
func (p *Postgres) CreateOrder(ctx context.Context, userID int64, number string) error {
	if _, err := p.pool.Exec(ctx, createOrderQuery, number, userID, StatusNew); err != nil {
		if isUniqueViolation(err, "orders_number_key") {
			return p.orderOwnerError(ctx, userID, number)
		}
		return fmt.Errorf("create order: %w", err)
	}
	return nil
}

// ListUserOrders returns user orders from newest to oldest.
func (p *Postgres) ListUserOrders(ctx context.Context, userID int64) ([]Order, error) {
	rows, err := p.pool.Query(ctx, listOrdersQuery, userID)
	if err != nil {
		return nil, fmt.Errorf("list user orders: %w", err)
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		order, err := scanOrderRow(rows)
		if err != nil {
			return nil, err
		}
		order.UserID = userID
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list user orders rows: %w", err)
	}
	return orders, nil
}

// ListPendingOrders returns a small batch of orders that need accrual polling.
func (p *Postgres) ListPendingOrders(ctx context.Context, limit int) ([]Order, error) {
	rows, err := p.pool.Query(ctx, listPendingQuery, StatusNew, StatusProcessing, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending orders: %w", err)
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var order Order
		var accrual pgtype.Text
		if err := rows.Scan(&order.Number, &order.UserID, &order.Status, &accrual, &order.UploadedAt); err != nil {
			return nil, fmt.Errorf("scan pending order: %w", err)
		}
		amount, err := parseNullableAmount(accrual)
		if err != nil {
			return nil, err
		}
		order.Accrual = amount
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending orders rows: %w", err)
	}
	return orders, nil
}

// UpdateOrder updates order status and accrual unless the order is already final.
func (p *Postgres) UpdateOrder(ctx context.Context, number string, status string, accrual *money.Amount) error {
	if _, err := p.pool.Exec(ctx, updateOrderQuery, number, status, nullableAmount(accrual), StatusInvalid, StatusProcessed); err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	return nil
}

// Balance calculates current and withdrawn points for a user.
func (p *Postgres) Balance(ctx context.Context, userID int64) (Balance, error) {
	return p.balance(ctx, p.pool, userID)
}

// Withdraw atomically saves a withdrawal if the user has enough points.
func (p *Postgres) Withdraw(ctx context.Context, userID int64, orderNumber string, amount money.Amount) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin withdraw tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var lockedID int64
	if err := tx.QueryRow(ctx, lockUserQuery, userID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock user: %w", err)
	}
	balance, err := p.balance(ctx, tx, userID)
	if err != nil {
		return err
	}
	if balance.Current < amount {
		return ErrInsufficientFunds
	}
	if _, err := tx.Exec(ctx, insertWithdrawalQuery, userID, orderNumber, amount.String()); err != nil {
		return fmt.Errorf("insert withdrawal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit withdraw tx: %w", err)
	}
	return nil
}

// ListWithdrawals returns user withdrawals from newest to oldest.
func (p *Postgres) ListWithdrawals(ctx context.Context, userID int64) ([]Withdrawal, error) {
	rows, err := p.pool.Query(ctx, listWithdrawalsQuery, userID)
	if err != nil {
		return nil, fmt.Errorf("list withdrawals: %w", err)
	}
	defer rows.Close()
	var withdrawals []Withdrawal
	for rows.Next() {
		var withdrawal Withdrawal
		var rawSum string
		if err := rows.Scan(&withdrawal.OrderNumber, &rawSum, &withdrawal.ProcessedAt); err != nil {
			return nil, fmt.Errorf("scan withdrawal: %w", err)
		}
		amount, err := money.Parse(rawSum)
		if err != nil {
			return nil, fmt.Errorf("parse withdrawal sum: %w", err)
		}
		withdrawal.Sum = amount
		withdrawals = append(withdrawals, withdrawal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list withdrawals rows: %w", err)
	}
	return withdrawals, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

func (p *Postgres) orderOwnerError(ctx context.Context, userID int64, number string) error {
	var ownerID int64
	if err := p.pool.QueryRow(ctx, findOrderOwnerQuery, number).Scan(&ownerID); err != nil {
		return fmt.Errorf("find duplicate order owner: %w", err)
	}
	if ownerID == userID {
		return ErrOrderExistsSameUser
	}
	return ErrOrderExistsAnotherUser
}

func (p *Postgres) balance(ctx context.Context, q rowQuerier, userID int64) (Balance, error) {
	var accruedRaw, withdrawnRaw string
	if err := q.QueryRow(ctx, balanceQuery, userID, StatusProcessed).Scan(&accruedRaw, &withdrawnRaw); err != nil {
		return Balance{}, fmt.Errorf("calculate balance: %w", err)
	}
	accrued, err := money.Parse(accruedRaw)
	if err != nil {
		return Balance{}, fmt.Errorf("parse accrual balance: %w", err)
	}
	withdrawn, err := money.Parse(withdrawnRaw)
	if err != nil {
		return Balance{}, fmt.Errorf("parse withdrawn balance: %w", err)
	}
	return Balance{Current: accrued - withdrawn, Withdrawn: withdrawn}, nil
}

func scanOrderRow(row rows) (Order, error) {
	var order Order
	var accrual pgtype.Text
	if err := row.Scan(&order.Number, &order.Status, &accrual, &order.UploadedAt); err != nil {
		return Order{}, fmt.Errorf("scan order: %w", err)
	}
	amount, err := parseNullableAmount(accrual)
	if err != nil {
		return Order{}, err
	}
	order.Accrual = amount
	return order, nil
}

func parseNullableAmount(value pgtype.Text) (*money.Amount, error) {
	if !value.Valid {
		return nil, nil
	}
	amount, err := money.Parse(value.String)
	if err != nil {
		return nil, fmt.Errorf("parse amount: %w", err)
	}
	return &amount, nil
}

func nullableAmount(amount *money.Amount) any {
	if amount == nil {
		return nil
	}
	return amount.String()
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == constraint
}
