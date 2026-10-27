#!/usr/bin/env bash
# Sourced by pre-push (and scripts/test-affected.sh).
#
# hm_affected <worktree> <git diff range>: decide what a push has to test.
#   HM_MODE      "full" (run everything, as before) or "affected"
#   HM_REASON    why it is full (empty when affected)
#   HM_SEEDS     "<module dir>\t<pkg>" lines, the packages the diff changes directly
#   HM_AFFECTED  the same format: the seeds plus every package that imports one of them
#                (tests included), per module, sorted. Empty for a push no test reads.
# Packages are module-relative ("./internal/admin", "."), like hm_go_targets.
#
# The graph comes from `go list -test`, built with and without -tags=integration and
# unioned. A non-Go file maps to a package when the package embeds it, or through
# scripts/affected-readers.txt (read from the pushed tree) for tests that read files by
# path. Anything this cannot place means FULL: a force-full path (module files, build
# constraints, migrations, the SDK, examples, the hooks and gate scripts, ...), a
# non-Go file under backend/ that nothing maps, a removed package, a failing `go list`, or
# an affected set above HATAMOTO_AFFECTED_MAX_PCT (default 60) of a module's packages.
# A Go change or an added file always adds ./internal/release (whole-tree guards).
# It never narrows silently: every FULL carries a reason.
#
# hm_affected_pkgs <module>: the affected packages of one module, one per line.
# hm_affected_ctx <module>: the gate-cache context; "mode:full" never equals an affected one.
# hm_valid_pkgs <tags> <package>...: the packages go can build under those tags (see below).

# hm_force_reason <repo-relative path>: print why this path forces a full run; 1 when it does not.
hm_force_reason() {
  local f=$1 b=${1##*/}
  case $f in
    go.mod | */go.mod | go.sum | */go.sum | go.work | go.work.* | */go.work | */go.work.*) echo "module file $f" ;;
    backend/vendor/*) echo "vendored code $f" ;;
    backend/migrations/*) echo "migration $f" ;;
    backend/pkg/sdk/*) echo "SDK $f" ;;
    examples/*) echo "example plugin $f" ;;
    .githooks/* | Makefile | .gitleaks.toml | .github/workflows/* | deploy/sql/*) echo "gate or build input $f" ;;
    staticcheck.conf | .staticcheck.conf | */staticcheck.conf) echo "staticcheck config $f" ;;
    scripts/affected-readers.txt | scripts/check-suites.*) echo "gate data $f" ;;
    scripts/go-test-flaky.sh | scripts/integration-gate.sh | scripts/known-flakes.txt | scripts/check-dco.sh | \
      scripts/check-neutral.sh | scripts/test-sdk-external.sh | scripts/test-hooks.sh | \
      scripts/pipeline-gate.sh | scripts/ship.sh) echo "gate script $f" ;;
    *) [[ $b =~ _(aix|android|darwin|dragonfly|freebsd|hurd|illumos|ios|js|linux|nacl|netbsd|openbsd|plan9|solaris|wasip1|windows|zos|386|amd64|arm|arm64|loong64|mips|mipsle|mips64|mips64le|ppc64|ppc64le|riscv64|s390x|sparc64|wasm)(_test)?\.go$ ]] \
         && echo "GOOS/GOARCH file $f" || return 1 ;;
  esac
}

# hm_affected_module <worktree> <repo-relative dir>: nearest go.mod directory ("" if none).
hm_affected_module() {
  local wt=$1 m=$2
  while [[ ! -f $wt/$m/go.mod ]]; do
    [[ $m == . || $m == / ]] && return 0
    m=$(dirname "$m")
  done
  echo "$m"
}

