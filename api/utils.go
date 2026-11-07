package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// Error codes sent in the error envelope.
const (
	codeInvalidJson      = "invalid_json"
	codeBodyTooLarge     = "body_too_large"
	codeValidationFailed = "validation_failed"
	codeInternal         = "internal"
)

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

var errNotSingleObject = errors.New("request body must be a single JSON object")

// writeJson writes v as a JSON response with the given status code.
func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode json response", "error", err)
	}
}

// writeError writes the error envelope {"error":{"code","message","fields"}}.
func writeError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	writeJson(w, status, errorResponse{Error: errorDetail{Code: code, Message: message, Fields: fields}})
}

// decodeJsonObject reads the request body as exactly one JSON object.
// Numbers are kept as json.Number so amounts stay exact. A key sent as null
// maps to nil, which is different from a missing key.
func decodeJsonObject(r *http.Request) (map[string]any, error) {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()

	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errNotSingleObject
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errNotSingleObject
	}
	return obj, nil
}

// writeDecodeError maps a decodeJsonObject error to a 413 or 400 response.
func writeDecodeError(w http.ResponseWriter, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		writeError(w, http.StatusRequestEntityTooLarge, codeBodyTooLarge, "request body is too large", nil)
		return
	}
	writeError(w, http.StatusBadRequest, codeInvalidJson, errNotSingleObject.Error(), nil)
}
