package app

import (
	"container/heap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const (
	defaultOccurrenceListLimit = 50
	maxOccurrenceListLimit     = 200
	occurrenceSourceBatchLimit = 200
)

// occurrenceCursor 绑定查询范围、有效 limit、generation 和最后排序键。
type occurrenceCursor struct {
	Version      int    `json:"v"`
	FromUnix     int64  `json:"from_unix"`
	ToUnix       int64  `json:"to_unix"`
	Limit        int    `json:"limit"`
	Generation   uint64 `json:"generation"`
	StartUnix    int64  `json:"start_unix"`
	ScheduleID   int64  `json:"schedule_id"`
	OriginalDate string `json:"original_date"`
}

// occurrenceHeapItem 保存一个日程当前唯一的下一条候选。
type occurrenceHeapItem struct {
	iterator   *domain.MergedOccurrenceIterator
	occurrence domain.Occurrence
	revision   int64
}

// occurrenceHeap 按实际开始、日程 ID、原始日期稳定排序。
type occurrenceHeap []occurrenceHeapItem

func (h occurrenceHeap) Len() int { return len(h) }
func (h occurrenceHeap) Less(i, j int) bool {
	return occurrenceSortKeyLess(h[i].occurrence, h[j].occurrence)
}
func (h occurrenceHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *occurrenceHeap) Push(value any) { *h = append(*h, value.(occurrenceHeapItem)) }
func (h *occurrenceHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

// ListOccurrences 查询任意历史到允许未来上界的发生，并按稳定游标分页。
func (s *Service) ListOccurrences(ctx context.Context, request ListOccurrencesRequest) (ListOccurrencesResponse, error) {
	if request.FromUnix >= request.ToUnix {
		return ListOccurrencesResponse{}, domain.NewValidation("from_unix must be before to_unix", "from_unix", "to_unix")
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultOccurrenceListLimit
	}
	if limit < 0 || limit > maxOccurrenceListLimit {
		return ListOccurrencesResponse{}, domain.NewValidation("limit is outside the allowed range", "limit")
	}
	if s.store == nil {
		return ListOccurrencesResponse{}, errors.New("schedule store is not configured")
	}
	queryStore, ok := s.store.(OccurrenceQueryStore)
	if !ok {
		return ListOccurrencesResponse{}, errors.New("schedule store does not support occurrence queries")
	}
	if s.clock == nil {
		return ListOccurrencesResponse{}, errors.New("schedule clock is not configured")
	}
	cursor, hasCursor, err := decodeOccurrenceCursor(request.Cursor)
	if err != nil {
		return ListOccurrencesResponse{}, err
	}
	if hasCursor {
		if cursor.FromUnix != request.FromUnix || cursor.ToUnix != request.ToUnix || cursor.Limit != limit {
			return ListOccurrencesResponse{}, domain.NewValidation("occurrence cursor parameters do not match request", "cursor", "from_unix", "to_unix", "limit")
		}
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ListOccurrencesResponse{}, fmt.Errorf("snapshot current time: %w", err)
	}
	bounds := domain.CalculateTimeBounds(snapshot)
	if request.ToUnix > bounds.QueryMaxExclusive.Unix() {
		return ListOccurrencesResponse{}, domain.NewTimeRangeExceeded([]string{"to_unix"}, bounds)
	}
	generation, err := queryStore.QueryGeneration(ctx)
	if err != nil {
		return ListOccurrencesResponse{}, fmt.Errorf("read occurrence query generation: %w", err)
	}
	if hasCursor && generation != cursor.Generation {
		return ListOccurrencesResponse{}, domain.NewStaleCursor("occurrence query cursor is stale", "cursor")
	}

	from := time.Unix(request.FromUnix, 0).In(snapshot.Location)
	to := time.Unix(request.ToUnix, 0).In(snapshot.Location)
	entries := make(occurrenceHeap, 0)
	var sourceCursor domain.ScheduleID
	for {
		sources, sourceErr := queryStore.ListOccurrenceSources(ctx, ListOccurrenceSourcesRequest{
			AfterScheduleID: sourceCursor, Limit: occurrenceSourceBatchLimit,
		})
		if sourceErr != nil {
			return ListOccurrencesResponse{}, fmt.Errorf("list occurrence sources after schedule %d: %w", sourceCursor, sourceErr)
		}
		if err := ensureOccurrenceGeneration(ctx, queryStore, generation); err != nil {
			return ListOccurrencesResponse{}, err
		}
		for _, source := range sources.Items {
			iterator, iteratorErr := domain.NewMergedOccurrenceIterator(source.Schedule, source.Overrides, from, to, snapshot.Location)
			if iteratorErr != nil {
				return ListOccurrencesResponse{}, fmt.Errorf("prepare occurrences for schedule %d: %w", source.Schedule.ID, iteratorErr)
			}
			// 游标只约束返回页，不裁剪扫线输入；页前发生仍可能与本页发生重叠。
			occurrence, occurrenceOK, nextErr := iterator.Next()
			if nextErr != nil {
				return ListOccurrencesResponse{}, fmt.Errorf("expand occurrences for schedule %d: %w", source.Schedule.ID, nextErr)
			}
			if occurrenceOK {
				heap.Push(&entries, occurrenceHeapItem{iterator: iterator, occurrence: occurrence, revision: source.Schedule.Revision})
			}
		}
		if sources.NextCursor == 0 {
			break
		}
		if sources.NextCursor <= sourceCursor {
			return ListOccurrencesResponse{}, errors.New("occurrence source cursor did not advance")
		}
		sourceCursor = sources.NextCursor
	}

	// 页前数据也参与扫线，但只把游标之后的前 limit 条作为返回候选。
	sweep := newOccurrenceSweep(func() (sweepResult, bool, error) {
		if entries.Len() == 0 {
			return sweepResult{}, false, nil
		}
		item := heap.Pop(&entries).(occurrenceHeapItem)
		next, nextOK, nextErr := item.iterator.Next()
		if nextErr != nil {
			return sweepResult{}, false, fmt.Errorf("advance occurrences for schedule %d: %w", item.occurrence.ScheduleID, nextErr)
		}
		if nextOK {
			heap.Push(&entries, occurrenceHeapItem{iterator: item.iterator, occurrence: next, revision: item.revision})
		}
		return sweepResult{occurrence: item.occurrence, revision: item.revision}, true, nil
	})
	candidates, marks, hasMore, err := sweep.collect(limit, func(item sweepResult) bool {
		return !hasCursor || occurrenceSortKeyLessCursor(cursor, item.occurrence)
	})
	if err != nil {
		return ListOccurrencesResponse{}, err
	}
	response := ListOccurrencesResponse{Items: make([]OccurrenceDTO, 0, len(candidates))}
	for _, item := range candidates {
		response.Items = append(response.Items, occurrenceDTOFromOccurrence(item.occurrence, item.revision, marks[sweepResultKey(item)]))
	}
	if err := ensureOccurrenceGeneration(ctx, queryStore, generation); err != nil {
		return ListOccurrencesResponse{}, err
	}
	if len(response.Items) == limit && hasMore {
		last := response.Items[len(response.Items)-1]
		response.NextCursor = encodeOccurrenceCursor(occurrenceCursor{
			Version: 1, FromUnix: request.FromUnix, ToUnix: request.ToUnix, Limit: limit,
			Generation: generation, StartUnix: last.StartUnix, ScheduleID: last.ScheduleID, OriginalDate: last.OriginalDate,
		})
	}
	return response, nil
}

// occurrenceSortKeyLess 比较发生的稳定排序键。
func occurrenceSortKeyLess(left, right domain.Occurrence) bool {
	if left.StartLocal.Unix() != right.StartLocal.Unix() {
		return left.StartLocal.Unix() < right.StartLocal.Unix()
	}
	if left.ScheduleID != right.ScheduleID {
		return left.ScheduleID < right.ScheduleID
	}
	return left.OriginalDate < right.OriginalDate
}

// occurrenceSortKeyLessCursor 判断发生是否严格位于游标之后。
func occurrenceSortKeyLessCursor(cursor occurrenceCursor, occurrence domain.Occurrence) bool {
	if occurrence.StartLocal.Unix() != cursor.StartUnix {
		return cursor.StartUnix < occurrence.StartLocal.Unix()
	}
	if int64(occurrence.ScheduleID) != cursor.ScheduleID {
		return cursor.ScheduleID < int64(occurrence.ScheduleID)
	}
	return cursor.OriginalDate < occurrence.OriginalDate
}

// occurrenceDTOFromOccurrence 将领域发生映射为受控查询视图。
func occurrenceDTOFromOccurrence(occurrence domain.Occurrence, revision int64, overlaps bool) OccurrenceDTO {
	return OccurrenceDTO{ScheduleID: int64(occurrence.ScheduleID), OriginalDate: occurrence.OriginalDate,
		StartUnix: occurrence.StartLocal.Unix(), EndUnix: occurrence.EndLocal.Unix(), Title: occurrence.Title,
		Notes: occurrence.Notes, ScheduleRevision: revision, Overlaps: overlaps}
}

// ensureOccurrenceGeneration 在每个源批次和最终返回前守住一致快照。
func ensureOccurrenceGeneration(ctx context.Context, store OccurrenceQueryStore, expected uint64) error {
	current, err := store.QueryGeneration(ctx)
	if err != nil {
		return fmt.Errorf("recheck occurrence query generation: %w", err)
	}
	if current != expected {
		return domain.NewStaleCursor("occurrence query changed during pagination", "cursor")
	}
	return nil
}

// encodeOccurrenceCursor 编码查询游标。
func encodeOccurrenceCursor(cursor occurrenceCursor) string {
	data, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// decodeOccurrenceCursor 校验查询游标格式。
func decodeOccurrenceCursor(value string) (occurrenceCursor, bool, error) {
	if value == "" {
		return occurrenceCursor{}, false, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return occurrenceCursor{}, false, domain.NewValidation("occurrence cursor is invalid", "cursor")
	}
	var cursor occurrenceCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.Limit <= 0 ||
		cursor.Limit > maxOccurrenceListLimit || cursor.Generation == 0 || cursor.ScheduleID <= 0 || cursor.OriginalDate == "" {
		return occurrenceCursor{}, false, domain.NewValidation("occurrence cursor is invalid", "cursor")
	}
	return cursor, true, nil
}
