# Homebrew Release Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automate cross-platform binary releases for Mockwave and keep a Homebrew tap (`lfdubiela/homebrew-mockwave`) in sync whenever a version tag is pushed.

**Architecture:** Three GitHub Actions jobs run in sequence on tag push — `create-release` creates the GitHub Release, `build` (5-platform matrix) compiles binaries and uploads them with SHA256 sidecars, and `update-tap` downloads the SHAs and opens a PR on the `homebrew-mockwave` repo updating the formula. The Homebrew formula installs the pre-built binary directly (no source compilation).

**Tech Stack:** GitHub Actions, Go cross-compilation (`CGO_ENABLED=0`), `gh` CLI (pre-installed on Actions runners), Ruby/Homebrew formula DSL, HOMEBREW_TAP_TOKEN secret (PAT with `repo` scope).

---

## File Map

| Repo | Action | Path | Responsibility |
|------|--------|------|----------------|
| `mockwave` | Modify | `cmd/mockwave/main.go` | Make version injectable via `-ldflags` |
| `mockwave` | Modify | `Makefile` | Add `release-local` target |
| `mockwave` | Create | `.github/workflows/release.yml` | Full CI release pipeline |
| `homebrew-mockwave` | Create | `Formula/mockwave.rb` | Homebrew formula (initial stub) |
| `homebrew-mockwave` | Create | `README.md` | Installation instructions |

---

## Task 1: Make version injectable via ldflags

**Files:**
- Modify: `cmd/mockwave/main.go`

The `versionCmd()` currently hardcodes `"mockwave v0.1.0"`. The Homebrew `test do` block runs `mockwave version` and asserts the output contains the formula's version string. This requires the binary to embed the version at build time via `-ldflags`.

- [ ] **Step 1: Add package-level version variable**

Open `cmd/mockwave/main.go`. Find the `versionCmd()` function and the file's `package main` declaration. Add a `version` variable at package level and update `versionCmd` to use it.

Find this function:
```go
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("mockwave v0.1.0") },
	}
}
```

Replace with:
```go
// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("mockwave " + version) },
	}
}
```

- [ ] **Step 2: Verify default build still works**

```bash
cd /Users/dub/projects/mockwave
go build -o /tmp/mockwave-test ./cmd/mockwave/
/tmp/mockwave-test version
```

Expected output: `mockwave dev`

- [ ] **Step 3: Verify ldflags injection works**

```bash
go build -ldflags="-X main.version=v0.1.0" -o /tmp/mockwave-test ./cmd/mockwave/
/tmp/mockwave-test version
```

Expected output: `mockwave v0.1.0`

- [ ] **Step 4: Run tests to confirm no regressions**

```bash
go test ./... -timeout 60s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/dub/projects/mockwave
git add cmd/mockwave/main.go
git commit -m "feat: make version injectable via ldflags for release builds"
```

---

## Task 2: Add release-local Makefile target

**Files:**
- Modify: `Makefile`

Adds a `release-local` target for testing cross-platform builds locally before pushing a tag.

- [ ] **Step 1: Add release-local target**

Open `Makefile`. Append after the existing `lint` target:

```makefile
.PHONY: release-local

# Build all release targets locally into dist/ (mirrors what CI does)
release-local:
	mkdir -p dist
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-darwin-amd64  ./cmd/mockwave/
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-darwin-arm64  ./cmd/mockwave/
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-linux-amd64   ./cmd/mockwave/
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-linux-arm64   ./cmd/mockwave/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(shell git describe --tags --always)" -o dist/mockwave-windows-amd64.exe ./cmd/mockwave/
	shasum -a 256 dist/* > dist/checksums.txt
	@echo "✓ All binaries built in dist/"
	@cat dist/checksums.txt
```

- [ ] **Step 2: Add dist/ to .gitignore**

```bash
echo "dist/" >> /Users/dub/projects/mockwave/.gitignore
```

- [ ] **Step 3: Verify release-local runs**

```bash
cd /Users/dub/projects/mockwave
make release-local
ls -lh dist/
```

Expected: 5 binaries + `checksums.txt` in `dist/`. The darwin-arm64 binary should be ~10-15 MB.

- [ ] **Step 4: Commit**

```bash
cd /Users/dub/projects/mockwave
git add Makefile .gitignore
git commit -m "build: add release-local Makefile target for cross-platform builds"
```

---

## Task 3: Create GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

Three jobs: `create-release` → `build` (matrix, 5 platforms) → `update-tap`.

- [ ] **Step 1: Create workflows directory**

