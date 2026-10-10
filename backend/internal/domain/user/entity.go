package user

import (
	"github.com/google/uuid"
	"time"
)

type User struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}
