//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// MessageHandler 处理共享 Win32 消息线程收到的一条消息。
type MessageHandler func(message uint32, wParam, lParam uintptr) error

// MessageHandlerToken 标识消息线程中注册的一组处理器。
type MessageHandlerToken uint64

// MessageThread 是时钟与托盘共用的单一消息线程抽象。
type MessageThread struct {
	mu        sync.Mutex
	handlers  map[MessageHandlerToken]MessageHandler
	nextID    MessageHandlerToken
	threadID  uint32
	started   bool
	closed    bool
	done      chan struct{}
	loopDone  chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// NewMessageThread 创建带受控消息处理器的 Win32 消息线程。
func NewMessageThread(handler MessageHandler) (*MessageThread, error) {
	thread := &MessageThread{handlers: make(map[MessageHandlerToken]MessageHandler), done: make(chan struct{}), loopDone: make(chan struct{})}
	if handler != nil {
		if _, err := thread.RegisterHandler(handler); err != nil {
			return nil, fmt.Errorf("create message thread: %w", err)
		}
	}
	return thread, nil
}

// RegisterHandler 在消息泵启动前或运行期间注册一个处理器，返回可用于移除的令牌。
func (t *MessageThread) RegisterHandler(handler MessageHandler) (MessageHandlerToken, error) {
	if t == nil {
		return 0, errors.New("register message handler: thread is nil")
	}
	if handler == nil {
		return 0, errors.New("register message handler: handler is nil")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, errors.New("register message handler: thread is closed")
	}
	t.nextID++
	t.handlers[t.nextID] = handler
	return t.nextID, nil
}

// UnregisterHandler 移除指定处理器；重复移除保持幂等。
func (t *MessageThread) UnregisterHandler(token MessageHandlerToken) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("unregister message handler: thread is closed")
	}
	delete(t.handlers, token)
	return nil
}

// Start 锁定操作系统线程并同步运行隐藏消息窗口的消息泵。
func (t *MessageThread) Start(ctx context.Context) error {
	if t == nil {
		return errors.New("start message thread: thread is nil")
	}
	if ctx == nil {
		return errors.New("start message thread: context is nil")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("start message thread: thread is closed")
	}
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.threadID = windows.GetCurrentThreadId()
	t.started = true
	t.mu.Unlock()
	defer close(t.loopDone)
	className, err := windows.UTF16PtrFromString(fmt.Sprintf("TimeCueMessageThread-%d", t.threadID))
	if err != nil {
		return fmt.Errorf("encode message thread class: %w", err)
	}
	windowProc := windows.NewCallback(messageThreadWindowProc)
	class := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WindowProc: windowProc, ClassName: className}
	atom, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("register message thread class: %w", callErr)
	}
	window, _, callErr := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, uintptr(hwndMessage), 0, 0, 0)
	if window == 0 {
		procUnregisterClass.Call(uintptr(unsafe.Pointer(className)), 0)
		return fmt.Errorf("create message thread window: %w", callErr)
	}
	t.mu.Lock()
	messageThreadWindows.Store(window, t)
	t.mu.Unlock()
	defer func() {
		messageThreadWindows.Delete(window)
		procDestroyWindow.Call(window)
		procUnregisterClass.Call(uintptr(unsafe.Pointer(className)), 0)
	}()
	finished := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-t.done:
			if err := t.postQuit(); err != nil {
				t.recordError(err)
			}
		case <-ctx.Done():
			if err := t.postQuit(); err != nil {
				t.recordError(err)
			}
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		<-watchDone
	}()
	for {
		var message winMessage
		result, _, callErr := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if result == getMessageError {
			return fmt.Errorf("read message thread message: %w", callErr)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

// Close 请求消息泵退出并等待线程释放窗口，且保持幂等。
func (t *MessageThread) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		started := t.started
		close(t.done)
		t.mu.Unlock()
		if !started {
			close(t.loopDone)
		} else if err := t.postQuit(); err != nil {
			t.recordError(err)
		}
	})
	<-t.loopDone
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeErr
}

// messageThreadWindowProc 将消息转交给受控处理器，不保存平台状态。
func messageThreadWindowProc(window windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	value, ok := messageThreadWindows.Load(uintptr(window))
	if ok {
		thread := value.(*MessageThread)
		thread.mu.Lock()
		handlers := make([]MessageHandler, 0, len(thread.handlers))
		for _, handler := range thread.handlers {
			handlers = append(handlers, handler)
		}
		thread.mu.Unlock()
		for _, handler := range handlers {
			if err := handler(message, wParam, lParam); err != nil {
				thread.recordError(fmt.Errorf("handle Windows message 0x%04x: %w", message, err))
			}
		}
	}
	result, _, _ := procDefWindowProc.Call(uintptr(window), uintptr(message), wParam, lParam)
	return result
}

// postQuit 向消息线程投递退出消息，并检查 Win32 BOOL 返回值。
func (t *MessageThread) postQuit() error {
	t.mu.Lock()
	threadID := t.threadID
	t.mu.Unlock()
	result, _, callErr := procPostThreadMessage.Call(uintptr(threadID), wmQuit, 0, 0)
	if result == 0 {
		return fmt.Errorf("post message thread quit: %w", callErr)
	}
	return nil
}

// recordError 保留消息处理或退出投递的首个错误，供 Close 返回。
func (t *MessageThread) recordError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closeErr == nil {
		t.closeErr = err
	} else {
		t.closeErr = errors.Join(t.closeErr, err)
	}
}

var messageThreadWindows sync.Map
