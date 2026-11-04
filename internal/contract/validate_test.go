package contract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"redline/internal/contract"
)

const validSpec = `{"openapi":"3.2.0","info":{"title":"Toolshop API"},"paths":{"/brands":{"get":{"responses":{"200":{"description":"ok"}}}},"/brands/{id}":{"get":{"responses":{"200":{"description":"ok"}}},"delete":{"responses":{"204":{"description":"deleted"}}}}}}`

func specServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestValidateSourceAcceptsOpenAPI(t *testing.T) {
	check := contract.ValidateSource(context.Background(), specServer(t, 200, "application/json", validSpec).URL+"/docs")
	if !check.OK || check.Kind != "" || check.Error != "" {
		t.Fatalf("spec valid ditolak: %+v", check)
	}
	if check.Version != "OpenAPI 3.2.0" || check.Title != "Toolshop API" || check.Endpoints != 3 {
		t.Errorf("metadata salah: %+v", check)
	}
	if check.StatusCode != 200 || check.DurationMS < 0 || check.CheckedAt.IsZero() {
		t.Errorf("info respons salah: %+v", check)
	}
}

func TestValidateSourceExplainsFailures(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		status  int
		ctype   string
		body    string
		kind    string
		message string
	}{
		{"html", "", 200, "text/html", "<!DOCTYPE html><html><body>Swagger UI</body></html>", "html", "HTML"},
		{"html without header", "", 200, "text/plain", "<!doctype html><html></html>", "html", "HTML"},
		{"not found", "", 404, "application/json", `{"error":"missing"}`, "status", "HTTP 404"},
		{"invalid json", "", 200, "application/json", "not json", "json", "JSON"},
		{"swagger 2", "", 200, "application/json", `{"swagger":"2.0","paths":{}}`, "not_openapi", "Swagger 2.0"},
		{"no openapi field", "", 200, "application/json", `{"paths":{}}`, "not_openapi", "openapi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := tc.url
			if url == "" {
				url = specServer(t, tc.status, tc.ctype, tc.body).URL
			}
			check := contract.ValidateSource(context.Background(), url)
			if check.OK || check.Kind != tc.kind {
				t.Fatalf("kind = %q ok=%v, mau %q: %+v", check.Kind, check.OK, tc.kind, check)
			}
			if !strings.Contains(check.Error, tc.message) {
				t.Errorf("pesan %q tidak memuat %q", check.Error, tc.message)
			}
			if check.Hint == "" && tc.kind != "json" {
				t.Errorf("hint kosong untuk %s: %+v", tc.kind, check)
			}
		})
	}
}

func TestValidateSourceGuards(t *testing.T) {
	check := contract.ValidateSource(context.Background(), "ftp://example.com/docs")
	if check.OK || check.Kind != "invalid_url" {
		t.Errorf("scheme ftp harus ditolak: %+v", check)
	}
	check = contract.ValidateSource(context.Background(), "http://169.254.169.254/latest/meta-data")
	if check.OK || check.Kind != "blocked" {
		t.Errorf("metadata cloud harus ditolak: %+v", check)
	}
	check = contract.ValidateSource(context.Background(), "http://127.0.0.1:1/docs")
	if check.OK || (check.Kind != "network" && check.Kind != "timeout") {
		t.Errorf("host mati harus network/timeout: %+v", check)
	}
}

func TestRegistryRuntimeUpdates(t *testing.T) {
	r := contract.NewRegistry(map[string]string{"toolshop": "https://api.example.com/docs"})
	if url, ok := r.URL("toolshop"); !ok || url != "https://api.example.com/docs" {
		t.Errorf("URL awal salah: %q %v", url, ok)
	}
	r.Set("coba2", "https://coba2.example.com/docs")
	r.Delete("toolshop")
	if _, ok := r.URL("toolshop"); ok {
		t.Error("toolshop harus terhapus")
	}
	copy := r.Specs()
	copy["coba2"] = "https://changed.example.com/docs"
	if url, _ := r.URL("coba2"); url != "https://coba2.example.com/docs" {
		t.Errorf("Specs harus menyalin map: %q", url)
	}
}

func TestRegistryFetch(t *testing.T) {
	srv := specServer(t, 200, "application/json", validSpec)
	r := contract.NewRegistry(map[string]string{"toolshop": srv.URL})

	url, doc, err := r.Fetch(context.Background(), "toolshop")
	if err != nil || url != srv.URL || len(doc) == 0 {
		t.Fatalf("fetch gagal: %q %d %v", url, len(doc), err)
	}
	if url, doc, err := r.Fetch(context.Background(), "missing"); err != nil || url != "" || doc != nil {
		t.Errorf("project tanpa kontrak harus kosong: %q %d %v", url, len(doc), err)
	}
}

func TestValidateSourceSuggestsJSONFromSwaggerUI(t *testing.T) {
	page := `<!DOCTYPE html><html><head><script>
		const ui = SwaggerUIBundle({ url: "https://api.example.com/docs?api-docs.json" });
	</script></head><body></body></html>`
	check := contract.ValidateSource(context.Background(), specServer(t, 200, "text/html", page).URL+"/api/documentation")
	if check.OK || check.Kind != "html" {
		t.Fatalf("Swagger UI harus ditolak sebagai html: %+v", check)
	}
	if len(check.Candidates) != 1 || check.Candidates[0] != "https://api.example.com/docs?api-docs.json" {
		t.Errorf("kandidat JSON salah: %+v", check.Candidates)
	}
}
