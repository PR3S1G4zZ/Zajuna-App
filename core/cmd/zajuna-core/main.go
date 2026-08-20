package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zajuna-app/core/internal/capture"
	"github.com/zajuna-app/core/internal/coursemaps"
	"github.com/zajuna-app/core/internal/evidence"
	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/reports"
	"github.com/zajuna-app/core/internal/scheduler"
	"github.com/zajuna-app/core/internal/secrets"
	"github.com/zajuna-app/core/internal/storage/backup"
	"github.com/zajuna-app/core/internal/storage/sqlite"
	"github.com/zajuna-app/core/internal/workers"
	"github.com/zajuna-app/core/internal/zajuna"
)

//go:embed web
var webFiles embed.FS

const appVersion = "0.1.0"

type appConfig struct {
	SetupComplete      bool   `json:"setupComplete"`
	ZajunaUsername     string `json:"zajunaUsername,omitempty"`
	ZajunaDocumentType string `json:"zajunaDocumentType,omitempty"`
	CredentialsStored  bool   `json:"credentialsStored"`
}

type setupRequest struct {
	ZajunaUsername     string `json:"zajunaUsername"`
	ZajunaDocumentType string `json:"zajunaDocumentType"`
	ZajunaPassword     string `json:"zajunaPassword"`
}

type endpointInfo struct {
	URL  string `json:"url"`
	Port int    `json:"port"`
}

