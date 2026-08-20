package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zajuna-app/core/internal/capture"
	"github.com/zajuna-app/core/internal/checklist"
	"github.com/zajuna-app/core/internal/coursemaps"
	"github.com/zajuna-app/core/internal/evidence"
	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/secrets"
	"github.com/zajuna-app/core/internal/security"
	"github.com/zajuna-app/core/internal/storage/sqlite"
	"github.com/zajuna-app/core/internal/zajuna"
)

const CaptureChecklistWorkerID = "capture-checklist"

type CaptureChecklistInput struct {
	FichaID      string   `json:"fichaId"`
	Username     string   `json:"username"`
	DocumentType string   `json:"documentType"`
	ItemCodes    []string `json:"itemCodes,omitempty"`
	MaxTargets   int      `json:"maxTargets,omitempty"`
}

type checklistCaptureFichaStore interface {
	GetFicha(context.Context, string) (sqlite.FichaRecord, error)
}

type checklistActivitySelectionStore interface {
	ListSelectedActivityIDs(context.Context, string) (map[string]bool, error)
}

type checklistRouteReviewStore interface {
	ListRouteReviews(context.Context, string) ([]checklist.RouteReview, error)
}

type CaptureChecklistWorker struct {
	runtime     capture.Runtime
	dataDir     string
	client      authenticatedCaptureClient
	credentials secrets.Store
	mapStore    coursemaps.Store
	fichaStore  checklistCaptureFichaStore
	evidence    evidence.Store
}

func NewCaptureChecklistWorker(runtime capture.Runtime, dataDir string, client authenticatedCaptureClient, credentials secrets.Store, mapStore coursemaps.Store, fichaStore checklistCaptureFichaStore, evidenceStore evidence.Store) (*CaptureChecklistWorker, error) {
	if strings.TrimSpace(dataDir) == "" || client == nil || credentials == nil || mapStore == nil || fichaStore == nil || evidenceStore == nil {
		return nil, errors.New("capture checklist worker requires runtime, data directory, client, credentials and stores")
	}
	return &CaptureChecklistWorker{
		runtime: runtime, dataDir: dataDir, client: client, credentials: credentials,
		mapStore: mapStore, fichaStore: fichaStore, evidence: evidenceStore,
	}, nil
}

func (w *CaptureChecklistWorker) ID() string { return CaptureChecklistWorkerID }

