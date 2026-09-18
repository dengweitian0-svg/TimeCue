package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const recoveryBatchLimit = 50

// recoveryRunner 保存一次冷启动恢复的固定上界、批次和事实 epoch。
type recoveryRunner struct {
	batches           []RecoveryBatch
	next              int
	recoveryUntilUnix int64
	epoch             uint64
	hasWork           bool
}

// recoveryStore 返回优先由提醒尝试端口提供的恢复能力。
func (s *ReminderScheduler) recoveryStore() (RecoveryStore, bool) {
	if s == nil {
		return nil, false
	}
	if store, ok := any(s.attempts).(RecoveryStore); ok && store != nil {
		return store, true
	}
	if store, ok := any(s.store).(RecoveryStore); ok && store != nil {
		return store, true
	}
	return nil, false
}

// prepareRecovery 读取启动 checkpoint 和日程事实，建立排序后的有限恢复批次。
// occurrence 只在内存中展开，数据库仍只保存提醒尝试和 checkpoint 两类事实。
func (s *ReminderScheduler) prepareRecovery(ctx context.Context, snapshot domain.TimeSnapshot) (*recoveryRunner, error) {
	store, ok := s.recoveryStore()
	if !ok {
		return nil, nil
	}
	if snapshot.Location == nil || snapshot.Now.IsZero() {
		return nil, errors.New("recovery startup time snapshot is incomplete")
	}
	checkpoint, err := store.GetSchedulerRecoveryCheckpoint(ctx)
	if err != nil {
		return nil, fmt.Errorf("read recovery checkpoint: %w", err)
	}
	attempts, err := listAllRecoveryAttempts(ctx, store)
	if err != nil {
		return nil, err
	}
	sources, err := s.readSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("read recovery occurrence sources: %w", err)
	}
	recoveryUntilUnix := snapshot.Now.Unix()
	candidates, err := expandRecoveryCandidates(sources, attempts, checkpoint, recoveryUntilUnix, snapshot.Location)
	if err != nil {
		return nil, err
	}
	batches := splitRecoveryBatches(candidates, checkpoint, recoveryUntilUnix)
	if len(batches) == 0 || batches[len(batches)-1].CheckpointUnix < recoveryUntilUnix {
		batches = append(batches, RecoveryBatch{CheckpointUnix: recoveryUntilUnix, RecoveryUntilUnix: recoveryUntilUnix})
	}
	return &recoveryRunner{
		batches:           batches,
		recoveryUntilUnix: recoveryUntilUnix,
		epoch:             s.coordinator.Epoch(),
		hasWork:           len(candidates) > 0,
	}, nil
}

// listAllRecoveryAttempts 通过稳定 ID 游标读取恢复所需的全部尝试记录。
func listAllRecoveryAttempts(ctx context.Context, store RecoveryStore) ([]ReminderAttemptRecord, error) {
	var (
		afterID int64
		result  []ReminderAttemptRecord
	)
	for {
		page, err := store.ListReminderAttemptsForRecovery(ctx, RecoveryAttemptListRequest{AfterID: afterID})
		if err != nil {
			return nil, fmt.Errorf("list recovery reminder attempts after %d: %w", afterID, err)
		}
		result = append(result, page.Items...)
		if page.NextCursor == 0 {
			return result, nil
		}
		if page.NextCursor <= afterID {
			return nil, errors.New("recovery reminder attempt cursor did not advance")
		}
		afterID = page.NextCursor
	}
}

