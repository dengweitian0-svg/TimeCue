BEGIN TRANSACTION;

CREATE TABLE schedules (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('single', 'period')),
    description TEXT,
    period_open INTEGER CHECK (period_open IN (0, 1)),
    period_cron TEXT,
    single_notify_time INTEGER,
    cancelled INTEGER NOT NULL DEFAULT 0 CHECK (cancelled IN (0, 1)),
    deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (type = 'single' AND period_open IS NULL AND period_cron IS NULL AND single_notify_time IS NOT NULL)
        OR (type = 'period' AND period_open IS NOT NULL AND period_cron IS NOT NULL AND single_notify_time IS NULL)
    )
);

CREATE TABLE notifications (
    id INTEGER PRIMARY KEY,
    schedule_id INTEGER NOT NULL REFERENCES schedules(id),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    notify_time INTEGER NOT NULL,
    acknowledged_at INTEGER,
    deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
    UNIQUE (schedule_id, notify_time)
);

CREATE INDEX idx_schedules_visible ON schedules(deleted, created_at DESC, id DESC);
CREATE INDEX idx_notifications_due ON notifications(deleted, status, notify_time);
CREATE INDEX idx_notifications_schedule ON notifications(schedule_id, notify_time DESC);

PRAGMA user_version = 1;

COMMIT;
