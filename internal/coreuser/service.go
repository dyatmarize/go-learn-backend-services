package coreuser

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type CoreUserService struct {
	users []CoreUser
}

func NewUserService() *CoreUserService {
	now := time.Now()
	return &CoreUserService{
		users: []CoreUser{
			{ID: 1, CreatedAt: now, UpdatedAt: now, Name: "Dyatmarize", Email: "dyatmarize@cool.app", RoleLevel: 1, Uuid: uuid.New().String()},
			{ID: 2, CreatedAt: now, UpdatedAt: now, Name: "admin", Email: "admin@cool.app", RoleLevel: 2, Uuid: uuid.New().String()},
			{ID: 3, CreatedAt: now, UpdatedAt: now, DeletedAt: &now, Name: "user 1", Email: "user1@cool.app", RoleLevel: 3, Uuid: uuid.New().String()},
		},
	}
}

func (s *CoreUserService) GetAll() ([]CoreUser, error) {
	return s.users, nil
}

func (s *CoreUserService) GetById(id int64) (*CoreUser, error) {
	for i, user := range s.users {
		if user.ID == id {
			return &s.users[i], nil
		}
	}
	return nil, fmt.Errorf("user with id %d not found", id)
}

func (s *CoreUserService) SetActive(id int64) (*CoreUser, error) {
	for i, user := range s.users {
		if user.ID == id {
			if user.IsActive() {
				return nil, fmt.Errorf("user with id %d is already active", id)
			}

			now := time.Now()
			s.users[i].UpdatedAt = now
			s.users[i].DeletedAt = nil

			return &s.users[i], nil
		}
	}
	return nil, fmt.Errorf("user with id %d not found", id)
}

func (s *CoreUserService) SetInactive(id int64) (*CoreUser, error) {
	for i, user := range s.users {
		if user.ID == id {
			if !user.IsActive() {
				return nil, fmt.Errorf("user with id %d is already inactive", id)
			}

			now := time.Now()
			s.users[i].UpdatedAt = now
			s.users[i].DeletedAt = nil

			return &s.users[i], nil
		}
	}
	return nil, fmt.Errorf("user with id %d not found", id)
}
