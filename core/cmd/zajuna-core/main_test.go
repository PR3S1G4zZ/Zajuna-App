package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zajuna-app/core/internal/evidence"
	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/reports"
	"github.com/zajuna-app/core/internal/storage/backup"
	"github.com/zajuna-app/core/internal/storage/sqlite"
	"github.com/zajuna-app/core/internal/workers"
	"github.com/zajuna-app/core/internal/zajuna"
)

type memoryCredentialStore struct {
	passwords map[string]string
}

func (s *memoryCredentialStore) Set(user, password string) error {
	if s.passwords == nil {
		s.passwords = map[string]string{}
	}
	s.passwords[user] = password
	return nil
}

func (s *memoryCredentialStore) Get(user string) (string, error) {
	return s.passwords[user], nil
}

func TestHealthAndSetupStatus(t *testing.T) {
	server := httptest.NewServer(newRouter(t.TempDir()))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected health status: %d", response.StatusCode)
	}

	var health map[string]string
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatalf("health response is not JSON: %v", err)
	}
	if health["status"] != "ok" {
		t.Fatalf("unexpected health response: %#v", health)
	}

	setupResponse, err := server.Client().Get(server.URL + "/api/setup/status")
	if err != nil {
		t.Fatalf("setup status request failed: %v", err)
	}
	defer setupResponse.Body.Close()
	if setupResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected setup status: %d", setupResponse.StatusCode)
	}

	body, err := io.ReadAll(setupResponse.Body)
	if err != nil {
		t.Fatalf("could not read setup response: %v", err)
	}
	if !strings.Contains(string(body), `"setupComplete":false`) {
		t.Fatalf("setup should be incomplete: %s", body)
	}
}

func TestDashboardIsServedFromCore(t *testing.T) {
	server := httptest.NewServer(newRouter(t.TempDir()))
	defer server.Close()
	client := *server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected dashboard status: %d location=%s", response.StatusCode, response.Header.Get("Location"))
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "<div id=\"root\"></div>") || !strings.Contains(string(contents), "type=\"module\"") {
		t.Fatal("core did not serve the embedded React shell")
	}
}

func TestReactDeepLinkUsesEmbeddedShell(t *testing.T) {
	server := httptest.NewServer(newRouter(t.TempDir()))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/checklist/1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected deep-link status: %d", response.StatusCode)
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "<div id=\"root\"></div>") {
		t.Fatal("deep link did not receive the embedded React shell")
	}
}

