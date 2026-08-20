package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zajuna-app/core/internal/coursemaps"
	"github.com/zajuna-app/core/internal/evidence"
	"github.com/zajuna-app/core/internal/jobs"
	"github.com/zajuna-app/core/internal/reports"
	"github.com/zajuna-app/core/internal/scheduler"
	"github.com/zajuna-app/core/internal/zajuna"
)

func TestOpenMigratesSchemaAndStoresJobs(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var version int
	if err := store.DB().QueryRowContext(context.Background(), `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", currentSchemaVersion, version)
	}

	var tables int
	if err := store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('jobs', 'job_events', 'fichas', 'evidences')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 4 {
		t.Fatalf("expected core tables, got %d", tables)
	}

	now := time.Now().UTC()
	if err := store.CreateSchedule(context.Background(), scheduler.Schedule{
		ID: "schedule-test", WorkerType: "sync-fichas", Input: []byte(`{"username":"user"}`),
		Interval: time.Hour, Enabled: true, NextRunAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueSchedules(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "schedule-test" {
		t.Fatalf("unexpected due schedules: %#v", due)
	}
	if err := store.MarkScheduleRun(context.Background(), "schedule-test", "job-test", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListSchedules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].LastJobID != "job-test" {
		t.Fatalf("schedule run was not persisted: %#v", items)
	}
	if err := store.CreateEvidence(context.Background(), evidence.Record{ID: "evidence-test", Name: "capture.html", FilePath: filepath.Join(t.TempDir(), "capture.html"), Format: "html", Source: "capture-evidence", SHA256: "abc123", CapturedAt: now}); err != nil {
		t.Fatal(err)
	}
	evidences, err := store.ListEvidences(context.Background(), 10)
	if err != nil || len(evidences) != 1 || evidences[0].ID != "evidence-test" {
		t.Fatalf("evidence was not persisted: %#v (%v)", evidences, err)
	}
	if err := store.CreateReport(context.Background(), reports.Record{ID: "report-test", Name: "Reporte", FilePath: "report.pdf", Format: "pdf", SHA256: "def456", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	reportsList, err := store.ListReports(context.Background(), 10)
	if err != nil || len(reportsList) != 1 || reportsList[0].ID != "report-test" {
		t.Fatalf("report was not persisted: %#v (%v)", reportsList, err)
	}

}

func TestJobNotificationsPersistAndCanBeRead(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-notification", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkRunning(context.Background(), "job-notification"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteJob(context.Background(), "job-notification", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListNotifications(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].Kind != "job_completed" || items[0].JobID != "job-notification" {
		t.Fatalf("unexpected notifications: %#v (%v)", items, err)
	}
	if err := store.MarkNotificationRead(context.Background(), items[0].ID); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListNotifications(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("notification was not marked read: %#v (%v)", items, err)
	}
	if err := store.SetAppSetting(context.Background(), "ui_preferences", `{"notifications":{"jobCompleted":false,"needsReview":true}}`); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-notification-muted", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkRunning(context.Background(), "job-notification-muted"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteJob(context.Background(), "job-notification-muted", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListNotifications(context.Background(), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("muted completion should not create a notification: %#v (%v)", items, err)
	}
}

func TestChecklistEvidenceRetryUpdatesTheExistingSlot(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.UpsertFichas(context.Background(), []zajuna.Ficha{{ExternalID: "3135429", Name: "Ficha", CourseID: "41080"}}); err != nil {
		t.Fatal(err)
	}
	fichas, err := store.ListFichas(context.Background(), 10)
	if err != nil || len(fichas) != 1 {
		t.Fatalf("unexpected ficha: %#v (%v)", fichas, err)
	}
	now := time.Now().UTC()
	base := evidence.Record{
		ID: "old-hash-based-id", FichaID: fichas[0].ID, ItemCode: "6.1", SlotNumber: 1,
		Name: "Actividad antigua", FilePath: "evidences/checklist/old.png", Format: "png",
		Source: "capture-checklist", SHA256: "old-hash", CapturedAt: now,
	}
	if err := store.CreateEvidence(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.ID = "new-deterministic-id"
	base.Name = "Actividad actualizada"
	base.FilePath = "evidences/checklist/new.png"
	base.SHA256 = "new-hash"
	base.CapturedAt = now.Add(time.Minute)
	if err := store.CreateEvidence(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListEvidences(context.Background(), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("retry created a duplicate evidence row: %#v (%v)", items, err)
	}
	if items[0].ID != "old-hash-based-id" || items[0].Name != "Actividad actualizada" || items[0].SHA256 != "new-hash" {
		t.Fatalf("retry did not update the current slot: %#v", items[0])
	}
}

func TestCourseMapRoundTrip(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	record := coursemaps.Record{
		CourseID:      "41080",
		CourseURL:     "https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080",
		ProfileURL:    "https://zajuna.sena.edu.co/zajuna/user/profile.php",
		ByItemCode:    map[string]json.RawMessage{"route.forum": json.RawMessage(`["https://zajuna.sena.edu.co/zajuna/mod/forum/view.php?id=1"]`)},
		Routes:        []coursemaps.Route{{URL: "https://zajuna.sena.edu.co/zajuna/mod/forum/view.php?id=1", Kind: "forum", Depth: 1}},
		LinkCount:     1,
		ItemCodeCount: 1,
		ScrapeStats:   coursemaps.Stats{Total: 1, Forums: 1},
		Source:        "fixture",
		DiscoveredAt:  now,
		UpdatedAt:     now,
	}
	if err := store.CreateOrReplaceCourseMap(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCourseMap(context.Background(), "41080")
	if err != nil {
		t.Fatal(err)
	}
	if got.CourseID != record.CourseID || got.LinkCount != 1 || len(got.Routes) != 1 || got.ScrapeStats.Forums != 1 {
		t.Fatalf("unexpected course map: %#v", got)
	}
	items, err := store.ListCourseMaps(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].CourseID != "41080" {
		t.Fatalf("unexpected course maps: %#v (%v)", items, err)
	}
}

func TestChecklistIsSeededForEveryFichaAndStatusPersists(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.UpsertFichas(context.Background(), []zajuna.Ficha{{ExternalID: "3135429", Name: "Programa demo", CourseID: "41080"}}); err != nil {
		t.Fatal(err)
	}
	fichas, err := store.ListFichas(context.Background(), 10)
	if err != nil || len(fichas) != 1 {
		t.Fatalf("unexpected fichas: %#v (%v)", fichas, err)
	}
	dashboard, err := store.GetChecklistDashboard(context.Background(), fichas[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Items) != 62 || len(dashboard.Categories) != 15 {
		t.Fatalf("expected 62 items and 15 categories, got %d and %d", len(dashboard.Items), len(dashboard.Categories))
	}
	if dashboard.Summary.Total != 62 || dashboard.Summary.Pending != 62 || dashboard.Summary.Percentage != 0 {
		t.Fatalf("unexpected initial checklist summary: %#v", dashboard.Summary)
	}
	if err := store.CreateEvidence(context.Background(), evidence.Record{
		ID: "checklist-evidence", FichaID: fichas[0].ID, ItemCode: "1.1.1", SlotNumber: 1,
		Name: "Cronograma", FilePath: filepath.Join(t.TempDir(), "cronograma.png"), Format: "png",
		Source: "test", SHA256: "hash-checklist", CapturedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	dashboard, err = store.GetChecklistDashboard(context.Background(), fichas[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Items[0].EvidenceCount != 1 || len(dashboard.Items[0].Evidences) != 1 {
		t.Fatalf("checklist evidence was not linked: %#v", dashboard.Items[0])
	}
	if err := store.SetChecklistItemStatus(context.Background(), fichas[0].ID, "1.1.1", "SI"); err != nil {
		t.Fatal(err)
	}
	dashboard, err = store.GetChecklistDashboard(context.Background(), fichas[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Summary.Yes != 1 || dashboard.Summary.Pending != 61 || dashboard.Summary.Percentage != 1 {
		t.Fatalf("unexpected updated checklist summary: %#v", dashboard.Summary)
	}
	detail, err := store.GetChecklistItemDetail(context.Background(), fichas[0].ID, "1.1.1")
	if err != nil || len(detail.Events) != 1 || detail.Events[0].FromStatus != "PENDIENTE" || detail.Events[0].ToStatus != "SI" {
		t.Fatalf("checklist history was not persisted: %#v (%v)", detail.Events, err)
	}
}

func TestEvidenceGroupsShareEquivalentPageSignatures(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.UpsertFichas(context.Background(), []zajuna.Ficha{{ExternalID: "3135429", Name: "Programa demo", CourseID: "41080"}}); err != nil {
		t.Fatal(err)
	}
	fichas, err := store.ListFichas(context.Background(), 10)
	if err != nil || len(fichas) != 1 {
		t.Fatalf("unexpected fichas: %#v (%v)", fichas, err)
	}
	fichaID := fichas[0].ID
	metadata := json.RawMessage(`{"finalUrl":"https://zajuna.sena.edu.co/zajuna/user/profile.php?id=7","selector":"#page-user-profile","selectorMatched":true,"groupName":"perfil_instructor"}`)
	for _, record := range []evidence.Record{
		{ID: "profile-1", FichaID: fichaID, ItemCode: "2.1.1", SlotNumber: 1, Name: "Perfil académico", FilePath: "profile-1.png", Format: "png", Source: "capture-checklist", SHA256: "hash-one", Metadata: metadata},
		{ID: "profile-2", FichaID: fichaID, ItemCode: "2.1.2", SlotNumber: 1, Name: "Correo institucional", FilePath: "profile-2.png", Format: "png", Source: "capture-checklist", SHA256: "hash-two", Metadata: metadata},
		{ID: "course-1", FichaID: fichaID, ItemCode: "4.1", SlotNumber: 1, Name: "Menú del curso", FilePath: "course.png", Format: "png", Source: "capture-checklist", SHA256: "hash-three", Metadata: json.RawMessage(`{"finalUrl":"https://zajuna.sena.edu.co/zajuna/course/view.php?id=41080","selector":"#region-main .course-content","selectorMatched":true,"groupName":"menu_curso"}`)},
	} {
		if err := store.CreateEvidence(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}

	groups, err := store.RebuildEvidenceGroups(context.Background(), fichaID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected two evidence groups, got %#v", groups)
	}
	var profileGroup *evidence.Group
	for index := range groups {
		if len(groups[index].ItemCodes) == 2 {
			profileGroup = &groups[index]
		}
	}
	if profileGroup == nil || profileGroup.Confidence != "suggested" || len(profileGroup.Evidences) != 2 {
		t.Fatalf("profile evidence was not grouped: %#v", groups)
	}
	loaded, err := store.ListEvidenceGroups(context.Background(), fichaID)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("persisted groups were not loaded: %#v (%v)", loaded, err)
	}
}

func TestJobTransitionsAreAtomicAndRejectInvalidStates(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-cas", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteJob(context.Background(), "job-cas", []byte(`{}`)); !errors.Is(err, jobs.ErrInvalidTransition) {
		t.Fatalf("completing a queued job should be rejected: %v", err)
	}
	if _, err := store.MarkRunning(context.Background(), "job-cas"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteJob(context.Background(), "job-cas", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCancelled(context.Background(), "job-cas"); !errors.Is(err, jobs.ErrInvalidTransition) {
		t.Fatalf("cancel after completion should be rejected: %v", err)
	}
	events, err := store.ListJobEvents(context.Background(), "job-cas")
	if err != nil || len(events) < 2 {
		t.Fatalf("expected transactional status events, got %#v (%v)", events, err)
	}
}

func TestConcurrentMarkRunningAllowsASingleWinner(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-race", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	winners := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.MarkRunning(context.Background(), "job-race"); err == nil {
				winners <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatalf("expected a single running transition, got %d", len(winners))
	}
	job, err := store.GetJob(context.Background(), "job-race")
	if err != nil || job.Status != jobs.StatusRunning || job.Attempt != 1 {
		t.Fatalf("unexpected winner state: %#v (%v)", job, err)
	}
}

func TestReconcileInterruptedJobsAfterSimulatedRestart(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-orphan", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkRunning(context.Background(), "job-orphan"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateJob(context.Background(), jobs.Job{ID: "job-queued", Type: "sync-fichas", Status: jobs.StatusQueued, Input: []byte(`{}`), CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	ready, err := store.ReconcileInterrupted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 2 {
		t.Fatalf("expected queued and recovered jobs, got %#v", ready)
	}
	orphan, err := store.GetJob(context.Background(), "job-orphan")
	if err != nil || orphan.Status != jobs.StatusRetrying || orphan.ErrorCode != "interrupted" {
		t.Fatalf("running job was not recovered: %#v (%v)", orphan, err)
	}
}
