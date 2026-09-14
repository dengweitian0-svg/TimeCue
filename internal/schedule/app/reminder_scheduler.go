package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const reminderDeliveryWindow = 5 * time.Second

// ReminderSourceStore 合并调度器所需的批量源读取和单日程一致读取能力。
type ReminderSourceStore interface {
	OccurrenceQueryStore
	OccurrenceSourceReader
}

// ReminderScheduler 维护所有开放日程的最早提醒批次，并只为全局最近批次持有计时器。
type ReminderScheduler struct {
	store        ReminderSourceStore
	attempts     ReminderAttemptStore
	notifier     ReminderNotifier
	clock        Clock
	timerFactory TimerFactory
	logger       ReminderLogger
	coordinator  *MutationCoordinator

	mu         sync.RWMutex
	status     SchedulerStatus
	candidates map[domain.ScheduleID]reminderCandidateBatch

	notificationMu     sync.Mutex
	notificationCancel context.CancelFunc
	notificationKilled bool
}

// reminderCandidateBatch 保存候选发生、其日程 revision 和读取时的调度 epoch。
type reminderCandidateBatch struct {
	batch    domain.ReminderCandidateBatch
	revision int64
	epoch    uint64
}

// realTimer 把标准库计时器收敛为应用层可替换的窄接口。
type realTimer struct{ timer *time.Timer }

// C 返回标准库计时器的到期通道。
func (t *realTimer) C() <-chan time.Time { return t.timer.C }

// Stop 停止标准库计时器。
func (t *realTimer) Stop() bool { return t.timer.Stop() }

// Reset 重置标准库计时器。
func (t *realTimer) Reset(duration time.Duration) bool { return t.timer.Reset(duration) }

// defaultTimerFactory 创建生产环境的标准库计时器。
func defaultTimerFactory(duration time.Duration) Timer {
	return &realTimer{timer: time.NewTimer(duration)}
}

// NewReminderScheduler 创建纯 Go 到期提醒调度器。
func NewReminderScheduler(store ReminderSourceStore, attempts ReminderAttemptStore, notifier ReminderNotifier, clock Clock, timerFactory TimerFactory, logger ReminderLogger, coordinator *MutationCoordinator) *ReminderScheduler {
	if timerFactory == nil {
		timerFactory = defaultTimerFactory
	}
	if coordinator == nil {
		coordinator = NewMutationCoordinator()
	}
	if logger == nil {
		logger = discardReminderLogger{}
	}
	return &ReminderScheduler{
		store: store, attempts: attempts, notifier: notifier, clock: clock,
		timerFactory: timerFactory, logger: logger, coordinator: coordinator,
		status:     SchedulerStatus{Available: true, Epoch: coordinator.Epoch(), State: coordinator.State()},
		candidates: make(map[domain.ScheduleID]reminderCandidateBatch),
	}
}

// GetRuntimeStatus 返回当前可用性、最近错误和失效 epoch。
func (s *ReminderScheduler) GetRuntimeStatus() SchedulerStatus {
	if s == nil || s.coordinator == nil {
		return SchedulerStatus{}
	}
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	status.Epoch = s.coordinator.Epoch()
	status.State = s.coordinator.State()
	return status
}