func TestStaticFallbackDoesNotMaskMissingAssetsOrAPI(t *testing.T) {
	server := httptest.NewServer(newRouter(t.TempDir()))
	defer server.Close()

	for _, route := range []string{"/assets/missing.js", "/api/missing"} {
		response, err := server.Client().Get(server.URL + route)
		if err != nil {
			t.Fatalf("request to %s failed: %v", route, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("expected %s to remain a 404, got %d", route, response.StatusCode)
		}
	}
}

func TestSetupPersistsUsername(t *testing.T) {
	dataDir := t.TempDir()
	credentials := &memoryCredentialStore{}
	server := httptest.NewServer(newRouterWithCredentialStore(dataDir, credentials))
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/setup", strings.NewReader(`{"zajunaUsername":"instructor-demo","zajunaPassword":"secret"}`))
	if err != nil {
		t.Fatalf("could not create setup request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("setup request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected setup response: %d", response.StatusCode)
	}

	config, err := readConfig(dataDir)
	if err != nil {
		t.Fatalf("could not read persisted config: %v", err)
	}
	if !config.SetupComplete || !config.CredentialsStored || config.ZajunaUsername != "instructor-demo" {
		t.Fatalf("unexpected persisted config: %#v", config)
	}
	if credentials.passwords["instructor-demo"] != "secret" {
		t.Fatalf("credential was not stored in the credential store")
	}
	contents, err := os.ReadFile(configPath(dataDir))
	if err != nil {
		t.Fatalf("could not read config: %v", err)
	}
	if strings.Contains(string(contents), "secret") {
		t.Fatalf("password must not be written to config.json")
	}
}

func TestSettingsAndDiagnosticsStayLocal(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credentials := &memoryCredentialStore{}
	server := httptest.NewServer(newRouterWithServices(dataDir, credentials, nil, store, nil))
	defer server.Close()

	settingsResponse, err := server.Client().Get(server.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	if settingsResponse.StatusCode != http.StatusOK {
		settingsResponse.Body.Close()
		t.Fatalf("unexpected settings status: %d", settingsResponse.StatusCode)
	}
	settingsResponse.Body.Close()

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/settings", strings.NewReader(`{"session":{"autoRenew":false},"capture":{"fullPage":false,"reuseSession":true,"motion":false},"notifications":{"jobCompleted":true,"needsReview":false},"storage":{"retentionKeep":7,"retentionDays":45}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected settings save status: %d", response.StatusCode)
	}
	stored, err := store.GetAppSetting(context.Background(), appSettingsKey)
	if err != nil || !strings.Contains(stored, `"autoRenew":false`) || !strings.Contains(stored, `"retentionKeep":7`) {
		t.Fatalf("settings were not persisted: %q, %v", stored, err)
	}

	if err := writeConfig(dataDir, appConfig{SetupComplete: true, ZajunaUsername: "local-user", CredentialsStored: true}); err != nil {
		t.Fatal(err)
	}
	diagnosticsResponse, err := server.Client().Get(server.URL + "/api/diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	defer diagnosticsResponse.Body.Close()
	if diagnosticsResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected diagnostics status: %d", diagnosticsResponse.StatusCode)
	}
	contents, err := io.ReadAll(diagnosticsResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "super-secret-value") || !strings.Contains(string(contents), `"checks"`) {
		t.Fatalf("diagnostics leaked data or had an invalid shape: %s", contents)
	}
}

func TestNotificationsAreExposedWithoutSensitiveJobMessages(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-notification-api", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkRunning(context.Background(), "job-notification-api"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteJob(context.Background(), "job-notification-api", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRouterWithServices(dataDir, &memoryCredentialStore{}, nil, store, nil))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected notifications status: %d", response.StatusCode)
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "Trabajo completado") || strings.Contains(string(contents), "password") {
		t.Fatalf("unexpected or sensitive notifications response: %s", contents)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/notifications/read-all", nil)
	if err != nil {
		t.Fatal(err)
	}
	readResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	readResponse.Body.Close()
	if readResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected mark all status: %d", readResponse.StatusCode)
	}
}

func TestWriteEndpointFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endpoint.json")
	if err := writeEndpointFile(path, endpointInfo{URL: "http://127.0.0.1:4321", Port: 4321}); err != nil {
		t.Fatalf("could not write endpoint file: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read endpoint file: %v", err)
	}
	var endpoint endpointInfo
	if err := json.Unmarshal(contents, &endpoint); err != nil {
		t.Fatalf("endpoint file is not JSON: %v", err)
	}
	if endpoint.URL != "http://127.0.0.1:4321" || endpoint.Port != 4321 {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
}

type apiTestWorker struct{}

type apiZajunaClient struct{}

func (apiZajunaClient) Login(context.Context, zajuna.Credentials) (zajuna.Session, error) {
	return zajuna.Session{}, nil
}

func (apiZajunaClient) ListFichas(context.Context, zajuna.Session) ([]zajuna.Ficha, error) {
	return []zajuna.Ficha{{ExternalID: "3135429", Name: "Ficha de prueba", CourseID: "41080"}}, nil
}

func (apiTestWorker) ID() string { return "api-test" }

