package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

type modelDelta struct {
	kind chat.EventType
	text string
	ack  chan error
}

type transactionalFault struct {
	chat.ExecutionRepository
	batchFailure bool
	failed       chan struct{}
	resume       chan struct{}
	attempts     atomic.Int32
}

type pauseFault struct{ chat.ExecutionRepository }

// Pause 仅注入正常退出保存失败，后续真实收尾仍可写入。
func (f pauseFault) Pause(context.Context, chat.ModelCall) error {
	return errors.New("退出保存失败")
}

// TestShutdownPersistenceFailureFinishesFailedWithTail 验证退出保存失败仍以真实尾批失败收尾。
func TestShutdownPersistenceFailureFinishesFailedWithTail(t *testing.T) {
	s, m, _ := eventFixture(t, func(ports *chat.Storage) { ports.Execution = pauseFault{ports.Execution} })
	id := createEventRun(t, s)
	m.emit(t, chat.EventText, "已接收尾批")
	s.Stop()
	r, err := s.GetRun(context.Background(), id)
	if err != nil || r.Status != chat.StatusFailed || r.Error == nil || r.Error.Code != "persistence_failed" || r.FinishedAt == nil {
		t.Fatalf("退出中的真实保存失败未按故障收尾: %+v %v", r, err)
	}
	p := eventPage(t, s, id, 0, 100)
	if len(p.Events) != 4 || p.Events[1].Type != chat.EventText || p.Events[3].Type != chat.EventEnd {
		t.Fatalf("失败收尾丢失尾批: %+v", p)
	}
}

// SaveEvents 在事务中途制造 JSON 约束失败，验证真实 SQLite 回滚。
func (f *transactionalFault) SaveEvents(ctx context.Context, call chat.ModelCall) error {
	if !f.batchFailure {
		return f.ExecutionRepository.SaveEvents(ctx, call)
	}
	broken := call
	broken.PendingEvents = append(append([]chat.Event{}, call.PendingEvents...), chat.Event{
		Type: chat.EventText,
		Data: json.RawMessage(`invalid`),
	})
	return f.ExecutionRepository.SaveEvents(ctx, broken)
}

// Finish 在第一次收尾事务中制造状态约束失败，并暂停重试供公开查询观察。
func (f *transactionalFault) Finish(ctx context.Context, call chat.ModelCall, run chat.Record) error {
	if f.attempts.Add(1) == 1 {
		broken := run
		broken.State = "invalid"
		err := f.ExecutionRepository.Finish(ctx, call, broken)
		close(f.failed)
		<-f.resume
		if err == nil {
			return errors.New("故障未触发约束")
		}
		return err
	}
	return f.ExecutionRepository.Finish(ctx, call, run)
}

type controlledModel struct {
	deltas chan modelDelta
	finish chan struct{}
}

// Complete 在模型边界提供逐条可控的响应，存储和运行用例仍使用真实实现。
func (m *controlledModel) Complete(ctx context.Context, _ chat.ModelConfig, _ chat.ModelRequest, emit func(chat.EventType, string) error) (chat.ModelOutput, *chat.RunError) {
	output := chat.ModelOutput{Message: chat.Message{Role: "assistant"}}
	for {
		select {
		case d := <-m.deltas:
			if d.kind == chat.EventText {
				output.Message.Content += d.text
			} else {
				output.Message.ReasoningContent += d.text
			}
			err := emit(d.kind, d.text)
			d.ack <- err
			if err != nil {
				return output, &chat.RunError{
					Code:    "persistence_failed",
					Message: "保存失败",
				}
			}
		case <-m.finish:
			return output, nil
		case <-ctx.Done():
			return output, &chat.RunError{
				Code:    "model_network_error",
				Message: "连接结束",
			}
		}
	}
}

func (m *controlledModel) emit(t *testing.T, kind chat.EventType, text string) {
	t.Helper()
	d := modelDelta{
		kind: kind,
		text: text,
		ack:  make(chan error, 1),
	}
	select {
	case m.deltas <- d:
	case <-time.After(3 * time.Second):
		t.Fatal("模型未读取增量")
	}
	select {
	case err := <-d.ack:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("增量未处理")
	}
}

func eventFixture(t *testing.T, configure ...func(*chat.Storage)) (*chat.Runtime, *controlledModel, chat.Storage) {
	t.Helper()
	ctx := context.Background()
	db := store.NewSqlite()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "events.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	ports := store.NewChatStorage(db)
	for _, apply := range configure {
		apply(&ports)
	}
	m := &controlledModel{
		deltas: make(chan modelDelta),
		finish: make(chan struct{}),
	}
	s := chat.NewService(ports, m, func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{
			Provider:        "test",
			Model:           "model",
			Key:             "key",
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
		}}, nil
	})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s, m, ports
}

