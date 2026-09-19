/**
 * 兼容旧模块导入路径；生产代码应直接依赖 feature、shared contract 或 port。
 */
export * from './features/schedules/contracts';
export * from './features/reminders/contracts';
export * from './features/settings/contracts';
export * from './shared/contracts/errors';
export * from './shared/contracts/runtime';
export * from './shared/ports/app-port';
