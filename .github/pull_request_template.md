## What and why

<!-- What does this change, and why? Link the issue it fixes (Fixes #123). -->

## How it was tested

<!-- Commands you ran and what they printed (go test, make check, manual steps). -->

## Checklist

- [ ] Every commit is signed off (`git commit -s`): I agree to the [Developer Certificate of Origin](https://developercertificate.org/) (see CONTRIBUTING.md).
- [ ] `cd backend && go test ./...` and `make check` pass.
- [ ] Tests cover the change (a bug fix starts with a failing test).
- [ ] Admin changes follow docs/ADMIN_UI_RULES.md (`go test ./internal/admin -run UILint`).
- [ ] No migration that has already shipped was edited.
- [ ] Docs updated where behaviour changed.
