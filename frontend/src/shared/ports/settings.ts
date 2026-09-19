import type { CloseBehavior, ConfigProperty, DirectoryKind, LogLevel, LogLevelSettings, StorageDirectories, StorageDirectory, Theme, WindowThemeColors } from '../../features/settings/contracts';

/** 用户设置读取、保存和恢复所需的窄端口。 */
export interface SettingsPort {
  getCloseBehavior(): Promise<CloseBehavior>;
  saveCloseBehavior(value: CloseBehavior): Promise<CloseBehavior>;
  getTheme(): Promise<Theme>;
  saveTheme(value: Theme): Promise<Theme>;
  applyWindowTheme(colors: WindowThemeColors): Promise<void>;
  getLogLevel(): Promise<LogLevelSettings>;
  saveLogLevel(value: LogLevel): Promise<LogLevelSettings>;
  getStorageDirectories(): Promise<StorageDirectories>;
  chooseDirectory(kind: DirectoryKind): Promise<string | null>;
  saveStorageDirectory(
    kind: DirectoryKind,
    path: string,
  ): Promise<StorageDirectory>;
  restoreConfigDefault(property: ConfigProperty): Promise<void>;
}
