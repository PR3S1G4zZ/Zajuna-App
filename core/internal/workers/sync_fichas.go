package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/secrets"
	"github.com/zajuna-app/core/internal/zajuna"
)

const SyncFichasWorkerID = "sync-fichas"

type syncFichasInput struct {
	Username     string `json:"username"`
	DocumentType string `json:"documentType"`
}

type zajunaClient interface {
	Login(ctx context.Context, credentials zajuna.Credentials) (zajuna.Session, error)
	ListFichas(ctx context.Context, session zajuna.Session) ([]zajuna.Ficha, error)
}

type fichaStore interface {
	UpsertFichas(ctx context.Context, fichas []zajuna.Ficha) (int, error)
}

type profileNameStore interface {
	SetAppSetting(context.Context, string, string) error
}

const profileNameSettingKey = "profile_name"

type SyncFichasWorker struct {
	client      zajunaClient
	credentials secrets.Store
	store       fichaStore
}

func NewSyncFichasWorker(client zajunaClient, credentials secrets.Store, store fichaStore) (*SyncFichasWorker, error) {
	if client == nil || credentials == nil || store == nil {
		return nil, errors.New("sync fichas worker requires client, credentials and store")
	}
	return &SyncFichasWorker{client: client, credentials: credentials, store: store}, nil
}

func (w *SyncFichasWorker) ID() string { return SyncFichasWorkerID }

func (w *SyncFichasWorker) Execute(ctx context.Context, job jobs.Job, reporter jobs.Reporter) jobs.Result {
	var input syncFichasInput
	if err := json.Unmarshal(job.Input, &input); err != nil {
		return jobs.Result{ErrorCode: "invalid_input", ErrorMessage: "la entrada de sincronización no es válida"}
	}
	if input.Username == "" {
		return jobs.Result{ErrorCode: "missing_username", ErrorMessage: "configura el usuario o documento de Zajuna"}
	}
	if err := reporter.Progress(ctx, "credentials", 10, "Preparando sesión segura de Zajuna"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	password, err := w.credentials.Get(input.Username)
	if err != nil || password == "" {
		return jobs.Result{ErrorCode: "credential_unavailable", ErrorMessage: "no se encontró la contraseña de Zajuna en el almacén seguro"}
	}
	if err := reporter.Progress(ctx, "login", 25, "Validando sesión en Zajuna"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	session, err := w.client.Login(ctx, zajuna.Credentials{DocumentType: input.DocumentType, Document: input.Username, Password: password})
	if err != nil {
		return jobs.Result{Retryable: retryableZajunaError(err), ErrorCode: "zajuna_login_failed", ErrorMessage: fmt.Sprintf("no se pudo iniciar sesión en Zajuna: %v", err)}
	}
	if err := reporter.Progress(ctx, "courses", 60, "Leyendo fichas disponibles"); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	fichas, err := w.client.ListFichas(ctx, session)
	if err != nil {
		return jobs.Result{Retryable: retryableZajunaError(err), ErrorCode: "fichas_read_failed", ErrorMessage: fmt.Sprintf("no se pudieron leer las fichas: %v", err)}
	}
	if len(fichas) == 0 {
		return jobs.Result{ErrorCode: "empty_fichas", ErrorMessage: "Zajuna no devolvió fichas para este usuario"}
	}
	if err := reporter.Progress(ctx, "persisting", 85, fmt.Sprintf("Guardando %d fichas localmente", len(fichas))); err != nil {
		return jobs.Result{ErrorCode: "progress_failed", ErrorMessage: err.Error()}
	}
	imported, err := w.store.UpsertFichas(ctx, fichas)
	if err != nil {
		return jobs.Result{ErrorCode: "fichas_persist_failed", ErrorMessage: err.Error()}
	}
	if profileName := strings.TrimSpace(session.ProfileName); profileName != "" {
		if profileStore, ok := w.store.(profileNameStore); ok {
			// El nombre es metadata no secreta; credenciales y cookies siguen
			// exclusivamente en el almacén seguro y la sesión en memoria.
			_ = profileStore.SetAppSetting(ctx, profileNameSettingKey, profileName)
		}
	}
	return jobs.Result{Output: map[string]any{"imported": imported, "total": len(fichas)}}
}

func retryableZajunaError(err error) bool {
	return !errors.Is(err, zajuna.ErrAuthentication) && !errors.Is(err, zajuna.ErrSessionExpired) && !errors.Is(err, zajuna.ErrChallengeRequired)
}
