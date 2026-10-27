#!/usr/bin/env bash
# check-migrations.sh - the rollback-safe schema linter (docs/UPGRADES.md,
# Schema rules). Run by `make check` and the n-1 CI job. Needs git, bash and
# awk only: no Go, no database.
#
# It judges migrations ADDED since the baseline (newest v* tag, else the
# merge-base with origin/main; MIGRATIONS_BASE overrides) and never the
# historical ones. The working tree counts, so committed, staged, unstaged and
# untracked files are all seen. The previous release's binary must keep working
# on the new schema, so an added migration must be expand-only:
#
#   contract (breaks the old binary): DROP COLUMN/TABLE/TYPE/SCHEMA/EXTENSION/
#     VIEW/TRIGGER/FUNCTION/INDEX/CONSTRAINT, RENAME, SET SCHEMA, DETACH PARTITION, ADD GENERATED, ALTER COLUMN ... TYPE /
#     SET NOT NULL / DROP DEFAULT, ADD CONSTRAINT/PRIMARY KEY/UNIQUE/FOREIGN
#     KEY/CHECK/EXCLUDE (NOT VALID and USING INDEX do not help: the old
#     binary's new writes are still checked), CREATE UNIQUE INDEX, CREATE
#     TRIGGER, inline ADD COLUMN ... UNIQUE/CHECK/REFERENCES, CREATE OR REPLACE FUNCTION/VIEW, ALTER FUNCTION, each unless the
#     table or object is created in the same file; TRUNCATE.
#       needs   -- hatamoto:contract unused-since vX.Y
#   backfill (rewrites rows): UPDATE <table> [[AS] alias] SET, DELETE FROM,
#     ON CONFLICT DO UPDATE, MERGE THEN UPDATE SET/DELETE, wherever they appear (CTEs, DO bodies).
#       needs   -- hatamoto:backfill idempotent
#
# Only the Up section is judged. Go migrations need `// hatamoto:backfill
# idempotent` (and the contract marker when they mention contract SQL).
# Editing or deleting a shipped migration fails with `history:`.
# Best effort. Comments are dropped; everything else is scanned as text, string
# literals included (EXECUTE '...', format('...'), $$ bodies), for the contract
# and backfill keywords above (COMMENT ON statements and function bodies for
# tables created in the same file excepted). E'..' escapes, '' and $tag$ quoting are tracked
# so a stray apostrophe cannot swallow the next statement; an unterminated
# quote fails the file. n-1.sh is the backstop.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$(git rev-parse --show-toplevel)"

base=$(OVERRIDE="${MIGRATIONS_BASE:-}" bash "$here/baseline-ref.sh") || exit 2
echo "baseline: $base"

fail=0
judged=0
mdir=backend/migrations
pathre="^$mdir/[0-9]+_[^/]+\\.(sql|go)\$"

# status<TAB>path for everything under backend/migrations that differs from base.
# quotepath is off: git would quote a non-ASCII name and the path match would skip it.
changes=$( {
  git -c core.quotepath=false diff --name-status --no-renames "$base" -- "$mdir"
  git -c core.quotepath=false ls-files -o --exclude-standard -- "$mdir" | sed 's/^/A\t/'
} | awk -F '\t' '{print $1 "\t" $2}' | sort -u -t$'\t' -k2,2 -k1,1 )

