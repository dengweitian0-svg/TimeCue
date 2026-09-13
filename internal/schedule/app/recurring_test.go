package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// countedRecurringClock 记录快照次数，验证请求只读取一次时间环境。
type countedRecurringClock struct {
	snapshot domain.TimeSnapshot
	calls    int
}

// Snapshot 返回固定快照并统计调用次数。
func (c *countedRecurringClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
	c.calls++
	return c.snapshot, nil
}

// recurringFixture 构造跨 UTC 日期边界的重复应用测试环境。
func recurringFixture() (*fakeStore, *countedRecurringClock, CreateScheduleRequest) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, loc)
	start, end, until := now.Add(9*time.Hour).Unix(), now.Add(10*time.Hour).Unix(), now.Unix()
	cron := "0 9 * * *"
	return &fakeStore{}, &countedRecurringClock{snapshot: domain.TimeSnapshot{Now: now, Location: loc}}, CreateScheduleRequest{Title: " recurring ", StartUnix: &start, EndUnix: &end, Cron: &cron, UntilUnix: &until}
}

// assertRecurringCode 验证应用错误保留机器可读分类。
func assertRecurringCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	var business *domain.Error
	if !errors.As(err, &business) || business.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

// TestServiceRecurringCreate 验证重复创建使用本地截止日期、实际时长和一次快照。
func TestServiceRecurringCreate(t *testing.T) {
	s, clock, request := recurringFixture()
	got, err := NewService(s, clock).CreateSchedule(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cron == nil || *got.Cron != *request.Cron || got.UntilDateLocal == nil || *got.UntilDateLocal != "2026-09-12" || got.MonthEndPolicy != nil || got.Title != "recurring" || got.DurationMinutes != 60 || !got.Open || got.Revision != 1 {
		t.Fatalf("created = %+v", got)
	}
	if clock.calls != 1 || s.createCalls != 1 {
		t.Fatalf("calls = %d/%d", clock.calls, s.createCalls)
	}
}

// TestServiceMonthlyCreate 验证创建月规则时策略由领域候选自动带出。
func TestServiceMonthlyCreate(t *testing.T) {
	s, clock, request := recurringFixture()
	cron := "0 9 31 * *"
	request.Cron = &cron

	got, err := NewService(s, clock).CreateSchedule(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.MonthEndPolicy == nil || *got.MonthEndPolicy != domain.MonthEndPolicyClamp {
		t.Fatalf("monthly policy = %#v", got.MonthEndPolicy)
	}
	if clock.calls != 1 || s.createCalls != 1 {
		t.Fatalf("calls = %d/%d", clock.calls, s.createCalls)
	}
}

// TestServiceRecurringCreateRejectsBeforeWrite 验证非法规则、缺失截止和单次携带重复字段均零写入。
func TestServiceRecurringCreateRejectsBeforeWrite(t *testing.T) {
	for _, name := range []string{"bad cron", "missing until", "null cron", "single cutoff", "null cutoff", "cutoff before first"} {
		t.Run(name, func(t *testing.T) {
			s, clock, request := recurringFixture()
			switch name {
			case "bad cron":
				value := "*/5 * * * *"
				request.Cron = &value
			case "missing until":
				request.UntilUnix = nil
			case "null cron":
				request.Cron = nil
				request.CronPresent = true
				request.UntilUnix = nil
			case "single cutoff":
				request.Cron = nil
			case "null cutoff":
				request.Cron = nil
				request.UntilUnix = nil
				request.UntilUnixPresent = true
			case "cutoff before first":
				value := *request.UntilUnix - 86400
				request.UntilUnix = &value
			}
			_, err := NewService(s, clock).CreateSchedule(context.Background(), request)
			assertRecurringCode(t, err, domain.Validation)
			if s.createCalls != 0 {
				t.Fatal("invalid create reached store")
			}
		})
	}
}

// TestServiceRecurringCronOnlyPreservesFields 验证历史定义仅改 cron 时补丁不携带其他业务字段。
func TestServiceRecurringCronOnlyPreservesFields(t *testing.T) {
	s, clock, request := recurringFixture()
	service := NewService(s, clock)
	if _, err := service.CreateSchedule(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	s.getResult, s.updateResult = s.created, s.created
	clock.snapshot.Now = clock.snapshot.Now.AddDate(2, 0, 0)
	cron := "30 10 * * 1"
	if _, err := service.UpdateSchedule(context.Background(), s.created.ID, UpdateScheduleRequest{Cron: &cron, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	c := s.updated
	if c.Cron == nil || *c.Cron != cron || !c.CronPresent || c.UntilDateLocal != nil || c.Title != nil || c.FirstStartLocal != nil || c.DurationMinutes != nil || c.ExpectedRevision != 1 || !c.UpdatedAtUTC.Equal(clock.snapshot.Now) {
		t.Fatalf("patch = %+v", c)
	}
	if clock.calls != 2 || s.updateCalls != 1 {
		t.Fatalf("calls = %d/%d", clock.calls, s.updateCalls)
	}
}

// TestServiceRecurringUpdateRejectsBeforeWrite 验证非法合并状态、清空字段和旧版本不调用写端口。
func TestServiceRecurringUpdateRejectsBeforeWrite(t *testing.T) {
	for _, name := range []string{"bad cron", "monthly cron", "monthly to weekly", "clear cron", "clear until", "old revision", "early cutoff", "start after cutoff", "single cutoff"} {
		t.Run(name, func(t *testing.T) {
			s, clock, create := recurringFixture()
			service := NewService(s, clock)
			if _, err := service.CreateSchedule(context.Background(), create); err != nil {
				t.Fatal(err)
			}
			s.getResult = s.created
			cron := "0 9 * * 0"
			request := UpdateScheduleRequest{Cron: &cron, ExpectedRevision: 1}
			want := domain.Validation
			switch name {
			case "bad cron":
				cron = "0 9 * * 7"
			case "monthly cron":
				cron = "0 9 31 * *"
			case "monthly to weekly":
				monthly := "0 9 31 * *"
				s.getResult.Cron = &monthly
			case "clear cron":
				request.Cron = nil
				request.CronPresent = true
			case "clear until":
				request.UntilUnixPresent = true
			case "old revision":
				s.getResult.Revision = 2
				want = domain.StaleRevision
			case "early cutoff":
				until := *create.UntilUnix - 86400
				request.UntilUnix = &until
			case "start after cutoff":
				start := *create.StartUnix + 86400
				request.StartUnix = &start
			case "single cutoff":
				s.getResult.Cron = nil
				request.Cron = nil
				request.UntilUnix = create.UntilUnix
			}
			_, err := service.UpdateSchedule(context.Background(), s.created.ID, request)
			assertRecurringCode(t, err, want)
			if s.updateCalls != 0 {
				t.Fatal("invalid update reached store")
			}
			if name == "old revision" && clock.calls != 1 {
				t.Fatal("stale request read clock")
			}
		})
	}
}

// TestServiceRecurringMergedEndpoints 验证整组修改和仅改一个端点时复用已有持续时长。
func TestServiceRecurringMergedEndpoints(t *testing.T) {
	for _, mode := range []string{"both", "start", "end", "cutoff"} {
		t.Run(mode, func(t *testing.T) {
			s, clock, create := recurringFixture()
			service := NewService(s, clock)
			if _, err := service.CreateSchedule(context.Background(), create); err != nil {
				t.Fatal(err)
			}
			s.getResult, s.updateResult = s.created, s.created
			start, end, until := *create.StartUnix+86400, *create.EndUnix+86400+3600, *create.UntilUnix+86400
			title := " new "
			request := UpdateScheduleRequest{Title: &title, UntilUnix: &until, ExpectedRevision: 1}
			if mode == "both" || mode == "start" {
				request.StartUnix = &start
			}
			if mode == "both" || mode == "end" {
				request.EndUnix = &end
			}
			if _, err := service.UpdateSchedule(context.Background(), s.created.ID, request); err != nil {
				t.Fatal(err)
			}
			c := s.updated
			if c.UntilDateLocal == nil || *c.UntilDateLocal != "2026-09-13" || c.Title == nil || *c.Title != "new" {
				t.Fatalf("patch = %+v", c)
			}
			if mode == "cutoff" {
				if c.FirstStartLocal != nil || c.DurationMinutes != nil {
					t.Fatal("cutoff changed endpoints")
				}
				return
			}
			wantDuration := 60
			if mode == "both" {
				wantDuration = 120
			}
			if mode == "end" {
				start = end - 3600
			}
			if c.FirstStartLocal == nil || c.FirstStartLocal.Unix() != start || c.DurationMinutes == nil || *c.DurationMinutes != wantDuration {
				t.Fatalf("endpoints = %+v", c)
			}
		})
	}
}

// TestRecurringJSONFields 验证创建和更新对省略、null、零值的存在性及非法类型处理。
func TestRecurringJSONFields(t *testing.T) {
	for _, input := range []string{`{}`, `{"cron":null,"until_unix":null}`, `{"cron":"0 9 * * *","until_unix":0}`} {
		create, err := DecodeCreateSchedule([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		update, err := DecodeUpdateSchedule([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		present := input != `{}`
		if create.CronPresent != present || create.UntilUnixPresent != present || update.CronPresent != present || update.UntilUnixPresent != present {
			t.Fatalf("presence lost: %s", input)
		}
		if strings.Contains(input, `:0`) && (create.UntilUnix == nil || update.UntilUnix == nil || *create.UntilUnix != 0 || *update.UntilUnix != 0) {
			t.Fatal("zero lost")
		}
	}
	for _, input := range []string{`[]`, `null`, `true`, `{"cron":3}`, `{"until_unix":1.0}`, `{"until_unix":1e2}`, `{"until_unix":"1"}`, `{"until_unix":true}`, `{"until_unix":[]}`, `{"until_unix":9223372036854775808}`} {
		_, err := DecodeCreateSchedule([]byte(input))
		assertRecurringCode(t, err, domain.Validation)
		_, err = DecodeUpdateSchedule([]byte(input))
		assertRecurringCode(t, err, domain.Validation)
	}
	date := "2026-09-13"
	for _, until := range []*string{nil, &date} {
		data, err := EncodeSchedule(ScheduleDTO{UntilDateLocal: until})
		if err != nil {
			t.Fatal(err)
		}
		want := `"until_date_local":null`
		if until != nil {
			want = fmt.Sprintf(`"until_date_local":%q`, *until)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("DTO = %s", data)
		}
	}
}

// TestRecurringUpdateKeepsCutoffOnMissingMidnight 验证午夜 DST 缺失日的截止日期不会退回前一天。
func TestRecurringUpdateKeepsCutoffOnMissingMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2018, 11, 4, 9, 0, 0, 0, loc)
	start, end, until := first.Unix(), first.Add(time.Hour).Unix(), first.Add(2*time.Hour).Unix()
	cron := "0 9 * * *"
	s := &fakeStore{}
	clock := &countedRecurringClock{snapshot: domain.TimeSnapshot{Now: first, Location: loc}}
	service := NewService(s, clock)
	if _, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{Title: "midnight DST", StartUnix: &start, EndUnix: &end, Cron: &cron, UntilUnix: &until}); err != nil {
		t.Fatal(err)
	}
	s.getResult, s.updateResult = s.created, s.created
	newStart := start + 3600
	if _, err := service.UpdateSchedule(context.Background(), s.created.ID, UpdateScheduleRequest{StartUnix: &newStart, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if s.updated.UntilDateLocal != nil || *s.created.UntilDateLocal != "2018-11-04" || s.updated.FirstStartLocal == nil || s.updated.FirstStartLocal.Unix() != newStart {
		t.Fatalf("patch = %+v", s.updated)
	}
}
