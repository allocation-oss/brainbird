package claim

import "github.com/google/uuid"

type Claim struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Status   string
}
