package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/zajuna-app/core/internal/capture"
	"github.com/zajuna-app/core/internal/evidence"
	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/secrets"
	"github.com/zajuna-app/core/internal/security"
	"github.com/zajuna-app/core/internal/zajuna"
)

const CaptureBrowserWorkerID = "capture-browser"

type CaptureBrowserInput struct {
	URL           string   `json:"url"`
	OutputPath    string   `json:"outputPath,omitempty"`
	FichaID       string   `json:"fichaId,omitempty"`
	ItemCode      string   `json:"itemCode,omitempty"`
	SlotNumber    int      `json:"slotNumber,omitempty"`
	Name          string   `json:"name,omitempty"`
	CSSSelector   string   `json:"cssSelector,omitempty"`
	CSSSelectors  []string `json:"cssSelectorFallbacks,omitempty"`
	LabelHint     string   `json:"labelHint,omitempty"`
	Authenticated bool     `json:"authenticated,omitempty"`
	Username      string   `json:"username,omitempty"`
	DocumentType  string   `json:"documentType,omitempty"`
}

type authenticatedCaptureClient interface {
	Login(ctx context.Context, credentials zajuna.Credentials) (zajuna.Session, error)
}

type CaptureBrowserWorker struct {
	runtime        capture.Runtime
	dataDir        string
	store          evidence.Store
	authClient     authenticatedCaptureClient
	credentials    secrets.Store
	allowedOrigins []string
}

func NewCaptureBrowserWorker(runtime capture.Runtime, dataDir string, stores ...evidence.Store) (*CaptureBrowserWorker, error) {
	if dataDir == "" {
		return nil, errors.New("capture data directory is required")
	}
	var store evidence.Store
	if len(stores) > 0 {
		store = stores[0]
	}
	return &CaptureBrowserWorker{runtime: runtime, dataDir: dataDir, store: store}, nil
}

func NewAuthenticatedCaptureBrowserWorker(runtime capture.Runtime, dataDir string, client authenticatedCaptureClient, credentials secrets.Store, stores ...evidence.Store) (*CaptureBrowserWorker, error) {
	if client == nil || credentials == nil {
		return nil, errors.New("authenticated capture worker requires client and credentials")
	}
	worker, err := NewCaptureBrowserWorker(runtime, dataDir, stores...)
	if err != nil {
		return nil, err
	}
	worker.authClient = client
	worker.credentials = credentials
	return worker, nil
}

// SetAllowedOrigins enables the production URL policy. Tests and developer
// fixtures may leave it empty to use local HTTP servers; the packaged core
// sets it to the configured Zajuna origin before registering the worker.
func (w *CaptureBrowserWorker) SetAllowedOrigins(origins ...string) {
	w.allowedOrigins = append([]string(nil), origins...)
}

func (w *CaptureBrowserWorker) ID() string { return CaptureBrowserWorkerID }

