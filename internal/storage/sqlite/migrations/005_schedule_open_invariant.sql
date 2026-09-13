-- 先修复历史遗留的单次关闭状态，再用触发器阻止该非法状态继续写入。
UPDATE schedules
SET open = 1
WHERE cron IS NULL AND open = 0;

CREATE TRIGGER IF NOT EXISTS schedules_single_open_insert
BEFORE INSERT ON schedules
WHEN NEW.cron IS NULL AND NEW.open = 0
BEGIN
    SELECT RAISE(ABORT, 'single schedules must remain open');
END;

CREATE TRIGGER IF NOT EXISTS schedules_single_open_update
BEFORE UPDATE OF cron, open ON schedules
WHEN NEW.cron IS NULL AND NEW.open = 0
BEGIN
    SELECT RAISE(ABORT, 'single schedules must remain open');
END;