-- +goose Up
-- State of the /llms.txt and /llms-full.txt files: one row (id is always true)
-- with the last item snapshot, the bounded list of recent changes, and the
-- hash, size and change time of each file. Additive only; read and written by
-- Go, validation lives there.
CREATE TABLE IF NOT EXISTS hatamoto_ai_files_state (
    id              boolean     PRIMARY KEY DEFAULT true CHECK (id),
    snapshot        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    changes         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    llms_hash       text        NOT NULL DEFAULT '',
    llms_changed_at timestamptz,
    llms_bytes      integer     NOT NULL DEFAULT 0,
    full_hash       text        NOT NULL DEFAULT '',
    full_changed_at timestamptz,
    full_bytes      integer     NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_ai_files_state;
