package chat

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// eventBatch 串行保存增量；首条期限不会被后续增量重置。
type eventBatch struct {
	mu         sync.Mutex
	repository ExecutionRepository
	call       ModelCall
	partial    ModelOutput
	timer      *time.Timer
	failure    error
	cancel     context.CancelFunc
	closed     bool
	batchID    uint64
}

func (b *eventBatch) append(kind EventType, content string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure != nil {
		return b.failure
	}
	if kind == EventText {
		b.partial.Message.Content += content
	} else {
		b.partial.Message.ReasoningContent += content
	}
	b.call.Output = &b.partial
	// 初始摘要正文不作为普通展示事件；摘要续写沿用已有增量契约。
	if b.call.CheckpointStage == "compact" && !b.call.Continuation {
		return nil
	}
	data, _ := json.Marshal(TextData{Content: content})
	b.call.PendingEvents = append(b.call.PendingEvents, Event{
		Type: kind,
		Data: data,
	})
	if len(b.call.PendingEvents) == 1 {
		b.batchID++
		batchID := b.batchID
		b.timer = time.AfterFunc(5*time.Second, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if !b.closed && b.failure == nil && b.batchID == batchID {
				b.flush()
			}
		})
	}
	if len(b.call.PendingEvents) >= 5 {
		b.flush()
	}
	return b.failure
}

func (b *eventBatch) flush() {
	// Stop 无法撤销已经触发并等待锁的回调，代次检查隔离旧批次。
	b.batchID++
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if err := b.repository.SaveEvents(context.Background(), b.call); err != nil {
		b.failure = err
		b.cancel()
		return
	}
	b.call.PendingEvents = nil
}

func (b *eventBatch) close() (ModelCall, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
	}
	return b.call, b.failure
}
