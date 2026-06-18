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

Outillage qualité en place ; **cœur du portage terminé** ; **Phase 2 + CLI livrées** ;
**v0.2.0 publiée** sur pkg.go.dev.

- **Repo public** : `github.com/oioio-space/unpixel` (ouvert), CodeQL + secret-scanning +
  Codecov gratuits maintenant activés. Tags thématiques et description ajoutés.
- **Release v0.2.0** : Phase 2 (beam+cache, SSIM, inférence block-size, fan-out goroutines) +
  CLI ergonomique. Consommable via `go get github.com/oioio-space/unpixel@v0.2.0` et
  `go install …/cmd/unpixel@v0.2.0`. API pré-1.0, rétro-compatible v0.1.0 (ajouts seulement).
  v0.1.0 : premier module public indexé sur pkg.go.dev.
- **Package core** : port Go fidèle de l'algorithme unredacter implémenté et testé. Le package
  racine (`unpixel`) expose `Engine`, `Config`, `Result`, `Eval`, `Offset`, les interfaces
  pluggables `Renderer`/`Pixelator`/`Metric`/`Strategy`, et une **API de progression library-agnostique**
  (`Progress` struct + `EventKind` + callback `OnProgress`) pour intégrer tout type d'UI
  (web/SSE, TUI, desktop). Flux : render → re-pixelate → image-distance → guided DFS.
- **Layout du package** : structure sous module `github.com/oioio-space/unpixel`. Internes
  dans `internal/` : `imutil` (utilitaires image), `pixelate` (pixelisation par blocs),
  `metric` (distance d'image ; défaut `pixelmatch`, fidèle à Jimp), `render` (pure-Go
  `golang.org/x/image/font/opentype` + Liberation Sans embarquée, compatible métriquement Arial),
  `search` (découverte offset + DFS guidée/beam, fan-out goroutines). Package `defaults` assure
  les dépendances et expose les choix de stratégie/métrique. **CLI `cmd/unpixel` opérationnelle**
  (urfave/cli/v3).
- **GoDoc/pkg.go.dev** : package et symboles exportés enrichis (overviews avec snippet d'usage,
  chaque symbole/champ/const documenté avec son contrat, `Example` exécutable). Qualité
  pkg.go.dev appliquée et documentée dans la gate style (`.claude/skills/go-style-guide`,
  pre-commit `style-checklist.md`).
- **README** : réécrit via skill `readme-author` (principes awesome-readme) : badges CI/Go
  Reference/Go Report Card/GPL-3.0, démo, features, install/usage vérifiés, config table,
  architecture, crédits/attribution.
- **Tests** : 150+ tests passants (`-race` propre). Couverture **~90%** ; seuil `COVER_MIN` à 85.
  **Matrice de récupération** : `internal/fixture` génère des images de référence (PNG + `manifest.json`
  liant chaque image à ses paramètres) via `go generate` ; `matrix_test.go` les recharge et vérifie
  la récupération sur blocs (4/8/16), tailles, gras, charsets (minuscules/alnum/symboles), padding
  (offset), formes de texte, dark-mode, et formats (PNG/GIF/JPEG). Un test Phase-2 skippé :
  récupérer le `secret.png` Chromium-original nécessite renderer `chromedp` (écart moteur-fidélité).
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
- [x] **CLI ergonomique** (`cmd/unpixel`, urfave/cli/v3) : stdin (`-`), `--format json|text`,
      `--top`, `--strategy guided|beam`, `--beam-width`, `--metric pixelmatch|ssim`, `--workers`,
      `--block-size 0`=auto, `--timeout`, progress live sur stderr, meilleur résultat sur stdout.
      Tests bout-en-bout (run/JSON/texte/validation). `go install …/cmd/unpixel@latest`.
