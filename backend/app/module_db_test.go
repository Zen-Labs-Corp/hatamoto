package app

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
	moddb "hatamoto.dev/core/module/db"
)

// lazyPool returns a pool that never dials.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), "postgres://x@127.0.0.1:1/x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestPublishModulePool(t *testing.T) {
	t.Cleanup(modhost.Reset)
	p := lazyPool(t)
	clear := publishModulePool(p)
	if got, err := moddb.Pool(); got != p || err != nil {
		t.Fatalf("Pool() = %v, %v", got, err)
	}
	clear()
	if _, err := moddb.Pool(); !errors.Is(err, moddb.ErrNotReady) {
		t.Fatalf("after clear: err = %v", err)
	}
}

func TestRunModulePublishesPool(t *testing.T) {
	t.Cleanup(modhost.Reset)
	var clearedBeforeClose atomic.Bool
	old := openModuleDB
	t.Cleanup(func() { openModuleDB = old })
	pool := lazyPool(t)
	openModuleDB = func(context.Context) (*pgxpool.Pool, func(), error) {
		return pool, func() {
			_, err := moddb.Pool()
			clearedBeforeClose.Store(errors.Is(err, moddb.ErrNotReady))
		}, nil
	}
	modhost.RegisterCommand(modhost.CLICommand{Name: "pool-check", Run: func(_ context.Context, env *modhost.CLIEnv, _ []string) error {
		p, err := moddb.Pool()
		if err != nil || p != env.DB {
			return errors.New("pool mismatch")
		}
		return nil
	}})
	if err := runModule(context.Background(), []string{"pool-check"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := moddb.Pool(); !errors.Is(err, moddb.ErrNotReady) {
		t.Fatalf("after runModule: err = %v", err)
	}
	if !clearedBeforeClose.Load() {
		t.Fatal("pool still published when the database closed")
	}
}

func TestModuleServiceSeesPool(t *testing.T) {
	t.Cleanup(modhost.Reset)
	p := lazyPool(t)
	clear := publishModulePool(p)
	started := make(chan struct{})
	var seen atomic.Bool
	modhost.RegisterService(modhost.Service{Name: "pool_user", Run: func(ctx context.Context) error {
		got, err := moddb.Pool()
		seen.Store(got == p && err == nil)
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	wait := startModuleServices(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("service did not start")
	}
	cancel()
	wait(5 * time.Second)
	clear()
	if !seen.Load() {
		t.Fatal("service did not see the published pool")
	}
}

// TestServePublishesPoolInOrder pins the defer order in serve: the clear runs
// after services stop and before db.Close, so it is registered after
// `defer db.Close()` and before the services and the server start.
func TestServePublishesPoolInOrder(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// calls reports whether n contains a call to name (pkg.Name when pkg != "").
	calls := func(n ast.Node, pkg, name string) bool {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return !found
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				found = found || (pkg == "" && fun.Name == name)
			case *ast.SelectorExpr:
				x, ok := fun.X.(*ast.Ident)
				found = found || (ok && x.Name == pkg && fun.Sel.Name == name)
			}
			return !found
		})
		return found
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "serve" {
			continue
		}
		closeIdx, publishIdx, servicesIdx, serverIdx := -1, -1, -1, -1
		for i, st := range fn.Body.List {
			if ds, ok := st.(*ast.DeferStmt); ok {
				if sel, ok := ds.Call.Fun.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "db" && sel.Sel.Name == "Close" {
						closeIdx = i
					}
				}
				if inner, ok := ds.Call.Fun.(*ast.CallExpr); ok {
					if id, ok := inner.Fun.(*ast.Ident); ok && id.Name == "publishModulePool" {
						publishIdx = i
					}
				}
			}
			if servicesIdx < 0 && calls(st, "", "startModuleServices") {
				servicesIdx = i
			}
			if serverIdx < 0 && calls(st, "server", "New") {
				serverIdx = i
			}
		}
		if closeIdx < 0 || publishIdx < 0 || servicesIdx < 0 || serverIdx < 0 {
			t.Fatalf("statements not found: close=%d publish=%d services=%d server=%d", closeIdx, publishIdx, servicesIdx, serverIdx)
		}
		if !(closeIdx < publishIdx && publishIdx < servicesIdx && publishIdx < serverIdx) {
			t.Fatalf("serve order wrong (close=%d publish=%d services=%d server=%d): the pool must be cleared after services stop and before db.Close, so defer publishModulePool after db.Close and before the services and server start",
				closeIdx, publishIdx, servicesIdx, serverIdx)
		}
		return
	}
	t.Fatal("serve not found in app.go")
}
