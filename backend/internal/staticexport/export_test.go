package staticexport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/s3"
	"hatamoto.dev/core/internal/s3/s3test"
)

type fakeRuns struct {
	mu       sync.Mutex
	startErr error
	started  int
	finished []RunResult
}

func (r *fakeRuns) Start(context.Context, string, string) (uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return uuid.Nil, r.startErr
	}
	r.started++
	return uuid.New(), nil
}
func (r *fakeRuns) Heartbeat(context.Context, uuid.UUID) error { return nil }
func (r *fakeRuns) Finish(_ context.Context, _ uuid.UUID, res RunResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, res)
	return nil
}

// fakeTarget stages into a temp dir and records what happens to it.
type fakeTarget struct {
	t          *testing.T
	stageCalls int
	published  bool
	discarded  bool
	pubErr     error
	prev       *Manifest // returned by Previous when set
	gotDiff    Diff
	gotMan     Manifest
}

func (f *fakeTarget) Key() string      { return "fake:out" }
func (f *fakeTarget) Describe() string { return "fake-out" }
func (f *fakeTarget) Stage(context.Context) (string, error) {
	f.stageCalls++
	return f.t.TempDir(), nil
}
func (f *fakeTarget) Previous(context.Context) (Manifest, bool, error) {
	if f.prev != nil {
		return *f.prev, true, nil
	}
	return Manifest{}, false, nil
}
func (f *fakeTarget) Publish(_ context.Context, _ string, d Diff, m Manifest) error {
	f.published = true
	f.gotDiff, f.gotMan = d, m
	return f.pubErr
}
func (f *fakeTarget) Discard(string) { f.discarded = true }

func okSource() *fakeSource {
	f := newFake("/admin", "/")
	f.html("/", `<p>home</p><a href="/about">a</a>`)
	f.html("/about", "<p>about</p>")
	return f
}

func TestExport_RenderErrorKeepsPreviousAndRecordsFailed(t *testing.T) {
	f := okSource()
	f.pages["/about"] = fakeResp{status: 500, ct: "text/html", body: "boom"}
	runs, tgt := &fakeRuns{}, &fakeTarget{t: t}
	_, err := Export(context.Background(), f, runs, tgt, Options{})
	if err == nil || !strings.Contains(err.Error(), "/about: render 500") {
		t.Fatalf("err = %v", err)
	}
	if tgt.published || !tgt.discarded {
		t.Fatalf("published=%v discarded=%v", tgt.published, tgt.discarded)
	}
	if len(runs.finished) != 1 || runs.finished[0].OK || len(runs.finished[0].Errors) == 0 {
		t.Fatalf("finished = %+v", runs.finished)
	}
}

