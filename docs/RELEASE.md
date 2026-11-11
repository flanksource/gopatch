# Releases

Successful CI on `main` invokes the reusable release workflow. Conventional Commit `fix:` and `perf:` commits trigger patch releases, `feat:` triggers minor releases, and breaking changes trigger major releases. Commits without a release-worthy change do not publish.

The pipeline uses Flanksource action-workflows v1.3.0, pinned by commit, to create a draft through semantic-release, publish signed multi-platform images to GHCR, and publish the draft after all artifacts and smoke checks succeed. It uses the repository GITHUB_TOKEN; GitHub Actions must be allowed to write contents and packages. Fork pull requests only run checks.

GoReleaser v2.12.7 attaches Linux, macOS, and Windows amd64/arm64 archives and checksums to the draft, retaining semantic-release notes. Linux/macOS archives use tar.gz and Windows uses ZIP. Docker publishes `vX.Y.Z`, `X.Y.Z`, and `latest` tags for Linux amd64/arm64. Hack modules are checked but not published.

Versioning creates no release commit. Both the release tag and the shared Docker workflow's event checkout therefore refer to the CI-verified source revision. Keep this invariant if adding semantic-release plugins.

Local verification (no publication):

```bash
GOWORK=off make lint lint-hack build build-hack cover test-hack
actionlint .github/workflows/go.yml .github/workflows/release.yml
GOWORK=off go run github.com/goreleaser/goreleaser/v2@v2.12.7 check
GOWORK=off go run github.com/goreleaser/goreleaser/v2@v2.12.7 release --snapshot --clean --skip=publish
docker build --build-arg VERSION=ci --build-arg GO_VERSION=1.27.4 -t gopatch:ci .
bash .github/scripts/smoke-image.sh gopatch:ci ci
```

The Docker Go version must match `go.mod`; CI derives it through setup-go. The smoke check verifies version/help and previews, applies, and compiles a typed move in a disposable module. Snapshot archives are written under `.tmp/dist`.

If packaging fails, the release remains a draft; inspect the failed job and rerun it from the original workflow run after resolving the cause. Do not move existing release tags. A full rerun may report no new semantic release, so rerun failed jobs rather than creating another version.
