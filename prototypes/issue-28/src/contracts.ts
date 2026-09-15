import type { Theme } from './themes';

export type DirectoryKind = 'logs' | 'backups';
export type CloseBehavior = 'tray' | 'exit';
export type BackgroundConfig = { kind: 'solid' } | { kind: 'image' | 'video'; resource: string; fit: 'cover' | 'contain'; overlay: number };
export interface Settings { theme: Theme; background: BackgroundConfig; closeBehavior: CloseBehavior; logs: string; backups: string }
export interface Reminder { id: string; title: string; body: string; at: string; confirmed: boolean }
export type Detail = { status: 'pending'; message: Reminder } | { status: 'confirmed' | 'missing'; id: string };
export interface RuntimeState {
  visibility: 'visible' | 'hidden' | 'minimized';
  lifecycle: 'running' | 'stopping' | 'stopped' | 'cleanup-failed';
  ready: boolean;
  pendingTarget: string | null;
  activation: { sequence: number; target: string } | null;
  notifications: string[];
}
export interface SettingsPort {
  readSettings(): Promise<Settings>;
  savePreferences(value: Pick<Settings, 'theme' | 'closeBehavior'>): Promise<Settings>;
  chooseDirectory(kind: DirectoryKind): Promise<string | null>;
  saveDirectory(kind: DirectoryKind, path: string): Promise<{ effectivePath: string }>;
}
export interface MessagesPort {
  list(): Promise<Reminder[]>;
  detail(id: string): Promise<Detail>;
  confirm(id: string): Promise<void>;
}
export interface WindowPort {
  runtime(): RuntimeState;
  close(): Promise<void>;
  minimize(): void;
  open(): void;
  launch(): void;
  exit(): Promise<void>;
}
export interface ActivationPort { activate(id: string): void; setReady(ready: boolean): void }
export type AdapterEvent = 'runtime' | 'external' | 'reset';
export interface Ports extends SettingsPort, MessagesPort, WindowPort, ActivationPort {
  subscribe(listener: (event: AdapterEvent) => void): () => void;
}

/** 原型边界错误携带可呈现信息与平台返回的实际生效路径。 */
export class PrototypeError extends Error {
  constructor(message: string, public readonly effectivePath?: string) { super(message); this.name = 'PrototypeError'; }
}
