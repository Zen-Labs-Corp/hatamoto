#!/usr/bin/env bash
# test-check-migrations.sh - fixture tests for scripts/check-migrations.sh.
# Builds a throwaway git repo with a shipped history, drops one migration at a
# time into the working tree (never committed, like a contributor's checkout)
# and asserts the linter's exit code and message. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
REPO=$PWD
LINT="$REPO/scripts/check-migrations.sh"
bash -n "$LINT" "$REPO/scripts/baseline-ref.sh"

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
R="$W/r"
git init -q -b main "$R"
cd "$R"
git config user.email t@t; git config user.name t
M=backend/migrations
mkdir -p $M/testdata
cat > $M/001_base.sql <<'SQL'
-- +goose Up
CREATE TABLE t (id bigint PRIMARY KEY, a int, b text);
-- +goose Down
DROP TABLE t;
SQL
# History may contain anything: the linter never judges it.
cat > $M/202610010000_old.sql <<'SQL'
-- +goose Up
ALTER TABLE t DROP COLUMN b;
UPDATE t SET a = 1;
-- +goose Down
ALTER TABLE t ADD COLUMN b text;
SQL
printf -- '-- +goose Up\nCREATE INDEX idx_zz_leads_intent_score ON t (a);\nCREATE INDEX idx_zz_other ON t (b);\n-- +goose Down\nSELECT 1;\n' > $M/001_baseline.sql
printf 'package migrations\n' > $M/head.go
# A shipped Go migration.
printf 'package migrations\n\nimport "hatamoto.dev/core/internal/author"\n\nvar _ = author.X\n' > $M/202610020000_oldgo.go
git add -A && git commit -qm history
BASE=$(git rev-parse HEAD)

bad=0
run()  { (cd "$R" && MIGRATIONS_BASE="$BASE" bash "$LINT" 2>&1); }
reset() { git -C "$R" reset -q --hard "$BASE"; git -C "$R" clean -fdq; }
ok()   { echo "ok   test $1"; }
want_ok()   { if out=$(run); then ok "$1"; else echo "FAIL test $1: expected pass"; echo "$out"; bad=1; fi; reset; }
want_fail() {
  if out=$(run); then echo "FAIL test $1: expected failure"; echo "$out"; bad=1
  elif ! grep -Eq "$2" <<<"$out"; then echo "FAIL test $1: output lacks /$2/"; echo "$out"; bad=1
  else ok "$1"; fi
  reset
}
# mig NAME BODY...: write an added migration whose Up section is BODY.
mig() { local f=$M/$1; shift; printf -- '-- +goose Up\n%s\n-- +goose Down\nSELECT 1;\n' "$*" > "$R/$f"; }
# mig_head NAME HEAD BODY: like mig, with extra lines before the Up marker.
mig_pre() { local f=$M/$1 pre=$2; shift 2; printf -- '%s\n-- +goose Up\n%s\n-- +goose Down\nSELECT 1;\n' "$pre" "$*" > "$R/$f"; }
N=300000000001_x.sql

# 1 additive
mig $N 'ALTER TABLE t ADD COLUMN note text;'
want_ok "additive ADD COLUMN"

# 2 contract marker
mig $N 'ALTER TABLE t DROP COLUMN a;'
want_fail "DROP COLUMN without marker" 'FAIL contract: .*300000000001_x\.sql:2: DROP'
mig_pre $N '-- hatamoto:contract unused-since v1.2' 'ALTER TABLE t DROP COLUMN a;'
want_ok "DROP COLUMN with contract marker"
mig_pre $N '-- hatamoto:contract unused-since 1.2' 'ALTER TABLE t DROP COLUMN a;'
want_fail "malformed contract marker" 'malformed contract marker'

# 3 Down section is not judged
printf -- '-- +goose Up\nALTER TABLE t ADD COLUMN note text;\n-- +goose Down\nALTER TABLE t DROP COLUMN note;\nUPDATE t SET a = 1;\n' > $R/$M/$N
want_ok "DROP and UPDATE only in Down"

