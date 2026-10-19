package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorBody is the JSON shape every error response uses.
type ErrorBody struct {
	Error APIError `json:"error"`
}

// APIError separates a stable code for programs from a message for people.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON sets the JSON content type and writes status plus body.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError writes the shared error shape.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{
		Error: APIError{Code: code, Message: message},
	})
}
