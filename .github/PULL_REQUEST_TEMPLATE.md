## What and why

<!-- One topic per pull request. Link the issue or plan in docs/plans/ if there is one. -->

## Checklist

- [ ] `make check` passes.
- [ ] Tests pin the new behaviour.
- [ ] Docs updated where behaviour changed (`docs/GUIDE.md` then `sh docs/guide/build.sh`, `docs/DESIGN.md`, `README.md`).
- [ ] `sh scripts/notices.sh --check` passes if dependencies, vendored files or borrowed skills changed.
- [ ] No em-dashes, no secrets, no personal or company names.
- [ ] It keeps the one rule: loadout never runs a model and never holds an AI key.