func main() {
	port := flag.String("port", os.Getenv("ZAJUNA_PORT"), "Puerto local; usa 0 para elegir uno libre")
	noBrowser := flag.Bool("no-browser", os.Getenv("ZAJUNA_NO_BROWSER") == "1", "No abrir el navegador automáticamente")
	endpointFile := flag.String("endpoint-file", "", "Archivo donde se publica el endpoint local")
	flag.Parse()
	if *port == "" {
		*port = "0"
	}

	dataDir, err := dataDirectory()
	if err != nil {
		log.Fatalf("no se pudo determinar la carpeta local de datos: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("no se pudo crear la carpeta local de datos: %v", err)
	}
	if restored, err := backup.ApplyPending(dataDir); err != nil {
		log.Fatalf("no se pudo aplicar la restauración pendiente: %v", err)
	} else if restored {
		log.Printf("restauración local aplicada antes de abrir SQLite")
	}
	localStore, err := sqlite.Open(dataDir)
	if err != nil {
		if rbErr := backup.RollbackApplied(dataDir); rbErr != nil {
			log.Fatalf("no se pudo abrir SQLite (%v) y el rollback falló (%v)", err, rbErr)
		}
		if retry, retryErr := sqlite.Open(dataDir); retryErr == nil {
			log.Printf("restauración revertida porque SQLite no abrió; se conservó la base anterior")
			localStore = retry
		} else {
			log.Fatalf("no se pudo abrir la base de datos local: %v", err)
		}
	} else if err := backup.CommitApplied(dataDir); err != nil {
		log.Printf("la base restaurada abrió, pero no se pudo confirmar el restore: %v", err)
	}
	defer localStore.Close()
	zajunaClient, err := zajuna.NewClient(zajuna.DefaultBaseURL)
	if err != nil {
		log.Fatalf("no se pudo configurar el cliente de Zajuna: %v", err)
	}
	jobRuntime, err := jobs.NewRuntime(localStore, 2)
	if err != nil {
		log.Fatalf("no se pudo crear el runtime de jobs: %v", err)
	}
	syncWorker, err := workers.NewSyncFichasWorker(zajunaClient, secrets.SystemStore{}, localStore)
	if err != nil {
		log.Fatalf("no se pudo registrar SyncFichasWorker: %v", err)
	}
	if err := jobRuntime.Register(syncWorker); err != nil {
		log.Fatalf("no se pudo registrar SyncFichasWorker: %v", err)
	}
	testConnectionWorker, err := workers.NewTestZajunaConnectionWorker(zajunaClient, secrets.SystemStore{})
	if err != nil {
		log.Fatalf("no se pudo crear TestZajunaConnectionWorker: %v", err)
	}
	if err := jobRuntime.Register(testConnectionWorker); err != nil {
		log.Fatalf("no se pudo registrar TestZajunaConnectionWorker: %v", err)
	}
	courseMapsWorker, err := workers.NewDiscoverCourseMapsWorker(zajunaClient, secrets.SystemStore{}, localStore)
	if err != nil {
		log.Fatalf("no se pudo crear DiscoverCourseMapsWorker: %v", err)
	}
	if err := jobRuntime.Register(courseMapsWorker); err != nil {
		log.Fatalf("no se pudo registrar DiscoverCourseMapsWorker: %v", err)
	}
	captureWorker, err := workers.NewAuthenticatedCaptureBrowserWorker(capture.Resolve(""), dataDir, zajunaClient, secrets.SystemStore{}, localStore)
	if err != nil {
		log.Fatalf("no se pudo crear CaptureBrowserWorker: %v", err)
	}
	captureWorker.SetAllowedOrigins(zajuna.DefaultBaseURL)
	if err := jobRuntime.Register(captureWorker); err != nil {
		log.Fatalf("no se pudo registrar CaptureBrowserWorker: %v", err)
	}
	checklistCaptureWorker, err := workers.NewCaptureChecklistWorker(capture.Resolve(""), dataDir, zajunaClient, secrets.SystemStore{}, localStore, localStore, localStore)
	if err != nil {
		log.Fatalf("no se pudo crear CaptureChecklistWorker: %v", err)
	}
	if err := jobRuntime.Register(checklistCaptureWorker); err != nil {
		log.Fatalf("no se pudo registrar CaptureChecklistWorker: %v", err)
	}
	htmlCaptureWorker, err := workers.NewCaptureEvidenceWorker(dataDir, localStore, zajuna.DefaultBaseURL)
	if err != nil {
		log.Fatalf("no se pudo crear CaptureEvidenceWorker: %v", err)
	}
	if err := jobRuntime.Register(htmlCaptureWorker); err != nil {
		log.Fatalf("no se pudo registrar CaptureEvidenceWorker: %v", err)
	}
	reportWorker, err := workers.NewExportReportWorker(dataDir, localStore, localStore, capture.Resolve(""))
	if err != nil {
		log.Fatalf("no se pudo crear ExportReportWorker: %v", err)
	}
	if err := jobRuntime.Register(reportWorker); err != nil {
		log.Fatalf("no se pudo registrar ExportReportWorker: %v", err)
	}
	jobRuntime.Start(context.Background())
	defer jobRuntime.Close()
	localScheduler, err := scheduler.New(localStore, jobRuntime, 30*time.Second)
	if err != nil {
		log.Fatalf("no se pudo crear el scheduler local: %v", err)
	}
	localScheduler.Start(context.Background())
	defer localScheduler.Close()
	backupManager, err := backup.NewManager(dataDir, localStore)
	if err != nil {
		log.Fatalf("no se pudo crear el gestor de copias locales: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		log.Fatalf("no se pudo abrir el puerto local: %v", err)
	}

	capability, err := newCapabilityToken()
	if err != nil {
		log.Fatalf("no se pudo crear la capacidad local del proceso: %v", err)
	}
	server := &http.Server{
		Handler:           protectLocalAPI(newRouterWithServices(dataDir, secrets.SystemStore{}, jobRuntime, localStore, backupManager), capability),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	url := "http://" + listener.Addr().String()
	log.Printf("Zajuna App local disponible en %s", url)
	if *endpointFile != "" {
		if err := writeEndpointFile(*endpointFile, endpointInfo{URL: url, Port: listener.Addr().(*net.TCPAddr).Port}); err != nil {
			log.Fatalf("no se pudo publicar el endpoint local: %v", err)
		}
		defer os.Remove(*endpointFile)
	}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("servidor local detenido con error: %v", err)
		}
	}()

	if err := waitUntilReady(url, 5*time.Second); err != nil {
		log.Printf("el núcleo local no respondió a tiempo: %v", err)
	} else if !*noBrowser {
		if err := openBrowser(url); err != nil {
			log.Printf("no se pudo abrir el navegador automáticamente: %v", err)
			log.Printf("abre manualmente %s", url)
		}
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("cierre forzado del servidor local: %v", err)
	}
}

func newRouter(dataDir string) http.Handler {
	return newRouterWithCredentialStore(dataDir, secrets.SystemStore{})
}

func newRouterWithCredentialStore(dataDir string, credentials secrets.Store) http.Handler {
	return newRouterWithRuntime(dataDir, credentials, nil)
}

func newRouterWithRuntime(dataDir string, credentials secrets.Store, jobRuntime *jobs.Runtime) http.Handler {
	return newRouterWithServices(dataDir, credentials, jobRuntime, nil, nil)
}

func newRouterWithServices(dataDir string, credentials secrets.Store, jobRuntime *jobs.Runtime, scheduleStore scheduler.Store, backupManager *backup.Manager) http.Handler {
	mux := http.NewServeMux()
	var profileStore appSettingsStore
	if candidate, ok := scheduleStore.(appSettingsStore); ok {
		profileStore = candidate
	}

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok", "app": "zajuna-app", "version": appVersion, "runtime": runtime.GOOS,
		})
	})

	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, _ *http.Request) {
		config, err := readConfig(dataDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		documentType := config.ZajunaDocumentType
		if documentType == "" {
			documentType = "CC"
		}
		response := map[string]any{
			"setupComplete": config.SetupComplete, "zajunaUsername": config.ZajunaUsername,
			"zajunaDocumentType": documentType,
			"hasZajunaPassword":  config.CredentialsStored,
		}
		if profileStore != nil {
			if profileName, profileErr := profileStore.GetAppSetting(context.Background(), "profile_name"); profileErr == nil && strings.TrimSpace(profileName) != "" {
				name := strings.TrimSpace(profileName)
				response["profile"] = map[string]string{"fullName": name, "name": name}
			}
		}
		writeJSON(w, http.StatusOK, response)
	})

	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		var request setupRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("configuraciÃƒÂ³n invÃƒÂ¡lida: %w", err))
			return
		}
		request.ZajunaUsername = strings.TrimSpace(request.ZajunaUsername)
		request.ZajunaDocumentType = strings.ToUpper(strings.TrimSpace(request.ZajunaDocumentType))
		if request.ZajunaDocumentType == "" {
			request.ZajunaDocumentType = "CC"
		}
		if request.ZajunaUsername == "" {
			writeError(w, http.StatusBadRequest, errors.New("el usuario de Zajuna es obligatorio"))
			return
		}
		if request.ZajunaPassword == "" {
			writeError(w, http.StatusBadRequest, errors.New("la contraseña de Zajuna es obligatoria"))
			return
		}
		if err := credentials.Set(request.ZajunaUsername, request.ZajunaPassword); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("no se pudo guardar la credencial en el almacén seguro del sistema: %w", err))
			return
		}

		if err := writeConfig(dataDir, appConfig{
			SetupComplete:      true,
			ZajunaUsername:     request.ZajunaUsername,
			ZajunaDocumentType: request.ZajunaDocumentType,
			CredentialsStored:  true,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		// Completing setup should take the user directly into a usable local
		// workspace. Queue the first ficha sync while retaining the fast setup
		// response; the UI can follow the normal jobs polling endpoint. Tests
		// and older clients only depend on the boolean `saved` field.
		response := map[string]bool{"saved": true}
		if jobRuntime != nil {
			if _, syncErr := jobRuntime.Submit(r.Context(), "sync-fichas", map[string]string{
				"username": request.ZajunaUsername, "documentType": request.ZajunaDocumentType,
			}); syncErr == nil {
				response["syncQueued"] = true
			}
		}
		writeJSON(w, http.StatusOK, response)
	})
	var jobStore jobLister
	if candidate, ok := scheduleStore.(jobLister); ok {
		jobStore = candidate
	}
	var fichaStore fichaLister
	if candidate, ok := scheduleStore.(fichaLister); ok {
		fichaStore = candidate
	}
	registerJobRoutes(mux, jobRuntime, jobStore)
	registerZajunaRoutes(mux, jobRuntime, dataDir)
	registerFichaRoutes(mux, fichaStore, jobRuntime, dataDir)
	var checklistStore checklistService
	if candidate, ok := scheduleStore.(checklistService); ok {
		checklistStore = candidate
	}
	registerChecklistRoutes(mux, checklistStore)
	var courseMapStore coursemaps.Store
	if candidate, ok := scheduleStore.(coursemaps.Store); ok {
		courseMapStore = candidate
	}
	registerCourseMapRoutes(mux, courseMapStore, fichaStore, jobRuntime, dataDir)
	var captureStore checklistCaptureStore
	if candidate, ok := scheduleStore.(checklistCaptureStore); ok {
		captureStore = candidate
	}
	registerChecklistCaptureRoutes(mux, captureStore, jobRuntime, dataDir)
	registerScheduleRoutes(mux, scheduleStore)
	var settingsStore appSettingsStore
	if candidate, ok := scheduleStore.(appSettingsStore); ok {
		settingsStore = candidate
	}
	registerSettingsRoutes(mux, settingsStore)
	registerDiagnosticsRoutes(mux, scheduleStore, dataDir, credentials)
	var notificationsStore notificationStore
	if candidate, ok := scheduleStore.(notificationStore); ok {
		notificationsStore = candidate
	}
	registerNotificationRoutes(mux, notificationsStore)
	registerBackupRoutes(mux, backupManager)
	var evidenceStore evidence.Store
	if candidate, ok := scheduleStore.(evidence.Store); ok {
		evidenceStore = candidate
	}
	var reportStore reports.Store
	if candidate, ok := scheduleStore.(reports.Store); ok {
		reportStore = candidate
	}
	registerEvidenceRoutes(mux, evidenceStore, dataDir)
	registerReportRoutes(mux, reportStore, jobRuntime, dataDir)

	staticFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, errors.New("ruta no encontrada"))
			return
		}

		resourcePath := strings.TrimPrefix(r.URL.Path, "/")
		if resourcePath == "" || resourcePath == "index.html" {
			serveEmbeddedIndex(w, staticFS)
			return
		}

		if _, err := fs.Stat(staticFS, resourcePath); err == nil {
			request := r.Clone(r.Context())
			request.URL.Path = "/" + resourcePath
			fileServer.ServeHTTP(w, request)
			return
		}

		// BrowserRouter necesita recibir index.html en una navegación directa
		// como /resumen o /checklist/1.1.1. Los recursos con extensión que no
		// existen deben conservar un 404 y no convertirse en una ruta React.
		if hasResourceExtension(resourcePath) {
			request := r.Clone(r.Context())
			request.URL.Path = "/" + resourcePath
			fileServer.ServeHTTP(w, request)
			return
		}

		serveEmbeddedIndex(w, staticFS)
	})

	return securityHeaders(mux)
}

