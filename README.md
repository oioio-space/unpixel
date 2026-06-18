# UnPixel

A Go port of [Bishop Fox's **Unredacter**](https://github.com/bishopfox/unredacter) — a tool
that reconstructs text hidden behind **pixelation**. See the original write-up:
[*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).

> **Status:** 🚧 work in progress — the engineering toolchain is in place; the port itself has
> not started yet. See [`PROGRESS.md`](PROGRESS.md) for the live roadmap.

## How it will work

Pixelation is a reversible, lossy transform. Given a pixelated redaction, UnPixel renders
candidate strings in the original font, re-applies the same pixelation, and compares the result
against the redacted region with an image-distance metric — searching the candidate space to
recover the most likely original text.

## Quick start

This repo is managed with [**mise**](https://mise.jdx.dev) (toolchain, env, and tasks).

```bash
mise run setup     # install pinned tools + wire git hooks
mise run ci        # run everything: secret/vuln scans, lint, tests, SBOM
mise run           # list all tasks
```

Common tasks: `mise run build | test | lint | fmt | cover | scan:code | sbom`.

## Quality & security gates

Every commit is gated (git hooks): **secret scan** (gitleaks) → **vulnerability scan**
(gosec + govulncheck) → **style** (gofmt + go vet + golangci-lint). CI re-runs all of it plus a
**CycloneDX SBOM** scanned by grype, and a full-history secret scan. See `PROGRESS.md` for details.

## License

GPL-3.0-or-later — see [`LICENSE`](LICENSE). This is a derivative work of Bishop Fox's Unredacter
(GPL-3.0); the copyleft license is preserved. © the UnPixel authors; original © Bishop Fox.
