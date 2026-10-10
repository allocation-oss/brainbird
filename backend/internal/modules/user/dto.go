package user

type CreateRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}
type Response struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