# 4 backfill marker
mig $N 'UPDATE t SET a = 1;'
want_fail "UPDATE without marker" 'FAIL backfill: .*UPDATE'
mig_pre $N '-- hatamoto:backfill idempotent' 'UPDATE t SET a = 1;'
want_ok "UPDATE with backfill marker"
mig $N 'DELETE FROM t WHERE a IS NULL;'
want_fail "DELETE FROM without marker" 'FAIL backfill: .*DELETE'
mig $N 'INSERT INTO t (id, a) VALUES (1, 2) ON CONFLICT (id) DO UPDATE SET a = 2;'
want_fail "ON CONFLICT DO UPDATE without marker" 'FAIL backfill: .*ON CONFLICT DO UPDATE'

# 5 things that must not trip
mig $N 'CREATE TABLE n (
  id bigint PRIMARY KEY,
  t_id bigint REFERENCES t(id) ON DELETE CASCADE ON UPDATE CASCADE,
  updated_at timestamptz
);
-- DROP TABLE x
/* DROP COLUMN a; */
CREATE VIEW v AS SELECT * FROM n FOR UPDATE;
ALTER TABLE t ALTER COLUMN a DROP NOT NULL;
ALTER TABLE t ADD COLUMN c int NOT NULL DEFAULT 0;
ALTER TABLE t ADD COLUMN d text;'
want_ok "non-trips: cascade, updated_at, FOR UPDATE, comments"

# 6 constraints
# NOT VALID skips existing rows only; the old binary's new writes are still checked.
mig $N 'ALTER TABLE t
  ADD CONSTRAINT c CHECK (a > 0)
  NOT VALID;'
want_fail "multi-line ADD CONSTRAINT NOT VALID is contract" 'FAIL contract: .*ADD CONSTRAINT'
mig_pre $N '-- hatamoto:contract unused-since v1.2' 'ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0) NOT VALID;'
want_ok "ADD CONSTRAINT NOT VALID with contract marker"
mig $N 'ALTER TABLE t
  ADD CONSTRAINT c CHECK (a > 0);'
want_fail "multi-line ADD CONSTRAINT without NOT VALID" 'FAIL contract: .*ADD CONSTRAINT'
mig $N 'ALTER TABLE t VALIDATE CONSTRAINT c;'
want_ok "VALIDATE CONSTRAINT"

# 6b every constraint kind, with and without NOT VALID / USING INDEX, and CREATE UNIQUE INDEX
i=0
while IFS= read -r stmt; do
  i=$((i + 1)); mig "30000000020${i}_k.sql" "$stmt"
  want_fail "new constraint: $stmt" 'FAIL contract:'
done <<'LIST'
ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0);
ALTER TABLE t ADD CHECK (a > 0) NOT VALID;
ALTER TABLE t ADD CONSTRAINT f FOREIGN KEY (a) REFERENCES t2(id) NOT VALID;
ALTER TABLE t ADD FOREIGN KEY (a) REFERENCES t2(id);
ALTER TABLE t ADD CONSTRAINT u UNIQUE (a);
ALTER TABLE t ADD UNIQUE (a);
ALTER TABLE t ADD CONSTRAINT p PRIMARY KEY (id);
ALTER TABLE t ADD PRIMARY KEY (id);
ALTER TABLE t ADD CONSTRAINT e EXCLUDE USING gist (a WITH =);
ALTER TABLE t ADD CONSTRAINT u UNIQUE USING INDEX ux;
ALTER TABLE ONLY public.t ADD CONSTRAINT c CHECK (a > 0) NOT VALID;
ALTER TABLE t ADD CONSTRAINT c1 CHECK (a > 0) NOT VALID, ADD CONSTRAINT c2 UNIQUE (a);
ALTER TABLE t ADD CONSTRAINT c1 FOREIGN KEY (a) REFERENCES t2(id), ADD CONSTRAINT c2 CHECK (b <> '') NOT VALID;
ALTER TABLE t ADD COLUMN z int, ADD CONSTRAINT c CHECK (z > 0) NOT VALID;
CREATE UNIQUE INDEX ux ON t (lower(b));
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux ON public.t(a) WHERE a IS NOT NULL;
DO $$ BEGIN ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0) NOT VALID; END $$;
LIST

