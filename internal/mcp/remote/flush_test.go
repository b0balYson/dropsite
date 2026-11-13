package remote

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// unwrapOnly is a middleware writer that exposes Unwrap but not Flush, like
// the API request logger in front of the MCP handler.
type unwrapOnly struct{ http.ResponseWriter }

func (w unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestRateWriterFlushesThroughWrappers(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &rateWriter{ResponseWriter: unwrapOnly{rec}, hit: &rateHit{}}
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Fatal(err)
	}
	if !rec.Flushed {
		t.Fatal("flush did not reach the connection")
	}
}