// ApplyRuntimeEvent 应用生命周期或时间环境事件，并按当前环境重建提醒候选。
// 事件先取消在途通知，再等待协调器完成状态转换；这样停止/休眠不会被阻塞
// 在通知适配器的同步提交上，旧 epoch 也无法在事件之后继续派发。
func (s *ReminderScheduler) ApplyRuntimeEvent(ctx context.Context, event RuntimeEvent) error {
	if s == nil || s.coordinator == nil {
		return errors.New("reminder scheduler is nil")
	}
	switch event.Type {
	case RuntimeEventSuspend, RuntimeEventResume, RuntimeEventClockChanged, RuntimeEventStop:
	default:
		return domain.NewValidation("runtime event type is invalid", "event")
	}
	// 先取消同步通知提交，再等待协调器锁；否则到期回调持锁等待
	// 通知适配器返回时，停止事件无法取得锁来取消该请求。
	s.cancelNotification()
	if err := s.coordinator.ApplyRuntimeEvent(event); err != nil {
		return err
	}

	state := s.coordinator.State()
	if state != RuntimeStateRunning {
		s.clearCandidates()
		return nil
	}

	if event.Type != RuntimeEventResume && event.Type != RuntimeEventClockChanged {
		return nil
	}
	if event.Snapshot.Now.IsZero() || event.Snapshot.Location == nil {
		return s.refresh(ctx)
	}
	if err := s.refreshAt(ctx, event.Snapshot); err != nil {
		s.markUnavailable(err)
		return err
	}
	return nil
}

// Run 启动唯一调度循环；取消 ctx 后返回，不创建每条日程独立 goroutine。
func (s *ReminderScheduler) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("reminder scheduler is nil")
	}
	if err := s.validate(); err != nil {
		s.markUnavailable(err)
		return err
	}
	if err := s.refresh(ctx); err != nil {
		s.markUnavailable(err)
	}

	var timer Timer
	for {
		if err := ctx.Err(); err != nil {
			if timer != nil {
				timer.Stop()
			}
			return err
		}
		if s.coordinator.State() != RuntimeStateRunning {
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			if err := waitForReminderWake(ctx, s.coordinator.Wake()); err != nil {
				return err
			}
			continue
		}
		if !s.GetRuntimeStatus().Available {
			if err := waitForReminderWake(ctx, s.coordinator.Wake()); err != nil {
				return err
			}
			if s.coordinator.State() == RuntimeStateRunning {
				if err := s.refresh(ctx); err != nil {
					s.markUnavailable(err)
				}
			}
			continue
		}

		now, err := s.clock.Snapshot(ctx)
		if err != nil {
			s.markUnavailable(fmt.Errorf("snapshot scheduler time: %w", err))
			continue
		}
		candidate, ok := s.nextCandidate()
		if !ok {
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			if err := waitForReminderWake(ctx, s.coordinator.Wake()); err != nil {
				return err
			}
			if err := s.refresh(ctx); err != nil {
				s.markUnavailable(err)
			}
			continue
		}

		delay := candidate.batch.Occurrences[0].StartLocal.Sub(now.Now)
		if delay < 0 {
			delay = 0
		}
		if timer == nil {
			timer = s.timerFactory(delay)
		} else {
			timer.Stop()
			timer.Reset(delay)
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-s.coordinator.Wake():
			timer.Stop()
			timer = nil
			if s.coordinator.State() == RuntimeStateRunning {
				if err := s.refresh(ctx); err != nil {
					s.markUnavailable(err)
				}
			} else {
				s.clearCandidates()
			}
		case <-timer.C():
			if err := s.processDue(ctx, candidate); err != nil {
				s.markUnavailable(err)
			}
		}
	}
}

// validate 检查调度循环启动所需的所有窄端口。
func (s *ReminderScheduler) validate() error {
	if s == nil || s.store == nil {
		return errors.New("reminder source store is not configured")
	}
	if s.attempts == nil {
		return errors.New("reminder attempt store is not configured")
	}
	if s.notifier == nil {
		return errors.New("reminder notifier is not configured")
	}
	if s.clock == nil {
		return errors.New("reminder clock is not configured")
	}
	return nil
}

// refresh 从当前时间之后重建每个日程的最早候选，启动和变更唤醒都走同一事实读取路径。
func (s *ReminderScheduler) refresh(ctx context.Context) error {
	s.coordinator.Lock()
	defer s.coordinator.unlock()
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot reminder refresh time: %w", err)
	}
	return s.refreshLocked(ctx, snapshot)
}