```bash
mkdir -p /Users/dub/projects/mockwave/.github/workflows
```

- [ ] **Step 2: Create release.yml**

Create `/Users/dub/projects/mockwave/.github/workflows/release.yml` with:

```yaml
name: Release

on:
  push:
    tags:
      - 'v*.*.*'

jobs:
  # ── Job 1: Create the GitHub Release ──────────────────────────────────────
  create-release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - name: Create GitHub Release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh release create "${{ github.ref_name }}" \
            --repo "${{ github.repository }}" \
            --title "Mockwave ${{ github.ref_name }}" \
            --generate-notes

  # ── Job 2: Build binaries (matrix) ────────────────────────────────────────
  build:
    needs: create-release
    runs-on: ubuntu-latest
    permissions:
      contents: write
    strategy:
      matrix:
        include:
          - goos: darwin
            goarch: amd64
            name: mockwave-darwin-amd64
          - goos: darwin
            goarch: arm64
            name: mockwave-darwin-arm64
          - goos: linux
            goarch: amd64
            name: mockwave-linux-amd64
          - goos: linux
            goarch: arm64
            name: mockwave-linux-arm64
          - goos: windows
            goarch: amd64
            name: mockwave-windows-amd64.exe
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build binary
        env:
          CGO_ENABLED: "0"
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: |
          go build \
            -ldflags="-s -w -X main.version=${{ github.ref_name }}" \
            -o "${{ matrix.name }}" \
            ./cmd/mockwave/

      - name: Compute SHA256
        run: |
          sha256sum "${{ matrix.name }}" | awk '{print $1}' > "${{ matrix.name }}.sha256"
          echo "SHA256: $(cat ${{ matrix.name }}.sha256)"

      - name: Upload binary and SHA256 to Release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh release upload "${{ github.ref_name }}" \
            "${{ matrix.name }}" \
            "${{ matrix.name }}.sha256" \
            --repo "${{ github.repository }}"

  # ── Job 3: Update Homebrew tap ─────────────────────────────────────────────
  update-tap:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - name: Download SHA256 sidecar files
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh release download "${{ github.ref_name }}" \
            --repo "${{ github.repository }}" \
            --pattern "*.sha256"
          ls -la *.sha256

      - name: Read SHA256 values
        id: sha
        run: |
          echo "darwin_amd64=$(cat mockwave-darwin-amd64.sha256)" >> $GITHUB_OUTPUT
          echo "darwin_arm64=$(cat mockwave-darwin-arm64.sha256)" >> $GITHUB_OUTPUT
          echo "linux_amd64=$(cat mockwave-linux-amd64.sha256)"   >> $GITHUB_OUTPUT
          echo "linux_arm64=$(cat mockwave-linux-arm64.sha256)"   >> $GITHUB_OUTPUT

      - name: Checkout homebrew-mockwave tap
        uses: actions/checkout@v4
        with:
          repository: lfdubiela/homebrew-mockwave
          token: ${{ secrets.HOMEBREW_TAP_TOKEN }}
          path: homebrew-mockwave

      - name: Render updated formula
        run: |
          VERSION="${{ github.ref_name }}"
          SEMVER="${VERSION#v}"

          cat > homebrew-mockwave/Formula/mockwave.rb << FORMULA
          class Mockwave < Formula
            desc "Open-source multi-protocol mock server (HTTP, GraphQL, SOAP, gRPC)"
            homepage "https://github.com/lfdubiela/mockwave"
            version "${SEMVER}"
            license "MIT"

            on_macos do
              if Hardware::CPU.arm?
                url "https://github.com/lfdubiela/mockwave/releases/download/${VERSION}/mockwave-darwin-arm64"
                sha256 "${{ steps.sha.outputs.darwin_arm64 }}"
              else
                url "https://github.com/lfdubiela/mockwave/releases/download/${VERSION}/mockwave-darwin-amd64"
                sha256 "${{ steps.sha.outputs.darwin_amd64 }}"
              end
            end

            on_linux do
              if Hardware::CPU.arm?
                url "https://github.com/lfdubiela/mockwave/releases/download/${VERSION}/mockwave-linux-arm64"
                sha256 "${{ steps.sha.outputs.linux_arm64 }}"
              else
                url "https://github.com/lfdubiela/mockwave/releases/download/${VERSION}/mockwave-linux-amd64"
                sha256 "${{ steps.sha.outputs.linux_amd64 }}"
              end
            end

            def install
              bin.install Dir["mockwave*"].reject { |f| f.end_with?(".sha256") }.first => "mockwave"
            end

            test do
              assert_match version.to_s, shell_output("#{bin}/mockwave version")
            end
          end
          FORMULA

      - name: Commit and open PR
        env:
          GH_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
        run: |
          VERSION="${{ github.ref_name }}"
          BRANCH="update-${VERSION}"

          cd homebrew-mockwave

          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"

          git checkout -b "${BRANCH}"
          git add Formula/mockwave.rb
          git commit -m "chore: update formula to ${VERSION}"
          git push origin "${BRANCH}"

          gh pr create \
            --repo lfdubiela/homebrew-mockwave \
            --title "Update formula to ${VERSION}" \
            --body "Automated update triggered by Mockwave ${VERSION} release." \
            --base main \
            --head "${BRANCH}"
```

