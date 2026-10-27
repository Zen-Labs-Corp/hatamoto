-- Content change log: every transaction that really changes content appends
-- one row. Each running server polls it and purges its in-process rendered-page
-- cache when a new committed row shows up, so a write from another process
-- (CLI, worker, another replica) reaches the pages already cached here.
-- Statement triggers cover every writer, including the raw UPDATEs outside
-- entity.Service, with no wiring.
--
-- Why a log and not a counter: a counter row is locked until commit by every
-- writer, which serializes content writes and adds deadlock shapes. Inserts
-- into an append-only table take no shared lock.
--
-- Statement triggers with transition tables fire for each statement, but log
-- only when the statement touched rows: the scheduled-publish worker's UPDATE
-- usually matches nothing and must not purge every cache every minute.
--
-- hatamoto:contract unused-since v0.30
-- The linter files CREATE TRIGGER on an existing table under contract, though
-- here it only adds a side effect: no release reads the log, so the previous
-- binary keeps working on this schema (and writes through the triggers).

-- +goose Up
CREATE TABLE hatamoto_content_changes (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    txid bigint NOT NULL,
    at   timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- txid: the once-per-transaction guard and the poller's "newer than" scan.
CREATE INDEX hatamoto_content_changes_txid_idx ON hatamoto_content_changes (txid);
-- at: pruning.
CREATE INDEX hatamoto_content_changes_at_idx ON hatamoto_content_changes (at);

-- SECURITY DEFINER: a writer role that may touch content tables need not be
-- granted the log table. search_path is pinned and every name qualified.
-- The statement only reaches this function when it touched rows (the
-- transition table is read to be sure), and logs once per transaction.
-- +goose StatementBegin
CREATE FUNCTION hatamoto_log_content_change() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM changed) THEN
        RETURN NULL;
    END IF;
    INSERT INTO public.hatamoto_content_changes (txid)
    SELECT pg_catalog.txid_current()
    WHERE NOT EXISTS (
        SELECT 1 FROM public.hatamoto_content_changes WHERE txid = pg_catalog.txid_current()
    );
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- Transition tables need one trigger per event.
CREATE TRIGGER trg_content_change_ins_entities
    AFTER INSERT ON hatamoto_entities
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();
CREATE TRIGGER trg_content_change_upd_entities
    AFTER UPDATE ON hatamoto_entities
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();
CREATE TRIGGER trg_content_change_del_entities
    AFTER DELETE ON hatamoto_entities
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();

CREATE TRIGGER trg_content_change_ins_menus
    AFTER INSERT ON hatamoto_menus
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();
CREATE TRIGGER trg_content_change_upd_menus
    AFTER UPDATE ON hatamoto_menus
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();
CREATE TRIGGER trg_content_change_del_menus
    AFTER DELETE ON hatamoto_menus
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();

CREATE TRIGGER trg_content_change_ins_menu_items
    AFTER INSERT ON hatamoto_menu_items
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();
CREATE TRIGGER trg_content_change_upd_menu_items
    AFTER UPDATE ON hatamoto_menu_items
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();
CREATE TRIGGER trg_content_change_del_menu_items
    AFTER DELETE ON hatamoto_menu_items
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION hatamoto_log_content_change();

-- +goose Down
DROP TRIGGER trg_content_change_del_menu_items ON hatamoto_menu_items;
DROP TRIGGER trg_content_change_upd_menu_items ON hatamoto_menu_items;
DROP TRIGGER trg_content_change_ins_menu_items ON hatamoto_menu_items;
DROP TRIGGER trg_content_change_del_menus ON hatamoto_menus;
DROP TRIGGER trg_content_change_upd_menus ON hatamoto_menus;
DROP TRIGGER trg_content_change_ins_menus ON hatamoto_menus;
DROP TRIGGER trg_content_change_del_entities ON hatamoto_entities;
DROP TRIGGER trg_content_change_upd_entities ON hatamoto_entities;
DROP TRIGGER trg_content_change_ins_entities ON hatamoto_entities;
DROP FUNCTION hatamoto_log_content_change();
DROP TABLE hatamoto_content_changes;