# hm_readers_match <table> <repo-relative path>: "<module>\t<pkg>" for each row whose glob
# matches (scripts/affected-readers.txt: "<glob> <module dir> <pkg>", # comments).
hm_readers_match() {
  local rg rmod rp
  [[ -f $1 ]] || return 0
  while read -r rg rmod rp; do
    [[ -z $rg || $rg == \#* ]] && continue
    rg=${rg//\*\*/*}
    # shellcheck disable=SC2254 # the glob is the point
    case $2 in $rg) printf '%s\t%s\n' "$rmod" "$rp" ;; esac
  done <"$1"
}

# The awk engine: reads the seed file, then `go list -test` rows, prints
# "SEED|AFF <pkg>", "UNMAPPED <module-relative file>" and "TOTAL <n>".
read -r -d '' _HM_AFFECTED_AWK <<'AWK' || true
function norm(s) { sub(/ \[.*\]$/, "", s); sub(/\.test$/, "", s); sub(/_test$/, "", s); return s }
function rel(ip) { return ip == mod ? "." : "./" substr(ip, length(mod) + 2) }
FNR == NR { n++; kind[n] = $1; val[n] = $2; next }
{
  ip = norm($1)
  if ($1 !~ / / && $1 !~ /\.test$/) real[ip] = 1
  nf = split($7 " " $8 " " $9, ef, " ")
  for (i = 1; i <= nf; i++) if (ef[i] != "") embed[$6 "/" ef[i]] = ip
  for (c = 3; c <= 5; c++) {
    ni = split($c, im, " ")
    for (i = 1; i <= ni; i++) {
      if (im[i] == "" || substr(im[i], 1, 1) == "[") continue
      d = norm(im[i])
      if (d != ip && !((d SUBSEP ip) in seen)) { seen[d SUBSEP ip] = 1; rdeps[d] = rdeps[d] " " ip }
    }
  }
}
END {
  for (i = 1; i <= n; i++) {
    if (kind[i] == "S") { ip = (val[i] == "." ? mod : mod "/" substr(val[i], 3)); seed[ip] = 1 }
    else if (embed[dir "/" val[i]] != "") seed[embed[dir "/" val[i]]] = 1
    else print "UNMAPPED", val[i]
  }
  for (s in seed) { queue[++qn] = s; vis[s] = 1 }
  for (h = 1; h <= qn; h++) {
    k = split(rdeps[queue[h]], r, " ")
    for (j = 1; j <= k; j++) if (!(r[j] in vis)) { vis[r[j]] = 1; queue[++qn] = r[j] }
  }
  for (s in seed) print "SEED", rel(s)
  for (v in vis) if ((v in real) || (v in seed)) print "AFF", rel(v)
  t = 0; for (p in real) t++
  print "TOTAL", t
}
AWK

# hm_affected_full <reason>: record a FULL decision.
hm_affected_full() { HM_MODE=full; HM_REASON=$1; HM_SEEDS=; HM_AFFECTED=; return 0; }

hm_affected() {
  local wt=$1 range=$2 tmp f d m rel reason pct=${HATAMOTO_AFFECTED_MAX_PCT:-60}
  HM_MODE=affected HM_REASON= HM_SEEDS= HM_AFFECTED=
  tmp=$(mktemp -d) || { hm_affected_full "no temp dir"; return 0; }
  local -A seeds=() files=() hit=()
  local -a changed=() mods=()
  git -C "$wt" diff -z --name-only --no-renames "$range" -- >"$tmp/changed" 2>/dev/null \
    || { rm -rf "$tmp"; hm_affected_full "cannot diff $range"; return 0; }
  mapfile -d '' -t changed <"$tmp/changed"
  if git -C "$wt" diff -U0 --no-renames "$range" -- '*.go' 2>/dev/null | grep -qE '^[-+][[:space:]]*//[[:space:]]*(go:build|\+build)'; then
    rm -rf "$tmp"; hm_affected_full "build constraint lines changed"; return 0
  fi

  # Whole-tree guards: internal/release scans every tracked file (private keys) and builds the binary
  # with and without the test-key seam, so any Go change or any added file runs it.
  local readers=$wt/scripts/affected-readers.txt rmod rp guard=$wt/backend/internal/release
  if [[ -d $guard ]] && m=$(hm_affected_module "$wt" backend/internal/release) && [[ -n $m ]] \
     && { printf '%s\n' "${changed[@]}" | grep -q '\.go$' \
          || [[ -n $(git -C "$wt" diff --name-only --diff-filter=A --no-renames "$range" -- 2>/dev/null) ]]; }; then
    seeds[$m]+="S	./internal/release"$'\n'
  fi
  for f in "${changed[@]}"; do
    if reason=$(hm_force_reason "$f"); then rm -rf "$tmp"; hm_affected_full "$reason"; return 0; fi
    d=$(dirname "$f")
    # A test that reads this path by name (any file, Go sources included) is a seed too.
    while read -r rmod rp; do
      [[ -n $rmod ]] || continue
      seeds[$rmod]+="S	$rp"$'\n'; hit[$f]=1
    done < <(hm_readers_match "$readers" "$f")
    if [[ $f == *.go && /$f != */testdata/* ]]; then
      if [[ ! -d $wt/$d ]] || ! compgen -G "$wt/$d/*.go" >/dev/null; then
        rm -rf "$tmp"; hm_affected_full "package removed: $d"; return 0
      fi
      m=$(hm_affected_module "$wt" "$d")
      [[ -n $m ]] || continue
      rel=${d#"$m"}; rel=${rel#/}; rel=${rel:+./$rel}
      seeds[$m]+="S	${rel:-.}"$'\n'
      continue
    fi
    # Not a Go package file: the reader table above and/or an embed maps it to packages.
    m=$(hm_affected_module "$wt" "$d")
    if [[ -n $m ]]; then
      files[$m]+="F	${f#"$m"/}"$'\n'
    elif [[ -z ${hit[$f]:-} && ( $f == backend/* || /$f == */testdata/* ) ]]; then
      rm -rf "$tmp"; hm_affected_full "no test mapping for $f"; return 0
    fi
  done

  local fmt='{{.ImportPath}}	{{.ForTest}}	{{join .Imports " "}}	{{join .TestImports " "}}	{{join .XTestImports " "}}	{{.Dir}}	{{join .EmbedFiles " "}}	{{join .TestEmbedFiles " "}}	{{join .XTestEmbedFiles " "}}'
  local mod modpath abs total n k line
  mapfile -t mods < <(printf '%s\n' "${!seeds[@]}" "${!files[@]}" | sort -u | grep .)
  for mod in "${mods[@]}"; do
    abs=$(cd "$wt/$mod" && pwd)
    modpath=$(cd "$wt/$mod" && GO_QUEUE_FORCE_LOCAL=1 go list -m 2>/dev/null) \
      || { rm -rf "$tmp"; hm_affected_full "go list -m failed in $mod"; return 0; }
    printf '%s%s' "${seeds[$mod]:-}" "${files[$mod]:-}" >"$tmp/seeds"
    (cd "$wt/$mod" && GO_QUEUE_FORCE_LOCAL=1 go list -test -tags=integration -f "$fmt" ./... \
       && GO_QUEUE_FORCE_LOCAL=1 go list -test -f "$fmt" ./...) >"$tmp/list" 2>"$tmp/err" \
      || { reason=$(head -1 "$tmp/err"); rm -rf "$tmp"; hm_affected_full "go list failed in $mod: $reason"; return 0; }
    awk -F'\t' -v mod="$modpath" -v dir="$abs" "$_HM_AFFECTED_AWK" "$tmp/seeds" "$tmp/list" >"$tmp/out"
    total=$(awk '$1=="TOTAL"{print $2}' "$tmp/out")
    while read -r k line; do
      case $k in
        SEED) HM_SEEDS+="$mod	$line"$'\n' ;;
        AFF) HM_AFFECTED+="$mod	$line"$'\n' ;;
        UNMAPPED)
          if [[ -z ${hit[$mod/$line]:-} && -z ${hit[$line]:-} ]]; then
            rm -rf "$tmp"; hm_affected_full "no test mapping for ${line}"; return 0
          fi ;;
      esac
    done <"$tmp/out"
    n=$(awk -v m="$mod" -F'\t' '$1==m' <<<"$HM_AFFECTED" | wc -l)
    if ((total > 0 && n * 100 > pct * total)); then
      rm -rf "$tmp"; hm_affected_full "affected set is $n of $total packages in $mod (over ${pct}%)"; return 0
    fi
  done
  HM_SEEDS=$(printf '%s' "$HM_SEEDS" | sort -u)
  HM_AFFECTED=$(printf '%s' "$HM_AFFECTED" | sort -u)
  rm -rf "$tmp"
}

hm_affected_pkgs() { awk -F'\t' -v m="$1" '$1==m{print $2}' <<<"$HM_AFFECTED"; }

hm_affected_ctx() {
  if [[ $HM_MODE == full ]]; then echo "mode:full"; else echo "mode:affected:$(hm_affected_pkgs "$1" | sort | tr '\n' ' ')"; fi
}

# hm_valid_pkgs <tags> <package>...: run in a module directory; print the packages go can build
# under those tags, one import path per line. A package made only of files the tags exclude
# ("build constraints exclude all Go files") is dropped instead of failing the gate.
# Exported by pre-push for the gates' bash -c scripts.
hm_valid_pkgs() {
  local tags=$1; shift
  GO_QUEUE_FORCE_LOCAL=1 go list -e ${tags:+-tags "$tags"} -f '{{if not .Error}}{{.ImportPath}}{{end}}' "$@" 2>/dev/null | grep . || true
}