// refreshAt 使用生命周期事件携带的最新环境快照重建候选。
func (s *ReminderScheduler) refreshAt(ctx context.Context, snapshot domain.TimeSnapshot) error {
	s.coordinator.Lock()
	defer s.coordinator.unlock()
	return s.refreshLocked(ctx, snapshot)
}

// refreshLocked 在已持有协调锁时读取已提交事实并替换全部候选。
func (s *ReminderScheduler) refreshLocked(ctx context.Context, snapshot domain.TimeSnapshot) error {
	if s.coordinator.state != RuntimeStateRunning {
		s.clearCandidatesLocked()
		return nil
	}
	sources, err := s.readSources(ctx)
	if err != nil {
		return err
	}
	updated := make(map[domain.ScheduleID]reminderCandidateBatch, len(sources))
	for _, source := range sources {
		batch, ok, batchErr := domain.NextReminderCandidateBatch(source.Schedule, source.Overrides, snapshot.Now, snapshot.Location)
		if batchErr != nil {
			return fmt.Errorf("calculate next reminder for schedule %d: %w", source.Schedule.ID, batchErr)
		}
		if ok {
			updated[source.Schedule.ID] = reminderCandidateBatch{batch: batch, revision: source.Schedule.Revision, epoch: s.coordinator.epoch}
		}
	}
	s.mu.Lock()
	s.candidates = updated
	s.status.Available = true
	s.status.LastError = ""
	s.status.Epoch = s.coordinator.epoch
	s.status.State = s.coordinator.state
	s.mu.Unlock()
	return nil
}

// readSources 分页读取全部日程定义及例外，不展开或持久化发生实例。
func (s *ReminderScheduler) readSources(ctx context.Context) ([]OccurrenceSourceDTO, error) {
	var result []OccurrenceSourceDTO
	var cursor domain.ScheduleID
	for {
		page, err := s.store.ListOccurrenceSources(ctx, ListOccurrenceSourcesRequest{AfterScheduleID: cursor})
		if err != nil {
			return nil, fmt.Errorf("read reminder occurrence sources: %w", err)
		}
		result = append(result, page.Items...)
		if page.NextCursor <= cursor || len(page.Items) == 0 {
			return result, nil
		}
		cursor = page.NextCursor
	}
}

// nextCandidate 线性扫描 map，选择实际开始时间最早且身份稳定的批次。
func (s *ReminderScheduler) nextCandidate() (reminderCandidateBatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result reminderCandidateBatch
	var resultID domain.ScheduleID
	for id, candidate := range s.candidates {
		if len(candidate.batch.Occurrences) == 0 {
			continue
		}
		if resultID == 0 || candidate.batch.Occurrences[0].StartLocal.Before(result.batch.Occurrences[0].StartLocal) ||
			(candidate.batch.Occurrences[0].StartLocal.Equal(result.batch.Occurrences[0].StartLocal) && id < resultID) {
			result, resultID = candidate, id
		}
	}
	return result, resultID != 0
}

