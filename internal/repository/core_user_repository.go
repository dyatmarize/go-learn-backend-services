package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"learn101/internal/db"
	"learn101/internal/domain"
)

type UserRepository struct {
	q    *db.Queries
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{
		q:    db.New(pool),
		pool: pool,
	}
}

func (r *UserRepository) ByEmail(ctx context.Context, email string) (db.CoreUser, error) {
	u, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

func (r *UserRepository) ByUUID(ctx context.Context, uuid string) (db.CoreUser, error) {
	u, err := r.q.GetUserByUUID(ctx, uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by uuid: %w", err)
	}
	return u, nil
}

func (r *UserRepository) List(ctx context.Context, limit, offset int32) ([]db.CoreUser, error) {
	users, err := r.q.ListUsers(ctx, db.ListUsersParams{Limit: limit, Offset: offset})
	if err != nil {
		// Lists "succeed" with zero rows; ErrNoRows is not expected here.
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (r *UserRepository) Create(ctx context.Context, p db.CreateUserParams) (db.CoreUser, error) {
	u, err := r.q.CreateUser(ctx, p)
	if err != nil {
		return db.CoreUser{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (r *UserRepository) SoftDelete(ctx context.Context, uuid string) error {
	rows, err := r.q.SoftDeleteUser(ctx, uuid)
	if err != nil {
		return fmt.Errorf("soft delete user: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}
