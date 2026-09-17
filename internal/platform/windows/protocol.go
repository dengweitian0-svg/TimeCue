// Package windows 提供 Windows 桌面边界的单实例与激活适配器。
package windows

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const (
	// maxActivationMessageBytes 限制跨进程输入，避免第二实例消耗过多内存。
	maxActivationMessageBytes = 4096
	// defaultActivationQueueSize 暂存 Runtime 尚未订阅时到达的激活事件。
	defaultActivationQueueSize = 16
)

// activationMessage 是命名 Pipe 上传输的受控 JSON 协议。
type activationMessage struct {
	Kind         string `json:"kind"`
	ScheduleID   int64  `json:"schedule_id,omitempty"`
	OriginalDate string `json:"original_date,omitempty"`
}

// ParseActivationArgs 解析 Toast 或显式第二实例参数，不读取任何平台状态。
func ParseActivationArgs(args []string) (platform.Activation, bool, error) {
	if len(args) == 0 {
		return platform.Activation{}, false, nil
	}
	if args[0] == "--second-instance" && len(args) == 1 {
		return platform.Activation{Kind: platform.ActivationKindSecondInstance}, true, nil
	}
	if args[0] != "--toast" {
		if len(args) != 1 {
			return platform.Activation{}, false, fmt.Errorf("parse activation arguments: unsupported argument %q", args[0])
		}
		activation, err := parseWailsNotificationArgument(args[0])
		if err != nil {
			return platform.Activation{}, false, fmt.Errorf("parse activation arguments: unsupported argument: %w", err)
		}
		return activation, true, nil
	}
	if len(args) != 3 {
		return platform.Activation{}, false, errors.New("parse activation arguments: toast requires schedule ID and original date")
	}
	scheduleID, err := parseScheduleID(args[1])
	if err != nil {
		return platform.Activation{}, false, fmt.Errorf("parse activation arguments: %w", err)
	}
	if err := validateOriginalDate(args[2]); err != nil {
		return platform.Activation{}, false, fmt.Errorf("parse activation arguments: %w", err)
	}
	return platform.Activation{
		Kind: platform.ActivationKindToast,
		Target: &domain.OccurrenceIdentity{
			ScheduleID:   domain.ScheduleID(scheduleID),
			OriginalDate: args[2],
		},
	}, true, nil
}

// wailsNotificationPayload 是 Wails v2.15 冷启动参数的受控外层形状。
type wailsNotificationPayload struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

// parseWailsNotificationArgument 解码 Wails 注册的单个 Base64 JSON 参数，只提取稳定身份。
func parseWailsNotificationArgument(encoded string) (platform.Activation, error) {
	if encoded == "" || len(encoded) > ((maxActivationMessageBytes+2)/3)*4 {
		return platform.Activation{}, errors.New("Wails notification argument size is invalid")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return platform.Activation{}, fmt.Errorf("decode Wails notification argument: %w", err)
	}
	if len(decoded) == 0 || len(decoded) > maxActivationMessageBytes {
		return platform.Activation{}, errors.New("Wails notification payload size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var payload wailsNotificationPayload
	if err := decoder.Decode(&payload); err != nil {
		return platform.Activation{}, fmt.Errorf("decode Wails notification JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return platform.Activation{}, errors.New("Wails notification JSON contains trailing data")
	}
	if payload.Action == "" {
		return platform.Activation{}, errors.New("Wails notification action must not be empty")
	}
	var options struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(payload.Payload, &options); err != nil {
		return platform.Activation{}, fmt.Errorf("decode Wails notification payload: %w", err)
	}
	return notificationResponseActivation(options.Data)
}

// EncodeActivation 将激活事件编码为受控的跨进程 JSON 消息。
func EncodeActivation(activation platform.Activation) ([]byte, error) {
	message := activationMessage{Kind: string(activation.Kind)}
	switch activation.Kind {
	case platform.ActivationKindSecondInstance:
		if activation.Target != nil {
			return nil, errors.New("encode activation: second instance must not have a target")
		}
	case platform.ActivationKindToast:
		if activation.Target == nil {
			return nil, errors.New("encode activation: toast target must not be nil")
		}
		if err := validateIdentity(*activation.Target); err != nil {
			return nil, fmt.Errorf("encode activation: %w", err)
		}
		message.ScheduleID = int64(activation.Target.ScheduleID)
		message.OriginalDate = activation.Target.OriginalDate
	default:
		return nil, fmt.Errorf("encode activation: unsupported kind %q", activation.Kind)
	}
	contents, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode activation: %w", err)
	}
	if len(contents) > maxActivationMessageBytes {
		return nil, errors.New("encode activation: message exceeds size limit")
	}
	return contents, nil
}

// DecodeActivation 严格校验跨进程激活消息并转换为平台契约事件。
func DecodeActivation(contents []byte) (platform.Activation, error) {
	if len(contents) == 0 || len(contents) > maxActivationMessageBytes {
		return platform.Activation{}, errors.New("decode activation: message size is invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(contents), maxActivationMessageBytes+1))
	decoder.DisallowUnknownFields()
	var message activationMessage
	if err := decoder.Decode(&message); err != nil {
		return platform.Activation{}, fmt.Errorf("decode activation: invalid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return platform.Activation{}, errors.New("decode activation: multiple JSON values are not allowed")
		}
		return platform.Activation{}, fmt.Errorf("decode activation: trailing data: %w", err)
	}
	switch platform.ActivationKind(message.Kind) {
	case platform.ActivationKindSecondInstance:
		if message.ScheduleID != 0 || message.OriginalDate != "" {
			return platform.Activation{}, errors.New("decode activation: second instance must not have a target")
		}
		return platform.Activation{Kind: platform.ActivationKindSecondInstance}, nil
	case platform.ActivationKindToast:
		if message.ScheduleID <= 0 {
			return platform.Activation{}, errors.New("decode activation: schedule ID must be positive")
		}
		if err := validateOriginalDate(message.OriginalDate); err != nil {
			return platform.Activation{}, fmt.Errorf("decode activation: %w", err)
		}
		return platform.Activation{
			Kind: platform.ActivationKindToast,
			Target: &domain.OccurrenceIdentity{
				ScheduleID:   domain.ScheduleID(message.ScheduleID),
				OriginalDate: message.OriginalDate,
			},
		}, nil
	default:
		return platform.Activation{}, fmt.Errorf("decode activation: unsupported kind %q", message.Kind)
	}
}

// parseScheduleID 解析正十进制日程 ID，拒绝符号、空值和溢出。
func parseScheduleID(value string) (int64, error) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, fmt.Errorf("schedule ID %q must be a positive integer", value)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("schedule ID %q must be a positive integer", value)
	}
	return parsed, nil
}

// validateOriginalDate 验证协议要求的严格 YYYY-MM-DD 日期格式。
func validateOriginalDate(value string) error {
	if len(value) != len("2006-01-02") {
		return fmt.Errorf("original date %q must use YYYY-MM-DD", value)
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return fmt.Errorf("original date %q must use YYYY-MM-DD", value)
	}
	return nil
}

// validateIdentity 验证 Toast 载荷复用的领域发生身份。
func validateIdentity(identity domain.OccurrenceIdentity) error {
	if identity.ScheduleID <= 0 {
		return errors.New("schedule ID must be positive")
	}
	return validateOriginalDate(identity.OriginalDate)
}
