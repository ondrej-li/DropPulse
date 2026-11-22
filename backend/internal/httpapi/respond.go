package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeNoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func parseForm(w http.ResponseWriter, r *http.Request, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return r.ParseForm()
}

func errorCode(err error) string {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return "request_too_large"
	}
	return "invalid_request"
}

func badRequest(w http.ResponseWriter, err error) {
	code := errorCode(err)
	message := "request body is invalid"
	if code == "request_too_large" {
		message = "request body is too large"
	}
	writeError(w, http.StatusBadRequest, code, message)
}

func internalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
}
