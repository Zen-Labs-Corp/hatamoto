# Governance

Hatamoto is developed in the open and stewarded by a company, Zen Labs
Corporation. This page says who decides what, how a contributor becomes a
maintainer, and what the project commits to about its licence.

## The steward

Zen Labs Corporation is the steward of the project. It owns the Hatamoto name
and logo (see [`TRADEMARKS.md`](TRADEMARKS.md)), runs the release process and
the signing keys, and employs or appoints the maintainers. The steward is
accountable for the project's direction and for keeping the commitments on this
page. This is a company-led project, not a foundation: if you need a
vendor-neutral governance body, this is not that, and we say so up front.

The people who currently hold these roles are in
[`MAINTAINERS.md`](MAINTAINERS.md).

## How decisions are made

- **Day to day** (bug fixes, small features, docs): any maintainer can approve
  and merge a pull request after review. Reviews are public, on the pull request.
- **Larger changes** (new subsystems, changes to the plugin API, the database
  schema or the update and signing process): open an issue or a discussion
  first. The maintainers aim to agree by consensus in the open. When they do
  not, the steward decides and writes the reason down in the issue.
- **Compatibility.** The plugin SDK and the update process have rules that exist
  to protect people running Hatamoto (see [`STABILITY.md`](STABILITY.md), `docs/PLUGIN_API.md` and
  `docs/site/updates/upgrade-rules-and-support.md`). Changes that break them
  need an explicit decision from the steward.
- **Security** problems are handled privately, by the process in
  [`SECURITY.md`](SECURITY.md).
- **Conduct** problems are handled by the process in
  [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## Becoming a maintainer

There is no quota and no waiting period. A contributor who has had several
non-trivial changes merged, reviews other people's work well, and follows the
project rules (the checks, the neutrality rule, the security process) can be
nominated by any maintainer. The steward confirms the nomination, the person is
added to `MAINTAINERS.md` and to `.github/CODEOWNERS`, and gets the access the
role needs. A maintainer who has been inactive for a long time, or who does not
follow the code of conduct, can be moved to emeritus by the steward; this is
recorded in `MAINTAINERS.md`.

Maintainers do not get access to release signing keys by default. Those are
held by the steward's release manager (see
[Signing keys](docs/site/updates/keys.md)).

## Licence commitment

Hatamoto is licensed under the [Apache License 2.0](LICENSE). Contributions are
accepted under the same licence with a Developer Certificate of Origin sign-off
(see [`CONTRIBUTING.md`](CONTRIBUTING.md)). There is **no contributor licence
agreement** and no copyright assignment: contributors keep the copyright in
their work, and Zen Labs Corporation receives it under Apache-2.0 on exactly the
same terms as everyone else, with no extra rights. **We will not relicense the
project or its contributions:** Hatamoto stays Apache-2.0, and code released
under Apache-2.0 stays available under it for everyone, whatever happens to the
steward or the project.

## Commercial plans

- Anyone, including companies that compete with the steward, may host Hatamoto
  and sell that service. The licence allows it and the project does not
  restrict it. The one condition is the trademark policy: describe your service
  accurately and do not imply that it is official.
- The steward may offer commercial plans, such as paid hosting. They run the
  same open-source code, under the same licence terms, and do not relicense
  anything (see above).

## Changing this page

Changes to this page go through a pull request, like any other change, and
need the steward's approval.
