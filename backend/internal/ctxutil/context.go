package ctxutil

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type contextKey string

const (
	tenantKey contextKey = "tenant_id"
	userKey   contextKey = "user_id"
)

func WithTenantID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantKey, id)
}
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userKey, id)
}

func GetTenantID(ctx context.Context) (uuid.UUID, error) { return get(ctx, tenantKey) }
func GetUserID(ctx context.Context) (uuid.UUID, error)   { return get(ctx, userKey) }

func get(ctx context.Context, key contextKey) (uuid.UUID, error) {
	id, ok := ctx.Value(key).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("missing %s in context", key)
	}
	return id, nil
}
