-- 为一次发生建立唯一的一次性提醒机会，并限制发送状态只能沿合法方向推进。
CREATE UNIQUE INDEX IF NOT EXISTS reminder_attempts_identity_idx
    ON reminder_attempts(schedule_id, occurrence_local);

CREATE TRIGGER IF NOT EXISTS reminder_attempts_status_insert
BEFORE INSERT ON reminder_attempts
WHEN NEW.status IS NULL OR NEW.status NOT IN ('claimed', 'submitted', 'failed', 'skipped')
BEGIN
    SELECT RAISE(ABORT, 'invalid reminder attempt status');
END;

CREATE TRIGGER IF NOT EXISTS reminder_attempts_status_update
BEFORE UPDATE OF status ON reminder_attempts
WHEN NEW.status IS NULL OR NEW.status NOT IN ('claimed', 'submitted', 'failed', 'skipped')
     OR OLD.status <> 'claimed'
     OR NEW.status NOT IN ('submitted', 'failed', 'skipped')
BEGIN
    SELECT RAISE(ABORT, 'invalid reminder attempt status transition');
END;
