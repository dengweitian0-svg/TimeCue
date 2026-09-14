package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

type occurrenceQueryFake struct {
	fakeStore
	generation        uint64
	sources           []OccurrenceSourceDTO
	sourceCalls       int
	changeDuringQuery bool
	generationCalls   int
}

func (s *occurrenceQueryFake) QueryGeneration(context.Context) (uint64, error) {
	s.generationCalls++
	if s.changeDuringQuery && s.generationCalls > 1 {
		return s.generation + 1, nil
	}
	return s.generation, nil
}

func (s *occurrenceQueryFake) ListOccurrenceSources(_ context.Context, request ListOccurrenceSourcesRequest) (ListOccurrenceSourcesResponse, error) {
	s.sourceCalls++
	result := ListOccurrenceSourcesResponse{}
	for _, source := range s.sources {
		if source.Schedule.ID > request.AfterScheduleID {
			result.Items = append(result.Items, source)
		}
	}
	return result, nil
}

func makeQuerySchedule(id domain.ScheduleID, hour int) domain.Schedule {
	cron := "0 " + string(rune('0'+hour)) + " * * *"
	until := "2026-01-05"
	return domain.Schedule{ID: id, Cron: &cron, UntilDateLocal: &until, Open: true,
		FirstStartLocal: time.Date(2026, 1, 1, hour, 0, 0, 0, time.UTC), DurationMinutes: 30, Revision: 1,
		Title: "schedule"}
}

func TestServiceListOccurrencesPaginatesStableCursor(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &occurrenceQueryFake{generation: 7, sources: []OccurrenceSourceDTO{
		{Schedule: makeQuerySchedule(1, 9)},
		{Schedule: makeQuerySchedule(2, 9)},
	}}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: time.UTC}})
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC).Unix()
	first, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 2})
	if err != nil {
		t.Fatalf("first ListOccurrences() error = %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("second ListOccurrences() error = %v", err)
	}
	if len(second.Items) != 2 {
		t.Fatalf("second page = %+v", second)
	}
	seen := map[string]bool{}
	for _, page := range []ListOccurrencesResponse{first, second} {
		for _, item := range page.Items {
			key := string(rune(item.ScheduleID)) + "/" + item.OriginalDate
			if seen[key] {
				t.Fatalf("duplicate item %q", key)
			}
			seen[key] = true
		}
	}
	if first.Items[0].ScheduleID != 1 || first.Items[1].ScheduleID != 2 || second.Items[0].OriginalDate != "2026-01-02" {
		t.Fatalf("unstable ordering: first=%+v second=%+v", first.Items, second.Items)
	}
}

func TestServiceListOccurrencesValidatesUpperBoundAndCursor(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &occurrenceQueryFake{generation: 1, sources: []OccurrenceSourceDTO{{Schedule: makeQuerySchedule(1, 9)}}}
	clock := &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: time.UTC}}
	service := NewService(store, clock)
	bounds := domain.CalculateTimeBounds(clock.snapshot)
	from := now.Unix()
	_, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: from, ToUnix: bounds.QueryMaxExclusive.Unix() + 1})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.TimeRangeExceeded {
		t.Fatalf("upper bound error = %#v", err)
	}
	if clock.calls != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.calls)
	}
	_, err = service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: 10, ToUnix: 10})
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("order error = %#v", err)
	}
	_, err = service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: from, ToUnix: bounds.QueryMaxExclusive.Unix(), Limit: maxOccurrenceListLimit + 1})
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("limit error = %#v", err)
	}
}

func TestServiceListOccurrencesRejectsStaleGeneration(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &occurrenceQueryFake{generation: 1, changeDuringQuery: true, sources: []OccurrenceSourceDTO{{Schedule: makeQuerySchedule(1, 9)}}}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: time.UTC}})
	_, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{
		FromUnix: now.Unix(), ToUnix: now.Add(24 * time.Hour).Unix(), Limit: 1,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleCursor {
		t.Fatalf("stale generation error = %#v", err)
	}
}