// processDue 在统一协调顺序中完成资格校验、占用、通知和结果回写。
func (s *ReminderScheduler) processDue(ctx context.Context, candidate reminderCandidateBatch) error {
	notificationCtx, unregister := s.registerNotification(ctx)
	defer unregister()
	s.coordinator.Lock()
	defer s.coordinator.unlock()
	if len(candidate.batch.Occurrences) == 0 {
		return nil
	}
	if s.coordinator.state != RuntimeStateRunning {
		s.removeCandidate(candidate.batch.Occurrences[0].ScheduleID)
		return nil
	}
	if candidate.epoch != s.coordinator.epoch {
		// 变更信号与计时器同时就绪时，先移除旧条目；唤醒信号随后会触发事实重建。
		s.removeCandidate(candidate.batch.Occurrences[0].ScheduleID)
		return nil
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot due reminder time: %w", err)
	}
	if snapshot.HasEnvironmentChange() {
		// 到期回调不能仅依赖事件顺序判断休眠或墙钟异常；当前快照明确
		// 标记异常时，拒绝整批发送并从异常快照之后重新建立候选。
		s.clearCandidatesLocked()
		return s.refreshLocked(ctx, snapshot)
	}
	if candidate.batch.Occurrences[0].StartLocal.After(snapshot.Now) {
		return nil
	}
	source, err := s.store.GetOccurrenceSource(ctx, candidate.batch.Occurrences[0].ScheduleID)
	if err != nil {
		if isReminderNotFound(err) {
			s.removeCandidate(candidate.batch.Occurrences[0].ScheduleID)
			return nil
		}
		return fmt.Errorf("read due reminder source: %w", err)
	}
	if source.Schedule.Revision != candidate.revision || !source.Schedule.Open {
		s.replaceCandidateLocked(source, snapshot.Now)
		return nil
	}
	if snapshot.Now.Sub(candidate.batch.Occurrences[0].StartLocal) > reminderDeliveryWindow {
		s.logger.Debug("reminder batch expired", "schedule_id", source.Schedule.ID, "batch_unix", candidate.batch.Occurrences[0].StartLocal.Unix())
		return s.advanceCandidateLocked(source, candidate, snapshot.Location)
	}

	submitted, failed, skipped := 0, 0, 0
	for _, occurrence := range candidate.batch.Occurrences {
		current, valid, checkErr := domain.OccurrenceForIdentity(source.Schedule, source.Overrides, occurrence.Identity(), snapshot.Location)
		if checkErr != nil {
			return fmt.Errorf("validate reminder occurrence %d/%s: %w", occurrence.ScheduleID, occurrence.OriginalDate, checkErr)
		}
		if !valid || !sameOccurrenceTime(current, occurrence) {
			skipped++
			continue
		}
		claimed, claimErr := s.attempts.ClaimReminderAttempt(ctx, occurrence.Identity(), snapshot.Now.UTC())
		if claimErr != nil {
			return fmt.Errorf("claim reminder %d/%s: %w", occurrence.ScheduleID, occurrence.OriginalDate, claimErr)
		}
		if !claimed {
			skipped++
			continue
		}

		// 占用后再次读取和检查，覆盖占用与通知提交之间的版本、状态和实际时间竞态。
		if s.coordinator.epoch != candidate.epoch {
			if writeErr := s.writeReminderStatus(ctx, occurrence.Identity(), ReminderAttemptSkipped); writeErr != nil {
				return writeErr
			}
			skipped++
			continue
		}
		latestSnapshot, snapshotErr := s.clock.Snapshot(ctx)
		if snapshotErr != nil {
			return fmt.Errorf("snapshot reminder notification time: %w", snapshotErr)
		}
		if latestSnapshot.HasEnvironmentChange() {
			// 初次到期检查后仍可能收到延迟到达的电源/时钟事实；
			// 通知提交前再次拒绝，避免在环境已经改变时误发旧候选。
			if writeErr := s.writeReminderStatus(ctx, occurrence.Identity(), ReminderAttemptSkipped); writeErr != nil {
				return writeErr
			}
			skipped++
			continue
		}
		latest, latestErr := s.store.GetOccurrenceSource(ctx, occurrence.ScheduleID)
		if latestErr != nil {
			if isReminderNotFound(latestErr) {
				if writeErr := s.writeReminderStatus(ctx, occurrence.Identity(), ReminderAttemptSkipped); writeErr != nil {
					return writeErr
				}
				skipped++
				continue
			}
			return fmt.Errorf("recheck reminder source: %w", latestErr)
		}
		current, valid, checkErr = domain.OccurrenceForIdentity(latest.Schedule, latest.Overrides, occurrence.Identity(), latestSnapshot.Location)
		if checkErr != nil {
			return fmt.Errorf("recheck reminder occurrence %d/%s: %w", occurrence.ScheduleID, occurrence.OriginalDate, checkErr)
		}
		if latest.Schedule.Revision != candidate.revision || !latest.Schedule.Open || !valid || !sameOccurrenceTime(current, occurrence) || latestSnapshot.Now.Sub(current.StartLocal) > reminderDeliveryWindow {
			if writeErr := s.writeReminderStatus(ctx, occurrence.Identity(), ReminderAttemptSkipped); writeErr != nil {
				return writeErr
			}
			skipped++
			continue
		}

		notification := ReminderNotification{Identity: current.Identity(), StartLocal: current.StartLocal, EndLocal: current.EndLocal, Title: current.Title, Notes: current.Notes}
		if notifyErr := s.notifier.Submit(notificationCtx, notification); notifyErr != nil {
			resultStatus := ReminderAttemptFailed
			if s.wasNotificationKilled() || s.coordinator.state != RuntimeStateRunning {
				resultStatus = ReminderAttemptSkipped
			}
			if writeErr := s.writeReminderStatus(ctx, occurrence.Identity(), resultStatus); writeErr != nil {
				return writeErr
			}
			if resultStatus == ReminderAttemptSkipped {
				skipped++
			} else {
				s.logger.Warn("reminder notification failed", "schedule_id", occurrence.ScheduleID, "original_date", occurrence.OriginalDate, "error", notifyErr)
				failed++
			}
			continue
		}
		if writeErr := s.writeReminderStatus(ctx, occurrence.Identity(), ReminderAttemptSubmitted); writeErr != nil {
			return writeErr
		}
		submitted++
	}
	s.logger.Info("reminder batch processed", "schedule_id", candidate.batch.Occurrences[0].ScheduleID, "batch_unix", candidate.batch.Occurrences[0].StartLocal.Unix(), "submitted", submitted, "failed", failed, "skipped", skipped)
	return s.advanceCandidateLocked(source, candidate, snapshot.Location)
}

