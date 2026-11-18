package schedule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

func deletionRequest(id, revision int, key string) (model.ScheduleDelete, error) {
	// 执行键是后端生成的完整工具身份，不能套用外部写入键的长度或字符限制。
	if id < 1 || revision < 1 || key == "" {
		return model.ScheduleDelete{}, ErrInvalidSchedule
	}
	input, _ := json.Marshal(struct {
		Operation string
		ID        int
		Revision  int
	}{
		Operation: "delete_schedule",
		ID:        id,
		Revision:  revision,
	})
	hash := sha256.Sum256(input)
	return model.ScheduleDelete{
		ID:               id,
		ExpectedRevision: revision,
		Key:              key,
		Digest:           hex.EncodeToString(hash[:]),
	}, nil
}

// DeleteScheduleVersion 提交版本删除，命中原回执优先于目标状态和版本判断。
func (s *Service) DeleteScheduleVersion(ctx context.Context, id, revision int, key string) (*model.ScheduleWrite, error) {
	request, err := deletionRequest(id, revision, key)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.ApplyScheduleDelete(ctx, request)
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	return result, err
}

// ReadScheduleDeleteReceipt 只读核对同一次删除，未命中时不产生任何业务效果。
func (s *Service) ReadScheduleDeleteReceipt(ctx context.Context, id, revision int, key string) (*model.ScheduleWrite, error) {
	request, err := deletionRequest(id, revision, key)
	if err != nil {
		return nil, err
	}
	return s.repo.ReadScheduleReceipt(ctx, request.Key, request.Digest)
}