// expandRecoveryCandidates 按当前日程事实展开历史 occurrence，并合并 claimed 重试身份。
func expandRecoveryCandidates(sources []OccurrenceSourceDTO, attempts []ReminderAttemptRecord, checkpoint RecoveryCheckpoint, recoveryUntilUnix int64, location *time.Location) ([]RecoveryCandidate, error) {
	attemptByIdentity := make(map[domain.OccurrenceIdentity]ReminderAttemptRecord, len(attempts))
	for _, attempt := range attempts {
		attemptByIdentity[attempt.Identity] = attempt
	}
	sourceBySchedule := make(map[domain.ScheduleID]OccurrenceSourceDTO, len(sources))
	for _, source := range sources {
		sourceBySchedule[source.Schedule.ID] = source
	}
	seen := make(map[domain.OccurrenceIdentity]struct{})
	result := make([]RecoveryCandidate, 0)
	until := time.Unix(recoveryUntilUnix, 0).In(location)
	for _, source := range sources {
		from := recoveryStartForSource(source, checkpoint, location)
		if from.After(until) {
			continue
		}
		occurrences, err := domain.ExpandMergedOccurrences(source.Schedule, source.Overrides, from, recoveryUpperBound(until), location)
		if err != nil {
			return nil, fmt.Errorf("expand recovery occurrences for schedule %d: %w", source.Schedule.ID, err)
		}
		for _, occurrence := range occurrences {
			if occurrence.StartLocal.Unix() < from.Unix() || occurrence.StartLocal.Unix() > recoveryUntilUnix {
				continue
			}
			identity := occurrence.Identity()
			attempt, exists := attemptByIdentity[identity]
			if exists && attempt.Status != ReminderAttemptClaimed {
				continue
			}
			mode := RecoveryCandidateNew
			if exists {
				mode = RecoveryCandidateReclaim
			}
			if _, duplicate := seen[identity]; duplicate {
				continue
			}
			seen[identity] = struct{}{}
			result = append(result, RecoveryCandidate{Occurrence: occurrence, Mode: mode, Revision: source.Schedule.Revision})
		}
	}

	// claimed 身份独立于 checkpoint lower bound 重建，避免通知提交后进程退出时
	// 下一次启动因 checkpoint 已经推进而永久遗漏遗留占用。
	for _, attempt := range attempts {
		if attempt.Status != ReminderAttemptClaimed {
			continue
		}
		if _, duplicate := seen[attempt.Identity]; duplicate {
			continue
		}
		source, ok := sourceBySchedule[attempt.Identity.ScheduleID]
		if !ok {
			continue
		}
		occurrence, valid, err := domain.OccurrenceForIdentity(source.Schedule, source.Overrides, attempt.Identity, location)
		if err != nil {
			return nil, fmt.Errorf("rebuild claimed recovery occurrence %d/%s: %w", attempt.Identity.ScheduleID, attempt.Identity.OriginalDate, err)
		}
		if !valid || occurrence.StartLocal.Unix() > recoveryUntilUnix {
			continue
		}
		seen[attempt.Identity] = struct{}{}
		result = append(result, RecoveryCandidate{Occurrence: occurrence, Mode: RecoveryCandidateReclaim, Revision: source.Schedule.Revision})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i].Occurrence, result[j].Occurrence
		if left.StartLocal.Unix() != right.StartLocal.Unix() {
			return left.StartLocal.Unix() < right.StartLocal.Unix()
		}
		if left.ScheduleID != right.ScheduleID {
			return left.ScheduleID < right.ScheduleID
		}
		return left.OriginalDate < right.OriginalDate
	})
	return result, nil
}

// recoveryStartForSource 计算有 checkpoint 或无 checkpoint 时的日程扫描下界。
func recoveryStartForSource(source OccurrenceSourceDTO, checkpoint RecoveryCheckpoint, location *time.Location) time.Time {
	if checkpoint.Exists {
		return time.Unix(checkpoint.CheckpointUnix, 0).In(location)
	}
	start := source.Schedule.FirstStartLocal.In(location)
	for _, override := range source.Overrides {
		if override.ScheduleID != source.Schedule.ID || !override.Open || !override.SnapshotPresent {
			continue
		}
		if start.IsZero() || override.StartLocal.Before(start) {
			start = override.StartLocal.In(location)
		}
	}
	return start
}

// recoveryUpperBound 将固定秒级上界转换为 occurrence 展开的半开结束时间。
func recoveryUpperBound(until time.Time) time.Time { return until.Add(time.Second) }

// splitRecoveryBatches 按实际开始时间切分最多 50 条，并保存每批最后身份的时间戳。
func splitRecoveryBatches(candidates []RecoveryCandidate, checkpoint RecoveryCheckpoint, recoveryUntilUnix int64) []RecoveryBatch {
	if len(candidates) == 0 {
		return nil
	}
	baseCheckpoint := int64(0)
	if checkpoint.Exists {
		baseCheckpoint = checkpoint.CheckpointUnix
	}
	batches := make([]RecoveryBatch, 0, (len(candidates)+recoveryBatchLimit-1)/recoveryBatchLimit)
	for start := 0; start < len(candidates); start += recoveryBatchLimit {
		end := start + recoveryBatchLimit
		if end > len(candidates) {
			end = len(candidates)
		}
		lastUnix := candidates[end-1].Occurrence.StartLocal.Unix()
		if lastUnix < baseCheckpoint {
			lastUnix = baseCheckpoint
		}
		batches = append(batches, RecoveryBatch{
			Candidates:        append([]RecoveryCandidate(nil), candidates[start:end]...),
			CheckpointUnix:    lastUnix,
			RecoveryUntilUnix: recoveryUntilUnix,
		})
		baseCheckpoint = lastUnix
	}
	return batches
}

