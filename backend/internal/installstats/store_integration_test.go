//go:build integration

package installstats

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchPool creates a throwaway database with only the install stats
// migrations applied and drops it afterwards.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return applyMigrations(t, scratchBare(t), "202610280900_install_stats.sql", "202610281000_install_stats_last_ok.sql", "202611091000_install_stats_level.sql")
}

// scratchBare creates a throwaway empty database and drops it afterwards.
func scratchBare(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_stats_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	return pool
}

// applyMigrations runs the Up part of each named migration file on pool.
func applyMigrations(t *testing.T, pool *pgxpool.Pool, files ...string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	for _, f := range files {
		sqlText, err := os.ReadFile("../../migrations/" + f)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(strings.TrimPrefix(string(sqlText), "-- +goose Up"), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func TestStoreMissingRowIsUnset_Integration(t *testing.T) {
	st := NewStore(scratchPool(t))
	got, err := st.Load(context.Background())
	if err != nil || got.Consent != ConsentUnset || got.InstallID != "" {
		t.Fatalf("missing row must be unset without an id: %+v %v", got, err)
	}
	if _, ok, err := st.Claim(context.Background(), "v1"); err != nil || ok {
		t.Fatalf("claim with consent unset: ok=%v err=%v", ok, err)
	}
}

func TestStoreEnableDisableReset_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))

	// Reset before consent creates nothing.
	if got, err := st.Reset(ctx); err != nil || got.InstallID != "" {
		t.Fatalf("reset without consent: %+v %v", got, err)
	}
	first, err := st.Enable(ctx, LevelBasic)
	if err != nil || first.Consent != ConsentYes || len(first.InstallID) != 32 {
		t.Fatalf("enable: %+v %v", first, err)
	}
	again, err := st.Enable(ctx, LevelBasic)
	if err != nil || again.InstallID != first.InstallID {
		t.Fatalf("a second enable must keep the id: %+v %v", again, err)
	}
	reset, err := st.Reset(ctx)
	if err != nil || reset.InstallID == "" || reset.InstallID == first.InstallID || reset.Consent != ConsentYes {
		t.Fatalf("reset must give a different id: %+v %v", reset, err)
	}
	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	off, err := st.Load(ctx)
	if err != nil || off.Consent != ConsentNo || off.InstallID != "" {
		t.Fatalf("disable must clear the id: %+v %v", off, err)
	}
	if got, err := st.Reset(ctx); err != nil || got.InstallID != "" {
		t.Fatalf("reset with consent no: %+v %v", got, err)
	}
	// A new yes after no is a new id.
	back, err := st.Enable(ctx, LevelBasic)
	if err != nil || back.InstallID == "" || back.InstallID == first.InstallID {
		t.Fatalf("enable after disable: %+v %v", back, err)
	}
}

func TestStoreClaim_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	enabled, err := st.Enable(ctx, LevelBasic)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ok, err := st.Claim(ctx, "v1")
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				if id != enabled.InstallID {
					t.Errorf("claim returned %q", id)
				}
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent claims: %d winners, want 1", wins)
	}

	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - interval '21 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
		t.Fatalf("claim after the interval: ok=%v err=%v", ok, err)
	}

	if err := st.RecordResult(ctx, "ok", "v1", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load(ctx)
	if got.LastStatus != "ok" || got.LastSentAt == nil || got.LastOKAt == nil {
		t.Fatalf("result not stored: %+v", got)
	}
	if err := st.RecordResult(ctx, "error: http 404", "v1", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := st.Load(ctx)
	if after.LastStatus != "error: http 404" || after.LastOKAt == nil || !after.LastOKAt.Equal(*got.LastOKAt) {
		t.Fatalf("an error changed the last success time: before %+v after %+v", got, after)
	}

	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx, "v1"); err != nil || ok {
		t.Fatalf("claim with consent no: ok=%v err=%v", ok, err)
	}
}

// rawScratch is scratchPool without any migration, for upgrade tests.
func rawScratch(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return applyMigrations(t, scratchBare(t), "202610280900_install_stats.sql", "202610281000_install_stats_last_ok.sql")
}

