package chat

import (
	"context"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/google/uuid"
)

type mockSeed struct {
	name   string
	input  string
	output string
}

// seedSessions 为默认临时 Mock 填入可搜索、可继续对话的完成会话。
func (m *Mock) seedSessions() {
	seeds := []mockSeed{
		{
			name:   "日程安排讨论",
			input:  "帮我把本周的工作和运动安排整理成清晰的计划。",
			output: "晨间瑜伽安排在周二。本周还可以周一整理项目任务，周三进行力量训练，周五复盘工作进度。",
		},
		{
			name:   "周末阅读计划",
			input:  "我想安排一个轻松的周末阅读计划。",
			output: "《海边灯塔》可以作为周末阅读。周六上午读小说并记录摘抄，周日读历史书，再整理读书笔记。",
		},
		{
			name:   "每月账单提醒",
			input:  "请帮我规划每月固定账单的提醒事项。",
			output: "把每月付款记录在蓝色账本。一日检查房租和网络账单，五日核对信用卡，十日确认水电费用。",
		},
	}

	wasAutoRun := m.options.AutoRun
	originalNow := m.now
	m.options.AutoRun = false
	defer func() {
		m.options.AutoRun = wasAutoRun
		m.now = originalNow
	}()

	base := time.Now().In(model.BeijingLocation()).Add(-time.Hour)
	clockStep := 0
	m.now = func() time.Time {
		at := base.Add(time.Duration(clockStep) * time.Minute)
		clockStep++
		return at
	}

	ctx := context.Background()
	for _, seed := range seeds {
		sessionID := uuid.NewString()
		runID, err := m.CreateRun(ctx, CreateRunInput{
			SessionID: sessionID, Input: seed.input, Model: mockModel,
			IdempotencyKey: "mock-seed:" + uuid.NewString(),
		})
		if err != nil {
			continue
		}
		m.mu.Lock()
		m.sessions[sessionID].name = seed.name
		m.mu.Unlock()

		if _, err := m.PublishEvent(runID, EventStart, mustJSON(StartData{})); err != nil {
			continue
		}
		if _, err := m.PublishEvent(runID, EventThinking, mustJSON(ThinkingData{Content: "正在整理这段对话。"})); err != nil {
			continue
		}
		if _, err := m.PublishEvent(runID, EventText, mustJSON(TextData{Content: seed.output})); err != nil {
			continue
		}
		_ = m.ConfirmRun(runID, StatusSucceeded, stringPointer(seed.output), nil, mustJSON(EndData{StopReason: "stop"}))
	}
}
