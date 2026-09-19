import { useEffect, useState, type CSSProperties } from 'react';
import { ScheduleError } from '../../shared/contracts/errors';
import type { RuntimeStatus } from '../../shared/contracts/runtime';
import { LoadingCard } from '../../shared/components/LoadingCard';
import { themeOrder, themeStyle, themes } from '../../themes';
import type { CloseBehavior, ConfigProperty, DirectoryKind, LogLevel, LogLevelSettings, StorageDirectories, StorageDirectory, Theme } from './contracts';

/** 设置页面所需的最小操作集合，Controller 通过结构类型满足该接口。 */
export interface SettingsActions {
  snapshot(): { error: ScheduleError | null; runtime: RuntimeStatus | null };
  saveCloseBehavior(value: CloseBehavior): Promise<void>;
  saveLogLevel(value: LogLevel): Promise<void>;
  saveTheme(value: Theme): Promise<void>;
  restoreConfigDefault(property: ConfigProperty): Promise<void>;
  chooseDirectory(kind: DirectoryKind): Promise<string | null>;
  saveStorageDirectory(
    kind: DirectoryKind,
    path: string,
  ): Promise<StorageDirectory | null>;
  retryCleanup(): Promise<void>;
}

/** 设置页面所需的状态投影，不暴露日程和提醒领域状态。 */
export interface SettingsViewState {
  theme: Theme;
  closeBehavior: CloseBehavior;
  logLevel: LogLevelSettings;
  storageDirectories: StorageDirectories | null;
  busy: boolean;
}

/** 设置页面呈现主题、窗口行为、日志和存储配置。 */
export function SettingsPage({
  actions,
  state,
}: {
  actions: SettingsActions;
  state: SettingsViewState;
}) {
  const [choice, setChoice] = useState(state.closeBehavior);
  const [level, setLevel] = useState<LogLevel>(state.logLevel.savedLevel);
  useEffect(() => setChoice(state.closeBehavior), [state.closeBehavior]);
  useEffect(
    () => setLevel(state.logLevel.savedLevel),
    [state.logLevel.savedLevel],
  );
  const runtime = actions.snapshot().runtime;
  return (
    <section className="page-section">
      <div className="page-heading">
        <div>
          <p className="kicker">PREFERENCES</p>
          <h1>设置</h1>
          <p>让 TimeCue 按你的习惯，安静陪伴。</p>
        </div>
      </div>
      <AppearanceSettings
        actions={actions}
        theme={state.theme}
        busy={state.busy}
      />
      <section className="settings-card">
        <p className="card-eyebrow">窗口行为</p>
        <h2>关闭窗口时</h2>
        <p className="card-copy">选择点击右上角关闭按钮后的行为。</p>
        <div className="preference-options">
          <Preference
            value="tray"
            choice={choice}
            busy={state.busy}
            onChange={setChoice}
            title="隐藏到托盘"
            copy="窗口收起，后台提醒继续。"
          />
          <Preference
            value="exit"
            choice={choice}
            busy={state.busy}
            onChange={setChoice}
            title="退出应用"
            copy="停止提醒并清理已投递通知。"
          />
        </div>
        <div className="form-actions">
          <button
            className="secondary-button"
            disabled={state.busy}
            onClick={() =>
              void actions.restoreConfigDefault('window.close_behavior')
            }
          >
            恢复默认
          </button>
          <button
            className="primary-button"
            disabled={state.busy || choice === state.closeBehavior}
            onClick={() => void actions.saveCloseBehavior(choice)}
          >
            保存偏好
          </button>
        </div>
      </section>
      <section className="settings-card settings-card-spaced">
        <p className="card-eyebrow">日志级别</p>
        <h2>记录详细程度</h2>
        <p className="card-copy">
          保存后重启应用生效，当前日志器不会立即切换。
        </p>
        <label className="select-field" htmlFor="log-level">
          日志级别
          <select
            id="log-level"
            value={level}
            disabled={state.busy}
            onChange={(event) => setLevel(event.target.value as LogLevel)}
          >
            <option value="debug">Debug · 最详细</option>
            <option value="info">Info · 常规</option>
            <option value="warn">Warn · 警告</option>
            <option value="error">Error · 错误</option>
          </select>
        </label>
        <div className="form-actions">
          <button
            className="secondary-button"
            disabled={state.busy}
            onClick={() => void actions.restoreConfigDefault('log.level')}
          >
            恢复默认
          </button>
          <button
            className="primary-button"
            disabled={state.busy || level === state.logLevel.savedLevel}
            onClick={() => void actions.saveLogLevel(level)}
          >
            保存级别
          </button>
        </div>
        {state.logLevel.restartRequired && (
          <p className="directory-result">
            已保存级别与当前生效级别不同，重启后生效。
          </p>
        )}
      </section>
      <div className="settings-subheading">
        <p className="card-eyebrow">存储位置</p>
        <h2>日志与备份</h2>
        <p>保存后重启应用生效，旧目录中的文件会保留。</p>
      </div>
      {state.storageDirectories ? (
        <>
          <StorageDirectoryCard
            actions={actions}
            directory={state.storageDirectories.logs}
            busy={state.busy}
          />
          <StorageDirectoryCard
            actions={actions}
            directory={state.storageDirectories.backups}
            busy={state.busy}
          />
        </>
      ) : (
        <LoadingCard />
      )}
      {runtime?.lifecycle === 'cleanup-failed' && (
        <section className="settings-card warning-card">
          <p className="card-eyebrow">运行状态</p>
          <h2>通知清理未完成</h2>
          <p className="card-copy">应用仍然可用，可以重试清理系统通知。</p>
          <button
            className="secondary-button"
            disabled={state.busy}
            onClick={() => void actions.retryCleanup()}
          >
            重试清理
          </button>
        </section>
      )}
    </section>
  );
}