func TestMigrationBackfillsLevel_Integration(t *testing.T) {
	ctx := context.Background()
	for consent, want := range map[string]Level{"yes": LevelBasic, "no": LevelOff, "unset": LevelUnset} {
		t.Run(consent, func(t *testing.T) {
			pool := rawScratch(t)
			id := "NULL"
			if consent == "yes" {
				id = "'0123456789abcdef0123456789abcdef'"
			}
			if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_install_stats (id, consent, install_id) VALUES (TRUE, '`+consent+`', `+id+`)`); err != nil {
				t.Fatal(err)
			}
			applyMigrations(t, pool, "202611091000_install_stats_level.sql")
			got, err := NewStore(pool).Load(ctx)
			if err != nil || got.Level != want || string(got.Consent) != consent {
				t.Fatalf("level = %q consent = %q (%v), want %q / %q", got.Level, got.Consent, err, want, consent)
			}
			if consent == "yes" && got.NudgeDismissed {
				t.Fatal("a migrated yes row must still get the Detailed offer")
			}
		})
	}
}

func TestClaimVersionChange_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	set := func(ago, version string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - $1::interval, last_sent_version = $2`, ago, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, ago, last, running string
		want                     bool
	}{
		{"same version, 2h", "2 hours", "v1", "v1", false},
		{"new version, 2h", "2 hours", "v1", "v2", true},
		{"new version, 5 min", "5 minutes", "v1", "v2", false},
		{"same version, 21h", "21 hours", "v1", "v1", true},
	} {
		set(tc.ago, tc.last)
		if _, ok, err := st.Claim(ctx, tc.running); err != nil || ok != tc.want {
			t.Fatalf("%s: ok=%v err=%v, want %v", tc.name, ok, err, tc.want)
		}
	}
}

func TestLastSentVersionOnlyOnOk_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))
	if _, err := st.Enable(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordResult(ctx, "error: network", "v2", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(ctx); got.LastSentVersion != "" {
		t.Fatalf("a failed send stored version %q", got.LastSentVersion)
	}
	if err := st.RecordResult(ctx, "ok", "v2", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(ctx); got.LastSentVersion != "v2" {
		t.Fatalf("version = %q", got.LastSentVersion)
	}
}

func TestHealthSurvivesRestart_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHealth(ctx, Counts{Panics: 2, PluginErrors: map[string]int64{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHealth(ctx, Counts{Panics: 1, HTTP5xx: 4, PluginErrors: map[string]int64{"a": 2, "b": 1}}); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(pool)
	got, err := restarted.Load(ctx)
	if err != nil || got.Counts.Panics != 3 || got.Counts.HTTP5xx != 4 || got.Counts.PluginErrors["a"] != 3 || got.Counts.PluginErrors["b"] != 1 || got.WindowStart == nil {
		t.Fatalf("after restart: %+v %v", got, err)
	}
	if err := restarted.RecordResult(ctx, "error: network", "v1", &got.Counts); err != nil {
		t.Fatal(err)
	}
	if after, _ := restarted.Load(ctx); after.Counts.Panics != 3 {
		t.Fatalf("a failed send cleared the counts: %+v", after.Counts)
	}
	if err := restarted.RecordResult(ctx, "ok", "v1", &got.Counts); err != nil {
		t.Fatal(err)
	}
	if after, _ := restarted.Load(ctx); after.Counts.Panics != 3 || after.Sent.Panics != 3 {
		t.Fatalf("ok must keep the day's counts and record what was sent: %+v sent %+v", after.Counts, after.Sent)
	}
}

func TestSameDayResend_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	// claim lets the next Claim pass, as a version change or a later day would.
	claim := func() State {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - interval '25 hours'`); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		got, _ := st.Load(ctx)
		return got
	}
	add := func(n int64) {
		t.Helper()
		if err := st.AddHealth(ctx, Counts{Panics: n}); err != nil {
			t.Fatal(err)
		}
	}

	add(2)
	first := claim()
	if first.Counts.Panics != 2 {
		t.Fatalf("first send carries %d, want 2", first.Counts.Panics)
	}
	add(1) // lands while the first send runs
	if err := st.RecordResult(ctx, "ok", "v1", &first.Counts); err != nil {
		t.Fatal(err)
	}

	// Same UTC day: the later send carries the day's total, window unchanged.
	second := claim()
	if second.Counts.Panics != 3 || !second.WindowStart.Equal(*first.WindowStart) {
		t.Fatalf("same-day send carries %d (window %v, was %v), want 3", second.Counts.Panics, second.WindowStart, first.WindowStart)
	}
	if err := st.RecordResult(ctx, "ok", "v1", &second.Counts); err != nil {
		t.Fatal(err)
	}
	add(4)

	// The next UTC day starts after the last send of the previous day.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET h_sent_at = now() - interval '25 hours', health_window_start = now() - interval '48 hours'`); err != nil {
		t.Fatal(err)
	}
	lastSend, _ := st.Load(ctx)
	next := claim()
	if next.Counts.Panics != 4 || !next.WindowStart.Equal(*lastSend.DetailedSentAt) {
		t.Fatalf("next-day send carries %d (window %v), want only the 4 added after the last send at %v", next.Counts.Panics, next.WindowStart, lastSend.DetailedSentAt)
	}
	// A failed attempt must not lose or repeat anything on the retry.
	if retry := claim(); retry.Counts.Panics != 4 {
		t.Fatalf("retry carries %d, want 4", retry.Counts.Panics)
	}
}

func TestAddHealthIgnoredUnlessDetailed_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))
	if err := st.AddHealth(ctx, Counts{Panics: 1}); err != nil { // no row yet
		t.Fatal(err)
	}
	if _, err := st.Enable(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHealth(ctx, Counts{Panics: 1}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(ctx); !got.Counts.IsZero() {
		t.Fatalf("basic level stored %+v", got.Counts)
	}
}

func TestBannerKinds_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))
	want := func(k BannerKind) {
		t.Helper()
		if got, err := st.Banner(ctx); err != nil || got != k {
			t.Fatalf("banner = %q (%v), want %q", got, err, k)
		}
	}
	want(BannerUnanswered)
	// An explicit choice of Basic is not nudged towards Detailed.
	if _, err := st.Enable(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	want(BannerNone)
	// A row migrated from consent yes is, once, until dismissed.
	if _, err := st.db.Exec(ctx, `UPDATE hatamoto_install_stats SET nudge_dismissed = FALSE`); err != nil {
		t.Fatal(err)
	}
	want(BannerNudge)
	if err := st.DismissNudge(ctx); err != nil {
		t.Fatal(err)
	}
	want(BannerNone)
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	want(BannerNone)
	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	want(BannerNone)
}

