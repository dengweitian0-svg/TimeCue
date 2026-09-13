ALTER TABLE schedules
    ADD COLUMN month_end_policy TEXT NULL
        CHECK (month_end_policy IS NULL OR month_end_policy = 'clamp');
