# Homebrew Release Automation Design

**Date:** 2026-05-25
**Status:** Approved

## Goal

Automate cross-platform binary releases and keep a Homebrew tap (`lfdubiela/homebrew-mockwave`) in sync automatically when a new version tag is pushed to `lfdubiela/mockwave`.

## Scope

Two deliverables:
1. GitHub Actions workflow in `lfdubiela/mockwave` — builds binaries, uploads to GitHub Release, opens PR on tap
2. New GitHub repo `lfdubiela/homebrew-mockwave` — contains the Homebrew formula

Windows binary is built and uploaded to the Release but is not part of the Homebrew formula (Homebrew does not support Windows).

---

## Architecture

```
git tag v0.2.0  →  release.yml  →  GitHub Release (5 binaries)
                              └──→  PR on homebrew-mockwave (updated formula)

User:
  brew tap lfdubiela/mockwave
  brew install mockwave
```

---

## Repo 1: `lfdubiela/mockwave` changes

### `.github/workflows/release.yml`

**Trigger:** `push` of tags matching `v*.*.*`

**Job: `build`**

Matrix strategy across 5 targets:

| GOOS    | GOARCH | Binary name                     |
|---------|--------|---------------------------------|
| darwin  | amd64  | mockwave-darwin-amd64           |
| darwin  | arm64  | mockwave-darwin-arm64           |
| linux   | amd64  | mockwave-linux-amd64            |
| linux   | arm64  | mockwave-linux-arm64            |
| windows | amd64  | mockwave-windows-amd64.exe      |

Each matrix job:
1. Checks out code
2. Sets up Go (version from `go.mod`)
3. Runs `CGO_ENABLED=0 go build -ldflags="-s -w" -o <binary-name> ./cmd/mockwave/`
4. Computes `sha256sum` of the binary
5. Uploads binary to the GitHub Release (created by the tag push) via `gh release upload`
6. Uploads a `<binary-name>.sha256` sidecar file

**Job: `update-tap`**

Runs after all `build` matrix jobs complete (`needs: build`).

Steps:
1. Download all 5 `.sha256` sidecar files from the Release
2. Read SHA256 values into variables
3. Checkout `lfdubiela/homebrew-mockwave` using `HOMEBREW_TAP_TOKEN`
4. Render new `Formula/mockwave.rb` from a template (inline in the workflow) with:
   - Version extracted from the tag (`v0.2.0` → `0.2.0`)
   - Download URLs pointing to the GitHub Release assets
   - SHA256 for each platform
5. Commit the updated formula to a branch `update-v<version>`
6. Open a PR against `main` of `homebrew-mockwave` via `gh pr create`

**Secret required:** `HOMEBREW_TAP_TOKEN` — GitHub Personal Access Token with `repo` scope on `lfdubiela/homebrew-mockwave`. Stored as a repository secret on `lfdubiela/mockwave`.

### `Makefile` additions

```makefile
# Build all release targets locally (for testing before tag push)
release-local:
	GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o dist/mockwave-darwin-amd64  ./cmd/mockwave/
	GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o dist/mockwave-darwin-arm64  ./cmd/mockwave/
	GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o dist/mockwave-linux-amd64   ./cmd/mockwave/
	GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w" -o dist/mockwave-linux-arm64   ./cmd/mockwave/
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/mockwave-windows-amd64.exe ./cmd/mockwave/
	shasum -a 256 dist/* > dist/checksums.txt
```

---

## Repo 2: `lfdubiela/homebrew-mockwave` (new)

### Structure

```
homebrew-mockwave/
  Formula/
    mockwave.rb
  README.md
```

### `Formula/mockwave.rb`

```ruby
class Mockwave < Formula
  desc "Open-source multi-protocol mock server (HTTP, GraphQL, SOAP, gRPC)"
  homepage "https://github.com/lfdubiela/mockwave"
  version "VERSION_PLACEHOLDER"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/lfdubiela/mockwave/releases/download/vVERSION/mockwave-darwin-arm64"
      sha256 "SHA256_DARWIN_ARM64"
    else
      url "https://github.com/lfdubiela/mockwave/releases/download/vVERSION/mockwave-darwin-amd64"
      sha256 "SHA256_DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/lfdubiela/mockwave/releases/download/vVERSION/mockwave-linux-arm64"
      sha256 "SHA256_LINUX_ARM64"
    else
      url "https://github.com/lfdubiela/mockwave/releases/download/vVERSION/mockwave-linux-amd64"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  def install
    binary = Dir["mockwave*"].reject { |f| f.end_with?(".sha256") }.first
    bin.install binary => "mockwave"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/mockwave version")
  end
end
```

### `README.md`

Documents `brew tap lfdubiela/mockwave` + `brew install mockwave` + upgrade instructions.

---

## Release flow (end-to-end)

```bash
# Developer cuts a release:
git tag v0.2.0
git push origin v0.2.0

# GitHub Actions:
# 1. Creates GitHub Release for v0.2.0
# 2. Builds 5 binaries in parallel
# 3. Uploads binaries + .sha256 sidecars to the Release
# 4. Opens PR on homebrew-mockwave updating formula

# Maintainer:
# Review + merge PR on homebrew-mockwave

# User:
brew tap lfdubiela/mockwave
brew install mockwave
# or
brew upgrade mockwave
```

---

## Out of scope

- Windows package manager (Chocolatey, Scoop, WinGet) — future work
- Homebrew Core (`homebrew/homebrew-core`) — requires project maturity/popularity
- Code signing / notarization for macOS binaries
- Automatic merge of formula PR (maintainer reviews before merge)

---

## Files to create/modify

| Repo | Action | Path |
|------|--------|------|
| `mockwave` | Create | `.github/workflows/release.yml` |
| `mockwave` | Modify | `Makefile` (add `release-local` target) |
| `homebrew-mockwave` | Create | `Formula/mockwave.rb` |
| `homebrew-mockwave` | Create | `README.md` |