# 6c unique indexes and constraints on a table created in the same file stay additive
mig $N 'CREATE TABLE n (id bigint, a int);
CREATE UNIQUE INDEX ux ON n (lower(a::text));
ALTER TABLE n ADD CONSTRAINT c CHECK (a > 0) NOT VALID;
CREATE INDEX ix ON t (a);
ALTER TABLE t ADD COLUMN z int;
ALTER TABLE t VALIDATE CONSTRAINT c;'
want_ok "same-file table: unique index and constraints; plain index; VALIDATE"

# 7 table created in the same file
mig $N 'CREATE TABLE n (id bigint, t_id bigint);
ALTER TABLE n ADD CONSTRAINT n_t FOREIGN KEY (t_id) REFERENCES t(id);'
want_ok "constraint on a table created in the same file"

# 8 each contract statement fails
i=0
while IFS= read -r stmt; do
  i=$((i + 1)); mig "30000000010${i}_c.sql" "$stmt"
  want_fail "contract: $stmt" 'FAIL contract:'
done <<'LIST'
ALTER TABLE t RENAME COLUMN a TO z;
ALTER TABLE t RENAME TO u;
ALTER TABLE t ALTER COLUMN a SET NOT NULL;
ALTER TABLE t ALTER COLUMN a TYPE bigint;
ALTER TABLE t ALTER COLUMN a SET DATA TYPE bigint;
ALTER TABLE t ALTER COLUMN a DROP DEFAULT;
DROP INDEX idx_a;
ALTER TABLE t DROP CONSTRAINT c;
TRUNCATE t;
DROP TABLE t;
ALTER TABLE t DROP a;
ALTER TABLE t DROP IF EXISTS a;
ALTER TABLE t DROP COLUMN IF EXISTS a;
ALTER TABLE t SET SCHEMA other;
ALTER TABLE t ALTER a ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE t DETACH PARTITION t1;
ALTER TABLE t ADD COLUMN z int, DROP a;
ALTER TABLE t ALTER a SET NOT NULL;
ALTER TABLE t ALTER a TYPE bigint;
ALTER TABLE t ALTER a SET DATA TYPE bigint;
ALTER TABLE t ALTER a DROP DEFAULT;
ALTER TABLE t ALTER COLUMN type TYPE text;
ALTER TABLE t ALTER COLUMN a DROP NOT NULL, DROP b;
DROP SEQUENCE s;
DROP DOMAIN d;
DROP POLICY p ON t;
DROP RULE r ON t;
LIST

# 8b relaxing or unrelated: must pass, including a column literally named type
mig $N 'ALTER TABLE t ADD COLUMN type text;
ALTER TABLE t ALTER COLUMN a DROP NOT NULL;
ALTER TABLE t ALTER a DROP NOT NULL;
ALTER TABLE t ALTER COLUMN type SET DEFAULT 1;
ALTER TABLE t ALTER type SET DEFAULT 1;
ALTER TABLE type ADD COLUMN q int;'
want_ok "DROP NOT NULL, column named type"
mig_pre $N '-- hatamoto:contract unused-since v1.2' 'ALTER TABLE t DROP a;'
want_ok "keyword-less DROP with contract marker"

# 8c backfill detection anywhere in the text
i=0
while IFS= read -r stmt; do
  i=$((i + 1)); mig "30000000030${i}_b.sql" "$stmt"
  want_fail "backfill: $stmt" 'FAIL backfill:'
