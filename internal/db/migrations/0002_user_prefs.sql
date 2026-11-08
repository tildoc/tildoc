-- Per-user UI preferences (validated in core.Prefs).
ALTER TABLE users ADD COLUMN prefs jsonb NOT NULL DEFAULT '{}'::jsonb;
