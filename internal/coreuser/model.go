package coreuser

import "time"

type BaseEntity struct {
	ID        int64
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

type CoreUser struct {
	BaseEntity
	Name      string
	Email     string
	RoleLevel int64
	Uuid      string
}

func (u *CoreUser) IsActive() bool {
	return u.DeletedAt == nil
}
