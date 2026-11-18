package model

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// ErrInvalidQueryCursor 表示不透明游标不符合查询用途或排序边界。
var ErrInvalidQueryCursor = errors.New("invalid query cursor")

// QueryCursor 保存查询游标绑定的用途、截止时刻与排序边界。
type QueryCursor struct {
	Scope         string    `json:"scope"`
	AsOf          time.Time `json:"asOf"`
	Time          time.Time `json:"time"`
	ID            int       `json:"id"`
	MemberVersion int       `json:"memberVersion,omitempty"`
}

// EncodeQueryCursor 将查询位置编码为不透明游标。
func EncodeQueryCursor(value QueryCursor) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeQueryCursor 校验游标用途、时刻与排序边界。
func DecodeQueryCursor(raw, scope string, now time.Time) (QueryCursor, error) {
	value := QueryCursor{Scope: scope, AsOf: now.In(BeijingLocation())}
	if raw == "" {
		return value, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) > 1024 {
		return value, ErrInvalidQueryCursor
	}
	if json.Unmarshal(data, &value) != nil || value.Scope != scope || value.AsOf.IsZero() || value.AsOf.After(now) || value.Time.IsZero() || value.ID <= 0 {
		return value, ErrInvalidQueryCursor
	}
	value.AsOf = value.AsOf.In(BeijingLocation())
	return value, nil
}
