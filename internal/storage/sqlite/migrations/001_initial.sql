CREATE TABLE IF NOT EXISTS schema_migrations (
    version         INTEGER PRIMARY KEY,
    applied_at_utc  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schedules (
    id                   INTEGER PRIMARY KEY,
    title                TEXT NOT NULL
                         CHECK (length(trim(title)) > 0),
    cron                 TEXT NULL,
    open                 INTEGER NOT NULL DEFAULT 1
                         CHECK (open IN (0, 1)),
    first_start_local    TEXT NOT NULL,
    original_start_local TEXT NOT NULL,
    duration_minutes     INTEGER NOT NULL
                         CHECK (duration_minutes > 0),
    revision             INTEGER NOT NULL DEFAULT 1
                         CHECK (revision > 0),
    created_at_utc       TEXT NOT NULL,
    updated_at_utc       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS schedules_first_start_local_idx
    ON schedules(first_start_local, id);

CREATE TABLE IF NOT EXISTS occurrence_overrides (
    id                INTEGER PRIMARY KEY,
    schedule_id       INTEGER NOT NULL,
    occurrence_local  TEXT NOT NULL,
    open              INTEGER NOT NULL CHECK (open IN (0, 1)),
    FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE CASCADE,
    UNIQUE (schedule_id, occurrence_local)
);

CREATE TABLE IF NOT EXISTS reminder_attempts (
    id                INTEGER PRIMARY KEY,
    schedule_id       INTEGER NOT NULL,
    occurrence_local  TEXT NOT NULL,
    attempted_at_utc  TEXT NOT NULL,
    status            TEXT NOT NULL,
    FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE CASCADE
);