func createEventRun(t *testing.T, s *chat.Runtime) string {
	t.Helper()
	r, err := s.CreateRun(context.Background(), chat.CreateRunInput{
		SessionID:      "session",
		Input:          "问题",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "key",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r.RunID
}

func eventPage(t *testing.T, s *chat.Runtime, id string, after int64, limit int) chat.EventPage {
	t.Helper()
	p, err := s.GetRunEvents(context.Background(), id, after, limit)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestEventsCommitAtFiveAndTailWithTerminal 验证五条提交与终态尾批的可见性。
func TestEventsCommitAtFiveAndTailWithTerminal(t *testing.T) {
	s, m, _ := eventFixture(t)
	id := createEventRun(t, s)
	for _, text := range []string{"甲", "乙", "丙", "丁"} {
		m.emit(t, chat.EventText, text)
	}
	if p := eventPage(t, s, id, 0, 100); len(p.Events) != 1 {
		t.Fatalf("五条前应只看到 start: %+v", p)
	}
	m.emit(t, chat.EventThinking, "想")
	p := eventPage(t, s, id, 0, 100)
	if len(p.Events) != 6 {
		t.Fatalf("满五条应提交原始五条增量: %+v", p)
	}
	for i, e := range p.Events {
		if e.ID != int64(i+1) {
			t.Fatalf("序号不连续: %+v", p)
		}
	}
	m.emit(t, chat.EventText, "尾")
	if p := eventPage(t, s, id, 6, 10); len(p.Events) != 0 {
		t.Fatalf("尾批提前可见: %+v", p)
	}
	close(m.finish)
	r := awaitRun(t, s, id)
	if r.FinishedAt == nil || r.Output == nil || *r.Output != "甲乙丙丁尾" {
		t.Fatalf("终态正文: %+v", r)
	}
	p = eventPage(t, s, id, 6, 10)
	var data chat.TextData
	if len(p.Events) != 2 || p.Events[0].ID != 7 || p.Events[1].Type != chat.EventEnd {
		t.Fatalf("尾批和终态未一起保存: %+v", p)
	}
	if err := json.Unmarshal(p.Events[0].Data, &data); err != nil || data.Content != "尾" {
		t.Fatalf("原始增量丢失: %+v %v", data, err)
	}
}

// TestFirstPendingEventDeadlineIsNotExtended 验证后续增量不延后首条期限。
func TestFirstPendingEventDeadlineIsNotExtended(t *testing.T) {
	s, m, _ := eventFixture(t)
	id := createEventRun(t, s)
	m.emit(t, chat.EventText, "首条")
	time.Sleep(3 * time.Second)
	m.emit(t, chat.EventThinking, "后来")
	deadline := time.Now().Add(3 * time.Second)
	for {
		p := eventPage(t, s, id, 1, 10)
		if len(p.Events) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("首条五秒期限被后续增量延后: %+v", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(m.finish)
	awaitRun(t, s, id)
}

// TestPaginationAndSubscriptionCrossNewCommits 验证分页与订阅交接以及旧清理隔离。
func TestPaginationAndSubscriptionCrossNewCommits(t *testing.T) {
	s, m, _ := eventFixture(t)
	id := createEventRun(t, s)
	ctx := context.Background()
	p := eventPage(t, s, id, 0, 1)
	if p.HasMore || p.NextAfterSeq != 1 {
		t.Fatalf("历史末尾不是终态: %+v", p)
	}
	for i := 0; i < 5; i++ {
		m.emit(t, chat.EventText, "首批")
	}
	p = eventPage(t, s, id, p.NextAfterSeq, 2)
	if len(p.Events) != 2 || p.NextAfterSeq != 3 || !p.HasMore {
		t.Fatalf("分页未接到新提交: %+v", p)
	}
	old, err := s.Subscribe(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Subscribe(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	old.Close()
	for i := 0; i < 5; i++ {
		m.emit(t, chat.EventThinking, "次批")
	}
	close(m.finish)
	for seq := int64(4); seq <= 12; seq++ {
		select {
		case e, ok := <-current.Events:
			if !ok || e.ID != seq {
				t.Fatalf("新连接受旧清理影响或交接丢失: seq=%d event=%+v open=%v", seq, e, ok)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("缺少事件 %d", seq)
		}
	}
	p = eventPage(t, s, id, 999, 10)
	if len(p.Events) != 0 || p.NextAfterSeq != 999 || p.HasMore {
		t.Fatalf("空页推进游标: %+v", p)
	}
}

// TestSlowOrDisconnectedSubscriberDoesNotStopRun 验证未消费的订阅不会阻塞模型或取消运行。
func TestSlowOrDisconnectedSubscriberDoesNotStopRun(t *testing.T) {
	s, m, _ := eventFixture(t)
	id := createEventRun(t, s)
	sub, err := s.Subscribe(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	for i := 0; i < 5; i++ {
		m.emit(t, chat.EventText, "正文")
	}
	sub.Close()
	m.emit(t, chat.EventThinking, "保留思考")
	close(m.finish)
	r := awaitRun(t, s, id)
	if r.Status != chat.StatusSucceeded {
		t.Fatalf("传输影响运行: %+v", r)
	}
	if _, err := s.Subscribe(context.Background(), id, 7); !errors.Is(err, chat.ErrRunTerminal) {
		t.Fatalf("真正终态未拒绝新流: %v", err)
	}
	if err := s.DeleteSession(context.Background(), "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRun(context.Background(), id); !errors.Is(err, chat.ErrRunNotFound) {
		t.Fatal(err)
	}
	if _, err := s.GetRunEvents(context.Background(), id, 0, 10); !errors.Is(err, chat.ErrRunNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Subscribe(context.Background(), id, 0); !errors.Is(err, chat.ErrRunNotFound) {
		t.Fatal(err)
	}
}

// TestCancelAndNormalStopFlushPendingTail 验证不足五条的取消与正常退出尾批立即保存。
func TestCancelAndNormalStopFlushPendingTail(t *testing.T) {
	for _, cancelRun := range []bool{true, false} {
		t.Run(fmt.Sprint(cancelRun), func(t *testing.T) {
			s, m, ports := eventFixture(t)
			id := createEventRun(t, s)
			m.emit(t, chat.EventText, "旧输出")
			if cancelRun {
				if _, err := s.CancelRun(context.Background(), id); err != nil {
					t.Fatal(err)
				}
				p := eventPage(t, s, id, 0, 10)
				if len(p.Events) != 3 || p.Events[2].Type != chat.EventEnd {
					t.Fatalf("取消尾批丢失: %+v", p)
				}
				return
			}
			s.Stop()
			p := eventPage(t, s, id, 0, 10)
			if len(p.Events) != 2 || p.Events[1].Type != chat.EventText {
				t.Fatalf("退出尾批丢失或产生终态: %+v", p)
			}
			replacement := &controlledModel{
				deltas: make(chan modelDelta),
				finish: make(chan struct{}),
			}
			restarted := chat.NewService(ports, replacement, func() ([]chat.ModelConfig, error) {
				return []chat.ModelConfig{{
					Provider:        "test",
					Model:           "model",
					Key:             "key",
					ContextWindow:   32768,
					MaxOutputTokens: 8192,
				}}, nil
			})
			if err := restarted.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer restarted.Stop()
			replacement.emit(t, chat.EventText, "重发输出")
			close(replacement.finish)
			r := awaitRun(t, restarted, id)
			if r.Output == nil || *r.Output != "重发输出" {
				t.Fatalf("旧输出混入重发正文: %+v", r)
			}
			p = eventPage(t, restarted, id, 0, 10)
			if len(p.Events) != 4 || p.Events[2].ID != 3 {
				t.Fatalf("恢复事件丢失或重编号: %+v", p)
			}
		})
	}
}

// TestFailedBatchAndTerminalTransactionsExposeNoEventsOrSequenceGaps 验证真实事务回滚和重试后的事件完整性。
func TestFailedBatchAndTerminalTransactionsExposeNoEventsOrSequenceGaps(t *testing.T) {
	for _, batchFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(batchFailure), func(t *testing.T) {
			fault := &transactionalFault{
				batchFailure: batchFailure,
				failed:       make(chan struct{}),
				resume:       make(chan struct{}),
			}
			s, m, _ := eventFixture(t, func(ports *chat.Storage) { fault.ExecutionRepository = ports.Execution; ports.Execution = fault })
			defer close(fault.resume)
			id := createEventRun(t, s)
			count := 2
			if batchFailure {
				count = 5
			}
			for i := 0; i < count; i++ {
				d := modelDelta{
					kind: chat.EventText,
					text: "文",
					ack:  make(chan error, 1),
				}
				m.deltas <- d
				err := <-d.ack
				if (err != nil) != (batchFailure && i == 4) {
					t.Fatalf("批次错误: %v", err)
				}
			}
			if !batchFailure {
				close(m.finish)
			}
			select {
			case <-fault.failed:
			case <-time.After(3 * time.Second):
				t.Fatal("未触发事务故障")
			}
			p := eventPage(t, s, id, 0, 100)
			if len(p.Events) != 1 || p.NextAfterSeq != 1 {
				t.Fatalf("事务失败泄露事件: %+v", p)
			}
			r, err := s.GetRun(context.Background(), id)
			if err != nil || r.FinishedAt != nil || r.Output != nil {
				t.Fatalf("失败事务泄露概况: %+v %v", r, err)
			}
			fault.resume <- struct{}{}
			deadline := time.Now().Add(8 * time.Second)
			for {
				r, err = s.GetRun(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				if r.FinishedAt != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("收尾未重试成功")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if batchFailure && (r.Status != chat.StatusFailed || r.Error == nil || r.Error.Code != "persistence_failed") {
				t.Fatalf("持久化错误误记成传输错误: %+v", r)
			}
			p = eventPage(t, s, id, 0, 100)
			want := count + 2
			if batchFailure {
				want++
			}
			if len(p.Events) != want {
				t.Fatalf("事件丢失或重复: %+v", p)
			}
			for i, e := range p.Events {
				if e.ID != int64(i+1) {
					t.Fatalf("序号缺口: %+v", p)
				}
			}
		})
	}
}
