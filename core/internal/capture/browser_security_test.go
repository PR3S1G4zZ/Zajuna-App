package capture

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestBrowserCookieForTargetPreservesSecurityAttributes(t *testing.T) {
	target := mustURL(t, "https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080")
	expires := time.Date(2026, 12, 1, 15, 4, 5, 0, time.UTC)
	got, err := BrowserCookieForTarget(http.Cookie{
		Name:     "MoodleSession",
		Value:    "fixture-session",
		Domain:   ".zajuna.sena.edu.co",
		Path:     "/zajuna",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
	}, target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "MoodleSession" || got.Value != "fixture-session" {
		t.Fatalf("cookie identity was not preserved: %#v", got)
	}
	if got.Domain != "zajuna.sena.edu.co" || got.Path != "/zajuna" {
		t.Fatalf("cookie scope was not preserved: %#v", got)
	}
	if !got.Secure || !got.HttpOnly || got.SameSite != "Strict" {
		t.Fatalf("security attributes were dropped: %#v", got)
	}
	if got.Expires == nil || !got.Expires.Equal(expires) {
		t.Fatalf("expires was not preserved: %#v", got.Expires)
	}

	fromMaxAge, err := BrowserCookieForTarget(http.Cookie{
		Name:   "MoodleSession",
		Value:  "fixture-session",
		Path:   "/",
		MaxAge: 3600,
		Secure: true,
	}, target)
	if err != nil {
		t.Fatal(err)
	}
	if fromMaxAge.Expires == nil || fromMaxAge.Expires.Before(time.Now().Add(30*time.Minute)) {
		t.Fatalf("MaxAge was not converted to Expires: %#v", fromMaxAge.Expires)
	}
}

func TestBrowserCookieForTargetRejectsIncompatibleDomain(t *testing.T) {
	target := mustURL(t, "https://zajuna.sena.edu.co/zajuna/my/")
	if _, err := BrowserCookieForTarget(http.Cookie{
		Name:   "sid",
		Value:  "fixture",
		Domain: "evil.example",
		Path:   "/",
	}, target); err == nil {
		t.Fatal("expected foreign cookie domain to be rejected")
	}
	if _, err := BrowserCookieForTarget(http.Cookie{
		Name:   "sid",
		Value:  "fixture",
		Domain: "sena.edu.co",
		Path:   "/",
	}, target); err == nil {
		t.Fatal("expected parent-domain cookie to be rejected")
	}
	if _, err := BrowserCookieForTarget(http.Cookie{
		Name:   "sid",
		Value:  "fixture",
		MaxAge: -1,
	}, target); err == nil {
		t.Fatal("expected expired cookie to be rejected")
	}
}

func TestValidateCaptureNavigationURLRejectsUnsafeTargets(t *testing.T) {
	origin := mustURL(t, "https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080")
	if err := ValidateCaptureNavigationURL("https://zajuna.sena.edu.co/zajuna/mod/page/view.php?id=10", origin); err != nil {
		t.Fatalf("same-origin Zajuna URL was rejected: %v", err)
	}
	for _, raw := range []string{
		"https://evil.example/phish",
		"http://127.0.0.1/",
		"http://192.168.1.10/admin",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,hi",
		"https://user:pass@zajuna.sena.edu.co/",
	} {
		if err := ValidateCaptureNavigationURL(raw, origin); err == nil {
			t.Fatalf("expected rejection for %s", raw)
		}
	}

	fixture := mustURL(t, "http://127.0.0.1:8080/fixture")
	if err := ValidateCaptureNavigationURL("http://127.0.0.1:8080/other", fixture); err != nil {
		t.Fatalf("controlled fixture host was rejected: %v", err)
	}
	if err := ValidateCaptureNavigationURL("http://192.168.0.5/", fixture); err == nil {
		t.Fatal("expected other private hosts to stay rejected")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid fixture URL %s: %v", raw, err)
	}
	return parsed
}
