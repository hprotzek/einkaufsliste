package httptransport

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hprotzek/einkaufsliste/internal/transport/http/openapi"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// These tests only exercise routing and the health endpoint, so the auth
// dependencies stay nil: reaching them would mean a route was wired to the
// wrong handler, and a nil panic says so loudly.

func TestHealthzReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	NewRouter(Deps{Log: testLogger()}).ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	if got, want := res.Header.Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	// The generated type, so a schema change that renames or retypes this
	// field fails here rather than in a client months later.
	var body openapi.Health
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if body.Status != openapi.Ok {
		t.Errorf("status field = %q, want %q", body.Status, openapi.Ok)
	}
}

// /me answered 501 until task 1.9 implemented it. Now it is routed and
// guarded: an unauthenticated request must be refused rather than reaching
// the handler's body. The router is still built from openapi.yaml, which is
// what routing it at all demonstrates.
func TestMeRequiresAuthentication(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()

	NewRouter(Deps{Log: testLogger()}).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()

	NewRouter(Deps{Log: testLogger()}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
