package app

import (
	"testing"
)

func TestNoopOpenBrowser(t *testing.T) {
	if err := NoopOpenBrowser("http://example"); err != nil {
		t.Fatalf("NoopOpenBrowser must return nil; got %v", err)
	}
	if err := NoopOpenBrowser(""); err != nil {
		t.Fatalf("NoopOpenBrowser must accept empty url; got %v", err)
	}
}

func TestDefaultOpenBrowser_EmptyURL(t *testing.T) {
	err := DefaultOpenBrowser("")
	if err == nil {
		t.Fatal("DefaultOpenBrowser must reject empty url")
	}
}

// Note: We deliberately do NOT exercise DefaultOpenBrowser with a non-empty
// URL in unit tests because that would actually launch the system browser.
// The integration smoke test in start.go (and the doctor command) is the
// real-world coverage path; CI sets the App.OpenBrowserFunc to NoopOpenBrowser
// to avoid spurious browser windows.