func TestDisableWipesCounters_Integration(t *testing.T) {
	ctx := context.Background()
	st := NewStore(scratchPool(t))
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHealth(ctx, Counts{Panics: 2, PluginErrors: map[string]int64{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load(ctx)
	if !got.Counts.IsZero() || got.WindowStart != nil || got.Level != LevelOff || got.Consent != ConsentNo || got.InstallID != "" {
		t.Fatalf("after disable: %+v", got)
	}
	// Leaving Detailed for Basic zeroes too, and consent stays yes.
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	_ = st.AddHealth(ctx, Counts{Panics: 1})
	basic, _ := st.Enable(ctx, LevelBasic)
	if !basic.Counts.IsZero() || basic.Consent != ConsentYes {
		t.Fatalf("detailed -> basic: %+v", basic)
	}
}

// An older binary writes only consent. Its Enable and Disable statements are
// run as they were, then the new Store must agree with them.
func TestOldBinaryConsentChangeIsReconciled_Integration(t *testing.T) {
	ctx := context.Background()
	const oldEnable = `INSERT INTO hatamoto_install_stats (id, consent, install_id) VALUES (TRUE, 'yes', '0123456789abcdef0123456789abcdef')
		ON CONFLICT (id) DO UPDATE SET consent = 'yes', install_id = COALESCE(hatamoto_install_stats.install_id, EXCLUDED.install_id)`
	const oldDisable = `INSERT INTO hatamoto_install_stats (id, consent, install_id) VALUES (TRUE, 'no', NULL)
		ON CONFLICT (id) DO UPDATE SET consent = 'no', install_id = NULL`

	for _, from := range []Level{LevelOff, LevelUnset} {
		t.Run("enable from "+string(from), func(t *testing.T) {
			pool := scratchPool(t)
			if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_install_stats (id, consent, level) VALUES (TRUE, 'no', $1)`, string(from)); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, oldEnable); err != nil {
				t.Fatal(err)
			}
			st := NewStore(pool)
			if _, ok, err := st.Claim(ctx, "v1"); err != nil || ok {
				t.Fatalf("claim before the level is repaired: ok=%v err=%v", ok, err)
			}
			got, err := st.Load(ctx)
			if err != nil || got.Level != LevelBasic || got.InstallID == "" {
				t.Fatalf("after old Enable: level %q id %q (%v), want basic with an id", got.Level, got.InstallID, err)
			}
			if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
				t.Fatalf("claim after repair: ok=%v err=%v", ok, err)
			}
		})
	}

	t.Run("disable from detailed", func(t *testing.T) {
		pool := scratchPool(t)
		st := NewStore(pool)
		if _, err := st.Enable(ctx, LevelDetailed); err != nil {
			t.Fatal(err)
		}
		if err := st.AddHealth(ctx, Counts{Panics: 2}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, oldDisable); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := st.Claim(ctx, "v1"); err != nil || ok {
			t.Fatalf("claim after old Disable: ok=%v err=%v", ok, err)
		}
		got, err := st.Load(ctx)
		if err != nil || got.Level != LevelOff || got.InstallID != "" || !got.Counts.IsZero() || got.WindowStart != nil {
			t.Fatalf("after old Disable: %+v (%v), want off, no id, no counts", got, err)
		}
		if kind, _ := st.Banner(ctx); kind != BannerNone {
			t.Fatalf("banner = %q, want none", kind)
		}
	})
}

// The window never moves back to a report that was not Detailed: an old
// last_ok_at (Off for months, or a Basic ping) must not reach before the
// moment Detailed was chosen.
func TestRolloverNeverReachesBeforeDetailedChoice_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	// A Basic report long ago, then Off, then Detailed.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_ok_at = now() - interval '200 days', last_sent_at = now() - interval '200 days'`); err != nil {
		t.Fatal(err)
	}
	if err := st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	detailed, err := st.Enable(ctx, LevelDetailed)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddHealth(ctx, Counts{Panics: 2}); err != nil {
		t.Fatal(err)
	}
	// The next claim, on a later UTC day than the old Basic report.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	got, _ := st.Load(ctx)
	if !got.WindowStart.Equal(*detailed.WindowStart) || got.Counts.Panics != 2 {
		t.Fatalf("window %v counts %d, want the Detailed choice at %v and 2", got.WindowStart, got.Counts.Panics, detailed.WindowStart)
	}

	// Basic reports, with Detailed chosen later: a Basic ok leaves no mark.
	if err := st.RecordResult(ctx, "ok", "v1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - interval '25 hours', last_ok_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if again, _ := st.Load(ctx); !again.WindowStart.Equal(*detailed.WindowStart) || again.Counts.Panics != 2 {
		t.Fatalf("persistent Basic fallback moved the window to %v or dropped counts (%d)", again.WindowStart, again.Counts.Panics)
	}
}

// A Basic report followed by the owner choosing Detailed: the first Detailed
// rollover starts from the choice, not from the Basic report.
func TestBasicThenDetailedWindowStartsAtChoice_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_ok_at = now() - interval '30 hours'`); err != nil {
		t.Fatal(err)
	}
	detailed, err := st.Enable(ctx, LevelDetailed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Claim(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(ctx); !got.WindowStart.Equal(*detailed.WindowStart) {
		t.Fatalf("window %v, want the Enable time %v", got.WindowStart, detailed.WindowStart)
	}
}

// DetailedSentToday follows the database's UTC day, and RecordKeptDay marks
// the version reported without touching the counts.
func TestDetailedSentTodayAndKeptDay_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHealth(ctx, Counts{Panics: 3}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if got, _ := st.Load(ctx); got.DetailedSentToday {
		t.Fatal("nothing was sent yet")
	}
	if err := st.RecordResult(ctx, "ok", "v1", &Counts{Panics: 3}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(ctx); !got.DetailedSentToday {
		t.Fatal("a Detailed ok today is not seen")
	}
	if err := st.RecordKeptDay(ctx, "v2"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load(ctx)
	if got.LastStatus != "error: build" || got.LastSentVersion != "v2" || got.Sent.Panics != 3 || got.Counts.Panics != 3 || !got.DetailedSentToday {
		t.Fatalf("after kept day: %+v", got)
	}
}

// A send claimed just before midnight UTC is booked by the receiver on that
// day, so it is stamped with the claim time, not the time the POST finished.
func TestResultIsStampedWithTheClaimTime_Integration(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	st := NewStore(pool)
	if _, err := st.Enable(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Claim(ctx, "v1"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	// Pretend the claim happened on the previous UTC day.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_install_stats SET last_sent_at = date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' - interval '2 seconds'`); err != nil {
		t.Fatal(err)
	}
	claimed, _ := st.Load(ctx)
	if err := st.RecordResult(ctx, "ok", "v1", &Counts{Panics: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load(ctx)
	if !got.LastOKAt.Equal(*claimed.LastSentAt) || !got.DetailedSentAt.Equal(*claimed.LastSentAt) || got.DetailedSentToday {
		t.Fatalf("last ok %v, detailed sent %v (today %v), want the claim time %v", got.LastOKAt, got.DetailedSentAt, got.DetailedSentToday, claimed.LastSentAt)
	}
}
