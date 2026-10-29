-- A menu item can be plain text ("No link"): link type 'none'. Per-item settings
-- (style, colour, badge, icon, visibility, id, title, aria-label, rel) live in one
-- jsonb column; css_classes keeps the free classes.
--
-- The old '#' placeholder item (custom, url '#') was how a heading without a link
-- was stored; it becomes a real 'none' item. Group rows with a NULL url (docs
-- sidebar) are not touched.
--
-- hatamoto:contract unused-since v1.0
-- hatamoto:backfill idempotent

-- +goose Up
-- The default keeps an older binary's inserts (which omit the column) valid.
ALTER TABLE hatamoto_menu_items ADD COLUMN IF NOT EXISTS attrs jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE hatamoto_menu_items DROP CONSTRAINT IF EXISTS hatamoto_menu_items_link_type_check;
ALTER TABLE hatamoto_menu_items ADD CONSTRAINT hatamoto_menu_items_link_type_check
    CHECK (link_type IN ('entity', 'external', 'archive', 'custom', 'none'));

UPDATE hatamoto_menu_items SET link_type = 'none', url = NULL WHERE link_type = 'custom' AND url = '#';

-- +goose Down
-- Lossy: every none item (also ones created later) becomes custom '#'; attrs is dropped.
UPDATE hatamoto_menu_items SET link_type = 'custom', url = '#' WHERE link_type = 'none';

ALTER TABLE hatamoto_menu_items DROP CONSTRAINT IF EXISTS hatamoto_menu_items_link_type_check;
ALTER TABLE hatamoto_menu_items ADD CONSTRAINT hatamoto_menu_items_link_type_check
    CHECK (link_type IN ('entity', 'external', 'archive', 'custom'));

ALTER TABLE hatamoto_menu_items DROP COLUMN IF EXISTS attrs;
