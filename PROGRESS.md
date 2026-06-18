# UnPixel — Suivi de projet

Fichier de continuité : objectif, état, reste à faire, décisions, historique des
commits. La section **Historique** est alimentée automatiquement par le hook git
`post-commit` ; les autres sections sont maintenues à la main (par Claude) avant
chaque commit.

## 🎯 Objectif

Porter [bishopfox/unredacter](https://github.com/bishopfox/unredacter) en **Go** :
un outil qui reconstruit du texte caché derrière une pixelisation (cf.
[l'article Bishop Fox](https://bishopfox.com/blog/unredacter-tool-never-pixelation)).

## 📍 État actuel

Outillage qualité en place ; **portage pas encore commencé**.

- Toolchain reproductible via **mise** (`mise.toml`) : go 1.26.4, golangci-lint 2.12.2,
  gofumpt, shellcheck, gotestsum, goreleaser, actionlint, yamlfmt, watchexec.
  Bootstrap : `mise run setup` (ou auto via le hook mise `enter`). Commandes :
  `mise run lint|test|ci|fmt`, `mise run test:watch` (TDD), `mise run release:snapshot`.
- CI = local : `.github/workflows/ci.yml` (généré) lance `mise run ci` avec les mêmes
  versions d'outils. Release multi-plateforme via `.goreleaser.yaml`.
- Optimisation tokens : les tasks build/test/lint passent par `scripts/rtkx.sh`
  (`rtk err` si rtk présent, sinon brut — non bloquant ; exit code préservé, détails
  d'échec conservés). Install optionnelle : `mise run tools:rtk`.
- **GitHub CLI** géré par mise (`gh`). À utiliser partout via `scripts/ghx.sh`
  (= gh version mise + sortie token-optimisée `rtk gh`) ou `mise run gh -- <args>`.
  gh authentifié (compte `oioio-space`).
- Skill `go-style-guide` (3 guides Google itemisés) — `.claude/skills/go-style-guide/`
- Skill `use-modern-go` (JetBrains) — idiomes Go modernes selon la version détectée
  (1.26.4). **Déclenchement sûr** : hook `PreToolUse` sur `Write|Edit|MultiEdit`
  (`.claude/hooks/modern-go-context.sh`) qui injecte les idiomes dès qu'un `.go` est
  écrit/édité (règles complètes 1×/session puis nudge). En plus de la description élargie.
- Gate déterministe pre-commit : gofmt + go vet + golangci-lint v2 + build + test
  (le hook git passe par `mise run lint:staged`).
- Revue IA pre-commit (style-guide) : `.claude/hooks/commit-style-review.sh`
- Gate `/simplify` pre-commit (bloquant) : `.claude/hooks/pre-commit-simplify.sh`
- **Anti-fuite de secrets** : gate déterministe `gitleaks --staged` (hook git, en 1er)
  + skill `secret-guard` IA (`.claude/hooks/commit-secret-review.sh`) + scan historique
  complet en CI (`mise run scan:secrets`). Tasks : `mise run scan:secrets[:staged]`.
- **Anti-vulnérabilités** : gate déterministe `gosec` (SAST) + `govulncheck`
  (atteignabilité) sur Go stagé (hook git, gate 2 ; `scripts/sec-scan.sh`)
  + skill `vuln-guard` IA (`.claude/hooks/commit-vuln-review.sh`) + **SBOM** CycloneDX
  (`syft`) scanné par `grype` en CI (`mise run sbom` / `scan:sbom`, fail-on high).
  Tasks : `mise run scan:code[:staged]`.
- Ordre des gates au commit : secrets → vulns code → style.
- Tracking commits : ce fichier + hook `.githooks/post-commit`

## ✅ Reste à faire

- [ ] Étudier l'algo d'unredacter (brute-force des combinaisons de caractères,
      re-pixelisation, comparaison de distance d'image).
- [ ] Choisir les libs Go (rendu de police/texte, manipulation d'image).
- [ ] Structurer le code (`internal/…`) et écrire les tests de caractérisation.
- [ ] Implémenter le cœur de l'attaque, puis une CLI.

## 🧭 Décisions clés

- Module : `github.com/mathieu/unpixel`, Go 1.26.
- Deux couches de garde-fou : linters (objectif) + revue IA (subjectif).
- Hooks scindés git-natif (universel) / Claude Code (revue & gates pilotés par Claude).

## 🗂️ Historique des commits

<!-- Lignes ajoutées automatiquement par .githooks/post-commit (ne pas éditer à la main) -->

- `705a884` 2026-06-18 — chore: bootstrap Go style-guide skill + pre-commit hooks
- `a593117` 2026-06-18 — feat: add /simplify pre-commit gate and post-commit progress tracking _(4 fichiers)_
- `b91a5b3` 2026-06-18 — build: manage toolchain, env and tasks with mise _(8 fichiers)_
- `30b6835` 2026-06-18 — build: add CI, gotestsum, goreleaser, TDD watch + enter-hook auto-wiring _(6 fichiers)_
- `1236a6d` 2026-06-18 — build: route mise tasks through rtk for token-optimized output _(3 fichiers)_
- `0071b1c` 2026-06-18 — build: install rtk from GitHub releases via mise (no compilation) _(2 fichiers)_
- `a4c002f` 2026-06-18 — build: manage GitHub CLI with mise + project-wide ghx wrapper _(3 fichiers)_
- `e0ced80` 2026-06-18 — feat: add gitleaks secret-scan gate + secret-guard skill/hook _(7 fichiers)_
- `5aec48e` 2026-06-18 — docs: record secret-scanning layers in PROGRESS _(1 fichiers)_
- `3ad67bf` 2026-06-18 — feat: add gosec+govulncheck vuln gates, SBOM/grype CI scan, vuln-guard skill _(8 fichiers)_
- `7664183` 2026-06-18 — feat: add JetBrains use-modern-go skill (broadened trigger) _(2 fichiers)_