// writeReminderStatus 回写有限终态；回写失败由调用方提升为不可用并停止计时。
func (s *ReminderScheduler) writeReminderStatus(ctx context.Context, identity domain.OccurrenceIdentity, status ReminderAttemptStatus) error {
	if err := s.attempts.SetReminderAttemptStatus(ctx, identity, status); err != nil {
		return fmt.Errorf("write reminder %s result %d/%s: %w", status, identity.ScheduleID, identity.OriginalDate, err)
	}
	return nil
}

// advanceCandidateLocked 只推进调度游标，不创建提醒事实；删除或关闭日程会从 map 中移除。
func (s *ReminderScheduler) advanceCandidateLocked(source OccurrenceSourceDTO, candidate reminderCandidateBatch, location *time.Location) error {
	next, ok, err := domain.AdvanceReminderCandidateBatch(source.Schedule, source.Overrides, candidate.batch, location)
	if err != nil {
		return fmt.Errorf("advance reminder candidate for schedule %d: %w", source.Schedule.ID, err)
	}
	s.setCandidate(source.Schedule, next, ok)
	return nil
}

// replaceCandidateLocked 用最新事实替换单个过期候选，避免版本变化导致到期计时器忙循环。
func (s *ReminderScheduler) replaceCandidateLocked(source OccurrenceSourceDTO, after time.Time) {
	if !source.Schedule.Open {
		s.removeCandidate(source.Schedule.ID)
		return
	}
	next, ok, err := domain.NextReminderCandidateBatch(source.Schedule, source.Overrides, after, after.Location())
	if err != nil {
		s.removeCandidate(source.Schedule.ID)
		return
	}
	s.setCandidate(source.Schedule, next, ok)
}

