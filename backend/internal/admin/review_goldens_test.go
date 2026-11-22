package admin

import (
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

// reviewGoldenRig is an owner's admin with review on and one change waiting.
func reviewGoldenRig(t *testing.T) *contentRig {
	t.Helper()
	since := time.Date(2020, 3, 4, 10, 42, 0, 0, time.UTC) // far enough back that the queue shows the date, not "3h ago"
	c := newContentRigWith(t, nil)
	live := c.ents.addEntity(21, c.service, "Pricing", "pricing", entity.StatusPublished, `{"summary":"Three plans"}`, t0)
	payload := *live
	payload.Name = "Pricing and plans"
	payload.Attributes = []byte(`{"summary":"Four plans, billed yearly"}`)
	pe := PendingEdit{ID: 7, EntityID: live.ID.String(), EntityName: payload.Name, Requester: fid(77).String(), RequesterName: "Sam Writer", Since: since, Payload: &payload}
	c.a.deps.Review = &fakeReviewFlow{on: true, queue: []PendingEdit{pe}, details: map[int64]ReviewDetail{7: {PendingEdit: pe, Live: live}}}
	return c
}

func TestReviewSettingsPageGolden(t *testing.T) {
	c := reviewGoldenRig(t)
	compareGolden(t, "pages/settings-review.html", normalizePage(c.get(t, "owner-token", "/admin/settings/review", "")))
}

func TestReviewQueuePageGolden(t *testing.T) {
	c := reviewGoldenRig(t)
	compareGolden(t, "pages/review-queue.html", normalizePage(c.get(t, "owner-token", "/admin/review", "")))
}

func TestReviewDetailPageGolden(t *testing.T) {
	c := reviewGoldenRig(t)
	compareGolden(t, "pages/review-detail.html", normalizePage(c.get(t, "owner-token", "/admin/review/7", "")))
}
