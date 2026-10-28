package chat_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestInvalidSubmissionDoesNotRegisterKey 验证公共用例拒绝缺少字段且不留下会话或幂等记录。
func TestInvalidSubmissionDoesNotRegisterKey(t *testing.T) {
	for _, field := range []string{"session", "input", "provider", "model", "key"} {
		t.Run(field, func(t *testing.T) {
			s, _, _ := eventFixture(t)
			input := admissionInput("session", "key")
			switch field {
			case "session":
				input.SessionID = " "
			case "input":
				input.Input = "\t"
			case "provider":
				input.Provider = ""
			case "model":
				input.Model = ""
			case "key":
				input.IdempotencyKey = ""
			}
			if _, err := s.CreateRun(context.Background(), input); !errors.Is(err, chat.ErrInvalidRequest) {
				t.Fatalf("非法提交应拒绝: %v", err)
			}
			sessions, err := s.ListSessions(context.Background())
			if err != nil || len(sessions) != 0 {
				t.Fatalf("拒绝请求留下会话: %v %v", sessions, err)
			}
			if _, err = s.CreateRun(context.Background(), admissionInput("session", "key")); err != nil {
				t.Fatalf("原键不能重新使用: %v", err)
			}
		})
	}
}

func admissionInput(session, key string) chat.CreateRunInput {
	return chat.CreateRunInput{
		SessionID:      session,
		Input:          "保留原始输入 ",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: key,
	}
}

type admissionModel struct {
	starts  atomic.Int32
	active  atomic.Int32
	peak    atomic.Int32
	started chan struct{}
	release chan struct{}
	failure *chat.RunError
}

