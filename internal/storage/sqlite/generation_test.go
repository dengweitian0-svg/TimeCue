package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestQueryGenerationTracksCommittedChanges 验证成功变更递增、幂等整组动作和失败事务不递增。
func TestQueryGenerationTracksCommittedChanges(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	before, err := store.QueryGeneration(ctx)
	if err != nil || before == 0 {
		t.Fatalf("initial generation = %d, %v", before, err)
	}
	candidate := recurringCandidate()
	schedule, err := store.CreateSchedule(ctx, candidate)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	afterCreate, _ := store.QueryGeneration(ctx)
	if afterCreate == before {
		t.Fatalf("generation did not change after create: %d", afterCreate)
	}

	cancelled, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{
		ExpectedRevision: schedule.Revision, UpdatedAtUTC: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
	afterCancel, _ := store.QueryGeneration(ctx)
	if afterCancel == afterCreate || cancelled.Changed != true {
		t.Fatalf("cancel generation/result = %d/%+v", afterCancel, cancelled)
	}
	idempotent, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{
		ExpectedRevision: cancelled.Revision, UpdatedAtUTC: time.Now().UTC(),
	})
	if err != nil || idempotent.Changed {
		t.Fatalf("idempotent cancel = %+v, %v", idempotent, err)
	}
	afterIdempotent, _ := store.QueryGeneration(ctx)
	if afterIdempotent != afterCancel {
		t.Fatalf("idempotent cancel changed generation: %d -> %d", afterCancel, afterIdempotent)
	}

	title := "stale"
	if _, err := store.UpdateSchedule(ctx, schedule.ID, domain.ScheduleUpdateCandidate{
		Title: &title, ExpectedRevision: cancelled.Revision - 1, UpdatedAtUTC: time.Now().UTC(),
	}); err == nil {
		t.Fatal("stale update error = nil")
	}
	afterFailure, _ := store.QueryGeneration(ctx)
	if afterFailure != afterIdempotent {
		t.Fatalf("failed update changed generation: %d -> %d", afterIdempotent, afterFailure)
	}
}

// TestListOccurrenceSourcesPaginatesAndReadsDefinitions 验证查询源按日程 ID 分页并返回完整定义。
func TestListOccurrenceSourcesPaginatesAndReadsDefinitions(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	first, err := store.CreateSchedule(ctx, recurringCandidate())
	if err != nil {
		t.Fatalf("first CreateSchedule() error = %v", err)
	}
	secondCandidate := recurringCandidate()
	secondCandidate.Title = "second"
	second, err := store.CreateSchedule(ctx, secondCandidate)
	if err != nil {
		t.Fatalf("second CreateSchedule() error = %v", err)
	}
	page, err := store.ListOccurrenceSources(ctx, app.ListOccurrenceSourcesRequest{Limit: 1})
	if err != nil {
		t.Fatalf("first source page error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Schedule.ID != first.ID || page.NextCursor != first.ID {
		t.Fatalf("first source page = %+v", page)
	}
	next, err := store.ListOccurrenceSources(ctx, app.ListOccurrenceSourcesRequest{AfterScheduleID: page.NextCursor, Limit: 1})
	if err != nil {
		t.Fatalf("second source page error = %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].Schedule.ID != second.ID || next.NextCursor != 0 {
		t.Fatalf("second source page = %+v", next)
	}
}
