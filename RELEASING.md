# Releasing

Releases, SDK pinning, partner (white-label) builds. Day-to-day work: [CONTRIBUTING.md](CONTRIBUTING.md).

## Two axes

Every release = one point on two independent axes.

- **Brand** = identity. One line in `brands.yml`. `gpupaas` = official. Other lines = partners. Brand sets type name, env
  prefix, docs. Brand never has own code, branch or tag.
- **Controller line** = which controller generation the provider talks to. One line = one branch `release/vX.Y`, SDK and
  provider tags `vX.Y.Z`.

| Rule | Why |
|---|---|
| Branches = controller generations: `main` (newest), `release/vX.Y` (frozen). Never `release/<brand>`. | Branch per partner diverges day one; every fix needs N backports. |
| SDK tag `vX.Y.Z` binds to controller line `vX.Y`. `gpupaas.ControllerAPI` is `"vX.Y"`. | SDK is source of truth for what controller speaks. |
| Provider `vX.Y.Z` pins an SDK tag of the same line. | One provider, one SDK, one controller line. |
| Partner binary never builds with local `replace` or pseudo-version. | Release must rebuild from tags alone. |
| Patch fixes land on their own line (cherry-pick onto `release/vX.Y`), never from `main` onto an older line. | Patch never changes the controller it targets. |
| README table "Controller compatibility" has a row per released line. | Partners pick provider by controller. |

Supported: controller line N with provider line N. Upgrade = upgrade controller, then switch to the provider of the new
line. Not supported: older controller with newer provider (sends kinds the controller rejects); newer controller with stale
provider (new resources unreachable). Controller has no version endpoint: nothing checks this at runtime. Table + gate are the contract.

## Release the SDK (`gpupaas-go`)

1. Controller generation freezes: `git switch -c release/v0.2 <commit>`.
2. Set `Version` and `ControllerAPI` in `version.go` (`go test .` fails if they disagree). Add the README table row.
3. Gate, then tag: `gofmt -l . && go vet ./... && go build ./... && go test ./...`, then
   `git tag v0.2.0 && git push origin release/v0.2 v0.2.0`.
4. Defect on an older line: cherry-pick onto its `release/vX.Y`, tag next patch (`v0.2.1`).

Tags must be real `vX.Y.Z`. `go get` rejects aliases.

## Release the provider

On the branch of the line (`main` or `release/vX.Y`):

1. **Pin SDK, drop local replace:** `make pin-sdk SDK_VERSION=v0.2.1` (`go mod edit -dropreplace`, `go get`, `go mod tidy`).
2. **Docs + README:** `make docs-check`; add the README table row for the line.
3. **Commit** the pin and the README row (`go.mod`, `go.sum`, `README.md`). The gate needs a clean tree.
4. **Gate:** `make check RELEASE_VERSION=0.2.1`. Fails, listing every problem, unless: SDK pinned to an immutable tag (no
   pseudo-version, no `replace`); provider and SDK share the line; branch is `main` or that line's `release/vX.Y`; tree is
   clean; README has the row.
5. **Tag** `v0.2.1` on that commit, push.
6. **Build every brand:** `make release RELEASE_VERSION=0.2.1`. Runs the gate again, builds one binary per platform, and per
   brand renders examples, generates docs (leak check for official names, page per type), packages `dist/<type>/`: zips,
   `SHA256SUMS`, manifest, `docs/`, `examples/`, `PUBLISHING.md`, `BUILD-INFO.json` (commit, branch, SDK tag, controller line).
7. **Sign + publish** per brand as `dist/<type>/PUBLISHING.md` says. Brand signs `SHA256SUMS` with its own GPG key (Rafay
   never holds it), publishes zips, checksums, signature, manifest as a release of its own repo. Docs and examples go in that repo too.

`brandctl release -allow-local` skips the gate for own testing (local `replace`, uncommitted changes). Output is marked
`"local": true` in `BUILD-INFO.json`. Never give it to a partner.

| Situation | Action |
|---|---|
| Official release, newest line | pin new SDK tag, release from `main` or `release/vX.Y`, build all brands |
| Partner stays on older controller | stay on its `release/vX.Y`; rebuild only brands on that line |
| Defect in older line | fix on `release/vX.Y`, tag SDK `vX.Y.Z+1`, pin, tag provider, rebuild that line's brands |
| Partner upgrades controller | partner switches to the provider of the new line; no brand branch involved |

## Not automated yet

No GoReleaser config, no tag-triggered CI. `brandctl release` runs by hand. Signing is manual. Docs/assets sync into brand
repos is manual. Controller line is not checked at runtime.