/** 展示四套主题预览，并将点击动作交给 Controller 的保存状态机。 */
function AppearanceSettings({
  actions,
  theme,
  busy,
}: {
  actions: SettingsActions;
  theme: Theme;
  busy: boolean;
}) {
  return (
    <section
      className="settings-card appearance-card"
      aria-labelledby="appearance-title"
    >
      <p className="card-eyebrow">外观</p>
      <h2 id="appearance-title">选择主题</h2>
      <p className="card-copy">点击主题卡片即可保存并立即应用。</p>
      <div className="theme-options" role="group" aria-label="颜色主题">
        {themeOrder.map((value) => (
          <ThemeCard
            key={value}
            theme={value}
            selected={theme === value}
            busy={busy}
            onSelect={() => void actions.saveTheme(value)}
          />
        ))}
      </div>
    </section>
  );
}

/** 单个主题选项使用原生按钮保证鼠标、键盘和辅助技术行为一致。 */
function ThemeCard({
  theme,
  selected,
  busy,
  onSelect,
}: {
  theme: Theme;
  selected: boolean;
  busy: boolean;
  onSelect: () => void;
}) {
  const palette = themes[theme];
  return (
    <button
      type="button"
      className={`theme-card${selected ? ' selected' : ''}`}
      aria-label={palette.name}
      aria-pressed={selected}
      disabled={busy}
      onClick={onSelect}
    >
      <span
        className="theme-preview"
        style={themeStyle(theme) as CSSProperties}
        aria-hidden="true"
      >
        <span className="theme-preview-sidebar" />
        <span className="theme-preview-content">
          <span />
          <b />
          <i />
        </span>
      </span>
      <span className="theme-card-copy">
        <strong>{palette.name}</strong>
        <small>{selected ? '当前使用' : '点击切换'}</small>
      </span>
      <span className="theme-card-check" aria-hidden="true">
        {selected ? '✓' : ''}
      </span>
    </button>
  );
}

/** 单项关闭行为选择。 */
function Preference({
  value,
  choice,
  busy,
  title,
  copy,
  onChange,
}: {
  value: CloseBehavior;
  choice: CloseBehavior;
  busy: boolean;
  title: string;
  copy: string;
  onChange: (value: CloseBehavior) => void;
}) {
  return (
    <label className={`preference ${choice === value ? 'selected' : ''}`}>
      <input
        type="radio"
        name="close-behavior"
        value={value}
        checked={choice === value}
        disabled={busy}
        onChange={() => onChange(value)}
      />
      <span>
        <strong>{title}</strong>
        <small>{copy}</small>
      </span>
      {choice === value && <b>✓</b>}
    </label>
  );
}