// Complete 仅在模型边界控制结束时机，便于并发验证真实服务和 SQLite。
func (m *admissionModel) Complete(ctx context.Context, _ chat.ModelConfig, _ chat.ModelRequest, _ func(chat.EventType, string) error) (chat.ModelOutput, *chat.RunError) {
	active := m.active.Add(1)
	defer m.active.Add(-1)
	for peak := m.peak.Load(); active > peak; peak = m.peak.Load() {
		if m.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	m.starts.Add(1)
	if m.started != nil {
		m.started <- struct{}{}
	}
	select {
	case <-m.release:
		return chat.ModelOutput{
			Message: chat.Message{
				Role:    "assistant",
				Content: "实际结果",
			},
			FinishReason: "stop",
		}, m.failure
	case <-ctx.Done():
		return chat.ModelOutput{}, nil
	}
}

// TestRecoveryQueuesExcessUnfinishedSessions 验证保存耗尽遗留超过三个未完成会话时，恢复仍只并发推进三个。
func TestRecoveryQueuesExcessUnfinishedSessions(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := ports.Runs.Create(ctx, chat.Record{
			Run: chat.Run{
				RunID:     fmt.Sprintf("recover-%d", i),
				SessionID: fmt.Sprintf("session-%d", i),
				Provider:  "test",
				Model:     "model",
				Input:     "原始输入",
				CreatedAt: time.Now(),
			},
			State:          chat.StateRunning,
			IdempotencyKey: "key",
		}); err != nil {
			t.Fatal(err)
		}
	}
	m := &admissionModel{
		started: make(chan struct{}, 4),
		release: make(chan struct{}),
	}
	s := startAdmissionService(t, ports, m, admissionSettings)
	for i := 0; i < 3; i++ {
		select {
		case <-m.started:
		case <-time.After(3 * time.Second):
			t.Fatal("前三个恢复运行未启动")
		}
	}
	select {
	case <-m.started:
		t.Fatal("第四个恢复运行绕过三个并发名额")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := s.CreateRun(ctx, admissionInput("new-session", "key")); !errors.Is(err, chat.ErrSessionConcurrencyLimit) {
		t.Fatalf("恢复未重建全部占用: %v", err)
	}
	// 不依赖恢复的调度顺序，所有原运行都能通过公共用例取消并保存终态。
	for i := 0; i < 4; i++ {
		if _, err := s.CancelRun(ctx, fmt.Sprintf("recover-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if m.peak.Load() > 3 {
		t.Fatalf("恢复执行超过全局上限: %d", m.peak.Load())
	}
	for i := 0; i < 4; i++ {
		run := awaitFinishedRun(t, s, fmt.Sprintf("recover-%d", i))
		if run.Status != chat.StatusCancelled {
			t.Fatalf("排队运行丢失或被伪造终态: %+v", run)
		}
	}
}

// TestConcurrentReplayStartsOnlyOneRun 验证同键并发重放只创建并启动一次，且原文参与冲突判断。
func TestConcurrentReplayStartsOnlyOneRun(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	m := &admissionModel{release: make(chan struct{})}
	s := startAdmissionService(t, ports, m, admissionSettings)
	input := admissionInput("same-session", "same-key")
	var wg sync.WaitGroup
	gate := make(chan struct{})
	ids := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			r, err := s.CreateRun(context.Background(), input)
			if err != nil || r.Status != chat.StatusRunning || r.Output != nil || r.Error != nil {
				t.Errorf("运行中重放: %+v %v", r, err)
			}
			ids <- r.RunID
		}()
	}
	close(gate)
	wg.Wait()
	close(ids)
	var original string
	for id := range ids {
		if original == "" {
			original = id
		}
		if id != original {
			t.Fatalf("同键产生不同运行: %s / %s", original, id)
		}
	}
	close(m.release)
	awaitRun(t, s, original)
	session, err := s.GetSession(context.Background(), input.SessionID)
	if err != nil || len(session.RunIDs) != 1 || m.starts.Load() != 1 {
		t.Fatalf("同键重复创建或启动: %+v %v starts=%d", session, err, m.starts.Load())
	}
	input.Input = "保留原始输入"
	if _, err = s.CreateRun(context.Background(), input); !errors.Is(err, chat.ErrIdempotencyConflict) {
		t.Fatalf("输入尾空格未参与冲突判断: %v", err)
	}
}

// TestConcurrentAdmissionLimitsSessions 验证同会话互斥及跨会话竞争最多取得三个名额。
func TestConcurrentAdmissionLimitsSessions(t *testing.T) {
	for _, sameSession := range []bool{true, false} {
		t.Run(fmt.Sprintf("same_session=%t", sameSession), func(t *testing.T) {
			_, ports, _ := admissionDatabase(t)
			m := &admissionModel{release: make(chan struct{})}
			s := startAdmissionService(t, ports, m, admissionSettings)
			var accepted atomic.Int32
			var rejected atomic.Int32
			var wg sync.WaitGroup
			gate := make(chan struct{})
			for i := 0; i < 24; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-gate
					session, key := fmt.Sprintf("session-%d", i), "same-key"
					expected := chat.ErrSessionConcurrencyLimit
					if sameSession {
						session, key = "same-session", fmt.Sprintf("key-%d", i)
						expected = chat.ErrSessionBusy
					}
					_, err := s.CreateRun(context.Background(), admissionInput(session, key))
					if err == nil {
						accepted.Add(1)
					} else if errors.Is(err, expected) {
						rejected.Add(1)
					} else {
						t.Errorf("准入返回意外错误: %v", err)
					}
				}(i)
			}
			close(gate)
			wg.Wait()
			want := int32(3)
			if sameSession {
				want = 1
			}
			if accepted.Load() != want || rejected.Load() != 24-want {
				t.Fatalf("准入数量错误: accepted=%d rejected=%d", accepted.Load(), rejected.Load())
			}
			sessions, err := s.ListSessions(context.Background())
			if err != nil || len(sessions) != int(want) {
				t.Fatalf("拒绝请求留下会话: %v %v", sessions, err)
			}
			for _, id := range sessions {
				session, err := s.GetSession(context.Background(), id)
				if err != nil || len(session.RunIDs) != 1 {
					t.Fatalf("同会话出现重叠运行: %+v %v", session, err)
				}
				// 历史查询和订阅替换不增加运行占用。
				for i := 0; i < 4; i++ {
					sub, err := s.Subscribe(context.Background(), session.RunIDs[0], 0)
					if err != nil {
						t.Fatal(err)
					}
					sub.Close()
				}
			}
			awaitModelStarts(t, m, want)
			close(m.release)
			s.Stop()
			if m.starts.Load() != want {
				t.Fatalf("被拒绝请求仍启动模型: %d", m.starts.Load())
			}
		})
	}
}