func (apiTestWorker) Execute(ctx context.Context, _ jobs.Job, reporter jobs.Reporter) jobs.Result {
	if err := reporter.Progress(ctx, "testing", 50, "Ejecutando job de prueba"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	return jobs.Result{Output: map[string]bool{"ok": true}}
}

func TestJobsAPIExecutesAndExposesEvents(t *testing.T) {
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime, err := jobs.NewRuntime(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(apiTestWorker{}); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()

	server := httptest.NewServer(newRouterWithServices(t.TempDir(), &memoryCredentialStore{}, runtime, store, nil))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/jobs", strings.NewReader(`{"type":"api-test","input":{"fixture":"ok"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("unexpected create status: %d", response.StatusCode)
	}
	var created jobView
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statusResponse, err := server.Client().Get(server.URL + "/api/jobs/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		var current jobView
		decodeErr := json.NewDecoder(statusResponse.Body).Decode(&current)
		statusResponse.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if current.Status == jobs.StatusCompleted {
			listResponse, err := server.Client().Get(server.URL + "/api/jobs?limit=5")
			if err != nil {
				t.Fatal(err)
			}
			var listed []jobView
			if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
				listResponse.Body.Close()
				t.Fatal(err)
			}
			listResponse.Body.Close()
			if len(listed) != 1 || listed[0].ID != created.ID {
				t.Fatalf("unexpected jobs listing: %#v", listed)
			}
			eventsResponse, err := server.Client().Get(server.URL + "/api/jobs/" + created.ID + "/events")
			if err != nil {
				t.Fatal(err)
			}
			var events []jobs.Event
			if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
				eventsResponse.Body.Close()
				t.Fatal(err)
			}
			eventsResponse.Body.Close()
			if len(events) < 2 {
				t.Fatalf("expected queued and progress events, got %d", len(events))
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not complete before timeout")
}

func TestFichasAPIListsLocalRecords(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.UpsertFichas(context.Background(), []zajuna.Ficha{{ExternalID: "3135429", Name: "Ficha de prueba", CourseID: "41080"}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRouterWithServices(dataDir, &memoryCredentialStore{}, nil, store, nil))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/fichas")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected fichas status: %d", response.StatusCode)
	}
	var fichas []fichaView
	if err := json.NewDecoder(response.Body).Decode(&fichas); err != nil {
		t.Fatal(err)
	}
	if len(fichas) != 1 || fichas[0].ExternalID != "3135429" {
		t.Fatalf("unexpected fichas response: %#v", fichas)
	}
}

func TestChecklistAPIExposesActiveFichaAndStatus(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.UpsertFichas(context.Background(), []zajuna.Ficha{{ExternalID: "3135429", Name: "Programa de prueba", CourseID: "41080"}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRouterWithServices(dataDir, &memoryCredentialStore{}, nil, store, nil))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/checklist/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard checklistDashboardView
	if err := json.NewDecoder(response.Body).Decode(&dashboard); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(dashboard.Items) != 62 || dashboard.Summary.Pending != 62 {
		t.Fatalf("unexpected checklist dashboard: status=%d items=%d summary=%#v", response.StatusCode, len(dashboard.Items), dashboard.Summary)
	}

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/checklist/items/1.1.1/status", strings.NewReader(`{"status":"SI"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var updated checklistDashboardView
	if err := json.NewDecoder(response.Body).Decode(&updated); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || updated.Summary.Yes != 1 || updated.Summary.Pending != 61 {
		t.Fatalf("unexpected updated checklist: status=%d summary=%#v", response.StatusCode, updated.Summary)
	}
	detailResponse, err := server.Client().Get(server.URL + "/api/checklist/items/1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	defer detailResponse.Body.Close()
	var detail checklistItemDetailView
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detailResponse.StatusCode != http.StatusOK || detail.Item.Status != "SI" || len(detail.Events) != 1 {
		t.Fatalf("unexpected checklist detail: status=%d detail=%#v", detailResponse.StatusCode, detail)
	}
}

func TestZajunaConnectionAPIEnqueuesWorker(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credentials := &memoryCredentialStore{}
	if err := credentials.Set("123456", "secret"); err != nil {
		t.Fatal(err)
	}
	runtime, err := jobs.NewRuntime(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := workers.NewTestZajunaConnectionWorker(apiZajunaClient{}, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(worker); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	if err := writeConfig(dataDir, appConfig{SetupComplete: true, ZajunaUsername: "123456", CredentialsStored: true}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRouterWithServices(dataDir, credentials, runtime, store, nil))
	defer server.Close()

	response, err := server.Client().Post(server.URL+"/api/zajuna/test-connection", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("unexpected connection test status: %d", response.StatusCode)
	}
	var created jobView
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statusResponse, err := server.Client().Get(server.URL + "/api/jobs/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		var current jobView
		decodeErr := json.NewDecoder(statusResponse.Body).Decode(&current)
		statusResponse.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if current.Status == jobs.StatusCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("connection test job did not complete before timeout")
}

func TestSchedulesAndBackupsAPI(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backupManager, err := backup.NewManager(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newRouterWithServices(dataDir, &memoryCredentialStore{}, nil, store, backupManager))
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/schedules", strings.NewReader(`{"workerType":"sync-fichas","input":{"username":"demo"},"intervalSeconds":3600}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		response.Body.Close()
		t.Fatalf("unexpected schedule status: %d", response.StatusCode)
	}
	var schedule scheduleView
	if err := json.NewDecoder(response.Body).Decode(&schedule); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if schedule.WorkerType != "sync-fichas" || schedule.IntervalSeconds != 3600 {
		t.Fatalf("unexpected schedule: %#v", schedule)
	}

	backupResponse, err := server.Client().Post(server.URL+"/api/backups", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer backupResponse.Body.Close()
	if backupResponse.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected backup status: %d", backupResponse.StatusCode)
	}
	var record map[string]any
	if err := json.NewDecoder(backupResponse.Body).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record["name"] == nil || record["path"] != nil {
		t.Fatalf("backup response exposed an unsafe path or has no name: %#v", record)
	}
	backupsResponse, err := server.Client().Get(server.URL + "/api/backups")
	if err != nil {
		t.Fatal(err)
	}
	defer backupsResponse.Body.Close()
	var backups []backupView
	if err := json.NewDecoder(backupsResponse.Body).Decode(&backups); err != nil {
		t.Fatal(err)
	}
	if backupsResponse.StatusCode != http.StatusOK || len(backups) != 1 || backups[0].Name == "" || backups[0].SizeBytes <= 0 {
		t.Fatalf("unexpected backup list: status=%d records=%#v", backupsResponse.StatusCode, backups)
	}
	backupName := backups[0].Name
	downloadResponse, err := server.Client().Get(server.URL + "/api/backups/" + backupName + "/download")
	if err != nil {
		t.Fatal(err)
	}
	if downloadResponse.StatusCode != http.StatusOK || downloadResponse.Header.Get("Content-Type") != "application/zip" {
		downloadResponse.Body.Close()
		t.Fatalf("unexpected backup download: status=%d content-type=%q", downloadResponse.StatusCode, downloadResponse.Header.Get("Content-Type"))
	}
	downloadResponse.Body.Close()
	restoreRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/backups/"+backupName+"/restore", nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreResponse, err := server.Client().Do(restoreRequest)
	if err != nil {
		t.Fatal(err)
	}
	var restore restoreView
	if err := json.NewDecoder(restoreResponse.Body).Decode(&restore); err != nil {
		restoreResponse.Body.Close()
		t.Fatal(err)
	}
	restoreResponse.Body.Close()
	if restoreResponse.StatusCode != http.StatusAccepted || !restore.Staged || !restore.RestartRequired || restore.SafetyBackup == "" {
		t.Fatalf("unexpected restore response: status=%d restore=%#v", restoreResponse.StatusCode, restore)
	}
	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/api/backups/"+backupName, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteResponse, err := server.Client().Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected backup delete status: %d", deleteResponse.StatusCode)
	}
	cleanupResponse, err := server.Client().Post(server.URL+"/api/backups/cleanup", "application/json", strings.NewReader(`{"keep":1,"olderThanDays":1}`))
	if err != nil {
		t.Fatal(err)
	}
	cleanupResponse.Body.Close()
	if cleanupResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected backup cleanup status: %d", cleanupResponse.StatusCode)
	}
	if err := store.CreateEvidence(context.Background(), evidence.Record{ID: "api-evidence", Name: "fixture.html", FilePath: filepath.Join(dataDir, "evidences", "fixture.html"), Format: "html", SHA256: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateReport(context.Background(), reports.Record{ID: "api-report", Name: "Reporte", FilePath: filepath.Join(dataDir, "reports", "report.html"), Format: "html", SHA256: "def"}); err != nil {
		t.Fatal(err)
	}
	evidenceResponse, err := server.Client().Get(server.URL + "/api/evidences")
	if err != nil {
		t.Fatal(err)
	}
	var evidences []evidenceView
	if err := json.NewDecoder(evidenceResponse.Body).Decode(&evidences); err != nil {
		evidenceResponse.Body.Close()
		t.Fatal(err)
	}
	evidenceResponse.Body.Close()
	if len(evidences) != 1 || evidences[0].ID != "api-evidence" {
		t.Fatalf("unexpected evidence API response: %#v", evidences)
	}
	reportResponse, err := server.Client().Get(server.URL + "/api/reports")
	if err != nil {
		t.Fatal(err)
	}
	var listedReports []reportView
	if err := json.NewDecoder(reportResponse.Body).Decode(&listedReports); err != nil {
		reportResponse.Body.Close()
		t.Fatal(err)
	}
	reportResponse.Body.Close()
	if len(listedReports) != 1 || listedReports[0].ID != "api-report" {
		t.Fatalf("unexpected report API response: %#v", listedReports)
	}
}
