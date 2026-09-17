-- 持久化提醒首次确认时间；NULL 表示尚未确认，不回填历史记录。
ALTER TABLE reminder_attempts ADD COLUMN processed_at_utc TEXT NULL;

-- 只为仍可处理的 submitted 记录建立索引，保持发送状态与处理状态独立。
CREATE INDEX IF NOT EXISTS reminder_attempts_pending_idx
    ON reminder_attempts(id)
    WHERE status = 'submitted' AND processed_at_utc IS NULL;