// processRecoveryBatch 执行一个“Claim → Submit → 结果/checkpoint”批次。
func (s *ReminderScheduler) processRecoveryBatch(ctx context.Context, runner *recoveryRunner) error {
	if runner == nil || runner.next >= len(runner.batches) {
		return nil
	}
	if err := s.checkRecoveryEpoch(runner); err != nil {
		return err
	}
	batch := runner.batches[runner.next]
	store, ok := s.recoveryStore()
	if !ok {
		return errors.New("recovery store is not configured")
	}
	claimIdentities := make([]domain.OccurrenceIdentity, 0, len(batch.Candidates))
	for _, candidate := range batch.Candidates {
		claimIdentities = append(claimIdentities, candidate.Occurrence.Identity())
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot recovery batch time: %w", err)
	}
	if snapshot.HasEnvironmentChange() {
		return errRecoveryRestart
	}
	claims, err := store.ClaimRecoveryBatch(ctx, claimIdentities, snapshot.Now.UTC())
	if err != nil {
		return fmt.Errorf("claim recovery batch: %w", err)
	}
	claimedByIdentity := make(map[domain.OccurrenceIdentity]struct{}, len(claims))
	for _, claim := range claims {
		claimedByIdentity[claim.Identity] = struct{}{}
		if claim.Mode == RecoveryCandidateReclaim {
			s.logger.Debug("reclaim claimed recovery reminder", "schedule_id", claim.Identity.ScheduleID, "original_date", claim.Identity.OriginalDate)
		}
	}
	results := make([]RecoveryBatchResult, 0, len(claims))
	notificationCtx, unregister := s.registerNotification(ctx)
	defer unregister()
	for _, candidate := range batch.Candidates {
		if _, accepted := claimedByIdentity[candidate.Occurrence.Identity()]; !accepted {
			continue
		}
		if err := s.checkRecoveryEpoch(runner); err != nil {
			return err
		}
		status := ReminderAttemptSubmitted
		current, revision, valid, currentErr := s.currentRecoveryOccurrence(ctx, candidate.Occurrence.Identity(), snapshot.Location)
		if currentErr != nil {
			if isReminderNotFound(currentErr) {
				status = ReminderAttemptSkipped
			} else {
				return currentErr
			}
		} else if revision != candidate.Revision || !valid || !sameOccurrenceTime(current, candidate.Occurrence) {
			status = ReminderAttemptSkipped
		} else {
			notification := ReminderNotification{Identity: current.Identity(), StartLocal: current.StartLocal, EndLocal: current.EndLocal, Title: current.Title, Notes: current.Notes}
			if notifyErr := s.notifier.Submit(notificationCtx, notification); notifyErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if s.wasNotificationKilled() || s.coordinator.State() != RuntimeStateRunning {
					return errRecoveryRestart
				}
				status = ReminderAttemptFailed
				s.logger.Warn("recovery reminder notification failed", "schedule_id", candidate.Occurrence.ScheduleID, "original_date", candidate.Occurrence.OriginalDate, "error", notifyErr)
			}
		}
		results = append(results, RecoveryBatchResult{Identity: candidate.Occurrence.Identity(), Status: status})
	}
	if err := s.checkRecoveryEpoch(runner); err != nil {
		return err
	}
	if err := store.CommitRecoveryBatch(ctx, RecoveryBatchCommit{Results: results, CheckpointUnix: batch.CheckpointUnix, UpdatedAtUTC: snapshot.Now.UTC()}); err != nil {
		return fmt.Errorf("commit recovery batch: %w", err)
	}
	s.logger.Info("reminder recovery batch completed", "batch", runner.next+1, "candidates", len(batch.Candidates), "claimed", len(claims), "results", len(results), "checkpoint_unix", batch.CheckpointUnix)
	runner.next++
	return nil
}

// currentRecoveryOccurrence 在通知前重新读取事实，阻止旧 revision 或旧时间被发送。
func (s *ReminderScheduler) currentRecoveryOccurrence(ctx context.Context, identity domain.OccurrenceIdentity, location *time.Location) (domain.Occurrence, int64, bool, error) {
	source, err := s.store.GetOccurrenceSource(ctx, identity.ScheduleID)
	if err != nil {
		return domain.Occurrence{}, 0, false, fmt.Errorf("read recovery source %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
	}
	occurrence, ok, err := domain.OccurrenceForIdentity(source.Schedule, source.Overrides, identity, location)
	if err != nil {
		return domain.Occurrence{}, source.Schedule.Revision, false, fmt.Errorf("rebuild recovery occurrence %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
	}
	return occurrence, source.Schedule.Revision, ok, nil
}

// checkRecoveryEpoch 检查取消、生命周期和日程事实是否仍允许继续处理当前批次。
func (s *ReminderScheduler) checkRecoveryEpoch(runner *recoveryRunner) error {
	if s == nil || s.coordinator == nil || runner == nil {
		return errors.New("recovery coordinator is not configured")
	}
	if s.coordinator.State() != RuntimeStateRunning {
		return errRecoveryStopped
	}
	if runner.epoch != s.coordinator.Epoch() {
		return errRecoveryRestart
	}
	return nil
}

var (
	errRecoveryRestart = errors.New("recovery facts changed")
	errRecoveryStopped = errors.New("recovery runtime stopped")
)
