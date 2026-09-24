package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const LimitMax int = 50

type DB interface {
	// Connect 连接数据库, 只能在初始化应用时触发一次, 并且必须触发一次
	Connect(ctx context.Context, path string) error
	// Close 关闭数据库连接, 只能在应用退出时触发一次, 并且必须触发一次
	Close() error
}

type Sqlite struct {
	db *sql.DB
}

func (s *Sqlite) Connect(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("[db] open sqlite err: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("[db] connect sqlite err: %w", err)
	}
	s.db = db
	return nil
}

func (s *Sqlite) Close() error {
	if s.db == nil {
		return fmt.Errorf("[db] no sqlite exited")
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("[db] close sqlite err: %w", err)
	}
	s.db = nil
	return nil
}
