-- 保存 scheduler 冷启动恢复已经完成到的 occurrence 开始时间。
-- id 固定为 1，保证整个数据库只有一个恢复进度事实。
CREATE TABLE IF NOT EXISTS scheduler_recovery_checkpoint (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    checkpoint_unix INTEGER NOT NULL,
    updated_at_utc TEXT NOT NULL
);