- [x] **Phase 2 — beam search + mémoïsation** : `BeamStrategy` (largeur bornée par niveau) +
      `CachingScorer` (cache LRU prefix-render, clé `guess+offset+style`), exposés publiquement
      via `defaults.BeamStrategy(width)` et les champs `Config.BeamWidth`/`CacheSize`. Course de
      données du renderer (face `opentype` partagée) corrigée (`glyphMu`), non-régression
      prouvée au benchstat.
- [x] **Phase 2 — classement top-N par confiance** : `Result.TopN`/`Confidence`/`Ambiguity`.
- [x] **Phase 2 — métrique SSIM** : `metric.SSIM` (structure locale, tolère AA/hinting),
      exposée via `defaults.SSIMMetric(window)`. Métrique alternative derrière l'interface.
- [x] **Phase 2 — inférence auto block-size** : `unpixel.InferBlockSize` (détection de la grille
      par PGCD des écarts de bordures) ; `New` l'applique quand `BlockSize<=0`. `Engine.Config()`.
- [x] **Phase 2 — fan-out goroutines** : `DiscoverOffsets` et la recherche par offset parallélisées
      sur `Config.Workers` (défaut GOMAXPROCS) avec **merge déterministe** ; les deux stratégies
      partagent `searchOffsets`. ~4× sur la découverte d'offsets (benchstat), aucune régression
      sur le chemin séquentiel (`Workers=1`), `-race` propre.
- [ ] **Phase 2 (reporté)** : renderer `chromedp` (fidélité Chromium) — dép. lourde exigeant un
      binaire Chrome au runtime/CI ; métriques edge-aware. Cf. `docs/DESIGN.md`.

### 🎯 Phase 3 — zéro-config « image → texte » (auto-détection + qualité, perf préservée)

**Étoile polaire** : l'utilisateur fournit une image de texte pixélisé → le texte en sort, avec
le minimum de paramètres. Tout ce qui peut être détecté l'est automatiquement ; **les paramètres
bas-niveau (`Config`) restent toujours accessibles aux experts**. **Contrainte** : pur Go / no-CGO
→ pas de DeepFont/CNN ni de LLM lourd ; l'ID de police et le modèle de langue doivent être
*classiques/embarquables* (rendu-et-comparaison, n-gram). Sources : Hill, Zhou, Saul & Shacham
(HMM + probas de transition + rendu par police) ; DeepFont (VFR, hors pur-Go) ; Positive Security
(multi-frame) ; challenge BishopFox (calibration police + déconvolution).

