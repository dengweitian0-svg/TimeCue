package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// configWriteMu 串行化配置文件修改和恢复默认，避免并发操作互相覆盖。
var configWriteMu sync.Mutex

// ChangeConfig 修改指定配置节中的一个 key。
// app 是只读配置节，不能通过用户配置修改；其他配置节允许写入用户配置文件。
func (m *Manager) ChangeConfig(section, key, value string) error {
	section = strings.ToLower(strings.TrimSpace(section))
	key = strings.TrimSpace(key)
	if section == "" {
		return errors.New("config section must not be empty")
	}
	if section == "app" {
		return errors.New("app config cannot be changed")
	}
	if key == "" {
		return errors.New("config key must not be empty")
	}
	if strings.Contains(key, ".") {
		return fmt.Errorf("config key %q must be a field name, not a path", key)
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	v := newConfigViper()
	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", configFileName, err)
	}

	// MergeConfigMap 更新 Viper 的用户配置层：已有 key 被覆盖，不存在的 key 被追加，
	// 同时保留文件中的未知配置；这里不加载默认值，避免把默认配置写入 config.yaml。
	if err := v.MergeConfigMap(map[string]any{
		section: map[string]any{key: value},
	}); err != nil {
		return fmt.Errorf("set %s.%s: %w", section, key, err)
	}

	var contents bytes.Buffer
	if err := v.WriteConfigTo(&contents); err != nil {
		return fmt.Errorf("encode %s: %w", configFileName, err)
	}
	if err := writeConfigAtomically(configFileName, contents.Bytes()); err != nil {
		return err
	}
	if err := m.reloadConfig(); err != nil {
		return fmt.Errorf("reload config after update: %w", err)
	}
	return nil
}

// ChangeLog 修改 log 配置节中的一个 key-value。
func (m *Manager) ChangeLog(key, value string) error {
	return m.ChangeConfig("log", key, value)
}

// ChangeDB 修改 db 配置节中的一个 key-value。
func (m *Manager) ChangeDB(key, value string) error {
	return m.ChangeConfig("db", key, value)
}

// RestoreDefault 清空用户配置文件，使下一次读取完全回退到默认配置。
// 即使 config.yaml 尚不存在，该方法也会创建一个空的 config.yaml。
func (m *Manager) RestoreDefault() error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	if err := writeConfigAtomically(configFileName, nil); err != nil {
		return err
	}
	if err := m.reloadConfig(); err != nil {
		return fmt.Errorf("reload config after restoring defaults: %w", err)
	}
	return nil
}

// writeConfigAtomically 通过同目录临时文件、写入、同步和替换保存配置。
func writeConfigAtomically(path string, contents []byte) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close temporary config file: %w", closeErr)
			}
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) && err == nil {
			err = fmt.Errorf("remove temporary config file: %w", removeErr)
		}
	}()

	written, err := temporary.Write(contents)
	if err != nil {
		return fmt.Errorf("write temporary config file: %w", err)
	}
	if written != len(contents) {
		return fmt.Errorf("write temporary config file: %w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary config file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config file: %w", err)
	}
	temporaryClosed = true

	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	return nil
}