func (w *CaptureBrowserWorker) Execute(ctx context.Context, job jobs.Job, reporter jobs.Reporter) jobs.Result {
	var input CaptureBrowserInput
	if err := json.Unmarshal(job.Input, &input); err != nil {
		return jobs.Result{ErrorCode: "invalid_input", ErrorMessage: "input de captura inválido"}
	}
	input.URL = strings.TrimSpace(input.URL)
	parsedTarget, err := security.ValidateHTTPURL(input.URL, w.allowedOrigins, len(w.allowedOrigins) == 0)
	if err != nil {
		return jobs.Result{ErrorCode: "invalid_input", ErrorMessage: err.Error()}
	}
	input.URL = parsedTarget.String()
	if input.SlotNumber <= 0 {
		input.SlotNumber = 1
	}
	if !w.runtime.Installed() {
		return jobs.Result{ErrorCode: "browser_not_installed", ErrorMessage: fmt.Sprintf("runtime Chromium no instalado en %s; ejecuta npm run browser:install", w.runtime.Root)}
	}
	outputPath, err := w.outputPath(job.ID, input.OutputPath)
	if err != nil {
		return jobs.Result{ErrorCode: "invalid_output_path", ErrorMessage: err.Error()}
	}
	if err := reporter.Progress(ctx, "capture", 10, "Preparando Chromium local"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	var browserCookies []capture.BrowserCookie
	if input.Authenticated {
		if w.authClient == nil || w.credentials == nil {
			return jobs.Result{ErrorCode: "auth_capture_not_configured", ErrorMessage: "la captura autenticada no está configurada"}
		}
		input.Username = strings.TrimSpace(input.Username)
		if input.Username == "" {
			return jobs.Result{ErrorCode: "missing_username", ErrorMessage: "la captura autenticada requiere el usuario de Zajuna"}
		}
		if input.DocumentType == "" {
			input.DocumentType = "CC"
		}
		if err := reporter.Progress(ctx, "login", 20, "Validando sesión de Zajuna para Chromium"); err != nil {
			return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
		}
		password, err := w.credentials.Get(input.Username)
		if err != nil || password == "" {
			return jobs.Result{ErrorCode: "credential_unavailable", ErrorMessage: "no se encontró la contraseña de Zajuna en el almacén seguro"}
		}
		session, err := w.authClient.Login(ctx, zajuna.Credentials{DocumentType: input.DocumentType, Document: input.Username, Password: password})
		if err != nil {
			if errors.Is(err, zajuna.ErrChallengeRequired) {
				return jobs.Result{ErrorCode: "zajuna_challenge_required", ErrorMessage: "Zajuna pidió CAPTCHA o MFA; la captura no se automatiza"}
			}
			return jobs.Result{Retryable: retryableZajunaError(err), ErrorCode: "zajuna_login_failed", ErrorMessage: fmt.Sprintf("no se pudo iniciar sesión para la captura: %v", err)}
		}
		base, baseErr := url.Parse(session.BaseURL)
		if baseErr != nil || base.Host == "" || base.Scheme != parsedTarget.Scheme || base.Host != parsedTarget.Host {
			return jobs.Result{ErrorCode: "invalid_authenticated_target", ErrorMessage: "la captura autenticada solo permite URLs del mismo origen de Zajuna"}
		}
		for _, cookie := range session.CookiesForURL(input.URL) {
			converted, convErr := capture.BrowserCookieForTarget(cookie, parsedTarget)
			if convErr != nil {
				continue
			}
			browserCookies = append(browserCookies, converted)
		}
		if len(browserCookies) == 0 {
			return jobs.Result{ErrorCode: "zajuna_session_expired", ErrorMessage: "Zajuna no devolvió cookies de sesión para la captura"}
		}
	}
	if err := reporter.Event(ctx, "capture_started", "Captura iniciada", map[string]string{"url": input.URL}); err != nil {
		return jobs.Result{ErrorCode: "event_failed", ErrorMessage: err.Error()}
	}
	captureResult, err := w.runtime.CaptureURLWithMetadataAndCookiesAndOptions(ctx, input.URL, outputPath, browserCookies, capture.CaptureOptions{Selector: input.CSSSelector, Selectors: input.CSSSelectors, LabelHint: input.LabelHint})
	if err != nil {
		if errors.Is(err, capture.ErrLoginPage) {
			return jobs.Result{ErrorCode: "zajuna_session_expired", ErrorMessage: "Zajuna redirigió la captura a la pantalla de login"}
		}
		if errors.Is(err, capture.ErrChallengePage) {
			return jobs.Result{ErrorCode: "zajuna_challenge_required", ErrorMessage: "Zajuna pidió CAPTCHA o MFA; la captura no se automatiza"}
		}
		return jobs.Result{ErrorCode: "capture_failed", ErrorMessage: err.Error(), Retryable: true}
	}
	if input.Authenticated && isZajunaLoginURL(captureResult.FinalURL) {
		return jobs.Result{ErrorCode: "zajuna_session_expired", ErrorMessage: "Zajuna redirigió la captura a la pantalla de login"}
	}
	if _, err := security.ValidateHTTPURL(captureResult.FinalURL, w.allowedOrigins, len(w.allowedOrigins) == 0); err != nil {
		return jobs.Result{ErrorCode: "invalid_capture_redirect", ErrorMessage: "la captura terminó fuera del origen permitido"}
	}
	hash, err := fileSHA256(outputPath)
	if err != nil {
		return jobs.Result{ErrorCode: "capture_hash_failed", ErrorMessage: err.Error()}
	}
	evidenceID := ""
	if w.store != nil {
		metadata, _ := json.Marshal(map[string]any{"url": security.RedactURL(input.URL), "finalUrl": security.RedactURL(captureResult.FinalURL), "title": captureResult.Title, "selector": captureResult.Selector, "selectorFallbacks": input.CSSSelectors, "selectorMatched": captureResult.SelectorMatched, "labelHint": input.LabelHint, "authenticated": input.Authenticated, "jobId": job.ID})
		name := input.Name
		if name == "" {
			name = filepath.Base(outputPath)
		}
		evidenceID = artifactID("evidence", input.FichaID, input.ItemCode, hash)
		if err := w.store.CreateEvidence(ctx, evidence.Record{ID: evidenceID, FichaID: input.FichaID, ItemCode: input.ItemCode, SlotNumber: input.SlotNumber, Name: name, FilePath: outputPath, Format: "png", Source: "capture-browser", SHA256: hash, Metadata: metadata, CapturedAt: time.Now().UTC()}); err != nil {
			return jobs.Result{ErrorCode: "evidence_persist_failed", ErrorMessage: err.Error()}
		}
	}
	if err := reporter.Progress(ctx, "capture", 100, "Captura guardada localmente"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	return jobs.Result{Output: map[string]any{"url": security.RedactURL(input.URL), "finalUrl": security.RedactURL(captureResult.FinalURL), "path": outputPath, "format": "png", "sha256": hash, "evidenceId": evidenceID, "selector": captureResult.Selector, "selectorFallbacks": input.CSSSelectors, "selectorMatched": captureResult.SelectorMatched, "labelHint": input.LabelHint, "authenticated": input.Authenticated}}
}

func isZajunaLoginURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "/login/") || strings.Contains(lower, "login_user")
}

func (w *CaptureBrowserWorker) outputPath(jobID, requested string) (string, error) {
	root := filepath.Join(w.dataDir, "evidences", "browser")
	if requested == "" {
		return filepath.Join(root, jobID+".png"), nil
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.dataDir, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("no se pudo resolver la salida de captura: %w", err)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("no se pudo resolver la carpeta de evidencias: %w", err)
	}
	relative, err := filepath.Rel(absoluteRoot, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("la salida de captura debe estar dentro de evidences/browser")
	}
	if filepath.Ext(absolute) == "" {
		absolute += ".png"
	}
	return absolute, nil
}
