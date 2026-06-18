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

Outillage qualité en place ; **cœur du portage terminé** ; **v0.1.0 publié** sur pkg.go.dev.

- **Repo public** : `github.com/oioio-space/unpixel` (ouvert), CodeQL + secret-scanning +
  Codecov gratuits maintenant activés. Tags thématiques et description ajoutés.
- **Release v0.1.0** : module publié et consommable via `go get github.com/oioio-space/unpixel@latest`
  (vérifié proxy Go, indexé sur pkg.go.dev). API stable pré-1.0 annoncée ; CLI phase suivante.
- **Package core** : port Go fidèle de l'algorithme unredacter implémenté et testé. Le package
  racine (`unpixel`) expose `Engine`, `Config`, `Result`, `Eval`, `Offset`, les interfaces
  pluggables `Renderer`/`Pixelator`/`Metric`/`Strategy`, et une **API de progression library-agnostique**
  (`Progress` struct + `EventKind` + callback `OnProgress`) pour intégrer tout type d'UI
  (web/SSE, TUI, desktop). Flux : render → re-pixelate → image-distance → guided DFS.
- **Layout du package** : structure sous module `github.com/oioio-space/unpixel`. Internes
  dans `internal/` : `imutil` (utilitaires image), `pixelate` (pixelisation par blocs),
  `metric` (distance d'image ; défaut `pixelmatch`, fidèle à Jimp), `render` (pure-Go
  `golang.org/x/image/font/opentype` + Liberation Sans embarquée, compatible métriquement Arial),
  `search` (découverte offset + DFS guidée). Package `defaults` assure les dépendances.
  CLI à `cmd/unpixel` : placeholder.
- **GoDoc/pkg.go.dev** : package et symboles exportés enrichis (overviews avec snippet d'usage,
  chaque symbole/champ/const documenté avec son contrat, `Example` exécutable). Qualité
  pkg.go.dev appliquée et documentée dans la gate style (`.claude/skills/go-style-guide`,
  pre-commit `style-checklist.md`).
- **README** : réécrit via skill `readme-author` (principes awesome-readme) : badges CI/Go
  Reference/Go Report Card/GPL-3.0, démo, features, install/usage vérifiés, config table,
  architecture, crédits/attribution.
- **Tests** : 34+ tests passants ; auto-redaction round-trip récupère le plaintext connu
  ("hello"). Couverture **~94%** ; seuil `COVER_MIN` relevé à 85. Un test Phase-2 skippé :
  récupérer le `secret.png` Chromium-original nécessite renderer `chromedp` (écart
  moteur-fidélité documenté).
- **Design doc** : `docs/DESIGN.md` ajouté (algo fidèle + choix libs + API progression +
  plan TDD + améliorations Phase-2).
- **⛔ AUCUN CGO** — règle absolue du projet. Go pur ; CGO interdit. `CGO_ENABLED=0` épinglé
  en `[env]` `mise.toml`, gate déterministe `cgo:check` (`scripts/cgo-check.sh`) intégré
  à `mise run ci` et hook pre-commit, documenté `CLAUDE.md`.
- **Benchmark hot-path** : rule absolue en force. Skill `go-benchmark` + hook `PreToolUse`
  (Write|Edit) déclenché à l'écriture d'un `func Benchmark…` + `benchstat` (`mise run bench` / 
  `bench:baseline` / `bench:compare`).
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
  + GoDoc/pkg.go.dev quality gate.
- Skill `use-modern-go` (JetBrains) — idiomes Go modernes selon la version détectée
  (1.26.4). **Déclenchement sûr** : hook `PreToolUse` sur `Write|Edit|MultiEdit`
  (`.claude/hooks/modern-go-context.sh`) qui injecte les idiomes dès qu'un `.go` est
  écrit/édité (règles complètes 1×/session puis nudge). En plus de la description élargie.
