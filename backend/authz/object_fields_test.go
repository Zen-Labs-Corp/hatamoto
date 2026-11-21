package authz_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/authz"
)

type objectRecorder struct{ got authz.Object }

func (r *objectRecorder) Can(_ context.Context, _ authz.Subject, _ authz.Action, o authz.Object) (bool, error) {
	r.got = o
	return true, nil
}

func TestObjectFieldsReachRegisteredAuthorizer(t *testing.T) {
	authz.ResetForTest()
	t.Cleanup(authz.ResetForTest)
	rec := &objectRecorder{}
	authz.Register(rec)

	want := authz.Object{Kind: "entity", ID: "e1", CreatedBy: "u1", Status: "draft"}
	if _, err := authz.Can(context.Background(), authz.Subject{UserID: "u2", Role: "author"}, authz.ActionEntityUpdate, want); err != nil {
		t.Fatal(err)
	}
	if rec.got != want {
		t.Fatalf("object = %+v, want %+v", rec.got, want)
	}
}
