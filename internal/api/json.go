package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/b0balYson/dropsite/internal/service"
)

// maxJSONBody bounds every JSON request body.
const maxJSONBody = 64 << 10

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		status = http.StatusInternalServerError
		buf.Reset()
		buf.WriteString(`{"error":{"code":"internal","message":"internal error"}}` + "\n")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func noContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// maxFilesBody bounds the JSON bodies that carry file content by value:
// 16 MB of content (the service limit) in base64 or escaped text plus framing.
const maxFilesBody = 32 << 20

// decodeJSON strictly decodes a single JSON object into dst.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decode(w, r, dst, false, maxJSONBody)
}

// decodeOptionalJSON is decodeJSON that accepts an empty body.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decode(w, r, dst, true, maxJSONBody)
}

// decodeFilesJSON is decodeJSON for bodies with file content.
func decodeFilesJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decode(w, r, dst, false, maxFilesBody)
}

func decode(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool, limit int64) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return invalid("request body is too large")
		}
		return invalid("could not read the request body")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if allowEmpty {
			return nil
		}
		return invalid("request body must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("invalid JSON: " + jsonProblem(err))
	}
	if dec.More() {
		return invalid("invalid JSON: unexpected data after the object")
	}
	return nil
}

// jsonProblem describes a decoding error without echoing request content
// beyond field names.
func jsonProblem(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return "field " + typeErr.Field + " has the wrong type"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "malformed JSON"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected end of input"
	}
	return err.Error()
}

// list is the envelope of every collection response.
type list[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

func newList[T any](items []T, total int) list[T] {
	if items == nil {
		items = []T{}
	}
	return list[T]{Items: items, Total: total}
}

// page reads limit and offset from the query string.
func page(q url.Values) (service.Page, error) {
	p := service.Page{Limit: service.DefaultPageSize}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > service.MaxPageSize {
			return p, invalidField("limit", "limit must be an integer from 1 to "+strconv.Itoa(service.MaxPageSize))
		}
		p.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, invalidField("offset", "offset must be a non-negative integer")
		}
		p.Offset = n
	}
	return p, nil
}