// setCandidate 写入或移除单个日程的当前最早批次。
func (s *ReminderScheduler) setCandidate(schedule domain.Schedule, batch domain.ReminderCandidateBatch, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok && schedule.Open {
		s.candidates[schedule.ID] = reminderCandidateBatch{batch: batch, revision: schedule.Revision, epoch: s.coordinator.epoch}
	} else {
		delete(s.candidates, schedule.ID)
	}
}

// clearCandidates 清空所有日程候选并关闭提醒派发资格。
func (s *ReminderScheduler) clearCandidates() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.clearCandidatesLocked()
	s.mu.Unlock()
}

// clearCandidatesLocked 清空候选；调用方必须持有 s.mu。
func (s *ReminderScheduler) clearCandidatesLocked() {
	s.candidates = make(map[domain.ScheduleID]reminderCandidateBatch)
	s.status.Available = false
	s.status.LastError = ""
	s.status.State = s.coordinator.state
	s.status.Epoch = s.coordinator.epoch
}

// registerNotification 为一次到期处理登记可被运行时事件取消的通知上下文。
func (s *ReminderScheduler) registerNotification(parent context.Context) (context.Context, func()) {
	notificationCtx, cancel := context.WithCancel(parent)
	s.notificationMu.Lock()
	s.notificationCancel = cancel
	s.notificationKilled = false
	s.notificationMu.Unlock()
	return notificationCtx, func() {
		cancel()
		s.notificationMu.Lock()
		if s.notificationCancel != nil {
			s.notificationCancel = nil
		}
		s.notificationMu.Unlock()
	}
}

// cancelNotification 取消当前通知提交并记录其属于运行时事件的中断。
func (s *ReminderScheduler) cancelNotification() {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	if s.notificationCancel != nil {
		s.notificationKilled = true
		s.notificationCancel()
	}
}

// wasNotificationKilled 判断最近一次通知失败是否由运行时事件主动取消。
func (s *ReminderScheduler) wasNotificationKilled() bool {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	return s.notificationKilled
}

// removeCandidate 移除已经永久删除的日程候选。
func (s *ReminderScheduler) removeCandidate(id domain.ScheduleID) {
	s.mu.Lock()
	delete(s.candidates, id)
	s.mu.Unlock()
}

// markUnavailable 记录最终错误并使调度器进入等待唤醒状态，避免失败忙循环。
func (s *ReminderScheduler) markUnavailable(err error) {
	if s == nil || s.coordinator == nil || err == nil {
		return
	}
	epoch := s.coordinator.Epoch()
	s.mu.Lock()
	s.status.Available = false
	s.status.LastError = err.Error()
	s.status.Epoch = epoch
	s.status.State = s.coordinator.State()
	s.mu.Unlock()
	s.logger.Error("reminder scheduler unavailable", "error", err)
}

// sameOccurrenceTime 比较稳定身份对应的实际区间，防止只校验身份而误发改期前候选。
func sameOccurrenceTime(left, right domain.Occurrence) bool {
	return left.Identity() == right.Identity() && left.StartLocal.Unix() == right.StartLocal.Unix() && left.EndLocal.Unix() == right.EndLocal.Unix()
}

// waitForReminderWake 等待变更信号或上下文取消；无候选时不创建计时器。
func waitForReminderWake(ctx context.Context, wake <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake:
		return nil
	}
}

// isNotFound 判断应用/存储包装链中的资源不存在错误。
func isReminderNotFound(err error) bool {
	var businessErr *domain.Error
	return errors.As(err, &businessErr) && businessErr.Code == domain.NotFound
}

// discardReminderLogger 丢弃未配置的可选调度日志。
type discardReminderLogger struct{}

// Debug 丢弃调试日志。
func (discardReminderLogger) Debug(string, ...any) {}

// Info 丢弃信息日志。
func (discardReminderLogger) Info(string, ...any) {}

// Warn 丢弃警告日志。
func (discardReminderLogger) Warn(string, ...any) {}

// Error 丢弃错误日志。
func (discardReminderLogger) Error(string, ...any) {}