- [ ] **Step 3: Verify YAML is valid**

```bash
cd /Users/dub/projects/mockwave
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml'))" && echo "YAML OK"
```

Expected: `YAML OK`

- [ ] **Step 4: Commit**

```bash
cd /Users/dub/projects/mockwave
git add .github/workflows/release.yml
git commit -m "ci: add cross-platform release workflow with homebrew tap auto-update"
```

- [ ] **Step 5: Push to remote**

```bash
git push origin main
```

---

## Task 4: Create homebrew-mockwave GitHub repo

**Context:** This task creates a new GitHub repository `lfdubiela/homebrew-mockwave` with an initial formula stub. The formula will be automatically updated by the release workflow when a tag is pushed. Requires `gh` CLI authenticated.

- [ ] **Step 1: Create the GitHub repo**

```bash
gh repo create lfdubiela/homebrew-mockwave \
  --public \
  --description "Homebrew tap for Mockwave — multi-protocol mock server" \
  --clone
cd homebrew-mockwave
```

- [ ] **Step 2: Create Formula directory and stub formula**

Create `Formula/mockwave.rb`:

```bash
mkdir -p Formula
```

Create `Formula/mockwave.rb` with this content (stub for v0.1.0 — will be replaced by CI on next tag push):

```ruby
class Mockwave < Formula
  desc "Open-source multi-protocol mock server (HTTP, GraphQL, SOAP, gRPC)"
  homepage "https://github.com/lfdubiela/mockwave"
  version "0.1.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/lfdubiela/mockwave/releases/download/v0.1.0/mockwave-darwin-arm64"
      sha256 "PLACEHOLDER_DARWIN_ARM64"
    else
      url "https://github.com/lfdubiela/mockwave/releases/download/v0.1.0/mockwave-darwin-amd64"
      sha256 "PLACEHOLDER_DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/lfdubiela/mockwave/releases/download/v0.1.0/mockwave-linux-arm64"
      sha256 "PLACEHOLDER_LINUX_ARM64"
    else
      url "https://github.com/lfdubiela/mockwave/releases/download/v0.1.0/mockwave-linux-amd64"
      sha256 "PLACEHOLDER_LINUX_AMD64"
    end
  end

  def install
    bin.install Dir["mockwave*"].reject { |f| f.end_with?(".sha256") }.first => "mockwave"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/mockwave version")
  end
end
```

- [ ] **Step 3: Create README.md**

Create `README.md`:

```markdown
# homebrew-mockwave

Homebrew tap for [Mockwave](https://github.com/lfdubiela/mockwave) — an open-source, multi-protocol mock server.

## Install

```bash
brew tap lfdubiela/mockwave
brew install mockwave
```

## Upgrade

```bash
brew upgrade mockwave
```

## Uninstall

```bash
brew uninstall mockwave
brew untap lfdubiela/mockwave
```

## Supported platforms

| Platform | Architecture |
|----------|-------------|
| macOS | arm64 (Apple Silicon) |
| macOS | amd64 (Intel) |
| Linux | amd64 |
| Linux | arm64 |

> Windows users: download the binary directly from [GitHub Releases](https://github.com/lfdubiela/mockwave/releases).
```

- [ ] **Step 4: Initial commit and push**

```bash
git add Formula/mockwave.rb README.md
git commit -m "chore: initial homebrew tap for mockwave"
git push origin main
```

- [ ] **Step 5: Set main as default branch (if not already)**

```bash
gh repo edit lfdubiela/homebrew-mockwave --default-branch main
```

- [ ] **Step 6: Verify tap is discoverable**

```bash
brew tap lfdubiela/mockwave https://github.com/lfdubiela/homebrew-mockwave
brew info mockwave
```

