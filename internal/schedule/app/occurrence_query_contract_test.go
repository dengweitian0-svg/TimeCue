package app

import (
	"errors"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestDecodeListOccurrencesTracksIntegerFields 验证查询 JSON 保留零值和 limit 字段存在性。
func TestDecodeListOccurrencesTracksIntegerFields(t *testing.T) {
	request, err := DecodeListOccurrences([]byte(`{"from_unix":0,"to_unix":60,"limit":0}`))
	if err != nil {
		t.Fatalf("DecodeListOccurrences() error = %v", err)
	}
	if !request.FromUnixPresent || !request.ToUnixPresent || !request.LimitPresent || request.FromUnix != 0 || request.ToUnix != 60 {
		t.Fatalf("request = %+v", request)
	}
}

// TestDecodeListOccurrencesRejectsInvalidInput 验证范围字段缺失、非整数和 null 都返回 VALIDATION。
func TestDecodeListOccurrencesRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{`null`, `{}`, `{"from_unix":1.0,"to_unix":60}`, `{"from_unix":1,"to_unix":"60"}`} {
		_, err := DecodeListOccurrences([]byte(input))
		var businessErr *domain.Error
		if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
			t.Errorf("input %q error = %#v, want validation", input, err)
		}
	}
}
