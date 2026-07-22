package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"studdle/backend/api/handler"
)

func TestDocs_SpecServesYAML(t *testing.T) {
	h := handler.NewDocsHandler()
	rec := httptest.NewRecorder()
	h.Spec(rec, httptest.NewRequest("GET", "/openapi.yaml", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml...", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"openapi:", "/ai/flashcards/prompt", "/ai/quota", "BearerAuth"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in spec body", want)
		}
	}
}

func TestDocs_UIReferencesSpec(t *testing.T) {
	h := handler.NewDocsHandler()
	rec := httptest.NewRecorder()
	h.UI(rec, httptest.NewRequest("GET", "/docs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html...", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`url: "/openapi.yaml"`, "SwaggerUIBundle", "swagger-ui-dist"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in UI body", want)
		}
	}
}
