package entity

import "testing"

// TestScheduledStatusIsValid locks 'scheduled' into the Go-side status set.
// The DB constraint (migration 121) and this function have to agree: if
// ValidStatus rejected it, the admin API would 400 on every attempt to
// schedule a post while the database happily accepted the value.
func TestScheduledStatusIsValid(t *testing.T) {
	if !ValidStatus(StatusScheduled) {
		t.Fatal("StatusScheduled must be a valid status")
	}

	got, err := ParseStatus("scheduled")
	if err != nil {
		t.Fatalf("ParseStatus(\"scheduled\"): unexpected error %v", err)
	}
	if got != StatusScheduled {
		t.Fatalf("ParseStatus(\"scheduled\") = %q, want %q", got, StatusScheduled)
	}
}

// TestStatusSetUnchangedOtherwise guards against a careless edit widening
// the status set further. 'redirected' exists in the DB constraint from
// migration 117 but deliberately has no Go constant — entities reach that
// state through the city-redirect path, not the status API.
func TestStatusSetUnchangedOtherwise(t *testing.T) {
	valid := []EntityStatus{StatusDraft, StatusScheduled, StatusPublished, StatusArchived}
	for _, s := range valid {
		if !ValidStatus(s) {
			t.Fatalf("%q should be valid", s)
		}
	}
	for _, s := range []EntityStatus{"redirected", "pending", "", "PUBLISHED"} {
		if ValidStatus(s) {
			t.Fatalf("%q should not be valid", s)
		}
	}
}
