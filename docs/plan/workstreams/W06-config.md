# W6 · Config Loader

> **Wave:** 1 · **Effort:** XS (~half day) · **Depends on:** W0 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths".

---

## Goal

Read/write `~/.agentdeck/config.toml`, exposing typed config to the rest of the app. Atomic writes. Defaults applied if file missing.

---

## Spec sections

- §11 (config layout)
- §18 #11 (telemetry off in v1)

---

## Owned paths

```
internal/config/config.go
internal/config/config_test.go
internal/config/paths.go     (per-OS default paths)
internal/config/paths_test.go
```

---

## Inputs

- W0's `internal/config/schema.go` (typed `Config` struct).

---

## Outputs

```go
package config

func Load() (*Config, error)         // reads ~/.agentdeck/config.toml; returns defaults if missing (and writes them)
func Save(*Config) error             // atomic write (temp + rename)

// paths.go
func ConfigDir() string              // ~/.agentdeck on macOS+Linux, %USERPROFILE%\.agentdeck on Windows
func ConfigFile() string             // ConfigDir + "/config.toml"
func DBPath() string                 // ConfigDir + "/agentdeck.db"
func PricingOverridePath() string    // ConfigDir + "/pricing.json"
```

For testability, expose package-level vars (e.g., `var HomeDir = os.UserHomeDir`) overrideable in tests.

---

## Per-OS path policy (v1)

- **macOS / Linux:** `~/.agentdeck/`
- **Windows:** `%USERPROFILE%\.agentdeck\`
- XDG support is v1.1 polish — not in scope.

---

## Acceptance criteria

- [ ] `Load()` returns defaults if file missing **and writes them** (so subsequent calls find a real file).
- [ ] `Save()` is atomic — write to temp file, fsync, rename. A crash mid-write must not corrupt the file.
- [ ] `Load()` round-trips a `Save()`d config.
- [ ] Path functions return correct values per-OS.
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

1. `TestLoad_DefaultsWhenMissing` — tempdir has no file → `Load()` returns defaults and writes the file.
2. `TestSave_Load_RoundTrip` — modify a field, save, load → field preserved.
3. `TestSave_Atomic_NoCorruption` — simulate crash mid-write (write to temp, kill before rename) → original file intact.
4. `TestPaths_PerOS` — table-driven; assert `ConfigDir()` matches expected per `runtime.GOOS`.
5. `TestLoad_InvalidTOML` — bad file → returns parse error, does NOT silently overwrite.

Use `t.TempDir()` and override `HomeDir` via a test helper.

---

## Hard boundaries

- Do **NOT** read API keys from config — env-var-only (W10's responsibility).
- Do **NOT** modify `internal/config/schema.go` — that's W0-owned.
- Do **NOT** touch any other package.

---

## Done

When W7's `/settings` handler and W9's pricing override and W12's wiring layer can call `config.Load()` cleanly.
