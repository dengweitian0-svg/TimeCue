package windows

import "context"

// messageThread 是时钟与未来托盘适配器共享的受控 Win32 消息线程窄契约。
type messageThread interface {
	Start(context.Context) error
	Close() error
	UnregisterHandler(MessageHandlerToken) error
}
