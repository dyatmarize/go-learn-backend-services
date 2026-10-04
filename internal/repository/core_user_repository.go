package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"learn101/internal/db"
	"learn101/internal/domain"
)

type CoreUserRepository struct {
	q    *db.Queries
	pool *pgxpool.Pool
}

func NewCoreUserRepository(pool *pgxpool.Pool) *CoreUserRepository {
	return &CoreUserRepository{
		q:    db.New(pool),
		pool: pool,
	}
}

func (r *CoreUserRepository) ByEmail(ctx context.Context, email string) (db.CoreUser, error) {
	u, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

func (r *CoreUserRepository) ByUUID(ctx context.Context, uuid string) (db.CoreUser, error) {
	u, err := r.q.GetUserByUUID(ctx, uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by uuid: %w", err)
	}
	return u, nil
}

func (r *CoreUserRepository) List(ctx context.Context, limit, offset int32) ([]db.CoreUser, error) {
	users, err := r.q.ListUsers(ctx, db.ListUsersParams{Limit: limit, Offset: offset})
	if err != nil {
		// Lists "succeed" with zero rows; ErrNoRows is not expected here.
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (r *CoreUserRepository) Create(ctx context.Context, p db.CreateUserParams) (db.CoreUser, error) {
	u, err := r.q.CreateUser(ctx, p)
	if err != nil {
		return db.CoreUser{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (r *CoreUserRepository) SoftDelete(ctx context.Context, uuid string) error {
	rows, err := r.q.SoftDeleteUser(ctx, uuid)
	if err != nil {
		return fmt.Errorf("soft delete user: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *CoreUserRepository) SetActive(ctx context.Context, id int64) (db.CoreUser, error) {
	slog.Debug("setting user active", "id", id)

	u, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		// Any other failure (dropped connection, bad SQL) must stop here.
		// Falling through would continue with a zero-value user, then run an
		// UPDATE with an empty uuid and report a database outage as "no rows".
		return db.CoreUser{}, fmt.Errorf("get user by id: %w", err)
	}
	if u.Status == domain.Active {
		return db.CoreUser{}, domain.ErrAccountAlreadyActive
	}

	u, err = r.q.UpdateUser(ctx, db.UpdateUserParams{
		Uuid:   u.Uuid,
		Name:   u.Name,
		Email:  u.Email,
		RoleID: u.RoleID,
		Status: domain.Active,
	})
	if err != nil {
		return db.CoreUser{}, fmt.Errorf("update user: %w", err)
	}

	slog.Debug("user activated", "id", u.ID, "status", u.Status)
	return u, nil
}

func (r *CoreUserRepository) SetInactive(ctx context.Context, id int64) (db.CoreUser, error) {
	slog.Debug("setting user inactive", "id", id)

	u, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CoreUser{}, domain.ErrNotFound
		}
		return db.CoreUser{}, fmt.Errorf("get user by id: %w", err)
	}
	if u.Status == domain.Inactive {
		return db.CoreUser{}, domain.ErrAccountAlreadyInactive
	}

	u, err = r.q.UpdateUser(ctx, db.UpdateUserParams{
		Uuid:   u.Uuid,
		Name:   u.Name,
		Email:  u.Email,
		RoleID: u.RoleID,
		Status: domain.Inactive,
	})
	if err != nil {
		return db.CoreUser{}, fmt.Errorf("update user: %w", err)
	}

	slog.Debug("user deactivated", "id", u.ID, "status", u.Status)
	return u, nil
}