func (w *CaptureChecklistWorker) Execute(ctx context.Context, job jobs.Job, reporter jobs.Reporter) jobs.Result {
	var input CaptureChecklistInput
	if err := json.Unmarshal(job.Input, &input); err != nil {
		return jobs.Result{ErrorCode: "invalid_input", ErrorMessage: "la entrada de captura del checklist no es válida"}
	}
	input.FichaID = strings.TrimSpace(input.FichaID)
	input.Username = strings.TrimSpace(input.Username)
	input.DocumentType = strings.TrimSpace(input.DocumentType)
	if input.FichaID == "" || input.Username == "" {
		return jobs.Result{ErrorCode: "invalid_input", ErrorMessage: "fichaId y usuario de Zajuna son obligatorios"}
	}
	if input.DocumentType == "" {
		input.DocumentType = "CC"
	}
	if !w.runtime.Installed() {
		return jobs.Result{ErrorCode: "browser_not_installed", ErrorMessage: fmt.Sprintf("runtime Chromium no instalado en %s; ejecuta npm run browser:install", w.runtime.Root)}
	}

	ficha, err := w.fichaStore.GetFicha(ctx, input.FichaID)
	if err != nil {
		return jobs.Result{ErrorCode: "ficha_not_found", ErrorMessage: "no se encontró la ficha local seleccionada"}
	}
	record, err := w.mapStore.GetCourseMap(ctx, ficha.CourseID)
	if err != nil {
		return jobs.Result{ErrorCode: "course_map_not_found", ErrorMessage: "la ficha todavía no tiene un mapa de rutas; ejecuta Buscar rutas primero"}
	}
	selectedActivityIDs := map[string]bool(nil)
	selectionStore, hasSelectionStore := w.fichaStore.(checklistActivitySelectionStore)
	if hasSelectionStore {
		selectedActivityIDs, err = selectionStore.ListSelectedActivityIDs(ctx, input.FichaID)
		if err != nil {
			return jobs.Result{ErrorCode: "activity_selection_read_failed", ErrorMessage: fmt.Sprintf("no se pudieron leer las actividades seleccionadas: %v", err), Retryable: true}
		}
	}
	if hasSelectionStore && len(selectedActivityIDs) == 0 && captureRequiresActivitySelection(input.ItemCodes) {
		return jobs.Result{ErrorCode: "activities_not_selected", ErrorMessage: "selecciona primero las actividades que pertenecen al instructor para filtrar fechas y evidencias"}
	}
	targets, summary, err := checklist.BuildCaptureTargetsForActivities(record, selectedActivityIDs)
	if err != nil {
		return jobs.Result{ErrorCode: "checklist_map_invalid", ErrorMessage: err.Error()}
	}
	if reviewStore, ok := w.fichaStore.(checklistRouteReviewStore); ok {
		reviews, reviewErr := reviewStore.ListRouteReviews(ctx, input.FichaID)
		if reviewErr != nil {
			return jobs.Result{ErrorCode: "route_review_read_failed", ErrorMessage: "no se pudieron leer las revisiones de rutas", Retryable: true}
		}
		targets = checklist.ApplyRouteReviews(targets, reviews)
	} else {
		targets = checklist.ApplyRouteReviews(targets, nil)
	}
	targets = filterCaptureTargets(targets, input.ItemCodes)
	if input.MaxTargets > 0 && len(targets) > input.MaxTargets {
		targets = targets[:input.MaxTargets]
	}
	summary.CaptureUnitCount = len(targets)
	summary.CoverageCount = captureTargetCoverageCount(targets)
	if len(targets) == 0 {
		return jobs.Result{ErrorCode: "checklist_map_empty", ErrorMessage: "el mapa no tiene rutas asociadas a los items seleccionados del checklist"}
	}
	if err := reporter.Progress(ctx, "credentials", 5, "Preparando captura dirigida por checklist"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	password, err := w.credentials.Get(input.Username)
	if err != nil || password == "" {
		return jobs.Result{ErrorCode: "credential_unavailable", ErrorMessage: "no se encontró la contraseña de Zajuna en el almacén seguro"}
	}
	if err := reporter.Progress(ctx, "login", 12, "Validando sesión de Zajuna para las evidencias"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	session, err := w.client.Login(ctx, zajuna.Credentials{DocumentType: input.DocumentType, Document: input.Username, Password: password})
	if err != nil {
		return jobs.Result{Retryable: retryableZajunaError(err), ErrorCode: "zajuna_login_failed", ErrorMessage: fmt.Sprintf("no se pudo iniciar sesión para el checklist: %v", err)}
	}
	baseURL, err := url.Parse(session.BaseURL)
	if err != nil || baseURL.Host == "" {
		return jobs.Result{ErrorCode: "invalid_zajuna_session", ErrorMessage: "la sesión de Zajuna no tiene un origen válido"}
	}
	var browserSession *capture.BrowserSession
	if strings.EqualFold(baseURL.Hostname(), "zajuna.sena.edu.co") {
		loginURL := *baseURL
		loginURL.Path = "/zajuna/login/index.php"
		loginURL.RawQuery = ""
		browserSession, err = w.runtime.OpenBrowserSession(ctx, capture.BrowserCredentials{
			LoginURL:     loginURL.String(),
			DocumentType: input.DocumentType,
			Document:     input.Username,
			Password:     password,
		})
		if err != nil {
			return jobs.Result{Retryable: retryableZajunaError(err) && !errors.Is(err, capture.ErrBlockedPage), ErrorCode: "zajuna_browser_login_failed", ErrorMessage: fmt.Sprintf("no se pudo iniciar sesión en Chromium para el checklist: %v", err)}
		}
		defer browserSession.Close()
	}
	ownerName := ""
	if browserSession != nil && checklist.RequiresInstructorIdentity(targets) {
		if err := reporter.Progress(ctx, "identity", 16, "Identificando al instructor autenticado para filtrar foros y anuncios"); err != nil {
			return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
		}
		ownerName, err = browserSession.AuthenticatedOwnerName(ctx, record.ProfileURL)
		if err != nil {
			return jobs.Result{ErrorCode: "instructor_identity_unavailable", ErrorMessage: fmt.Sprintf("no se pudo verificar el instructor autenticado: %v", err)}
		}
	}

	captured := 0
	evidenceRecords := 0
	failed := 0
	failures := make([]string, 0)
	targetItemCodes := make(map[string]bool)
	for _, target := range targets {
		for _, itemCode := range coveredItemCodes(target) {
			targetItemCodes[itemCode] = true
		}
	}
	for index, target := range targets {
		if err := ctx.Err(); err != nil {
			return jobs.Result{ErrorCode: "capture_cancelled", ErrorMessage: err.Error()}
		}
		percent := 18 + ((index * 76) / len(targets))
		if err := reporter.Progress(ctx, "capture", percent, fmt.Sprintf("Capturando %s · evidencia %d de %d", target.ItemCode, target.SlotNumber, len(targets))); err != nil {
			return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
		}
		parsedTarget, parseErr := security.ValidateHTTPURL(target.URL, []string{baseURL.String()}, false)
		if parseErr != nil || parsedTarget.Host == "" || parsedTarget.Scheme != baseURL.Scheme || parsedTarget.Host != baseURL.Host {
			failed++
			failures = append(failures, target.ItemCode+": origen de URL no permitido")
			continue
		}
		outputPath := filepath.Join(w.dataDir, "evidences", "checklist", safePathPart(input.FichaID), safePathPart(target.ItemCode), fmt.Sprintf("slot-%d.png", target.SlotNumber))
		var captureResult capture.CaptureResult
		var captureErr error
		options := capture.CaptureOptions{Selector: target.CSSSelector, Selectors: target.CSSSelectorFallbacks, RevealSelectors: target.RevealSelectors, HideSelectors: target.HideSelectors, ViewportWidth: target.ViewportWidth, ViewportHeight: target.ViewportHeight, FullPage: target.FullPage, LabelHint: target.LabelHint, OwnerName: ownerName, RequireSelector: target.RequireSelector, OwnerOnly: target.OwnerOnly}
		if browserSession != nil {
			captureResult, captureErr = browserSession.CaptureURLWithMetadataAndOptions(ctx, target.URL, outputPath, options)
		} else {
			cookies := make([]capture.BrowserCookie, 0)
			for _, cookie := range session.CookiesForURL(target.URL) {
				converted, convErr := capture.BrowserCookieForTarget(cookie, parsedTarget)
				if convErr != nil {
					continue
				}
				cookies = append(cookies, converted)
			}
			if len(cookies) == 0 {
				failed++
				failures = append(failures, target.ItemCode+": sesión sin cookies para la ruta")
				continue
			}
			captureResult, captureErr = w.runtime.CaptureURLWithMetadataAndCookiesAndOptions(ctx, target.URL, outputPath, cookies, options)
		}
		if captureErr != nil {
			failed++
			if errors.Is(captureErr, capture.ErrLoginPage) {
				failures = append(failures, target.ItemCode+": sesión de Zajuna expirada o página de login")
			} else if errors.Is(captureErr, capture.ErrChallengePage) {
				failures = append(failures, target.ItemCode+": Zajuna pidió CAPTCHA o MFA")
			} else {
				failures = append(failures, target.ItemCode+": "+captureErr.Error())
			}
			continue
		}
		if isZajunaLoginURL(captureResult.FinalURL) {
			failed++
			failures = append(failures, target.ItemCode+": Zajuna redirigió a login")
			continue
		}
		if _, finalErr := security.ValidateHTTPURL(captureResult.FinalURL, []string{baseURL.String()}, false); finalErr != nil {
			failed++
			failures = append(failures, target.ItemCode+": redirección fuera del origen permitido")
			continue
		}
		hash, hashErr := fileSHA256(outputPath)
		if hashErr != nil {
			failed++
			failures = append(failures, target.ItemCode+": no se pudo calcular el hash")
			continue
		}
		metadata, _ := json.Marshal(map[string]any{
			"url": security.RedactURL(target.URL), "finalUrl": security.RedactURL(captureResult.FinalURL), "title": security.RedactText(captureResult.Title), "routeKey": target.RouteKey, "reviewStatus": target.ReviewStatus,
			"selector": captureResult.Selector, "selectorFallbacks": target.CSSSelectorFallbacks, "selectorMatched": captureResult.SelectorMatched,
			"labelHint": target.LabelHint, "routeKind": target.RouteKind, "groupName": target.GroupName, "revealSelectors": target.RevealSelectors, "hideSelectors": target.HideSelectors, "viewportWidth": target.ViewportWidth, "viewportHeight": target.ViewportHeight, "fullPage": target.FullPage, "phaseSection": target.PhaseSection, "jobId": job.ID,
			"activityId": target.ActivityID, "activityTitle": target.ActivityTitle, "technical": target.Technical, "ownerOnly": target.OwnerOnly,
			"coveredItemCodes": coveredItemCodes(target), "captureUnitKey": target.RouteKey,
		})
		capturedAt := time.Now().UTC()
		for _, itemCode := range coveredItemCodes(target) {
			evidenceID := artifactID("evidence", input.FichaID, itemCode+"#"+strconv.Itoa(target.SlotNumber), "")
			if err := w.evidence.CreateEvidence(ctx, evidence.Record{
				ID: evidenceID, FichaID: input.FichaID, ItemCode: itemCode, SlotNumber: target.SlotNumber,
				Name: target.Name, FilePath: outputPath, Format: "png", Source: "capture-checklist", SHA256: hash,
				Metadata: metadata, CapturedAt: capturedAt,
			}); err != nil {
				failed++
				failures = append(failures, itemCode+": no se pudo registrar la evidencia")
				continue
			}
			evidenceRecords++
			_ = reporter.Event(ctx, "evidence_alias_created", "Criterio asociado a la misma captura", map[string]any{"itemCode": itemCode, "slotNumber": target.SlotNumber, "evidenceId": evidenceID, "captureUnitKey": target.RouteKey})
		}
		captured++
		_ = reporter.Event(ctx, "evidence_captured", "Evidencia guardada", map[string]any{"itemCode": target.ItemCode, "coveredItemCodes": coveredItemCodes(target), "slotNumber": target.SlotNumber, "selectorMatched": captureResult.SelectorMatched})
	}
	groupCount := 0
	if groupStore, ok := w.evidence.(evidence.GroupStore); ok {
		groups, groupErr := groupStore.RebuildEvidenceGroups(ctx, input.FichaID)
		if groupErr != nil {
			return jobs.Result{ErrorCode: "evidence_group_failed", ErrorMessage: fmt.Sprintf("no se pudieron construir los grupos de evidencia: %v", groupErr), Retryable: true}
		}
		groupCount = len(groups)
		_ = reporter.Event(ctx, "evidence_groups_rebuilt", "Evidencias agrupadas para evitar duplicados", map[string]any{"fichaId": input.FichaID, "groupCount": groupCount})
	}
	if err := reporter.Progress(ctx, "completed", 100, fmt.Sprintf("Captura dirigida terminada: %d guardadas, %d con error", captured, failed)); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	if failed > 0 {
		message := fmt.Sprintf("captura incompleta: %d guardadas, %d con error", captured, failed)
		if len(failures) > 0 {
			message += ". Primer error: " + failures[0]
		}
		return jobs.Result{ErrorCode: "capture_partial_failure", ErrorMessage: message}
	}
	return jobs.Result{Output: map[string]any{
		"fichaId": input.FichaID, "courseId": ficha.CourseID, "targets": len(targets), "captured": captured,
		"failed": failed, "unresolved": summary.UnresolvedItems, "slotCount": len(targets), "captureUnitCount": len(targets), "coverageCount": evidenceRecords,
		"targetItems": len(targetItemCodes), "itemCount": summary.ItemCount, "groupCount": groupCount, "failures": failures,
	}}
}

func captureRequiresActivitySelection(itemCodes []string) bool {
	if len(itemCodes) == 0 {
		return true
	}
	for _, itemCode := range itemCodes {
		switch strings.TrimSpace(itemCode) {
		case "6.1", "10.1.1", "10.1.2":
			return true
		}
	}
	return false
}

func filterCaptureTargets(targets []checklist.CaptureTarget, itemCodes []string) []checklist.CaptureTarget {
	if len(itemCodes) == 0 {
		return targets
	}
	allowed := make(map[string]bool, len(itemCodes))
	for _, code := range itemCodes {
		if code = strings.TrimSpace(code); code != "" {
			allowed[code] = true
		}
	}
	filtered := make([]checklist.CaptureTarget, 0, len(targets))
	for _, target := range targets {
		if allowed[target.ItemCode] || anyAllowedCoverage(target, allowed) {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func coveredItemCodes(target checklist.CaptureTarget) []string {
	if len(target.CoveredItemCodes) == 0 {
		return []string{target.ItemCode}
	}
	return target.CoveredItemCodes
}

func anyAllowedCoverage(target checklist.CaptureTarget, allowed map[string]bool) bool {
	for _, itemCode := range coveredItemCodes(target) {
		if allowed[itemCode] {
			return true
		}
	}
	return false
}

func captureTargetCoverageCount(targets []checklist.CaptureTarget) int {
	count := 0
	for _, target := range targets {
		count += len(coveredItemCodes(target))
	}
	return count
}

func safePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	value = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "..", "_").Replace(value)
	return value
}
