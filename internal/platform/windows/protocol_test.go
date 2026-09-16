package windows

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestParseActivationArgs 验证普通启动、Toast 激活和显式非法参数的纯解析行为。
func TestParseActivationArgs(t *testing.T) {
	identity := domain.OccurrenceIdentity{ScheduleID: 42, OriginalDate: "2026-09-16"}
	tests := []struct {
		name    string
		args    []string
		want    platform.Activation
		found   bool
		wantErr bool
	}{
		{name: "ordinary launch", want: platform.Activation{}, found: false},
		{name: "second instance", args: []string{"--second-instance"}, want: platform.Activation{Kind: platform.ActivationKindSecondInstance}, found: true},
		{name: "toast", args: []string{"--toast", "42", "2026-09-16"}, want: platform.Activation{Kind: platform.ActivationKindToast, Target: &identity}, found: true},
		{name: "unknown argument", args: []string{"--unexpected"}, wantErr: true},
		{name: "missing toast fields", args: []string{"--toast", "42"}, wantErr: true},
		{name: "invalid toast ID", args: []string{"--toast", "0", "2026-09-16"}, wantErr: true},
		{name: "invalid toast date", args: []string{"--toast", "42", "2026-02-30"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := ParseActivationArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseActivationArgs() error = %v, want error %v", err, tt.wantErr)
			}
			if found != tt.found {
				t.Fatalf("ParseActivationArgs() found = %v, want %v", found, tt.found)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseActivationArgs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestActivationProtocolRoundTrip 验证激活消息编码解码保持稳定身份。
func TestActivationProtocolRoundTrip(t *testing.T) {
	identity := domain.OccurrenceIdentity{ScheduleID: 7, OriginalDate: "2026-01-02"}
	for _, want := range []platform.Activation{
		{Kind: platform.ActivationKindSecondInstance},
		{Kind: platform.ActivationKindToast, Target: &identity},
	} {
		contents, err := EncodeActivation(want)
		if err != nil {
			t.Fatalf("EncodeActivation() error = %v", err)
		}
		got, err := DecodeActivation(contents)
		if err != nil {
			t.Fatalf("DecodeActivation() error = %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("DecodeActivation() = %+v, want %+v", got, want)
		}
	}
}

// TestDecodeActivationRejectsUntrustedPayload 验证未知字段、错误身份和过长消息均被拒绝。
func TestDecodeActivationRejectsUntrustedPayload(t *testing.T) {
	tests := []string{
		`{"kind":"toast","schedule_id":1,"original_date":"2026-09-16","extra":"x"}`,
		`{"kind":"toast","schedule_id":-1,"original_date":"2026-09-16"}`,
		`{"kind":"toast","schedule_id":1,"original_date":"2026-9-16"}`,
		`{"kind":"second_instance","schedule_id":1}`,
		`{"kind":"invalid"}`,
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := DecodeActivation([]byte(input)); err == nil {
				t.Fatal("DecodeActivation() error = nil, want validation error")
			}
		})
	}
	if _, err := DecodeActivation([]byte(strings.Repeat("x", maxActivationMessageBytes+1))); err == nil {
		t.Fatal("DecodeActivation() error = nil for oversized message")
	}
}

// TestEncodeActivationRejectsInvalidIdentity 验证发送端同样执行身份校验。
func TestEncodeActivationRejectsInvalidIdentity(t *testing.T) {
	for _, activation := range []platform.Activation{
		{Kind: platform.ActivationKindToast},
		{Kind: platform.ActivationKindToast, Target: &domain.OccurrenceIdentity{ScheduleID: 0, OriginalDate: "2026-09-16"}},
		{Kind: platform.ActivationKindToast, Target: &domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-09-16T00:00:00Z"}},
		{Kind: platform.ActivationKind("unexpected")},
	} {
		if _, err := EncodeActivation(activation); err == nil {
			t.Fatalf("EncodeActivation(%+v) error = nil, want error", activation)
		}
	}
	if _, err := DecodeActivation([]byte(`{"kind":"toast","schedule_id":1,"original_date":"2026-09-16"} {}`)); err == nil {
		t.Fatal("DecodeActivation() error = nil for multiple values")
	}
}
