-- 由迁移版本表保证只执行一次，旧日程保留 NULL 截止日期。
ALTER TABLE schedules ADD COLUMN until_date_local TEXT NULL;
