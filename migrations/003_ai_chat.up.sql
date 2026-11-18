BEGIN TRANSACTION;
CREATE TABLE ai_sessions (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL UNIQUE,
 session_name TEXT NOT NULL DEFAULT '', compact_event_id INTEGER REFERENCES ai_events(id),
 deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1)), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE ai_runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL UNIQUE,
 session_id TEXT NOT NULL REFERENCES ai_sessions(session_id), provider TEXT NOT NULL, model TEXT NOT NULL, input TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('running','wait_for_cancel','succeeded','failed','cancelled')),
 output TEXT, error JSON CHECK(error IS NULL OR json_valid(error)), idempotency_key TEXT NOT NULL,
 created_at INTEGER NOT NULL, finished_at INTEGER, UNIQUE(session_id,idempotency_key)
);
CREATE TABLE ai_model_calls (
 id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES ai_runs(run_id), session_id TEXT NOT NULL REFERENCES ai_sessions(session_id),
 input JSON NOT NULL CHECK(json_valid(input)), output JSON CHECK(output IS NULL OR json_valid(output)), usage JSON CHECK(usage IS NULL OR json_valid(usage)),
 model_reply_id TEXT, status TEXT NOT NULL CHECK(status IN ('running','retry_wait','succeeded','failed','cancelled')),
 continuation INTEGER NOT NULL DEFAULT 0 CHECK(continuation IN (0,1)), error JSON CHECK(error IS NULL OR json_valid(error)),
 retry_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_count>=0 AND retry_count<=max_retry), max_retry INTEGER NOT NULL CHECK(max_retry>=0),
 next_retry_at INTEGER, retry_history JSON NOT NULL DEFAULT '[]' CHECK(json_valid(retry_history)), created_at INTEGER NOT NULL, finished_at INTEGER
);
CREATE TABLE ai_checkpoints (
 run_id TEXT NOT NULL PRIMARY KEY REFERENCES ai_runs(run_id), model_call_id INTEGER NOT NULL REFERENCES ai_model_calls(id),
 status TEXT NOT NULL CHECK(status IN ('compact','model_call_start','model_call_end','tool_start','tool_end')), state JSON NOT NULL CHECK(json_valid(state))
);
CREATE TABLE ai_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES ai_runs(run_id), session_id TEXT NOT NULL REFERENCES ai_sessions(session_id),
 model_call_id INTEGER REFERENCES ai_model_calls(id), model_reply_id TEXT,
 type TEXT NOT NULL CHECK(type IN ('start','thinking','text','tool_start','tool_result','error','end','compact')),
 data JSON NOT NULL CHECK(json_valid(data)), seq INTEGER NOT NULL CHECK(seq>=1), compact_succeeded INTEGER, created_at INTEGER NOT NULL, UNIQUE(run_id,seq)
);
CREATE TABLE ai_tool_calls (
 id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES ai_runs(run_id), session_id TEXT NOT NULL REFERENCES ai_sessions(session_id),
 model_call_id INTEGER NOT NULL REFERENCES ai_model_calls(id), tool_call_id TEXT NOT NULL, name TEXT NOT NULL,
 input JSON NOT NULL CHECK(json_valid(input)), output JSON CHECK(output IS NULL OR json_valid(output)), error JSON CHECK(error IS NULL OR json_valid(error)),
 status TEXT NOT NULL CHECK(status IN ('running','retry_wait','succeeded','failed','skipped')), idempotency_key TEXT,
 retry_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_count>=0 AND retry_count<=max_retry), max_retry INTEGER NOT NULL CHECK(max_retry>=0),
 next_retry_at INTEGER, created_at INTEGER NOT NULL, finished_at INTEGER, UNIQUE(run_id,idempotency_key)
);
PRAGMA user_version = 3;
COMMIT;
