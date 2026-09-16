//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"golang.org/x/sys/windows"
)

// ErrActivationWithoutInstance 表示 Toast 激活启动时没有可转发的主实例。
var ErrActivationWithoutInstance = errors.New("toast activation has no running instance")

// WindowsInstanceBrokerOptions 配置 Windows 单实例和激活通道。
type WindowsInstanceBrokerOptions struct {
	ApplicationName string
	Args            []string
	DialTimeout     time.Duration
	QueueSize       int
}

// WindowsInstanceBroker 实现当前登录用户范围内的 Mutex 和命名 Pipe 单实例守卫。
type WindowsInstanceBroker struct {
	applicationName string
	args            []string
	dialTimeout     time.Duration
	queueSize       int
	userSID         string
	mutexName       string
	pipeName        string
	pipeSDDL        string
	activation      platform.Activation
	explicit        bool

	mu         sync.Mutex
	mutex      windows.Handle
	listener   net.Listener
	started    bool
	closed     bool
	acceptErr  error
	done       chan struct{}
	events     chan platform.Activation
	handlers   sync.WaitGroup
	publishers sync.WaitGroup

	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

// NewWindowsInstanceBroker 使用当前进程参数创建 Windows 单实例 broker。
func NewWindowsInstanceBroker(applicationName string) (*WindowsInstanceBroker, error) {
	return NewWindowsInstanceBrokerWithOptions(WindowsInstanceBrokerOptions{
		ApplicationName: applicationName,
		Args:            os.Args[1:],
	})
}

// NewWindowsInstanceBrokerWithOptions 创建可测试的 Windows 单实例 broker 配置。
func NewWindowsInstanceBrokerWithOptions(options WindowsInstanceBrokerOptions) (*WindowsInstanceBroker, error) {
	if err := validateApplicationName(options.ApplicationName); err != nil {
		return nil, fmt.Errorf("create instance broker: %w", err)
	}
	activation, explicit, err := ParseActivationArgs(options.Args)
	if err != nil {
		return nil, fmt.Errorf("create instance broker: %w", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		return nil, fmt.Errorf("create instance broker: get current user SID: %w", err)
	}
	if options.DialTimeout <= 0 {
		options.DialTimeout = 2 * time.Second
	}
	if options.QueueSize <= 0 {
		options.QueueSize = defaultActivationQueueSize
	}
	b := &WindowsInstanceBroker{
		applicationName: options.ApplicationName,
		args:            append([]string(nil), options.Args...),
		dialTimeout:     options.DialTimeout,
		queueSize:       options.QueueSize,
		userSID:         sid,
		mutexName:       `Local\TimeCue.` + options.ApplicationName + `.` + sid,
		pipeName:        `\\.\pipe\TimeCue.` + options.ApplicationName + `.` + sid,
		pipeSDDL:        "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)",
		done:            make(chan struct{}),
		events:          make(chan platform.Activation, options.QueueSize),
		closeDone:       make(chan struct{}),
	}
	if explicit {
		b.args = append([]string(nil), options.Args...)
		b.activation = activation
		b.explicit = true
	}
	return b, nil
}

// Acquire 在创建数据库前取得当前用户的命名 Mutex，并在返回前启动 Pipe listener。
func (b *WindowsInstanceBroker) Acquire(ctx context.Context) (func() error, error) {
	if b == nil {
		return nil, errors.New("acquire instance broker: broker is nil")
	}
	if ctx == nil {
		return nil, errors.New("acquire instance broker: context is nil")
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("acquire instance broker: broker is closed")
	}
	if b.started {
		b.mu.Unlock()
		return nil, errors.New("acquire instance broker: broker is already acquired")
	}
	b.mu.Unlock()
	mutexName, err := windows.UTF16PtrFromString(b.mutexName)
	if err != nil {
		return nil, fmt.Errorf("acquire instance broker: encode mutex name: %w", err)
	}
	// Mutex 不要求线程拥有权；这里只持有句柄维持命名对象生命周期，避免跨 goroutine ReleaseMutex 的线程亲和性问题。
	handle, createErr := windows.CreateMutex(nil, false, mutexName)
	alreadyRunning := errors.Is(createErr, windows.ERROR_ALREADY_EXISTS)
	if createErr != nil && !alreadyRunning {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, fmt.Errorf("acquire instance broker: create mutex %q: %w", b.mutexName, createErr)
	}
	if alreadyRunning {
		if handle != 0 {
			if err := windows.CloseHandle(handle); err != nil {
				return nil, fmt.Errorf("acquire instance broker: close duplicate mutex handle: %w", err)
			}
		}
		activation := platform.Activation{Kind: platform.ActivationKindSecondInstance}
		if b.explicit {
			activation = b.activation
		}
		if err := b.forward(ctx, activation); err != nil {
			return nil, fmt.Errorf("acquire instance broker: forward activation: %w", err)
		}
		return nil, bootstrap.ErrAlreadyRunning
	}
	if b.explicit {
		if err := windows.CloseHandle(handle); err != nil {
			return nil, fmt.Errorf("acquire instance broker: close toast mutex: %w", err)
		}
		return nil, ErrActivationWithoutInstance
	}
	pipeListener, err := winio.ListenPipe(b.pipeName, &winio.PipeConfig{
		SecurityDescriptor: b.pipeSDDL,
		MessageMode:        true,
		InputBufferSize:    maxActivationMessageBytes,
		OutputBufferSize:   maxActivationMessageBytes,
	})
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("acquire instance broker: listen on pipe %q: %w", b.pipeName, err)
	}
	b.mu.Lock()
	b.mutex = handle
	b.listener = pipeListener
	b.started = true
	b.mu.Unlock()
	b.handlers.Add(1)
	go b.acceptLoop()
	return b.Close, nil
}

// Start 满足 ActivationSource 生命周期契约；Pipe 在 Acquire 阶段已开始接收事件。
func (b *WindowsInstanceBroker) Start(context.Context) error {
	if b == nil {
		return errors.New("start instance broker: broker is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("start instance broker: broker is closed")
	}
	if !b.started {
		return errors.New("start instance broker: acquire must complete before start")
	}
	return nil
}

// Events 返回 Pipe listener 暂存的激活事件流。
func (b *WindowsInstanceBroker) Events() <-chan platform.Activation {
	if b == nil {
		return nil
	}
	return b.events
}

// Publish 将进程内通知激活安全地写入 broker；关闭后拒绝发布且不会触发 send-on-closed-channel。
func (b *WindowsInstanceBroker) Publish(activation platform.Activation) error {
	if b == nil {
		return errors.New("publish activation: broker is nil")
	}
	if activation.Kind != platform.ActivationKindToast && activation.Kind != platform.ActivationKindSecondInstance {
		return fmt.Errorf("publish activation: unsupported kind %q", activation.Kind)
	}
	if activation.Kind == platform.ActivationKindToast && activation.Target == nil {
		return errors.New("publish activation: toast target must not be nil")
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("publish activation: broker is closed")
	}
	b.publishers.Add(1)
	events := b.events
	done := b.done
	b.mu.Unlock()
	defer b.publishers.Done()
	select {
	case events <- activation:
		return nil
	case <-done:
		return errors.New("publish activation: broker is closed")
	}
}

// Close 停止 listener、等待请求处理、关闭事件流并释放命名 Mutex，且保持幂等。
func (b *WindowsInstanceBroker) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		defer close(b.closeDone)
		b.mu.Lock()
		b.closed = true
		listener := b.listener
		mutex := b.mutex
		close(b.done)
		b.mu.Unlock()
		if listener != nil {
			if err := listener.Close(); err != nil {
				b.closeErr = errors.Join(b.closeErr, fmt.Errorf("close activation pipe: %w", err))
			}
		}
		b.handlers.Wait()
		b.publishers.Wait()
		close(b.events)
		b.mu.Lock()
		acceptErr := b.acceptErr
		b.mu.Unlock()
		if acceptErr != nil {
			b.closeErr = errors.Join(b.closeErr, acceptErr)
		}
		if mutex != 0 {
			if err := windows.CloseHandle(mutex); err != nil {
				b.closeErr = errors.Join(b.closeErr, fmt.Errorf("close instance mutex: %w", err))
			}
		}
	})
	<-b.closeDone
	return b.closeErr
}