func TestExport_UploadErrorRecordsFailed(t *testing.T) {
	runs, tgt := &fakeRuns{}, &fakeTarget{t: t, pubErr: errors.New("upload index.html: s3 put: 500")}
	_, err := Export(context.Background(), okSource(), runs, tgt, Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !tgt.discarded || len(runs.finished) != 1 || runs.finished[0].OK {
		t.Fatalf("discarded=%v finished=%+v", tgt.discarded, runs.finished)
	}
	if got := runs.finished[0].Errors; len(got) != 1 || !strings.Contains(got[0], "upload index.html") {
		t.Fatalf("errors = %v", got)
	}
}

type refusingSource struct{ *fakeSource }

func (refusingSource) Site(context.Context) (Site, error) { return Site{}, ErrSiteAccessOn }

func TestExport_SiteAccessRefused(t *testing.T) {
	runs, tgt := &fakeRuns{}, &fakeTarget{t: t}
	var out bytes.Buffer
	_, err := Export(context.Background(), refusingSource{okSource()}, runs, tgt, Options{Out: &out})
	if !errors.Is(err, ErrSiteAccessOn) {
		t.Fatalf("err = %v", err)
	}
	if tgt.stageCalls != 0 {
		t.Fatal("Stage ran for a refused export")
	}
	if len(runs.finished) != 1 || runs.finished[0].OK || runs.finished[0].Errors[0] != ErrSiteAccessOn.Error() {
		t.Fatalf("finished = %+v", runs.finished)
	}
	if !strings.Contains(out.String(), "site access gate is on") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestExport_BusyNoRow(t *testing.T) {
	runs, tgt := &fakeRuns{startErr: ErrBusy}, &fakeTarget{t: t}
	_, err := Export(context.Background(), okSource(), runs, tgt, Options{})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(runs.finished) != 0 || tgt.stageCalls != 0 {
		t.Fatalf("finished=%v stage=%d", runs.finished, tgt.stageCalls)
	}
}

func TestExport_ProgressLines(t *testing.T) {
	runs, tgt := &fakeRuns{}, &fakeTarget{t: t}
	var out bytes.Buffer
	res, err := Export(context.Background(), okSource(), runs, tgt, Options{Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if !tgt.published || tgt.discarded {
		t.Fatalf("published=%v discarded=%v", tgt.published, tgt.discarded)
	}
	for _, want := range []string{
		"static: 2 pages, 0 assets, 0 media files  (0.0 MB)",
		"static: changed 3, unchanged 0, removed 0",
		"static: wrote _redirects (0 rules) + 0 redirect stubs, 404.html",
		"static: uploading 3 changed, deleting 0 removed (last) to fake-out",
		"static: done in ",
		", 0 errors",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output misses %q:\n%s", want, out.String())
		}
	}
	if len(runs.finished) != 1 || !runs.finished[0].OK || runs.finished[0].Pages != 2 || len(res.Diff.Changed) != 3 {
		t.Fatalf("finished = %+v diff = %+v", runs.finished, res.Diff)
	}
}

func TestExport_ProgressLinesListingPages(t *testing.T) {
	f := newFake("/admin", "/blog")
	f.html("/blog", `<a href="/blog?page=2">next</a>`)
	f.html("/blog?page=2", "<p>two</p>")
	var out bytes.Buffer
	if _, err := Export(context.Background(), f, &fakeRuns{}, &fakeTarget{t: t}, Options{Out: &out}); err != nil {
		t.Fatal(err)
	}
	if want := "static: 1 pages (+1 listing pages), 0 assets, 0 media files"; !strings.Contains(out.String(), want) {
		t.Errorf("output misses %q:\n%s", want, out.String())
	}
}

func TestExport_DirTargetSwapLine(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	tgt, err := NewDirTarget(out, DirGuard{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Export(context.Background(), okSource(), &fakeRuns{}, tgt, Options{Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "static: swapped "+out+" (previous kept at "+out+".prev)") {
		t.Fatalf("output = %s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestExport_NoSecretInOutput(t *testing.T) {
	srv := s3test.NewServer(t)
	srv.FailPut(func(key string) bool { return key == "about/index.html" })
	c, err := s3.New(s3.Config{Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	runs := &fakeRuns{}
	var out bytes.Buffer
	_, err = Export(context.Background(), okSource(), runs, NewS3Target(c, srv.URL, s3test.Bucket, ""), Options{Out: &out})
	if err == nil {
		t.Fatal("expected an upload error")
	}
	all := out.String() + err.Error() + strings.Join(runs.finished[0].Errors, "\n")
	if strings.Contains(all, s3test.SecretKey) {
		t.Fatal("the secret key leaked into output, error or run record")
	}
	for _, r := range srv.Requests() {
		if r.Method == "DELETE" {
			t.Fatal("unexpected DELETE after a failed upload")
		}
	}
}

func TestExport_FullRepublishesEverything(t *testing.T) {
	first := &fakeTarget{t: t}
	if _, err := Export(context.Background(), okSource(), &fakeRuns{}, first, Options{}); err != nil {
		t.Fatal(err)
	}
	prev := first.gotMan
	prev.Files = map[string]Entry{}
	for p, e := range first.gotMan.Files {
		prev.Files[p] = e
	}
	prev.Files["gone.html"] = Entry{SHA256: "x", Size: 1}

	incr := &fakeTarget{t: t, prev: &prev}
	if _, err := Export(context.Background(), okSource(), &fakeRuns{}, incr, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(incr.gotDiff.Changed) != 0 || len(incr.gotDiff.Unchanged) == 0 {
		t.Fatalf("incremental diff = %+v", incr.gotDiff)
	}

	full := &fakeTarget{t: t, prev: &prev}
	var out bytes.Buffer
	if _, err := Export(context.Background(), okSource(), &fakeRuns{}, full, Options{Full: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	d := full.gotDiff
	if len(d.Unchanged) != 0 || len(d.Changed) != len(first.gotMan.Files) {
		t.Fatalf("full diff = %+v, want %d changed", d, len(first.gotMan.Files))
	}
	if !sort.StringsAreSorted(d.Changed) {
		t.Fatalf("changed not sorted: %v", d.Changed)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "gone.html" {
		t.Fatalf("removed = %v", d.Removed)
	}
	if !strings.Contains(out.String(), "full rebuild") {
		t.Fatalf("output = %s", out.String())
	}
}
