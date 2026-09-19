/** 关闭窗口时的用户偏好。 */
export type CloseBehavior = 'tray' | 'exit';

/** 可配置目录的业务类型。 */
export type DirectoryKind = 'logs' | 'backups';

/** 日志级别。 */
export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

/** 可恢复默认值的配置属性。 */
export type ConfigProperty = 'log.level' | 'log.path' | 'backup.dir' | 'window.close_behavior' | 'theme';

/** 支持的主题值。 */
export type Theme = 'peach' | 'mint' | 'sky' | 'lavender';

/** 同步 Windows 原生标题栏所需的最小颜色集合。 */
export interface WindowThemeColors {
  background: string;
  text: string;
}

/** 单个存储目录的有效值和保存值。 */
export interface StorageDirectory {
  kind: DirectoryKind;
  effectivePath: string;
  savedPath: string;
  restartRequired: boolean;
}

/** 日志目录和备份目录的设置投影。 */
export interface StorageDirectories {
  logs: StorageDirectory;
  backups: StorageDirectory;
}

/** 日志当前生效值与下次启动保存值。 */
export interface LogLevelSettings {
  effectiveLevel: LogLevel;
  savedLevel: LogLevel;
  restartRequired: boolean;
}
