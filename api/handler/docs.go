package handler

import (
	_ "embed"
	"net/http"
)

//go:embed docs_openapi.yaml
var openapiYAML []byte

// swaggerUIHTML is the minimal HTML that loads Swagger UI from unpkg and points it
// at /openapi.yaml. No bundled JS — the browser fetches Swagger UI itself.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Studdle API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
window.onload = () => {
  window.ui = SwaggerUIBundle({
    url: "/openapi.yaml",
    dom_id: "#swagger-ui",
    persistAuthorization: true,
  });
};
</script>
</body>
</html>`

// DocsHandler serves the OpenAPI spec and Swagger UI browser page.
type DocsHandler struct{}

// NewDocsHandler constructs a DocsHandler.
func NewDocsHandler() *DocsHandler {
	return &DocsHandler{}
}

// Spec serves the embedded OpenAPI YAML.
func (h *DocsHandler) Spec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openapiYAML)
}

// UI serves the Swagger UI HTML page.
func (h *DocsHandler) UI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}
