package sqlite

import (
	"context"
	"errors"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
)

// QueryGeneration 返回当前进程内已提交事实的 generation。
func (s *Store) QueryGeneration(context.Context) (uint64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite store is not open")
	}
	return s.generation.Load(), nil
}

// bumpGeneration 在成功提交状态变化后递增查询快照版本。
func (s *Store) bumpGeneration() {
	if s != nil {
		s.generation.Add(1)
	}
}

var _ app.OccurrenceQueryStore = (*Store)(nil)
