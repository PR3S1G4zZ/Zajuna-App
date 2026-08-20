package capture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesExecutableAdjacentRuntime(t *testing.T) {
	runtime := Resolve(filepath.Join("C:", "Program Files", "Zajuna App", "zajuna-core.exe"))
	if runtime.Root != filepath.Join("C:", "Program Files", "Zajuna App", "playwright") {
		t.Fatalf("unexpected runtime root: %s", runtime.Root)
	}
}

func TestResolveHonorsConfiguredRuntime(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("ZAJUNA_PLAYWRIGHT_DIR", directory)
	runtime := Resolve("")
	if runtime.Root != directory || runtime.Installed() {
		t.Fatalf("unexpected configured runtime: %#v", runtime)
	}
	if err := os.MkdirAll(filepath.Join(directory, "driver", "package"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "driver", "package", "cli.js"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "browsers", "chromium-test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !runtime.Installed() {
		t.Fatal("expected runtime to be detected")
	}
}

func TestLoginAndBlockedPageMarkers(t *testing.T) {
	if !isZajunaLoginPage("https://zajuna.sena.edu.co/zajuna/login/index.php", "", "") {
		t.Fatal("login URL was not rejected")
	}
	if !isZajunaLoginPage("https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080", "Acceso", "Número de documento Contraseña Iniciar sesión") {
		t.Fatal("login form markers were not rejected")
	}
	if !containsBlockedPageMarkers("Web Page Blocked Attack ID: 20000051 Message ID: 007047") {
		t.Fatal("WAF markers were not detected")
	}
	if containsBlockedPageMarkers("Curso de Animación 3D") {
		t.Fatal("normal course content was falsely classified as blocked")
	}
	if !isChallengePage("https://zajuna.sena.edu.co/zajuna/login/index.php", "Verificación", "Complete el reCAPTCHA para continuar") {
		t.Fatal("CAPTCHA page was not detected")
	}
	if isChallengePage("https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080", "Curso", "Contenido del curso") {
		t.Fatal("course content was falsely classified as a challenge")
	}
}

func TestCaptureURLSmoke(t *testing.T) {
	if os.Getenv("ZAJUNA_RUN_BROWSER_SMOKE") != "1" {
		t.Skip("browser smoke disabled")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><h1>Zajuna local smoke</h1></body></html>`))
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "smoke.png")
	if err := Resolve("").CaptureURL(context.Background(), server.URL, output); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("screenshot is empty")
	}
}

func TestCaptureURLSelectorSmoke(t *testing.T) {
	if os.Getenv("ZAJUNA_RUN_BROWSER_SMOKE") != "1" {
		t.Skip("browser smoke disabled")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><main id="region-main"><section class="course-content"><h2>Fases</h2><p>Contenido dirigido</p></section></main></body></html>`))
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "selector.png")
	result, err := Resolve("").CaptureURLWithMetadataAndCookiesAndOptions(context.Background(), server.URL, output, nil, CaptureOptions{Selector: "#region-main .course-content", LabelHint: "Fases"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SelectorMatched || result.Selector != "#region-main .course-content" {
		t.Fatalf("selector was not applied: %#v", result)
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		t.Fatalf("selector screenshot is missing or empty: %v", err)
	}
}
