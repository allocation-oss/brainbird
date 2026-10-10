package claim

import "context"

type Repository interface {
	FindByID(ctx context.Context, id string) (Claim, error)
}
