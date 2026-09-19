import type { Activation, ActivationListener, FrontendStatus, RecoveryListener, RuntimeStatus } from '../contracts/runtime';

/** 运行时状态、激活握手和恢复动作所需的窄端口。 */
export interface RuntimePort {
  getRuntimeStatus(): Promise<RuntimeStatus>;
  getDesktopStatus(): Promise<FrontendStatus>;
  frontendReady(lastSequence: number): Promise<Activation | null>;
  retryCleanup(): Promise<void>;
  subscribeActivation(listener: ActivationListener): () => void;
}

/** 可选的提醒恢复事件扩展端口。 */
export interface RecoveryPort {
  subscribeRecovery?(listener: RecoveryListener): () => void;
}
