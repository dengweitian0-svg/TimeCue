package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const defaultScheduleListLimit = 50

// Service 编排日程应用用例。
type Service struct {
	store ScheduleStore
	clock Clock
}

// NewService 创建日程应用服务。
func NewService(store ScheduleStore, clock Clock) *Service {
	return &Service{store: store, clock: clock}
}

// CreateSchedule 创建并持久化单次日程。
func (s *Service) CreateSchedule(ctx context.Context, request CreateScheduleRequest) (ScheduleDTO, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return ScheduleDTO{}, domain.NewValidation("title must not be empty", "title")
	}
	if request.StartUnix == nil {
		return ScheduleDTO{}, domain.NewValidation("start time is required", "start_unix")
	}
	if request.EndUnix == nil {
		return ScheduleDTO{}, domain.NewValidation("end time is required", "end_unix")
	}
	if s.clock == nil {
		return ScheduleDTO{}, errors.New("schedule clock is not configured")
	}
	if s.store == nil {
		return ScheduleDTO{}, errors.New("schedule store is not configured")
	}

	// 每个请求只读取一次时间环境，确保边界计算和审计时间来自同一个快照。
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	candidate, err := domain.ValidateSingleEndpoints(snapshot, *request.StartUnix, *request.EndUnix)
	if err != nil {
		return ScheduleDTO{}, err
	}
	candidate.Title = title

	// 领域校验是进入存储副作用前的最后守卫，非法请求不能调用写方法。
	schedule, err := s.store.CreateSchedule(ctx, candidate)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("create schedule: %w", err)
	}
	return scheduleDTO(schedule), nil
}

// GetSchedule 读取单条日程。
func (s *Service) GetSchedule(ctx context.Context, id domain.ScheduleID) (ScheduleDTO, error) {
	if s.store == nil {
		return ScheduleDTO{}, errors.New("schedule store is not configured")
	}
	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return ScheduleDTO{}, fmt.Errorf("get schedule %d: %w", id, err)
	}
	// DTO 是内部状态到受控对外视图的投影，不暴露原始开始时间等内部字段。
	return scheduleDTO(schedule), nil
}

// ListSchedules 按游标分页读取日程。
func (s *Service) ListSchedules(ctx context.Context, request ListSchedulesRequest) (ListSchedulesResponse, error) {
	if s.store == nil {
		return ListSchedulesResponse{}, errors.New("schedule store is not configured")
	}
	if request.Limit < 0 {
		return ListSchedulesResponse{}, domain.NewValidation("limit must not be negative", "limit")
	}
	if request.Limit == 0 {
		request.Limit = defaultScheduleListLimit
	}
	// 列表只读取已经持久化的事实，不在分页过程中展开未来发生实例。
	return s.store.ListSchedules(ctx, request)
}

// GetScheduleTimeBounds 返回当前时间边界。
func (s *Service) GetScheduleTimeBounds(ctx context.Context) (TimeBoundsDTO, error) {
	if s.clock == nil {
		return TimeBoundsDTO{}, errors.New("schedule clock is not configured")
	}
	// 边界接口与创建接口一样只读取一次时钟，避免请求内跨越本地日期造成不一致。
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return TimeBoundsDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	return timeBoundsDTO(domain.CalculateTimeBounds(snapshot)), nil
}

// scheduleDTO 将领域日程转换为应用层的受控视图。
func scheduleDTO(schedule domain.Schedule) ScheduleDTO {
	return ScheduleDTO{
		ID:              int64(schedule.ID),
		Title:           schedule.Title,
		Cron:            schedule.Cron,
		Open:            schedule.Open,
		FirstStartLocal: domain.FormatLocalTime(schedule.FirstStartLocal),
		DurationMinutes: schedule.DurationMinutes,
		Revision:        schedule.Revision,
		CreatedAtUTC:    schedule.CreatedAtUTC,
		UpdatedAtUTC:    schedule.UpdatedAtUTC,
	}
}