/** 独立呈现一类目录的待保存输入、当前生效值和重启提示。 */
function StorageDirectoryCard({
  actions,
  directory,
  busy,
}: {
  actions: SettingsActions;
  directory: StorageDirectory;
  busy: boolean;
}) {
  const label = directory.kind === 'logs' ? '日志' : '备份';
  const [input, setInput] = useState(directory.savedPath);
  const [result, setResult] = useState<{ text: string; error: boolean } | null>(
    null,
  );
  useEffect(() => setInput(directory.savedPath), [directory.savedPath]);

  /** 目录选择取消时保留原输入和当前生效/保存状态。 */
  async function choose() {
    if (busy) return;
    const selected = await actions.chooseDirectory(directory.kind);
    const error = actions.snapshot().error;
    if (error) setResult({ text: error.message, error: true });
    else if (selected === null)
      setResult({ text: '已取消选择，目录未改变。', error: false });
    else {
      setInput(selected);
      setResult(null);
    }
  }

  /** 保存失败时保留输入，成功时只显示后端返回的 effective/saved 结果。 */
  async function save() {
    if (busy) return;
    const saved = await actions.saveStorageDirectory(directory.kind, input);
    if (saved) {
      setInput(saved.savedPath);
      setResult({
        text: saved.restartRequired
          ? '已保存，重启后生效。当前仍使用旧目录。'
          : '已保存，当前目录无需切换。',
        error: false,
      });
    } else {
      const error = actions.snapshot().error;
      setResult({ text: error?.message ?? '保存失败，请重试。', error: true });
    }
  }

  /** 恢复当前目录属性的默认值，并复用控制器的统一结果反馈。 */
  async function restoreDefault() {
    if (busy) return;
    await actions.restoreConfigDefault(
      directory.kind === 'logs' ? 'log.path' : 'backup.dir',
    );
    const error = actions.snapshot().error;
    setResult(
      error
        ? { text: error.message, error: true }
        : { text: '已恢复默认，重启后生效。', error: false },
    );
  }

  return (
    <section
      className="settings-card directory-card"
      aria-label={`${label}目录设置`}
    >
      <div className="directory-heading">
        <div>
          <p className="card-eyebrow">{label}目录</p>
          <h2>{label}文件存储位置</h2>
        </div>
        {directory.restartRequired && (
          <span className="restart-badge">重启后生效</span>
        )}
      </div>
      <dl className="directory-status">
        <div>
          <dt>当前生效</dt>
          <dd data-testid={`${directory.kind}-effective`}>
            {directory.effectivePath}
          </dd>
        </div>
        <div>
          <dt>已保存</dt>
          <dd data-testid={`${directory.kind}-saved`}>{directory.savedPath}</dd>
        </div>
      </dl>
      <div className="directory-row">
        <label className="visually-hidden" htmlFor={`${directory.kind}-path`}>
          {label}目录
        </label>
        <input
          id={`${directory.kind}-path`}
          className="path-input"
          value={input}
          disabled={busy}
          onChange={(event) => setInput(event.target.value)}
          spellCheck={false}
        />
        <button
          className="secondary-button"
          disabled={busy}
          onClick={() => void choose()}
        >
          选择目录
        </button>
        <button
          className="secondary-button"
          disabled={busy}
          onClick={() => void restoreDefault()}
        >
          恢复默认
        </button>
        <button
          className="primary-button"
          disabled={busy || input === directory.savedPath}
          onClick={() => void save()}
        >
          {busy ? '正在保存…' : '保存'}
        </button>
      </div>
      {result && (
        <p
          className={
            result.error ? 'directory-result error' : 'directory-result'
          }
          role={result.error ? 'alert' : 'status'}
        >
          {result.text}
        </p>
      )}
    </section>
  );
}
