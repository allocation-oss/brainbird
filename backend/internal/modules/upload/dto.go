package upload

type ShareIntentRequest struct {
	Filename    string `json:"filename" validate:"required"`
	ContentType string `json:"content_type" validate:"required"`
}
type ShareIntentResponse struct {
	UploadID string `json:"upload_id"`
	URL      string `json:"url"`
}