Expected: formula info shown (stub version 0.1.0, placeholder SHAs). The formula is not yet installable until real binaries exist — that's OK.

```bash
brew untap lfdubiela/mockwave  # clean up for now
```

---

## Task 5: Set HOMEBREW_TAP_TOKEN secret and smoke-test the pipeline

**Context:** Manual steps to wire up the PAT and trigger a test release. This task cannot be automated — it requires browser + GitHub UI.

- [ ] **Step 1: Create Personal Access Token**

1. Go to https://github.com/settings/tokens/new
2. Token name: `MOCKWAVE_HOMEBREW_TAP`
3. Expiration: 1 year (or No expiration)
4. Scopes: check `repo` (full control of private repositories — needed to push branches and open PRs on `homebrew-mockwave`)
5. Click "Generate token"
6. Copy the token (shown only once)

- [ ] **Step 2: Add secret to mockwave repo**

```bash
gh secret set HOMEBREW_TAP_TOKEN \
  --repo lfdubiela/mockwave \
  --body "<paste-token-here>"
```

Or via GitHub UI: https://github.com/lfdubiela/mockwave/settings/secrets/actions → New repository secret → Name: `HOMEBREW_TAP_TOKEN`

- [ ] **Step 3: Push a test release tag**

```bash
cd /Users/dub/projects/mockwave

# Delete old v0.1.0 tag locally and remotely (it had no binaries)
git tag -d v0.1.0
git push origin :refs/tags/v0.1.0

# Recreate and push
git tag v0.1.0
git push origin v0.1.0
```

> ⚠️ This will trigger the `release.yml` workflow. The old GitHub Release for v0.1.0 (if it existed) will need to be deleted first via `gh release delete v0.1.0 --yes` before the workflow can create it.

- [ ] **Step 4: Watch the workflow**

```bash
gh run list --repo lfdubiela/mockwave --workflow release.yml
gh run watch --repo lfdubiela/mockwave
```

Expected sequence:
1. `create-release` job: PASS (~10s)
2. `build` jobs (5 parallel): PASS (~2-3 min each)
3. `update-tap` job: PASS (~30s)

- [ ] **Step 5: Verify GitHub Release has binaries**

```bash
gh release view v0.1.0 --repo lfdubiela/mockwave
```

Expected: 5 binaries + 5 `.sha256` files listed as assets.

- [ ] **Step 6: Verify PR opened on homebrew-mockwave**

```bash
gh pr list --repo lfdubiela/homebrew-mockwave
```

Expected: PR titled "Update formula to v0.1.0" from branch `update-v0.1.0`.

- [ ] **Step 7: Review and merge the PR**

```bash
gh pr view 1 --repo lfdubiela/homebrew-mockwave
gh pr merge 1 --repo lfdubiela/homebrew-mockwave --merge
```

Or merge via GitHub UI.

- [ ] **Step 8: Test actual installation**

```bash
brew tap lfdubiela/mockwave
brew install mockwave
mockwave version
```

Expected: `mockwave v0.1.0`

```bash
brew test mockwave
```

Expected: PASS (the `test do` block runs `mockwave version` and checks it contains `0.1.0`).

---

## Self-Review

**Spec coverage:**
- ✅ Build matrix 5 platforms — Task 3 `build` job matrix
- ✅ SHA256 sidecars — Task 3 `Compute SHA256` + `Upload` steps
- ✅ `update-tap` job reads SHAs, updates formula, opens PR — Task 3 `update-tap` job
- ✅ `HOMEBREW_TAP_TOKEN` PAT setup — Task 5 Steps 1-2
- ✅ `release-local` Makefile target — Task 2
- ✅ `homebrew-mockwave` repo with formula + README — Task 4
- ✅ Version injectable via ldflags — Task 1 (prerequisite for `brew test`)
- ✅ Windows binary built but not in formula — `build` matrix includes windows, formula has no `on_windows` block

**Placeholder scan:**
- `Formula/mockwave.rb` stub in Task 4 has `PLACEHOLDER_*` SHA256 values — intentional, will be overwritten by CI on first real tag push. Documented in context.
- No other TBDs.

**Type consistency:**
- `version` variable in `main.go` → used in `versionCmd` → output matches `assert_match version.to_s` in formula test. ✅
- Binary names consistent across matrix, SHA sidecar names, `update-tap` download pattern, formula URLs. ✅
- `HOMEBREW_TAP_TOKEN` secret name consistent between Task 3 YAML and Task 5 `gh secret set`. ✅
