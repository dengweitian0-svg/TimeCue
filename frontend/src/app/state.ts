import type { Controller } from '../controller';

/** 暴露给应用壳的统一状态快照类型，不重新定义 Controller 状态。 */
export type AppState = ReturnType<Controller['snapshot']>;