Priorité haute :
- [ ] **P3.1 — API une-ligne** `Recover(img) (Result, error)` + `RecoverFile`/`RecoverReader` +
      options fonctionnelles (`WithCharset`/`WithWorkers`/`WithFonts`/`WithLanguageModel`…). Faire
      du défaut CLI un vrai zéro-config. `Config` conservé pour les experts. *(catalyseur de l'objectif)*
- [ ] **P3.2 — détecteur de cohérence/validité** *(piste utilisateur n°2 — plus fort levier qualité)* :
      modèle de langue char-level (n-gram) pur-Go embarqué + wordlist optionnelle → score de
      log-vraisemblance/perplexité. Double usage : (a) **prior** pour guider/élaguer la recherche,
      (b) **validation a posteriori** + confiance. Combiné à la distance image. Sous-tend P3.6/P3.8.
- [~] **P3.4 — auto-détection étendue des paramètres** *(piste n°3)* :
      - [x] **auto-contraste fond sombre** : `New` détecte un fond sombre (bordure) et inverse
            l'image (`InferDarkBackground`), pour les captures *dark-mode* (code !). Chemin clair
            inchangé (no-op) → aucune régression ; round-trip inversé testé.
      - [ ] couleur exacte texte/fond (au-delà du clair/sombre), taille de police (hauteur d'x),
            poids/gras, padding/baseline. Tout dérivé de l'image, surchargeable via `Config`.
      (block-size & offset déjà auto.)
- [ ] **P3.3 — police comme dimension de recherche / auto-calibration** *(piste n°1 reformulée)* :
      bundler des polices métrique-compatibles redistribuables (Liberation=Arial/Times/Courier,
      Carlito≈Calibri, Arimo/Tinos/Cousine, DejaVu/Noto) couvrant les plus courantes ; tester
      chaque police, scorer par re-pixelisation, **élaguer via une sonde bon-marché** avant la
      recherche complète. (Pas de DeepFont : CNN exclu en pur-Go.)

Priorité moyenne :
- [ ] **P3.8 — confiance calibrée + abandon honnête** : fusionner distance image + score LM +
      ambiguïté en une confiance calibrée ; signaler « incertain / probablement irrécupérable »
      plutôt qu'un best-guess trompeur (répond à la critique de fabrication, A. Madry).
- [ ] **P3.5 — localisation auto de la zone caviardée** dans une capture entière (carte de
      variance/blockiness) → passer le screenshot complet, pas une zone pré-découpée.
- [ ] **P3.6 — escalade auto du charset** : minuscules+espace → chiffres → majuscules →
      ponctuation, déclenchée par l'absence de résultat cohérent (P3.2) ; charset déduit de la
      zone en clair si présente. Évite l'explosion combinatoire.
- [ ] **P3.7 — priors structurés / secrets** : wordlist (mots de passe communs), formats
      (UUID, clés API), checksums (Luhn) → récupération + validation haute-confiance des cibles réelles.

Priorité basse / exploratoire :
- [ ] **P3.9 — entrée multi-images / vidéo** : exploiter le jitter sous-pixel entre plusieurs
      pixelisations du même texte (information réelle, pas d'hallucination — Positive Security).
- [ ] **P3.10 — pré-traitement déconvolution** (Richardson-Lucy/Wiener, pur-Go) pour gérer le
      **flou** en plus du mosaïque (technique gagnante du challenge BishopFox).
- [ ] **P3.11 — perf pour soutenir l'automatisation** (prouvé benchstat) : cache du rendu
      `prevGuess` (gros gain déjà identifié), SIMD pur-Go (asm AVX2 + fallback), pruning agressif
      via P3.2/P3.3 pour compenser le coût des polices/charsets élargis.
- [ ] **P3.12 — (spike) classifieur de police par deep-learning, pur-Go** : DL *utile pour la
      reconnaissance de police* (DeepFont/VFR, >80% top-5) car exécuté **une seule fois par image**
      (latence amortie, hors boucle chaude — contrairement au modèle de cohérence P3.2 où le n-gram
      reste supérieur sur CPU). Voie conforme no-CGO : **inférence ONNX pur-Go** (onnx-go / gonnx /
      backend pur-Go de Hugot ; ~8× plus lent que XLA mais one-shot → acceptable). Optionnel et
      *derrière l'interface* : accélère/affine le candidat-police de P3.3 (render-and-match reste le
      défaut). **Reporté** : onnxruntime via purego ou CGO (dép. native runtime par plateforme, même
      arbitrage que chromedp). Évaluer coût (taille binaire du modèle embarqué, maturité libs,
      sourcing/entraînement) vs gain réel sur l'ensemble de polices bundlé.

#### Sous-axe : dépixéliser du **code source** (au-delà de a–z + espace)

Constat clé : `Config.Charset` accepte déjà n'importe quelle chaîne (utilisable *aujourd'hui* via
`--charset`). Mais le code a deux propriétés qui changent la donne : (1) il est presque toujours en
police **monospace** → la grille de pixelisation s'aligne sur une grille de cellules régulière ;
(2) il est très structuré/répétitif (« naturalness of software »).

- [ ] **P3.13 — presets de charset** : `unpixel.CharsetLower` (actuel), `CharsetAlnum`,
      `CharsetASCIIPrintable`, `CharsetCode` (ASCII imprimable complet ~95 car.) ; flag
      `--charset-preset lower|alnum|ascii|code`. Rendre l'escalade (P3.6) consciente du code
      (lettres → +chiffres → +symboles).
- [ ] **P3.14 — fast-path monospace (déverrouillage majeur pour le code)** : détecter l'avance
      fixe / la grille de cellules régulière, puis **classifier chaque cellule indépendamment** et
      en parallèle contre son/ses bloc(s) — supprime la cascade d'erreur de position des polices à
      chasse variable. Énorme gain perf **et** précision : 95 candidats/cellule indépendants au lieu
      d'un DFS séquentiel. Implémenté derrière l'interface `Strategy` (`MonospaceStrategy`), à côté
      de guided/beam. Réf. OCR monospace (box-connectivity, character grid).
- [ ] **P3.15 — cohérence/validité spécifique au code** : étend P3.2 avec (a) un n-gram
      caractère/token entraîné sur du code, (b) une **validation lexicale/syntaxique** — tokeniser
      (chrF, n-grams de tokens) et idéalement parser : Go via `go/scanner`+`go/parser` (dogfooding),
      autres langages via grammaires tree-sitter (GLR + error-recovery). Un candidat qui tokenise/
      parse vaut une confiance bien plus élevée.
- [ ] **P3.3+ — polices monospace de code** : étendre le bundle P3.3 avec les monospaces de code
      OFL/redistribuables les plus courantes (JetBrains Mono, Fira Code, Source Code Pro, IBM Plex
      Mono, Liberation Mono≈Courier New, DejaVu Sans Mono, Hack/MIT, Cascadia Code).

## 🧭 Décisions clés

- **Repo public** ; **v0.1.0** (premier module public) puis **v0.2.0** (Phase 2 + CLI) publiées
  sur pkg.go.dev. API stable pré-1.0 (peut évoluer avant 1.0.0).
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
- `a00192f` 2026-06-18 — feat(search): beam-search strategy + prefix-render cache via defaults _(12 fichiers)_
- `45772ef` 2026-06-18 — docs(claude): add Commands and Architecture sections to CLAUDE.md _(2 fichiers)_
- `37d1be1` 2026-06-18 — feat: Phase-2 — SSIM metric, block-size inference, offset fan-out _(12 fichiers)_
- `0191899` 2026-06-18 — feat(cli): expose strategy, metric, workers, and auto block-size _(2 fichiers)_
- `e05fceb` 2026-06-18 — docs: document the CLI and Phase-2 features in README/PROGRESS _(2 fichiers)_
- `356d6cf` 2026-06-18 — docs(progress): record the v0.2.0 release _(1 fichiers)_
- `6a20677` 2026-06-18 — ci: publish a goreleaser GitHub Release on v* tags _(1 fichiers)_
- `a240b27` 2026-06-19 — docs(progress): add Phase 3 roadmap — zero-config image→text + code support _(1 fichiers)_
- `4e18ac2` 2026-06-19 — feat(api): one-call Recover/RecoverFile/RecoverReader + functional options (P3.1) _(3 fichiers)_
- `bf6352b` 2026-06-19 — feat(charset): charset presets (alnum, ascii/code) + --charset-preset (P3.13) _(5 fichiers)_
- `e95ed8a` 2026-06-19 — fix(hooks): stage the PROGRESS history line so it lands in a later commit _(2 fichiers)_
- `0f919e4` 2026-06-19 — feat(auto): dark-background auto-contrast (P3.4, partial) _(3 fichiers)_
- `c1ee424` 2026-06-19 — feat(api): printable Result and Eval (String methods) _(3 fichiers)_
- `a000af0` 2026-06-19 — test(matrix): reference-image recovery matrix + generator (+ WithStyle option) _(23 fichiers)_
- `f934264` 2026-06-19 — build(mise): add gen and gen:check tasks for the test fixtures _(3 fichiers)_
