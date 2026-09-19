import type { CloseBehavior } from '../../features/settings/contracts';

/** 桌面运行单元生命周期。 */
export type Lifecycle = 'running' | 'stopping' | 'stopped' | 'cleanup-failed';

/** 提醒恢复子状态。 */
export type RecoveryStatus = 'idle' | 'running' | 'failed';

/** 页面展示运行状态所需的运行时投影。 */
export interface RuntimeStatus {
  visibility: 'visible' | 'hidden' | 'minimized';
  lifecycle: Lifecycle;
  ready: boolean;
  recoveryStatus?: RecoveryStatus;
}

/** 冷启动提醒恢复事件。 */
export interface RecoveryEvent {
  status: RecoveryStatus;
  errorCode?: string;
}

/** 系统通知激活事件。 */
export interface Activation {
  sequence: number;
  targetId: string;
  originalDate: string;
}

/** 前端就绪握手返回的桌面状态。 */
export interface FrontendStatus {
  runtime: RuntimeStatus;
  closeBehavior: CloseBehavior;
  activation: Activation | null;
}

/** 激活事件监听器。 */
export interface ActivationListener {
  (activation: Activation): void;
}

/** 恢复事件监听器。 */
export interface RecoveryListener {
  (event: RecoveryEvent): void;
}