- Skill `readme-author` : précepts awesome-readme, hook + template.
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
- **Nettoyage artefacts** : skill `repo-janitor` + gate déterministe `clean:check`
  (hook git, **gate 0**) qui supprime les artefacts régénérables non suivis et bloque
  tout artefact stagé + hook IA `commit-cleanup-review.sh`. Task : `mise run clean`.
- Ordre des gates au commit : **artefacts (gate 0)** → secrets → vulns code → style.
- **Routage sous-agents** (économie tokens) : `.claude/agents/*.md`, tier dans le
  frontmatter (`model`/`effort`/`skills`/`description`). Chaque skill et hook de revue
  IA routé vers l'agent compétent le moins cher : Haiku = mécanique (`quality-runner`
  +`repo-janitor`, `scribe`+`readme-author`, `explorer`) ; Sonnet = dev/review (`go-dev`,
  `go-reviewer`, skills go-style-guide+use-modern-go préchargés) ; Opus = design algo
  (`algo-architect`+`research-grounding`) + audit sécu (`security-auditor`+secret/vuln-guard).
  Préchargement seulement là où aucun hook ne couvre déjà le skill. Politique inter-agents : `CLAUDE.md`.
  Ne PAS définir `CLAUDE_CODE_SUBAGENT_MODEL` (écraserait les `model:`).
- **Recherche d'abord** : skill `research-grounding` + hook `UserPromptSubmit`
  (`.claude/hooks/research-grounding.sh`) qui rappelle à chaque prompt de fonder les
  réponses non-triviales sur l'existant (web/SOTA, GitHub via `ghx.sh`, docs officielles)
  plutôt que la mémoire (directive complète 1×/session puis nudge). Politique : `CLAUDE.md`.
- Tracking commits : ce fichier + hook `.githooks/post-commit`

## ✅ Reste à faire