judge_sql() { # $1 = file; prints ok/FAIL lines, exit 1 when any FAIL
  awk -v file="$1" '
    # alter_drops(s): 1 when an ALTER TABLE statement has a DROP action other than
    # DROP NOT NULL (DROP [COLUMN] [IF EXISTS] x, DROP CONSTRAINT, DROP DEFAULT ...).
    function alter_drops(str,   rest, seg, d) {
      rest = str
      while (match(rest, /(^|[^A-Z0-9_])ALTER TABLE /)) {
        rest = substr(rest, RSTART + RLENGTH)
        seg = rest
        if (match(seg, /(^|[^A-Z0-9_])ALTER TABLE /)) seg = substr(seg, 1, RSTART - 1)
        d = seg
        while (match(d, /(^|[ ,(])DROP /)) {
          d = substr(d, RSTART + RLENGTH)
          if (d !~ /^NOT NULL/) return 1
        }
      }
      return 0
    }
    # table_of(str): the table name at the start of str (after ONLY / IF EXISTS), unquoted, schema dropped.
    function table_of(str,   t) {
      t = str; sub(/^(ONLY )?(IF EXISTS )?/, "", t); sub(/ .*/, "", t); sub(/[(].*/, "", t)
      gsub(/"/, "", t); sub(/^[^.]*[.]/, "", t)
      return t
    }
    # alter_adds(s): 1 when any ADD clause of an ALTER TABLE adds a constraint to a table not created in
    # this file. NOT VALID and USING INDEX do not help: the previous binary'"'"'s new writes are still checked.
    function alter_adds(str,   rest, seg) {
      rest = str
      while (match(rest, /(^|[^A-Z0-9_])ALTER TABLE /)) {
        rest = substr(rest, RSTART + RLENGTH)
        seg = rest
        if (match(seg, /(^|[^A-Z0-9_])ALTER TABLE /)) seg = substr(seg, 1, RSTART - 1)
        if (seg ~ /(^|[ ,(])ADD (CONSTRAINT|PRIMARY KEY|UNIQUE|FOREIGN KEY|CHECK|EXCLUDE)( |$)/ && !(table_of(seg) in created)) return 1
        # ADD [COLUMN] x type ... UNIQUE / CHECK (..) / REFERENCES / PRIMARY KEY: the same constraint, inline
        if (seg ~ /(^|[ ,(])ADD / && (seg ~ / (UNIQUE|REFERENCES|PRIMARY KEY)( |[(]|,|$)/ || seg ~ / CHECK ?[(]/) && !(table_of(seg) in created)) return 1
      }
      return 0
    }
    # unique_index(s): 1 for CREATE UNIQUE INDEX on a table not created in this file.
    function unique_index(str,   rest, t) {
      rest = str
      while (match(rest, /(^|[^A-Z0-9_])CREATE UNIQUE INDEX /)) {
        rest = substr(rest, RSTART + RLENGTH)
        if (!match(rest, / ON (ONLY )?/)) return 1
        t = substr(rest, RSTART + RLENGTH)
        if (!(table_of(t) in created)) return 1
      }
      return 0
    }
    # has_update(s): UPDATE <table> [[AS] alias] SET anywhere (CTEs, DO bodies, EXECUTE strings), but not
    # ON UPDATE SET NULL / ON UPDATE CASCADE / FOR UPDATE.
    function has_update(str,   i) {
      while (match(str, /UPDATE (ONLY )?[^ ]+ ((AS )?[^ ]+ )?SET[ (]/)) {
        i = RSTART
        if ((i == 1 || substr(str, i - 1, 1) !~ /[A-Z0-9_]/) && substr(str, 1, i - 1) !~ /(^| )(ON|FOR) $/) return 1
        str = substr(str, i + 1)
      }
      return 0
    }
    # has_truncate(s): the TRUNCATE statement, not the trigger event (AFTER TRUNCATE ON t, TRUNCATE OR INSERT).
    function has_truncate(str,   i) {
      while (match(str, /TRUNCATE (TABLE |ONLY )?[^ ]+/)) {
        i = RSTART
        if ((i == 1 || substr(str, i - 1, 1) !~ /[A-Z0-9_]/) && substr(str, i) !~ /^TRUNCATE (ON|OR) /) return 1
        str = substr(str, i + 1)
      }
      return 0
    }
    # trigger_on_existing(s): CREATE [OR REPLACE] [CONSTRAINT] TRIGGER on a table not created in this file.
    function trigger_on_existing(str,   t) {
      if (str !~ /^CREATE (OR REPLACE )?(CONSTRAINT )?TRIGGER /) return 0
      if (!match(str, / ON /)) return 1
      t = substr(str, RSTART + RLENGTH)
      return !(table_of(t) in created)
    }
    # replaces_existing(s): CREATE OR REPLACE FUNCTION/VIEW or ALTER FUNCTION on an object not created in
    # this file. It changes behaviour the previous binary relies on (trigger functions, views).
    function replaces_existing(str,   t) {
      if (str ~ /^CREATE OR REPLACE (FUNCTION|PROCEDURE|VIEW|RECURSIVE VIEW|TEMP VIEW|TEMPORARY VIEW) /) {
        t = str; sub(/^CREATE OR REPLACE (RECURSIVE |TEMP |TEMPORARY )?(FUNCTION|PROCEDURE|VIEW) /, "", t)
        return !(table_of(t) in created)
      }
      if (str ~ /^ALTER (FUNCTION|PROCEDURE|VIEW|MATERIALIZED VIEW) /) {
        t = str; sub(/^ALTER (FUNCTION|PROCEDURE|VIEW|MATERIALIZED VIEW) /, "", t)
        return !(table_of(t) in created)
      }
      return 0
    }
    # mentions_created(s): does s name a table created in this file (as a whole word)?
    function mentions_created(str,   k, rest, p) {
      for (k in created) {
        rest = str
        while ((p = index(rest, k)) > 0) {
          if ((p == 1 || substr(rest, p - 1, 1) !~ /[A-Z0-9_]/) && substr(rest, p + length(k), 1) !~ /[A-Z0-9_]/) return 1
          rest = substr(rest, p + 1)
        }
      }
      return 0
    }
    function flush(   s, a, t, label) {
      s = toupper(stmt); stmt = ""
      gsub(/[ \t]+/, " ", s); sub(/^ /, "", s); sub(/ $/, "", s)
      if (s == "") return
      own = ""
      if (s ~ /^COMMENT ON /) return   # the text is a string literal, not SQL that runs
      if (match(s, /^CREATE (OR REPLACE )?(FUNCTION|PROCEDURE|VIEW|MATERIALIZED VIEW) (IF NOT EXISTS )?[^ (]+/)) {
        t = substr(s, RSTART, RLENGTH); sub(/^CREATE (OR REPLACE )?(FUNCTION|PROCEDURE|VIEW|MATERIALIZED VIEW) (IF NOT EXISTS )?/, "", t)
        if (s !~ /^CREATE OR REPLACE /) own = table_of(t)   # a plain CREATE of a new object; registered once this statement is judged
      }
      if (match(s, /CREATE (UNLOGGED )?TABLE (IF NOT EXISTS )?[^ (]+/)) {
        t = substr(s, RSTART, RLENGTH); sub(/^CREATE (UNLOGGED )?TABLE (IF NOT EXISTS )?/, "", t)
        created[table_of(t)] = 1
      }
      a = s; gsub(/ALTER (FOREIGN )?TABLE /, "ALTER_TABLE ", a); gsub(/ALTER COLUMN /, "ALTER ", a)   # COLUMN is optional in SQL
      label = ""
      if (s ~ /(^|[^A-Z0-9_])DROP (COLUMN|TABLE|TYPE|SCHEMA|EXTENSION|VIEW|MATERIALIZED VIEW|TRIGGER|FUNCTION|PROCEDURE|INDEX|CONSTRAINT|SEQUENCE|DOMAIN|POLICY|RULE)( |$)/) label = "DROP"
      else if (s ~ /(^| )RENAME( |$)/) label = "RENAME"
      else if (s ~ /(^| )SET SCHEMA /) label = "SET SCHEMA"
      else if (s ~ /(^| )DETACH PARTITION /) label = "DETACH PARTITION"
      else if (s ~ /(^| )ADD GENERATED /) label = "ADD GENERATED"
      else if (a ~ /(^|[^A-Z0-9_])ALTER [^ ]+ ADD GENERATED /) label = "ADD GENERATED"
      else if (a ~ /(^|[^A-Z0-9_])ALTER [^ ]+ (SET DATA )?TYPE /) label = "ALTER COLUMN TYPE"
      else if (a ~ /(^|[^A-Z0-9_])ALTER [^ ]+ SET NOT NULL/) label = "SET NOT NULL"
      else if (alter_drops(s)) label = "DROP"
      else if (has_truncate(s)) label = "TRUNCATE"
      else if (alter_adds(s)) label = "ADD CONSTRAINT on an existing table"
      else if (unique_index(s)) label = "CREATE UNIQUE INDEX on an existing table"
      else if (trigger_on_existing(s)) label = "CREATE TRIGGER on an existing table"
      else if (replaces_existing(s)) label = "CREATE OR REPLACE / ALTER FUNCTION or VIEW"
      if (label != "") { nc++; cmsg[nc] = "contract: " file ":" start ": " label ": " substr(s, 1, 80) }
      label = ""
      # a function body for a table created in this file cannot rewrite existing rows
      if (s ~ /^CREATE (OR REPLACE )?(FUNCTION|PROCEDURE) / && mentions_created(s)) { if (own != "") created[own] = 1; return }
      if (has_update(s)) label = "UPDATE"
      else if (s ~ /(^|[^A-Z0-9_])DELETE FROM /) label = "DELETE"
      else if (s ~ /DO UPDATE SET /) label = "ON CONFLICT DO UPDATE"
      else if (s ~ /THEN (UPDATE SET|DELETE)( |$)/) label = "MERGE UPDATE/DELETE"
      if (label != "") { nb++; bmsg[nb] = "backfill: " file ":" start ": " label ": " substr(s, 1, 80) }
      if (own != "") created[own] = 1
    }
    BEGIN { sq = "\047" }
    {
      raw = $0; sub(/\r$/, "", raw)
      if (raw ~ /^[ \t]*--[ \t]*[+]goose[ \t]+Down/) { down = 1; next }
      if (raw ~ /^[ \t]*--[ \t]*[+]goose[ \t]+Up/) { down = 0; sawup = 1; next }
      if (down) next
      if (raw ~ /hatamoto:contract/) {
        if (raw ~ /^[ \t]*--[ \t]*hatamoto:contract[ \t]+unused-since[ \t]+v[0-9]+[.][0-9]+[ \t]*$/) {
          has_contract = 1; v = raw; sub(/^.*unused-since[ \t]+/, "", v); sub(/[ \t]*$/, "", v); cver = v
        } else { bad++; print "FAIL contract: " file ":" NR ": malformed contract marker (want \047-- hatamoto:contract unused-since vX.Y\047)" }
      }
      if (raw ~ /^[ \t]*--[ \t]*hatamoto:backfill[ \t]+idempotent[ \t]*$/) has_backfill = 1
      n = length(raw)
      for (i = 1; i <= n; i++) {
        c = substr(raw, i, 1); c2 = substr(raw, i, 2)
        if (inblk) { if (c2 == "*/") { inblk = 0; i++ } continue }
        if (inq) {
          stmt = stmt c
          if (estr && c == "\\") { i++; stmt = stmt substr(raw, i, 1) }   # backslash escapes in E-strings
          else if (c == sq) { if (substr(raw, i + 1, 1) == sq) { i++; stmt = stmt sq } else inq = 0 }
          continue
        }
        if (indq) {   # inside $tag$ ... $tag$: quotes are not tracked, comments still are
          if (substr(raw, i, length(dtag)) == dtag) { stmt = stmt dtag; i += length(dtag) - 1; indq = 0; continue }
          if (c2 == "--") break
          stmt = stmt c
          continue
        }
        if (c2 == "--") break
        if (c2 == "/*") { inblk = 1; i++; continue }
        if (c == ";") { flush(); continue }
        if (stmt ~ /^[ \t]*$/ && c !~ /[ \t]/) start = NR
        if (c == "$" && (i == 1 || substr(raw, i - 1, 1) !~ /[A-Za-z0-9_]/) && match(substr(raw, i), /^[$]([A-Za-z_][A-Za-z0-9_]*)?[$]/)) {
          dtag = substr(raw, i, RLENGTH); indq = 1; stmt = stmt dtag; i += RLENGTH - 1; continue
        }
        if (c == sq) {
          inq = 1
          estr = (i > 1 && substr(raw, i - 1, 1) ~ /[Ee]/ && (i == 2 || substr(raw, i - 2, 1) !~ /[A-Za-z0-9_]/))
        }
        stmt = stmt c
      }
      stmt = stmt " "
    }
    END {
      if (inq || indq || inblk) { bad++; print "FAIL " file ": unterminated " (inq ? "quote" : indq ? "dollar quote" : "comment") " (the linter cannot judge what follows)" }
      flush()
      if (!sawup) { bad++; print "FAIL " file ": no goose Up marker" }
      if (nc > 0 && !has_contract) { for (i = 1; i <= nc; i++) print "FAIL " cmsg[i]; bad += nc }
      if (nb > 0 && !has_backfill) { for (i = 1; i <= nb; i++) print "FAIL " bmsg[i]; bad += nb }
      if (bad == 0) {
        kind = ""
        if (nc > 0) kind = "contract (unused-since " cver ")"
        if (nb > 0) kind = kind (kind == "" ? "" : ", ") "backfill (idempotent)"
        if (kind == "") kind = "additive"
        print "ok   " file ": " kind
      }
      exit (bad > 0)
    }
  ' "$1"
}

judge_go() { # $1 = file
  local f=$1 bad=0
  if ! grep -Eq '^[ 	]*//[ 	]*hatamoto:backfill[ 	]+idempotent[ 	]*$' "$f"; then
    echo "FAIL backfill: $f: new Go migration needs '// hatamoto:backfill idempotent'"; bad=1
  fi
  if sed -E 's/DROP +NOT +NULL//I' "$f" | grep -Eiq 'DROP +(COLUMN|TABLE|TYPE|SCHEMA|EXTENSION|VIEW|TRIGGER|FUNCTION|PROCEDURE|INDEX|CONSTRAINT|SEQUENCE|DOMAIN|POLICY|RULE|IF +EXISTS|DEFAULT)|ALTER +TABLE.* DROP +[A-Za-z"]|RENAME|SET +NOT +NULL|ALTER +(COLUMN +)?[^ ]+ +(SET +DATA +)?TYPE|ADD +(CONSTRAINT|PRIMARY +KEY|UNIQUE|FOREIGN +KEY|CHECK|EXCLUDE)|CREATE +UNIQUE +INDEX' \
     && ! grep -Eq '^[ 	]*//[ 	]*hatamoto:contract[ 	]+unused-since[ 	]+v[0-9]+\.[0-9]+[ 	]*$' "$f"; then
    echo "FAIL contract: $f: new Go migration mentions contract SQL; needs '// hatamoto:contract unused-since vX.Y'"; bad=1
  fi
  [[ $bad -eq 0 ]] && echo "ok   $f: go migration (backfill idempotent)"
  return $bad
}

# The one sanctioned edit of a shipped migration (README): the four lead indexes
# in 001_baseline.sql lose a middle name segment. True only when the file equals
# the baseline's after that single rename is applied to both, so any other
# change to the file still fails with `history:`.
baseline_index_rename_only() {
  local re='s/\bidx_[a-z0-9]+_leads_(intent_score|intent_tier|merged_into|sales_engaged_at)\b/idx_leads_\1/g'
  cmp -s <(git show "$base:$mdir/001_baseline.sql" | sed -E "$re") <(sed -E "$re" "$mdir/001_baseline.sql")
}

while IFS=$'\t' read -r status path; do
  [[ -n "$path" ]] || continue
  [[ "$path" =~ $pathre ]] || continue
  [[ "$path" == *_test.go ]] && continue
  if [[ "$status" == M && "$path" == "$mdir/001_baseline.sql" ]] && baseline_index_rename_only; then
    echo "ok   $path: lead index names made neutral (the one sanctioned edit, backend/migrations/README.md)"
    continue
  fi
  if [[ "$status" != A ]]; then
    case "$status" in D) what=deleted ;; *) what=modified ;; esac
    echo "FAIL history: $path was $what since $base; never edit a shipped migration (backend/migrations/README.md)"
    fail=1; continue
  fi
  judged=$((judged + 1))
  case "$path" in
    *.sql) judge_sql "$path" || fail=1 ;;
    *.go)  judge_go "$path" || fail=1 ;;
  esac
done <<< "$changes"

if [[ $judged -eq 0 && $fail -eq 0 ]]; then
  echo "ok   migrations: 0 judged files (baseline $base)"
fi
exit $fail
