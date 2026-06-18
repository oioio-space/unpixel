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
- Skill `go-style-guide` (3 guides Google itemisés) — `.claude/skills/go-style-guide/`
- Gate déterministe pre-commit : gofmt + go vet + golangci-lint v2 + build + test
  (le hook git passe par `mise run lint:staged`).
- Revue IA pre-commit (style-guide) : `.claude/hooks/commit-style-review.sh`
- Gate `/simplify` pre-commit (bloquant) : `.claude/hooks/pre-commit-simplify.sh`
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