- [x] Étudier l'algo d'unredacter (brute-force des combinaisons de caractères,
      re-pixelisation, comparaison de distance d'image).
- [x] Choisir les libs Go (rendu de police/texte, manipulation d'image).
- [x] Structurer le code (`internal/…`) et écrire les tests de caractérisation.
- [x] Implémenter le cœur de l'attaque.
- [x] Passer le repo public → CodeQL + secret-scanning + Codecov gratuits.
- [x] Monter COVER_MIN à 85.
- [ ] Implémenter une CLI ergonomique (package → CLI au-dessus).
- [x] **Phase 2 — beam search + mémoïsation** : `BeamStrategy` (largeur bornée par niveau) +
      `CachingScorer` (cache LRU prefix-render, clé `guess+offset+style`), exposés publiquement
      via `defaults.BeamStrategy(width)` et les champs `Config.BeamWidth`/`CacheSize`. Course de
      données du renderer (face `opentype` partagée) corrigée (`glyphMu`), non-régression
      prouvée au benchstat.
- [x] **Phase 2 — classement top-N par confiance** : `Result.TopN`/`Confidence`/`Ambiguity`.
- [ ] **Phase 2 (suite)** (cf. `docs/DESIGN.md`) : goroutine fan-out, renderer chromedp pour
      fidélité Chromium, inférence auto block-size/offset, métriques SSIM/edge-aware.

## 🧭 Décisions clés

- **Repo public** et **v0.1.0 publié** : module consommable sur pkg.go.dev. API stable
  pré-1.0 (peut évoluer avant 1.0.0) ; CLI phase 2.
- Module : `github.com/oioio-space/unpixel`, Go 1.26 (aligné sur le repo).
- Licence : **GPL-3.0** (œuvre dérivée de bishopfox/unredacter, GPL-3.0 — copyleft préservé).
- Deux couches de garde-fou : linters (objectif) + revue IA (subjectif).
- Hooks scindés git-natif (universel) / Claude Code (revue & gates pilotés par Claude).
- **⛔ AUCUN CGO** : projet Go pur, CGO interdit. `CGO_ENABLED=0` épinglé en `mise.toml`,
  gate déterministe `cgo:check` en pre-commit et CI, documenté `CLAUDE.md`.
- **API de progression library-agnostique** : `Progress` struct + `EventKind` + callback
  `OnProgress` pour que tout UI (web, TUI, desktop) puisse suivre la recherche.
- **Renderer pure-Go** : `golang.org/x/image/font/opentype` + Liberation Sans embarquée
  (metriquement compatible Arial). Fidélité jugée par auto-cohérence moteur ; écart vs
  Chromium (moteur-rendering) comblé plus tard via renderer chromedp Phase-2.

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
- `56a75de` 2026-06-18 — feat: auto-apply use-modern-go via PreToolUse hook on Go edits _(3 fichiers)_
- `d276b49` 2026-06-18 — feat: add token-economical sub-agent routing (.claude/agents + CLAUDE.md) _(9 fichiers)_
- `545772c` 2026-06-18 — docs: sync PROGRESS history before handoff _(1 fichiers)_
- `f0c6324` 2026-06-18 — ci: add README, GPL-3.0 LICENSE, pre-push CI gate, coverage+Codecov _(7 fichiers)_
- `d5192f8` 2026-06-18 — docs: record GitHub remote, license, and open decisions in PROGRESS _(1 fichiers)_
- `8c79f20` 2026-06-18 — feat: align module path with repo; add repo-janitor + go-benchmark skills _(14 fichiers)_
- `fbc1e03` 2026-06-18 — docs: record repo-janitor and go-benchmark skills in PROGRESS _(1 fichiers)_
- `e5265fc` 2026-06-18 — refactor: simplify tooling per /simplify review _(14 fichiers)_
- `c41d004` 2026-06-18 — feat: add research-grounding skill + UserPromptSubmit hook _(5 fichiers)_
- `38de483` 2026-06-18 — feat(research-grounding): go beyond the existing — seek improvements and out-of-the-box ideas _(4 fichiers)_
- `6498640` 2026-06-18 — feat(core): faithful pure-Go port of the unredacter algorithm _(25 fichiers)_
- `88505e4` 2026-06-18 — test(bench): add hot-path benchmarks for the search core _(5 fichiers)_
- `f24f941` 2026-06-18 — build: enforce no-CGO rule and strengthen the benchmark gate _(5 fichiers)_
- `dd40832` 2026-06-18 — docs: record the port, the no-CGO and benchmark rules, and project state _(2 fichiers)_
- `2dbfac3` 2026-06-18 — test: cover the real scorer/search, defaults and engine paths (61% → 94%) _(4 fichiers)_
- `c133d0d` 2026-06-18 — feat(skill): add readme-author skill distilled from awesome-readme _(1 fichiers)_
- `7c39e49` 2026-06-18 — chore(agents): route every skill and review hook to its cheapest competent agent _(8 fichiers)_
- `55c7a6f` 2026-06-18 — docs(readme): rewrite README with the readme-author skill _(1 fichiers)_
- `e358ebc` 2026-06-18 — docs(godoc): enrich package/symbol docs and add a runnable Example _(3 fichiers)_
- `1d6a1a1` 2026-06-18 — feat(style): enforce GoDoc/pkg.go.dev quality in the style gate _(2 fichiers)_
- `5c7d6eb` 2026-06-18 — docs(readme): add Go Reference and Go Report Card badges _(1 fichiers)_
- `8dd956d` 2026-06-18 — docs: sync PROGRESS — public repo, v0.1.0, GoDoc, README, routing _(1 fichiers)_
- `6a42682` 2026-06-18 — feat(cli): ergonomic CLI (urfave/cli/v3) + Top-N/confidence reporting _(8 fichiers)_
- `8bc53bc` 2026-06-18 — feat(skill): helper-ergonomics skill + pre-commit hook (human-facing API) _(4 fichiers)_
