BEGIN TRANSACTION;

DROP TABLE notifications;
DROP TABLE schedules;

PRAGMA user_version = 0;

COMMIT;
