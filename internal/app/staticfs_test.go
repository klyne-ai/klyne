package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeUI builds a minimal embedded-FS-style fstest.MapFS rooted at
// "ui/build/", matching what the production embed.FS exposes.
func fakeUI() fstest.MapFS {
	return fstest.MapFS{
		"ui/build/index.html":        &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"ui/build/_app/main.js":      &fstest.MapFile{Data: []byte("console.log('hi')")},
		"ui/build/favicon.png":       &fstest.MapFile{Data: []byte{0x89, 0x50, 0x4e, 0x47}},
	}
}

func TestStaticFSHandler_ServesIndex(t *testing.T) {
	h, err := NewStaticFSHandler(fakeUI())
	if err != nil {
		t.Fatalf("NewStaticFSHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "SPA") {
		t.Fatalf("body = %q, want SPA", body)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
}

func TestStaticFSHandler_ServesAsset(t *testing.T) {
	h, err := NewStaticFSHandler(fakeUI())
	if err != nil {
		t.Fatalf("NewStaticFSHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_app/main.js", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "hi") {
		t.Fatalf("body = %q, want main.js content", body)
	}
}

func TestStaticFSHandler_SPAFallback(t *testing.T) {
	h, err := NewStaticFSHandler(fakeUI())
	if err != nil {
		t.Fatalf("NewStaticFSHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions/abc123", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback to index.html)", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "SPA") {
		t.Fatalf("body = %q, want SPA fallback", body)
	}
}

func TestStaticFSHandler_RejectsNonGet(t *testing.T) {
	h, err := NewStaticFSHandler(fakeUI())
	if err != nil {
		t.Fatalf("NewStaticFSHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestStaticFSHandler_MissingIndex(t *testing.T) {
	// build/ exists but index.html is missing
	fs := fstest.MapFS{
		"ui/build/_app/main.js": &fstest.MapFile{Data: []byte("x")},
	}
	h, err := NewStaticFSHandler(fs)
	if err != nil {
		t.Fatalf("NewStaticFSHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