done <<'LIST'
UPDATE t x SET a = 1;
UPDATE t AS x SET a = 1 FROM u WHERE u.id = x.id;
UPDATE ONLY t SET a = 1;
UPDATE t SET(a, b) = (1, 2);
MERGE INTO t USING u ON t.id = u.id WHEN MATCHED THEN UPDATE SET a = u.a;
MERGE INTO t USING u ON t.id = u.id WHEN MATCHED THEN DELETE;
WITH d AS ( DELETE FROM t RETURNING id ) SELECT 1;
WITH u AS ( UPDATE t SET a = 1 RETURNING id ) SELECT 1;
DO $$ BEGIN EXECUTE 'UPDATE t SET a = 1'; END $$;
DO $$ BEGIN DELETE FROM t WHERE a IS NULL; END $$;
SELECT format('DELETE FROM %I', 't');
INSERT INTO t (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET a = 2;
INSERT INTO t (id) VALUES (1) ON CONFLICT ON CONSTRAINT t_pkey DO UPDATE SET a = 2;
LIST
mig $N 'TRUNCATE TABLE t;'
want_fail "TRUNCATE is contract" 'FAIL contract: .*TRUNCATE'
mig $N "DO \$\$ BEGIN EXECUTE 'TRUNCATE t'; END \$\$;"
want_fail "EXECUTE of TRUNCATE" 'FAIL contract: .*TRUNCATE'
mig $N "DO \$\$ BEGIN EXECUTE 'DROP TABLE t'; END \$\$;"
want_fail "EXECUTE of DROP TABLE" 'FAIL contract: .*DROP'
mig $N 'CREATE TABLE n (id bigint, t_id bigint REFERENCES t(id) ON UPDATE SET NULL ON DELETE SET NULL, c bigint REFERENCES t(id) ON UPDATE CASCADE);
CREATE TRIGGER trg AFTER INSERT OR UPDATE OF a ON n FOR EACH ROW EXECUTE FUNCTION f();
CREATE TRIGGER trg2 BEFORE TRUNCATE ON n FOR EACH STATEMENT EXECUTE FUNCTION f();
SELECT * FROM t FOR UPDATE SKIP LOCKED;
SELECT updated_at, last_update FROM t; -- UPDATE t SET a = 1;'
want_ok "non-trips: ON UPDATE SET NULL, trigger events, FOR UPDATE, updated_at"

# 8e triggers and replaced functions/views change behaviour the old binary relies on
i=0
while IFS= read -r stmt; do
  i=$((i + 1)); mig "30000000040${i}_t.sql" "$stmt"
  want_fail "contract: $stmt" 'FAIL contract:'
done <<'LIST'
CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION f();
CREATE OR REPLACE TRIGGER trg BEFORE INSERT ON public.t FOR EACH ROW EXECUTE FUNCTION f();
CREATE CONSTRAINT TRIGGER trg AFTER INSERT ON t DEFERRABLE FOR EACH ROW EXECUTE FUNCTION f();
CREATE OR REPLACE FUNCTION f() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'no'; END $$ LANGUAGE plpgsql;
CREATE OR REPLACE VIEW v AS SELECT 1;
ALTER FUNCTION f() RENAME TO g;
ALTER FUNCTION f() SECURITY DEFINER;
LIST
mig_pre $N '-- hatamoto:contract unused-since v1.2' 'CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION f();'
want_ok "trigger on an existing table with contract marker"
mig $N 'CREATE TABLE n (id bigint, a int);
CREATE FUNCTION nf() RETURNS trigger AS $$ BEGIN RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER trg BEFORE INSERT ON n FOR EACH ROW EXECUTE FUNCTION nf();
CREATE OR REPLACE FUNCTION nf() RETURNS trigger AS $$ BEGIN RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE VIEW nv AS SELECT * FROM n;
CREATE OR REPLACE VIEW nv AS SELECT id FROM n;'
want_ok "functions, triggers and views for objects created in the same file"

# 8f inline column constraints on an existing table are constraints
i=0
while IFS= read -r stmt; do
  i=$((i + 1)); mig "30000000050${i}_i.sql" "$stmt"
  want_fail "inline constraint: $stmt" 'FAIL contract: .*ADD CONSTRAINT'
done <<'LIST'
ALTER TABLE t ADD COLUMN ext text DEFAULT 'none' UNIQUE;
ALTER TABLE t ADD COLUMN ext text UNIQUE;
ALTER TABLE t ADD COLUMN x int DEFAULT 0 CHECK (x > 0);
ALTER TABLE t ADD COLUMN IF NOT EXISTS x int CHECK(x > 0);
ALTER TABLE t ADD COLUMN u bigint REFERENCES t2(id);
ALTER TABLE t ADD COLUMN u bigint REFERENCES t2(id) ON DELETE CASCADE;
ALTER TABLE t ADD COLUMN z int, ADD COLUMN y int UNIQUE;
ALTER TABLE t ADD z int PRIMARY KEY;
LIST
mig $N 'CREATE TABLE n (id bigint);
ALTER TABLE n ADD COLUMN x int UNIQUE CHECK (x > 0);
ALTER TABLE t ADD COLUMN unique_id text DEFAULT '"''"', ADD COLUMN checked boolean;'
want_ok "inline constraints on a new table; column names containing the keywords"

# 8g false positives: COMMENT ON text, function bodies for new tables, materialized views
mig $N "CREATE TABLE n (id bigint, a int);
COMMENT ON TABLE n IS 'Rows are removed with DELETE FROM after 30 days; UPDATE t SET a = 1; DROP TABLE x';
COMMENT ON COLUMN n.a IS 'ALTER TABLE t DROP COLUMN a';"
want_ok "COMMENT ON text is not judged"
mig $N "COMMENT ON TABLE t IS 'x';
DROP TABLE t;"
want_fail "statement after COMMENT ON is still judged" 'FAIL contract: .*DROP'
mig $N 'CREATE TABLE n (entity_id bigint PRIMARY KEY, n int);
CREATE FUNCTION bump() RETURNS trigger AS $$ BEGIN
  INSERT INTO n (entity_id, n) VALUES (NEW.id, 1) ON CONFLICT (entity_id) DO UPDATE SET n = n.n + 1;
  UPDATE n SET n = n + 1 WHERE entity_id = NEW.id;
  RETURN NEW; END $$ LANGUAGE plpgsql;'
want_ok "backfill keywords in a function body for a table created in the same file"
mig $N 'CREATE TABLE n (id bigint);
CREATE FUNCTION wipe() RETURNS void AS $$ BEGIN UPDATE t SET a = 1; END $$ LANGUAGE plpgsql;'
want_fail "function body updating an existing table is still a backfill" 'FAIL backfill: .*UPDATE'
mig $N 'CREATE FUNCTION wipe() RETURNS void AS $$ BEGIN DELETE FROM t; END $$ LANGUAGE plpgsql;'
want_fail "function body with no new table is still a backfill" 'FAIL backfill: .*DELETE'
mig $N 'CREATE MATERIALIZED VIEW stats AS SELECT count(*) AS c, 1 AS id FROM t;
CREATE UNIQUE INDEX stats_id ON stats (id);'
want_ok "unique index on a materialized view created in the same file"

# 8d stray apostrophes: a following statement is still judged
mig $N "INSERT INTO t (id, b) VALUES (1, E'it\\'s');
UPDATE t SET a = 1;"
want_fail "E-string with escaped quote, then UPDATE" 'FAIL backfill: .*UPDATE'
mig $N "INSERT INTO t (id, b) VALUES (1, 'it''s');
DROP TABLE t;"
want_fail "doubled quote, then DROP TABLE" 'FAIL contract: .*DROP'
mig $N "DO \$\$ BEGIN -- don't panic
  RAISE NOTICE 'a'; END \$\$;
DROP TABLE t;"
want_fail "apostrophe in a comment inside a DO body, then DROP TABLE" 'FAIL contract: .*DROP'
mig $N "SELECT 'unterminated;
DROP TABLE t;"
want_fail "unterminated quote fails closed" 'unterminated quote'
mig_pre $N '-- hatamoto:backfill idempotent' "INSERT INTO t (id, b) VALUES (1, E'it\\'s');
UPDATE t SET a = 1;"
want_ok "E-string then UPDATE with marker"

# 9 Go migrations
printf 'package migrations\n\nfunc up() {}\n' > $R/$M/300000000002_x.go
want_fail "Go migration without marker" 'FAIL backfill: .*needs'
printf 'package migrations\n\n// hatamoto:backfill idempotent\nfunc up() {}\n' > $R/$M/300000000002_x.go
want_ok "Go migration with marker"
printf 'package migrations\n\n// hatamoto:backfill idempotent\nconst q = "ALTER TABLE t DROP COLUMN a"\n' > $R/$M/300000000002_x.go
want_fail "Go migration with contract SQL needs the contract marker" 'FAIL contract: .*Go migration'

# 10 history
printf -- '-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n' > $R/$M/202610010000_old.sql
want_fail "editing a shipped migration" 'FAIL history: .*202610010000_old\.sql was modified'
rm $R/$M/202610010000_old.sql
want_fail "deleting a shipped migration" 'FAIL history: .*202610010000_old\.sql was deleted'
mv $R/$M/202610010000_old.sql $R/$M/202610010001_old.sql
want_fail "renaming a shipped migration" 'FAIL history: .*202610010000_old\.sql was deleted'
sed -i 's#author\.X#author.Y#' $R/$M/202610020000_oldgo.go
want_fail "editing a shipped Go migration" 'FAIL history: .*202610020000_oldgo\.go was modified'
printf 'package migrations\n\nimport "example.com/core/internal/author"\n\nvar _ = author.X\n' > $R/$M/202610020000_oldgo.go
want_fail "changing the import path of a shipped Go migration" 'FAIL history: .*202610020000_oldgo\.go was modified'

# 10b the one sanctioned edit of 001_baseline.sql: the lead index rename, nothing else
sed -i 's/idx_zz_leads_intent_score/idx_leads_intent_score/' $R/$M/001_baseline.sql
want_ok "baseline lead index rename is the sanctioned edit"
sed -i 's/idx_zz_leads_intent_score/idx_leads_intent_score/; s/idx_zz_other/idx_other/' $R/$M/001_baseline.sql
want_fail "baseline edit beyond the lead index rename" 'FAIL history: .*001_baseline\.sql was modified'
sed -i 's/idx_zz_leads_intent_score/idx_leads_intent_score/; s/(a)/(b)/' $R/$M/001_baseline.sql
want_fail "baseline index rename plus another change" 'FAIL history: .*001_baseline\.sql was modified'
sed -i 's/idx_zz_other/idx_leads_other/' $R/$M/001_baseline.sql
want_fail "baseline rename of an index outside the four" 'FAIL history: .*001_baseline\.sql was modified'

# 11 non-migration files next to the migrations
mkdir -p $R/$M/testdata
printf 'DROP TABLE t;\nUPDATE t SET a = 1;\n' > $R/$M/testdata/x.sql
printf 'package migrations\n\nvar x = 1\n' > $R/$M/head.go
printf 'package migrations\n' > $R/$M/x_test.go
want_ok "testdata, head.go and _test.go are not judged"

# 12 untracked and staged files are seen
mig $N 'ALTER TABLE t DROP COLUMN a;'
want_fail "untracked bad migration" 'FAIL contract:'
mig $N 'ALTER TABLE t DROP COLUMN a;'
git -C "$R" add -A
want_fail "staged bad migration" 'FAIL contract:'

# 12b non-ASCII file names are judged (git would quote them by default)
mig 300000000003_café.sql 'ALTER TABLE t DROP COLUMN a;'
want_fail "untracked non-ASCII name" 'FAIL contract: .*caf'
mig 300000000003_café.sql 'ALTER TABLE t DROP COLUMN a;'
git -C "$R" add -A
want_fail "staged non-ASCII name" 'FAIL contract: .*caf'

# 13 no baseline: no override, no tag, no origin/main
mig $N 'ALTER TABLE t ADD COLUMN note text;'
if out=$(cd "$R" && env -u MIGRATIONS_BASE bash "$LINT" 2>&1); then rc=0; else rc=$?; fi
if [[ $rc -eq 2 ]] && grep -q 'cannot resolve' <<<"$out"; then ok "unresolvable baseline exits 2"
else echo "FAIL test unresolvable baseline: rc=$rc"; echo "$out"; bad=1; fi
reset

# 13b baseline-ref.sh: tags and merge-bases (no override)
BR="$REPO/scripts/baseline-ref.sh"
B="$W/b"
git init -q -b main "$B"
bc() { git -C "$B" commit -q --allow-empty -m "$1"; }
bref() { (cd "$B" && env -u MIGRATIONS_BASE -u N1_BASE "$@" bash "$BR" 2>&1) || true; }
expect() { # name want got
  if [[ "$2" == "$3" ]]; then ok "$1"; else echo "FAIL test $1: want '$2' got '$3'"; bad=1; fi
}
git -C "$B" config user.email t@t; git -C "$B" config user.name t
bc c1; git -C "$B" tag v1.0.0
bc c2; git -C "$B" tag v1.1.0-rc.1
expect "prerelease tag is not a baseline" v1.0.0 "$(bref)"
bc c3; git -C "$B" tag v1.1.0
expect "release beats its earlier prerelease" v1.1.0 "$(bref)"
bc c4; git -C "$B" tag v1.9.0; bc c5; git -C "$B" tag v1.10.0; bc c6; git -C "$B" tag v1.10.1-beta.2
expect "semver order (1.10.0 over 1.9.0, prerelease ignored)" v1.10.0 "$(bref)"
git -C "$B" checkout -q -b fix v1.0.0; bc f1
git -C "$B" tag vendor; git -C "$B" tag v2; git -C "$B" tag validated
expect "non-semver v* tags (v2, vendor, validated) are not baselines" v1.0.0 "$(bref)"
expect "newer tag not reachable from HEAD is ignored" v1.0.0 "$(bref)"
git -C "$B" checkout -q main

# no tags: merge-base with origin/main, first parent when HEAD is origin/main
O="$W/o.git"; C="$W/c"
git init -q --bare -b main "$O"
git clone -q "$O" "$C" 2>/dev/null
git -C "$C" config user.email t@t; git -C "$C" config user.name t
cc() { git -C "$C" commit -q --allow-empty -m "$1"; }
cref() { (cd "$C" && env -u MIGRATIONS_BASE -u N1_BASE "$@" bash "$BR" 2>&1) || true; }
cc m1; cc m2; git -C "$C" push -q origin main
m2=$(git -C "$C" rev-parse HEAD); m1=$(git -C "$C" rev-parse HEAD^1)
expect "merge-base == HEAD uses the first parent" "$m1" "$(cref)"
git -C "$C" checkout -q -b feat; cc p1; cc p2
expect "branch: merge-base with origin/main" "$m2" "$(cref)"
git -C "$C" checkout -q main; git -C "$C" merge -q --no-ff -m merge feat; git -C "$C" push -q origin main
expect "push of a merge: first parent is the previous main" "$m2" "$(cref)"
git -C "$C" push -q origin "$m1:refs/heads/rel"; git -C "$C" fetch -q origin
git -C "$C" checkout -q -b feat2 "$m2"; cc q1
expect "GITHUB_BASE_REF picks the merge-base target" "$m1" "$(cref GITHUB_BASE_REF=rel)"
expect "override still wins over tags and merge-bases" "$m1" "$(cref MIGRATIONS_BASE=$m1 OVERRIDE=$m1)"

# a root commit on origin/main (the one-commit public snapshot) is its own baseline
O1="$W/o1.git"; C1="$W/c1"
git init -q --bare -b main "$O1"
git clone -q "$O1" "$C1" 2>/dev/null
git -C "$C1" -c user.email=t@t -c user.name=t commit -q --allow-empty -m "Initial public release"
git -C "$C1" push -q origin main
r1=$(git -C "$C1" rev-parse HEAD)
expect "root commit on origin/main is its own baseline" "$r1" "$( (cd "$C1" && env -u MIGRATIONS_BASE -u N1_BASE bash "$BR" 2>&1) || true)"

# 14 this repo at HEAD judges nothing
CL="$W/clone"
git clone -q --no-hardlinks --no-checkout "$REPO" "$CL"
sha=$(git -C "$REPO" rev-parse HEAD)
git -C "$CL" cat-file -e "$sha^{commit}" 2>/dev/null || git -C "$CL" fetch -q origin "$sha"
git -C "$CL" checkout -q --detach "$sha"
if out=$(cd "$CL" && MIGRATIONS_BASE=HEAD bash "$LINT" 2>&1) && grep -q ' 0 judged files' <<<"$out"; then ok "HEAD vs HEAD judges 0 files"
else echo "FAIL test HEAD vs HEAD:"; echo "$out"; bad=1; fi

if [[ $bad -ne 0 ]]; then echo "test-check-migrations: FAILED"; exit 1; fi
echo "test-check-migrations: PASSED"
