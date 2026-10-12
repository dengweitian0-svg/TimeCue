BEGIN TRANSACTION;

ALTER TABLE schedules ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE notifications ADD COLUMN delivery_stopped INTEGER NOT NULL DEFAULT 0 CHECK (delivery_stopped IN (0, 1));
ALTER TABLE notifications ADD COLUMN snapshot_taken INTEGER NOT NULL DEFAULT 0 CHECK (snapshot_taken IN (0, 1));
ALTER TABLE notifications ADD COLUMN name_snapshot TEXT;
ALTER TABLE notifications ADD COLUMN description_snapshot TEXT;

-- 规则版本保留有效区间，后续恢复不能用当前开关重建暂停期间。
CREATE TABLE schedule_versions (
    schedule_id INTEGER NOT NULL REFERENCES schedules(id),
    revision INTEGER NOT NULL,
    effective_from INTEGER NOT NULL,
    effective_until INTEGER,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    description TEXT,
    period_open INTEGER,
    period_cron TEXT,
    single_notify_time INTEGER,
    cancelled INTEGER NOT NULL,
    deleted INTEGER NOT NULL,
    PRIMARY KEY (schedule_id, revision)
);
CREATE UNIQUE INDEX idx_schedule_versions_current ON schedule_versions(schedule_id) WHERE effective_until IS NULL;
INSERT INTO schedule_versions
SELECT id, revision, updated_at, NULL, name, type, description, period_open, period_cron, single_notify_time, cancelled, deleted FROM schedules;

-- 旧数据只能以升级时仍可读的内容回退，无法恢复升级前的原始文字。
UPDATE notifications SET snapshot_taken = 1,
    name_snapshot = (SELECT name FROM schedules WHERE id = schedule_id),
    description_snapshot = (SELECT description FROM schedules WHERE id = schedule_id)
WHERE notify_time <= CAST(strftime('%s', 'now') AS INTEGER) * 1000000000;
UPDATE notifications SET delivery_stopped = 1
WHERE schedule_id IN (SELECT id FROM schedules WHERE deleted = 1 OR cancelled = 1 OR period_open = 0);

CREATE TABLE schedule_write_receipts (
    operation_key TEXT PRIMARY KEY,
    request_digest TEXT NOT NULL,
    result_json TEXT NOT NULL
);
PRAGMA user_version = 2;
COMMIT;