func awaitModelStarts(t *testing.T, m *admissionModel, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for m.starts.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("模型启动数: got=%d want=%d", m.starts.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestConfigurationRejectionLeavesKeyReusable 验证新建配置门禁不保存会话、幂等键或占用。
func TestConfigurationRejectionLeavesKeyReusable(t *testing.T) {
	for _, mode := range []string{"unconfigured", "missing_key", "invalid_budget", "settings_error"} {
		t.Run(mode, func(t *testing.T) {
			_, ports, _ := admissionDatabase(t)
			configs, _ := admissionSettings()
			var settingsError error
			expected := chat.ErrModelUnavailable
			switch mode {
			case "unconfigured":
				configs = nil
				expected = chat.ErrInvalidRequest
			case "missing_key":
				configs[0].Key = ""
			case "invalid_budget":
				configs[0].ContextWindow = configs[0].MaxOutputTokens
			case "settings_error":
				settingsError = errors.New("配置暂时不可读")
			}
			m := &admissionModel{release: make(chan struct{})}
			s := startAdmissionService(t, ports, m, func() ([]chat.ModelConfig, error) { return configs, settingsError })
			input := admissionInput("first", "key")
			if _, err := s.CreateRun(context.Background(), input); !errors.Is(err, expected) {
				t.Fatalf("配置拒绝: %v", err)
			}
			sessions, err := s.ListSessions(context.Background())
			if err != nil || len(sessions) != 0 || m.starts.Load() != 0 {
				t.Fatalf("拒绝请求留下会话或启动模型: %v %v starts=%d", sessions, err, m.starts.Load())
			}
			configs, settingsError = admissionSettings()
			input.Input = "修复配置后的输入"
			if _, err = s.CreateRun(context.Background(), input); err != nil {
				t.Fatalf("配置拒绝留下幂等键: %v", err)
			}
			for _, session := range []string{"second", "third"} {
				if _, err = s.CreateRun(context.Background(), admissionInput(session, "key")); err != nil {
					t.Fatalf("配置拒绝泄漏名额: %v", err)
				}
			}
		})
	}
}

// TestRestartRebuildsAllSlotsBeforeAdmission 验证启动恢复先重建三个名额，拒绝请求不登记键，重复终态取消不多释放名额。
func TestRestartRebuildsAllSlotsBeforeAdmission(t *testing.T) {
	db, ports, path := admissionDatabase(t)
	m := &admissionModel{release: make(chan struct{})}
	s := startAdmissionService(t, ports, m, admissionSettings)
	ctx := context.Background()
	var ids []string
	for _, session := range []string{"first", "second", "third"} {
		r, err := s.CreateRun(ctx, admissionInput(session, "key"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.RunID)
	}
	awaitModelStarts(t, m, 3)
	s.Stop()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	resumedModel := &admissionModel{release: make(chan struct{})}
	resumed := startAdmissionService(t, store.NewChatStorage(db), resumedModel, admissionSettings)
	if _, err := resumed.CreateRun(ctx, admissionInput("fourth", "rejected-key")); !errors.Is(err, chat.ErrSessionConcurrencyLimit) {
		t.Fatalf("恢复前接纳第四会话: %v", err)
	}
	if _, err := resumed.CreateRun(ctx, admissionInput("first", "busy-key")); !errors.Is(err, chat.ErrSessionBusy) {
		t.Fatalf("恢复前接纳同会话: %v", err)
	}
	for i, session := range []string{"first", "second", "third"} {
		r, err := resumed.CreateRun(ctx, admissionInput(session, "key"))
		if err != nil || r.RunID != ids[i] || r.Status != chat.StatusRunning {
			t.Fatalf("恢复重放原运行: %+v %v", r, err)
		}
	}
	awaitModelStarts(t, resumedModel, 3)
	if status, err := resumed.CancelRun(ctx, ids[0]); err != nil || status != chat.StatusCancelled {
		t.Fatalf("恢复后取消: %s %v", status, err)
	}
	awaitFinishedRun(t, resumed, ids[0])
	retry := admissionInput("fourth", "rejected-key")
	retry.Input = "先前被拒绝的请求没有登记键"
	if _, err := resumed.CreateRun(ctx, retry); err != nil {
		t.Fatalf("全局拒绝消耗键或取消未释放名额: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := resumed.CancelRun(ctx, ids[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := resumed.CreateRun(ctx, admissionInput("fifth", "key")); !errors.Is(err, chat.ErrSessionConcurrencyLimit) {
		t.Fatalf("重复取消多释放名额: %v", err)
	}
	if status, err := resumed.CancelRun(ctx, ids[1]); err != nil || status != chat.StatusCancelled {
		t.Fatalf("取消第二会话: %s %v", status, err)
	}
	awaitFinishedRun(t, resumed, ids[1])
	if _, err := resumed.CreateRun(ctx, admissionInput("first", "busy-key")); err != nil {
		t.Fatalf("同会话忙碌拒绝消耗键: %v", err)
	}
}

type createRollback struct {
	chat.RunRepository
	fail atomic.Bool
}

// Create 仅在第一次创建的真实事务中注入约束错误，验证回滚和名额回退。
func (f *createRollback) Create(ctx context.Context, r chat.Record) error {
	if f.fail.Swap(false) {
		r.State = "invalid"
	}
	return f.RunRepository.Create(ctx, r)
}

// TestCreationRollbackDoesNotConsumeKeyOrSlot 验证创建事务失败后原会话、原键及全部三个名额仍可使用。
func TestCreationRollbackDoesNotConsumeKeyOrSlot(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	f := &createRollback{RunRepository: ports.Runs}
	f.fail.Store(true)
	ports.Runs = f
	m := &admissionModel{release: make(chan struct{})}
	s := startAdmissionService(t, ports, m, admissionSettings)
	input := admissionInput("first", "key")
	if _, err := s.CreateRun(context.Background(), input); err == nil {
		t.Fatal("未触发真实创建事务失败")
	}
	if _, err := s.GetSession(context.Background(), input.SessionID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("失败事务留下会话: %v", err)
	}
	input.Input = "失败后更正的输入"
	if _, err := s.CreateRun(context.Background(), input); err != nil {
		t.Fatalf("失败请求留下幂等键或名额: %v", err)
	}
	for _, session := range []string{"second", "third"} {
		if _, err := s.CreateRun(context.Background(), admissionInput(session, "key")); err != nil {
			t.Fatalf("创建失败泄漏全局名额: %v", err)
		}
	}
}

// TestReplayPrecedesConfigurationAndAdmission 验证重放绕过新建配置、忙碌与全局门禁，删除优先于重放及冲突。
func TestReplayPrecedesConfigurationAndAdmission(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	m := &admissionModel{release: make(chan struct{})}
	var unavailable bool
	s := startAdmissionService(t, ports, m, func() ([]chat.ModelConfig, error) {
		if unavailable {
			return nil, errors.New("配置加载失败")
		}
		return admissionSettings()
	})
	input := admissionInput("first", "key")
	original, err := s.CreateRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"second", "third"} {
		if _, err = s.CreateRun(context.Background(), admissionInput(session, "key")); err != nil {
			t.Fatal(err)
		}
	}
	unavailable = true
	input.Provider, input.Model = "removed", "unknown"
	replay, err := s.CreateRun(context.Background(), input)
	if err != nil || replay.RunID != original.RunID || replay.Status != chat.StatusRunning {
		t.Fatalf("重放被新建门禁阻断: %+v %v", replay, err)
	}
	run, err := s.GetRun(context.Background(), original.RunID)
	if err != nil || run.Provider != "test" || run.Model != "model" {
		t.Fatalf("重放修改原模型: %+v %v", run, err)
	}
	input.Input = "不同输入"
	if _, err = s.CreateRun(context.Background(), input); !errors.Is(err, chat.ErrIdempotencyConflict) {
		t.Fatalf("冲突未先于配置拒绝: %v", err)
	}
	if err = s.DeleteSession(context.Background(), input.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRun(context.Background(), input); !errors.Is(err, chat.ErrSessionDeleted) {
		t.Fatalf("删除会话未优先拒绝: %v", err)
	}
}

// TestTerminalReceiptSurvivesReopeningDatabase 验证成功和失败回执跨进程模型配置失效后仍原样重放。
func TestTerminalReceiptSurvivesReopeningDatabase(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			db, ports, path := admissionDatabase(t)
			m := &admissionModel{release: make(chan struct{})}
			if failed {
				m.failure = &chat.RunError{
					Code:    "model_network_error",
					Message: "已有部分正文后断开",
				}
			}
			close(m.release)
			s := startAdmissionService(t, ports, m, admissionSettings)
			input := admissionInput("session", "original-key")
			original, err := s.CreateRun(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			finished := awaitRun(t, s, original.RunID)
			s.Stop()
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			if err = db.Connect(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			resumed := startAdmissionService(t, store.NewChatStorage(db), m, func() ([]chat.ModelConfig, error) {
				return nil, errors.New("旧配置已不可用")
			})
			input.Provider, input.Model = "other-provider", "other-model"
			replay, err := resumed.CreateRun(context.Background(), input)
			if err != nil || replay.RunID != original.RunID || replay.Status != finished.Status || replay.Output == nil || *replay.Output != "实际结果" || m.starts.Load() != 1 {
				t.Fatalf("跨进程回执丢失: %+v %v starts=%d", replay, err, m.starts.Load())
			}
			if failed && (replay.Error == nil || *replay.Error != *m.failure) || !failed && replay.Error != nil {
				t.Fatalf("终态错误未保留: %+v", replay.Error)
			}
		})
	}
}

func admissionSettings() ([]chat.ModelConfig, error) {
	return []chat.ModelConfig{{
		Provider:        "test",
		Model:           "model",
		Key:             "fixture-key",
		ContextWindow:   32768,
		MaxOutputTokens: 8192,
	}}, nil
}

func admissionDatabase(t *testing.T) (*store.Sqlite, chat.Storage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admission.db")
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PrepareSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, store.NewChatStorage(db), path
}

func startAdmissionService(t *testing.T, ports chat.Storage, m chat.ModelClient, settings func() ([]chat.ModelConfig, error)) *chat.Runtime {
	t.Helper()
	s := chat.NewService(ports, m, settings, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

type blockedFinish struct {
	chat.ExecutionRepository
	entered chan struct{}
	release chan struct{}
}

// Finish 暂停终态事务，公开查询可观察取消投影及仍然占用的名额。
func (f *blockedFinish) Finish(ctx context.Context, call chat.ModelCall, r chat.Record) error {
	if r.RunID == "recover-cancel" {
		close(f.entered)
		<-f.release
	}
	return f.ExecutionRepository.Finish(ctx, call, r)
}

// TestRecoveredCancellationHoldsAdmissionWithoutCallingModel 验证取消恢复不重发模型，终态提交前仍占会话和全局名额。
func TestRecoveredCancellationHoldsAdmissionWithoutCallingModel(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	ctx := context.Background()
	r := chat.Record{
		Run: chat.Run{
			RunID:     "recover-cancel",
			SessionID: "cancel-session",
			Provider:  "test",
			Model:     "model",
			Input:     "保留原始输入 ",
			CreatedAt: time.Now(),
		},
		State:          chat.StateWaitForCancel,
		IdempotencyKey: "key",
	}
	if err := ports.Runs.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	_, err := ports.Execution.BeginCall(ctx, chat.ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		Input:     chat.ModelRequest{Model: "model"},
		State:     chat.StateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &blockedFinish{
		ExecutionRepository: ports.Execution,
		entered:             make(chan struct{}),
		release:             make(chan struct{}),
	}
	ports.Execution = f
	m := &admissionModel{release: make(chan struct{})}
	s := startAdmissionService(t, ports, m, admissionSettings)
	defer close(f.release)
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("取消恢复未进入收尾")
	}
	if m.starts.Load() != 0 {
		t.Fatalf("取消恢复重新调用模型: %d", m.starts.Load())
	}
	replay, err := s.CreateRun(ctx, admissionInput(r.SessionID, "key"))
	if err != nil || replay.RunID != r.RunID || replay.Status != chat.StatusCancelled {
		t.Fatalf("取消投影重放: %+v %v", replay, err)
	}
	run, err := s.GetRun(ctx, r.RunID)
	if err != nil || run.FinishedAt != nil || run.Status != chat.StatusCancelled {
		t.Fatalf("取消投影误作终态: %+v %v", run, err)
	}
	if _, err = s.CreateRun(ctx, admissionInput(r.SessionID, "next")); !errors.Is(err, chat.ErrSessionBusy) {
		t.Fatalf("取消收尾提前释放会话: %v", err)
	}
	for _, session := range []string{"second", "third"} {
		if _, err = s.CreateRun(ctx, admissionInput(session, "key")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateRun(ctx, admissionInput("fourth", "key")); !errors.Is(err, chat.ErrSessionConcurrencyLimit) {
		t.Fatalf("取消收尾提前释放全局名额: %v", err)
	}
}
