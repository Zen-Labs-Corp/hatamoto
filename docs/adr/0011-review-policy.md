# ADR 0011: Review policy

Status: accepted (2026-11-22)

## The rules in plain words

1. A human edit can be staged as a changeset and published only after every
   review stage it was given has been approved, in order.
2. Which stages an edit gets is decided by a review policy. A build that
   registers none uses the free default.
3. A policy that fails holds the edit. It is never published on a doubtful
   answer.
4. An edit waits for its reviewers however long it takes. It never expires.
5. A default install behaves as before. Nothing is staged and no setting
   changes until something asks for an edit to be staged.

## Context

[ADR 0004](0004-plugin-changesets.md) made the changeset the unit of review
for plugin runs: staged, previewed, published or discarded, with a 72 hour
limit. Edits made by people have no such step. A site that wants a second pair
of eyes on an edit needs the same staging, but with a person's pace: no
deadline, and an ordered list of stages that can differ from site to site.

The core should not hard-code those lists. A wrapper module that builds its own
binary around `app.Main` (the same way it registers an edition) can supply the
rule.

## Decision

### 1. The open interface

The public package `hatamoto.dev/core/review` has:

- `Edit`: what is about to be staged, with the site's review switch
  (`ReviewEnabled`).
- `Stage`: one ordered step, a `Name` and the `Group` that may decide it.
- `Policy`: `Stages(ctx, Edit) ([]Stage, error)`. No stages means no review.
- `Register(Policy)`: called from an `init` function. It panics when called a
  second time, with a nil policy, or after `app.Main` has frozen the
  registration.
- `StagesFor(ctx, Edit)`: the stages of an edit. An error, a nil result from a
  failing policy, or a stage without a name or group returns `ErrPolicy`, which
  means: hold the edit. A nil result with no error means no review, the same as
  an empty list.

### 2. The free default

With no policy registered: review off gives no stages (an empty list, so the
edit may publish at once); review on gives one stage, "Review", for the group
`reviewers`, which is the Owner, Editor and Reviewer roles.

### 3. The data model

- `hatamoto_changesets` gets `kind` (`run` or `edit`, default `run`),
  `requested_by` and `stages`, a snapshot of the resolved stages taken when the
  edit was staged. Later policy changes do not alter an edit already staged.
- `hatamoto_changeset_stage_decisions` holds one row per stage: reviewer,
  `approved` or `rejected`, comment and time. A stage has one decision.
- Decisions are ordered. A stage can be decided only when all earlier stages
  are approved, and nothing is accepted after a rejection.
- Publish of an edit needs every stage approved. An edit with no stages may
  publish at once.
- An edit has `expires_at` set far in the future, so the column stays NOT NULL
  and every expiry check works unchanged. The hourly sweeper expires only
  changesets past their time, so it never touches an edit. The 72 hour rule of
  ADR 0004 applies to run changesets.
- A person may edit an object of any origin, so staging and publishing an edit
  skip the plugin ownership check. Run changesets keep it.
- Staging into an edit is refused (`ErrReviewStarted`) once any stage decision
  exists, so an approval covers the items that were staged when it was given.
  Run changesets are unchanged.
- Publishing an edit writes origin `owner` for entities. A plugin-owned entity
  therefore becomes owner-owned. The takeover rule, its audit row and the
  reviewer-group check belong to the follow-up that routes edits here.
- Existing rows are plain run changesets with no stages. The previous release
  never reads the new columns or table. A rollback is safe today because
  nothing creates edit changesets. The release that first creates edits must
  raise the rollback floor, since the previous release would not know an
  edit's stages or decisions.

## Not decided here

- The takeover at publish: what happens to an object that changed after the
  edit was staged.
- The reviewer-group check. The engine records a decision for the reviewer it
  is given; that the reviewer belongs to the stage's group is the caller's
  check.
- Any screen or API that stages an edit or records a decision.

## Consequences

- A standard build is unchanged: it registers nothing and stages nothing.
- A broken policy blocks edits instead of letting them through. The fix is in
  the policy, not a way around it.
- An edit that nobody reviews stays staged until someone discards it.

## Amendment: the review step (2026-11-22)

The first caller of the edit changeset is the entity service, behind one gate.

- `review.Edit` gains `RequesterRole` (the requester's role, or a key's creator's role) and `Bypass` (the
  requester holds the new action `entity.review.bypass`). The free default returns no stage when the switch is
  off or `Bypass` is true, else one "Review" stage for the `reviewers` group. A registered policy sees both
  fields and its answer wins; a policy error still holds the change.
- The site's review switch is read from the database on every gated write, never from a cache.
- A person's publish, schedule, restore or edit of published content is staged when the policy returns stages.
  The service then asks `entity.review.request` instead of `entity.update` or `entity.publish`. No subject on
  the context (the scheduler, imports, the CLI without `--as`) is the system and is never held.
- The stage group `reviewers` maps to the action `entity.review.decide` (Owner, Editor, Reviewer). A stage naming
  any other group cannot be decided in core: it fails closed and a holder of that right (Owner, Editor, Reviewer) can discard the change.
- Nobody decides their own change, whatever their role. An owner is exempt by default, so a single-owner site
  never deadlocks.
- An approval publishes through the engine's `Publish`, not `PublishReviewed`: the import risk classifier is
  not meant for human edits, and `Publish` honours the stage gate and the base token. Before deciding, the
  entry's version is compared with the one the edit was based on; a changed entry refuses the approval.
- Send back records a rejection and keeps the staged change for as long as nobody else replaces it. A new send
  from the same person replaces it; they may also withdraw it. One change waits per entry; another person's
  is refused, unless it was sent back: nobody can approve a sent-back change, so a new send by anyone replaces it
  (its decision and comment stay, but the staged content of the replaced edit is deleted, so its requester loses that version; the audit row of the replacing send names them as `superseded_requester`). Any holder of `entity.review.decide` may discard a waiting change, with no
  comment and no mail to the requester.
- Publishing an edit of an entry a plugin owns takes it over and is audited as `entity.take_over`
  (`detail` `{from, changeset}`).
- Blocks and theme parts are always stored as drafts and are not held; they stay behind `builder.block.manage`.
- `review.Edit.SlugChange` is not set by core in this release: the plan is taken before the input is known, so a
  policy's slug rule never sees a change. `Delete` is not set either.
- This is the first release that creates edit changesets, so it raises the rollback floor (see
  [Upgrades](../UPGRADES.md)). A binary from before it ignores the review switch, so review would silently turn
  off and changes would be written directly.
