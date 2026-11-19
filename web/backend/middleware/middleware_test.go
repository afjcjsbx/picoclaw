package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCSRF_AllowsGetWithoutHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	CSRF(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET without X-Requested-With: status = %d, want 200", rec.Code)
	}
}

func TestCSRF_BlocksMutatingRequestWithoutHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/api/config", nil)
			CSRF(inner).ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s without header: status = %d, want 403", method, rec.Code)
			}
		})
	}
}

func TestCSRF_AllowsMutatingRequestWithHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	CSRF(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST with header: status = %d, want 200", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	SecurityHeaders(inner).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
}
