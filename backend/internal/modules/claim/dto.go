package claim

type CreateRequest struct {
	Status string `json:"status" validate:"required"`
}
type Response struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
