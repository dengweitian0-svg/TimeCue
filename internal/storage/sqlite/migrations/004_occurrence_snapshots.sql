ALTER TABLE schedules
    ADD COLUMN notes TEXT NOT NULL DEFAULT '';

-- 例外表已经随初始迁移创建，但本版本才启用完整快照字段。
-- 重建表以便把“快照字段必须全部为空或全部完整”的规则落为表约束。
CREATE TABLE occurrence_overrides_v4 (
    id                 INTEGER PRIMARY KEY,
    schedule_id        INTEGER NOT NULL,
    occurrence_local   TEXT NOT NULL,
    open               INTEGER NOT NULL CHECK (open IN (0, 1)),
    actual_start_local TEXT NULL,
    duration_minutes   INTEGER NULL
                         CHECK (duration_minutes IS NULL OR duration_minutes > 0),
    title              TEXT NULL,
    notes              TEXT NULL,
    FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE CASCADE,
    UNIQUE (schedule_id, occurrence_local),
    CHECK (
        (open = 1 AND actual_start_local IS NOT NULL AND duration_minutes IS NOT NULL
            AND title IS NOT NULL AND length(trim(title)) > 0 AND notes IS NOT NULL)
        OR
        (open = 0 AND (
            (actual_start_local IS NULL AND duration_minutes IS NULL AND title IS NULL AND notes IS NULL)
            OR
            (actual_start_local IS NOT NULL AND duration_minutes IS NOT NULL
                AND title IS NOT NULL AND length(trim(title)) > 0 AND notes IS NOT NULL)
        ))
    )
);

INSERT INTO occurrence_overrides_v4(id, schedule_id, occurrence_local, open)
SELECT id, schedule_id, occurrence_local, open
FROM occurrence_overrides;

DROP TABLE occurrence_overrides;
ALTER TABLE occurrence_overrides_v4 RENAME TO occurrence_overrides;