func serveEmbeddedIndex(w http.ResponseWriter, staticFS fs.FS) {
	contents, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(contents)
}

func hasResourceExtension(resourcePath string) bool {
	lastSlash := strings.LastIndexByte(resourcePath, '/')
	fileName := resourcePath
	if lastSlash >= 0 {
		fileName = resourcePath[lastSlash+1:]
	}
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".css", ".gif", ".ico", ".jpeg", ".jpg", ".js", ".json", ".map", ".mjs", ".png", ".svg", ".ttf", ".webp", ".woff", ".woff2":
		return true
	default:
		// Los itemCode del checklist usan puntos (por ejemplo, 1.1.1),
		// pero siguen siendo rutas de BrowserRouter y deben recibir index.html.
		return false
	}
}

func writeEndpointFile(path string, endpoint endpointInfo) error {
	contents, err := json.Marshal(endpoint)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(contents, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func dataDirectory() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA no estÃƒÂ¡ disponible")
		}
		return filepath.Join(base, "ZajunaApp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "ZajunaApp"), nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "zajuna-app"), nil
}

func configPath(dataDir string) string { return filepath.Join(dataDir, "config.json") }

func readConfig(dataDir string) (appConfig, error) {
	contents, err := os.ReadFile(configPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return appConfig{}, nil
	}
	if err != nil {
		return appConfig{}, err
	}
	var config appConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return appConfig{}, fmt.Errorf("configuraciÃƒÂ³n local daÃƒÂ±ada: %w", err)
	}
	return config, nil
}

func writeConfig(dataDir string, config appConfig) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(dataDir), append(contents, '\n'), 0o600)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func openBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		command, args = "open", []string{url}
	default:
		command, args = "xdg-open", []string{url}
	}
	return exec.Command(command, args...).Start()
}

func waitUntilReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		response, err := client.Get(url + "/api/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("el endpoint /api/health no estuvo disponible")
}
