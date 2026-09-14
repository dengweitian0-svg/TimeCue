package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

// recordingQualification 记录独占资格获取和释放，用于验证生命周期顺序。
type recordingQualification struct {
	events *[]string
	err    error
	close  error
}

// Acquire 记录资格确认并返回记录释放事件的函数。
func (q recordingQualification) Acquire(context.Context) (func() error, error) {
	*q.events = append(*q.events, "qualification")
	if q.err != nil {
		return nil, q.err
	}
	return func() error {
		*q.events = append(*q.events, "release")
		return q.close
	}, nil
}

// fakeStore 是只用于 bootstrap 生命周期测试的可关闭 Store。
type fakeStore struct {
	events *[]string
	err    error
}

// Close 记录 Store 关闭事件并返回预设错误。
func (s *fakeStore) Close() error {
	*s.events = append(*s.events, "store-close")
	return s.err
}

// initializedConfig 返回所有路径都已明确的初始化测试配置。
func initializedConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{
		App:    config.AppConfig{Name: "TimeCue", Version: "test"},
		Log:    config.LogConfig{Level: "info", Path: filepath.Join(root, "logs", "timecue.log")},
		Backup: config.BackupConfig{Dir: filepath.Join(root, "backups")},
		ResolvedPaths: config.ResolvedPaths{
			DatabasePath: filepath.Join(root, "data", "timecue.db"),
			LogPath:      filepath.Join(root, "logs", "timecue.log"),
			BackupDir:    filepath.Join(root, "backups"),
		},
	}
}

func TestInitializeOrdersQualificationDirectoriesAndStore(t *testing.T) {
	cfg := initializedConfig(t)
	events := make([]string, 0, 3)
	originalOpener := storeOpener
	t.Cleanup(func() { storeOpener = originalOpener })
	storeOpener = func(_ context.Context, paths config.ResolvedPaths, logger *slog.Logger) (Store, error) {
		events = append(events, "store")
		if logger == nil {
			return nil, errors.New("store opener received nil logger")
		}
		for _, path := range []string{filepath.Dir(paths.DatabasePath), paths.BackupDir, filepath.Dir(paths.LogPath)} {
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("path %q was not prepared: %w", path, err)
			}
		}
		return &fakeStore{events: &events}, nil
	}

	resources, err := Initialize(context.Background(), cfg, recordingQualification{events: &events})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if got, want := events, []string{"qualification", "store"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initialization events = %v, want %v", got, want)
	}
	if err := resources.Close(); err != nil {
		t.Fatalf("Resources.Close() error = %v", err)
	}
	if got, want := events, []string{"qualification", "store", "store-close", "release"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup events = %v, want %v", got, want)
	}
}

func TestInitializeRejectsMissingExclusiveQualificationBeforeSQLite(t *testing.T) {
	cfg := initializedConfig(t)
	originalOpener := storeOpener
	t.Cleanup(func() { storeOpener = originalOpener })
	storeOpener = func(context.Context, config.ResolvedPaths, *slog.Logger) (Store, error) {
		t.Fatal("store opener called without exclusive qualification")
		return nil, nil
	}
	wantErr := errors.New("another instance owns startup")

	_, err := Initialize(context.Background(), cfg, recordingQualification{events: new([]string), err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Initialize() error = %v, want qualification cause", err)
	}
	if _, statErr := os.Stat(cfg.ResolvedPaths.DatabasePath); !os.IsNotExist(statErr) {
		t.Fatalf("database state = %v, want not created", statErr)
	}
	if _, statErr := os.Stat(cfg.ResolvedPaths.BackupDir); !os.IsNotExist(statErr) {
		t.Fatalf("backup directory state = %v, want not created", statErr)
	}
}

func TestInitializeCleansPartialResourcesWhenSQLiteFails(t *testing.T) {
	cfg := initializedConfig(t)
	events := make([]string, 0, 2)
	originalOpener := storeOpener
	t.Cleanup(func() { storeOpener = originalOpener })
	wantErr := errors.New("sqlite unavailable")
	storeOpener = func(_ context.Context, _ config.ResolvedPaths, _ *slog.Logger) (Store, error) {
		return nil, wantErr
	}

	_, err := Initialize(context.Background(), cfg, recordingQualification{events: &events})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Initialize() error = %v, want sqlite cause", err)
	}
	if got, want := events, []string{"qualification", "release"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup events = %v, want %v", got, want)
	}
	if err := os.Rename(cfg.ResolvedPaths.LogPath, cfg.ResolvedPaths.LogPath+".closed"); err != nil {
		t.Fatalf("rename log after failed initialization: %v", err)
	}
}

func TestResourcesClosePreservesStoreAndQualificationErrors(t *testing.T) {
	events := make([]string, 0, 2)
	storeErr := errors.New("store close failed")
	lockErr := errors.New("lock release failed")
	logFile, err := os.CreateTemp(t.TempDir(), "timecue-log-*.log")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	resources := &Resources{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:       &fakeStore{events: &events, err: storeErr},
		logFile:     logFile,
		releaseLock: func() error { events = append(events, "release"); return lockErr },
	}

	closeErr := resources.Close()
	if !errors.Is(closeErr, storeErr) || !errors.Is(closeErr, lockErr) {
		t.Fatalf("Resources.Close() error = %v, want both close causes", closeErr)
	}
	if got, want := events, []string{"store-close", "release"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup events = %v, want %v", got, want)
	}
}
