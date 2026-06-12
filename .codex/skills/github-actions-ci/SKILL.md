---
name: github-actions-ci
description: Use when debugging or changing this repository's GitHub Actions workflows, release automation, lint wiring, or CI failures.
---

# Sentinel233 GitHub Actions CI

Use this project skill before editing `.github/workflows/*`, `.golangci.yml`, `Makefile`, release scripts, or validation scripts.

## Workflow

1. Check GitHub CLI auth:

   ```powershell
   gh auth status
   ```

   If auth is invalid, report that remote CI logs cannot be fetched and continue with local reproduction.

2. Inspect the current PR checks when available:

   ```powershell
   gh pr view --json number,url
   gh pr checks --watch=false
   ```

3. Run local CI equivalents:

   ```powershell
   go vet ./...
   go test ./... -count=1 -timeout=120s
   golangci-lint run --timeout=5m
   powershell -ExecutionPolicy Bypass -File scripts/validate-github-actions.ps1
   git diff --check
   ```

4. When touching release automation, also run:

   ```powershell
   go build ./cmd/sentinel233
   go build ./cmd/sentinel233-agent
   ```

5. Never stage local runtime data, secrets, or generated database files.