// acceptLoop 接受命名 Pipe 连接；关闭期间的 listener 错误属于正常收尾。
func (b *WindowsInstanceBroker) acceptLoop() {
	defer b.handlers.Done()
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			select {
			case <-b.done:
				return
			default:
			}
			b.mu.Lock()
			if b.acceptErr == nil {
				b.acceptErr = fmt.Errorf("accept activation pipe: %w", err)
			}
			b.mu.Unlock()
			return
		}
		b.handlers.Add(1)
		go b.handleConnection(connection)
	}
}

// handleConnection 读取、校验并发布一条激活消息，不记录不可信原始载荷。
func (b *WindowsInstanceBroker) handleConnection(connection net.Conn) {
	defer b.handlers.Done()
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(b.dialTimeout))
	contents, err := io.ReadAll(io.LimitReader(connection, maxActivationMessageBytes+1))
	if err != nil || len(contents) > maxActivationMessageBytes {
		return
	}
	activation, err := DecodeActivation(contents)
	if err != nil {
		return
	}
	select {
	case b.events <- activation:
	case <-b.done:
	}
}

// forward 将激活请求发送给已有主实例。
func (b *WindowsInstanceBroker) forward(parent context.Context, activation platform.Activation) error {
	contents, err := EncodeActivation(activation)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, b.dialTimeout)
	defer cancel()
	connection, err := winio.DialPipeContext(ctx, b.pipeName)
	if err != nil {
		return fmt.Errorf("dial pipe: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetWriteDeadline(deadline)
	}
	if _, err := connection.Write(contents); err != nil {
		return fmt.Errorf("write activation: %w", err)
	}
	return nil
}

// currentUserSID 读取当前进程访问令牌中的用户 SID，确保命名对象只在当前用户作用域内复用。
func currentUserSID() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// validateApplicationName 限制命名对象名称，避免把外部输入拼接进系统对象路径。
func validateApplicationName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("application name must not be empty")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `\\/:`) {
		return errors.New("application name contains path separators")
	}
	return nil
}

var _ interface {
	bootstrap.ExclusiveStartup
	platform.ActivationSource
} = (*WindowsInstanceBroker)(nil)
