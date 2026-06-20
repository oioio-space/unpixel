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
**v0.4.0 publiée** sur pkg.go.dev (récupération **mosaïque + flou gaussien**, zéro-config) ;
**v0.5.0 en préparation** : déconvolution Richardson-Lucy optionnelle, automation perf
(auto Top-K + intra-node parallelism), bundle de polices élargi (Adwaita Mono, Noto Sans Mono).

- **Mosaïque linéaire (GEGL/GIMP) + échantillon réel "Hello World !"** : la plupart des outils
  (GIMP/GEGL Pixelize, CSS, navigateurs) moyennent les blocs en **lumière linéaire**, pas en sRGB —
  la moyenne d'un texte sombre sur fond clair y est nettement plus claire. Ajout de
  `pixelate.NewLinearBlockAverage` / `defaults.LinearBlockAverage` + flag CLI `--gamma` (chemin sRGB
  par défaut **inchangé/fidèle**). Échantillon réel `testdata/real/text_hello-world.png` (capture GIMP :
  Noto Sans Mono 62 px → Pixelize 16 → mise à l'échelle ~2×) **décodé** : le modèle generate-and-test,
  avec Noto Sans Mono (police « Monospace » de Fedora) + moyenne linéaire, **reproduit la redaction à
  l'identique** (pixelmatch **0,0000**) pour « Hello World ! », strictement mieux que tout quasi-voisin
  ou le modèle sRGB (0,30) — cf. `real_mosaic_test.go`. La recherche aveugle de bout en bout reste
  impraticable sur 13 glyphes monospace très peu encrés (signal par-caractère trop faible) ; la
  confirmation par le modèle direct est le garde-fou retenu (comme `real_image_test.go`).
- **Repo public** : `github.com/oioio-space/unpixel` (ouvert), CodeQL + secret-scanning +
  Codecov gratuits maintenant activés. Tags thématiques et description ajoutés.
- **Release v0.4.0** : **au-delà de la mosaïque → flou gaussien** (type du défi Bishop Fox).
  Opérateur `pixelate.GaussianBlur` + **FastBlur** (box O(1)/px, ~3× ; auto au grand σ, `--blur-exact`) ;
  zéro-config flou : `InferBlurSigma` (σ auto), `LocateRedaction` (bande floutée dans une capture),
  `InferFontSize` (taille) ; **prior de langue** char-bigram (`internal/lang`, `--language`) ;
  **fast-path monospace** (`--strategy mono`, ~8–16×) ; matrice de récupération flou. `#4` (compare
  en résolution réduite) **implémenté puis rejeté au benchmark** (perte de temps mesurée). Delta
  complet vs v0.3.0 et vs Bishop Fox (perf + fonctionnalités) : `docs/DELTA.md`.
  `go get …@v0.4.0` / `go install …/cmd/unpixel@v0.4.0`. API pré-1.0, additive depuis v0.3.x.
- **Release v0.3.0** : **polices personnalisées + balayage de polices** — récupérer une redaction
  sans connaître sa typographie. `render.NewXImageFromFonts` / `defaults.RendererFromFonts` /
  `unpixel.WithRenderer` ; `Style.LetterSpacing` ; flags CLI `--font` (répétable) / `--font-dir` /
  `--font-size` / `--letter-spacing`. Helper lib `unpixel.RecoverMultiFont` (+ `FontResult`) :
  essaie plusieurs polices **en parallèle** et garde la meilleure par **score d'image entière**
  (`Result.BestTotal`). Plus deux garde-fous (avertir si aucune grille mosaïque ; ne jamais
  retenir un résultat tout-espaces). Release goreleaser auto sur le tag (CI verte requise).
  `go get …@v0.3.0` / `go install …/cmd/unpixel@v0.3.0`. API pré-1.0, additive depuis v0.2.x.
- **Release v0.2.0** : Phase 2 (beam+cache, SSIM, inférence block-size, fan-out goroutines) +
  CLI ergonomique. v0.1.0 : premier module public indexé sur pkg.go.dev.
- **Package core** : port Go fidèle de l'algorithme unredacter implémenté et testé. Le package
  racine (`unpixel`) expose `Engine`, `Config`, `Result`, `Eval`, `Offset`, les interfaces
  pluggables `Renderer`/`Pixelator`/`Metric`/`Strategy`, et une **API de progression library-agnostique**
  (`Progress` struct + `EventKind` + callback `OnProgress`) pour intégrer tout type d'UI
  (web/SSE, TUI, desktop). **Option `WithPriors`** (P3.7/P3.2) : composable système de priors
  (formats secrets structurés + dict mots = bonus additif pour départager candidats). Flux : render → 
  re-pixelate → image-distance → guided DFS.
- **Layout du package** : structure sous module `github.com/oioio-space/unpixel`. Internes
  dans `internal/` : `imutil` (utilitaires image), `pixelate` (pixelisation par blocs),
  `metric` (distance d'image ; défaut `pixelmatch`, fidèle à Jimp), `render` (pure-Go
  `golang.org/x/image/font/opentype` + Liberation Sans embarquée, compatible métriquement Arial),
  `search` (découverte offset + DFS guidée/beam/**mono**, fan-out goroutines), `lang` (prior
  bigramme + **wordlist cohérence**), **`secrets` (détecteur plausibilité : Luhn, formats structurés,
  mots de passe courants fr/en)**. Opérateurs : `pixelate` (mosaïque **+ flou gaussien/FastBlur**).
  Package `defaults` assure les dépendances et expose stratégie/métrique/opérateur. **CLI `cmd/unpixel`
  opérationnelle** (urfave/cli/v3). **Pooling buffers hot-path** (P4.8 : −8% DiscoverOffsets).
- **GoDoc/pkg.go.dev** : package et symboles exportés enrichis (overviews avec snippet d'usage,
  chaque symbole/champ/const documenté avec son contrat, `Example` exécutable). Qualité
  pkg.go.dev appliquée et documentée dans la gate style (`.claude/skills/go-style-guide`,
  pre-commit `style-checklist.md`).
- **README** : réécrit via skill `readme-author` (principes awesome-readme) : badges CI/Go
  Reference/Go Report Card/GPL-3.0, démo, features, install/usage vérifiés, config table,
  architecture, crédits/attribution.
- **Tests** : 150+ tests passants (`-race` propre). Couverture **~89%** ; seuil `COVER_MIN` à 85.
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

### 🔤 v0.3.0 — polices personnalisées & balayage (récupérer sans connaître la typo)

- [x] **Polices externes** : `render.NewXImageFromFonts(regular, bold)` + `defaults.RendererFromFonts`
      + option `unpixel.WithRenderer` ; flags CLI `--font` / `--font-bold` / `--font-size`.
      L'utilisateur fournit ses `.ttf` → aucun souci de licence côté projet.
- [x] **Letter-spacing** : `Style.LetterSpacing` (façon CSS, négatif possible) ; chemin zéro-spacing
      byte-identique (kerning préservé) → fixtures inchangées, hot-path neutre (benchstat).
- [x] **Balayage de polices** : `--font` répétable + `--font-dir` ; chaque police essayée **en
      parallèle dans un budget de cœurs** (pas de sur-souscription), classée par fidélité image
      entière. Helper lib `unpixel.RecoverMultiFont(ctx, img, []Renderer, …) []FontResult`.
      Le CLI délègue à ce helper (source unique).
- [x] **Score d'image entière `Result.BestTotal`** (`PipelineScorer.TotalScore` + interface
      `TotalScorer`, `RankFinal`) : départage la réponse finale — choisit la chaîne **complète**
      plutôt qu'un préfixe correct ou un faux-positif marginal, et permet de classer les polices.
      Réintroduit le `totalScore` retiré du chemin chaud en P4.1, **uniquement pour le classement
      final** (pas par-candidat) → benchstat GuidedSearch neutre.
- [x] **Garde-fous qualité** : avertir si aucune grille mosaïque détectée (`InferBlockSize==0`,
      image probablement non pixélisée) ; ne jamais sélectionner un résultat tout-espaces
      (`Substantive`) qui scorait 0 et donnait une confiance trompeuse de 1.

### 🌫️ Flou (Gaussian blur) — au-delà de la mosaïque

Le challenge **Bishop Fox** (`bf_challenge.png`) caviarde par **flou gaussien**, pas mosaïque.
Le flou est aussi déterministe (`B = K*L`) → même attaque generate-and-test (rendre → flouter →
comparer). Le `Pixelator` pluggable = l'opérateur de redaction, donc le flou s'y branche sans
toucher la recherche.

- [x] **Opérateur de flou** : `pixelate.NewGaussianBlur(sigma)` (séparable, bords clampés) +
      `defaults.GaussianBlur` / `defaults.BlockAverage` + option `WithPixelator`. Prouvé par un
      round-trip auto-cohérent (flouter « go »/« cat », le récupérer via le moteur inchangé, `BlockSize=1`).
- [x] **σ auto + détection** : `unpixel.InferBlurSigma` (σ ≈ contraste/(g_pic·√2π)) ; CLI
      `--redaction auto|mosaic|blur` + `--blur-sigma`. Zéro-config démontré : `--redaction blur`
      estime σ et balaie le bundle → récupère un secret flouté court sans police ni σ fournis.
- [~] **P3.10 — flou réel (`bf_challenge`)** : capacité en place + secrets floutés courts résolus
      zéro-config ; le **vrai défi** (ligne longue, charset/contenu arbitraires, σ≈5,6) reste
      niveau-recherche. Bloquants mesurés : (1) σ doit s'estimer sur la **région caviardée** (image
      entière biaisée par le texte net : 0,59 vs 5,59) → **localisation de région (P3.5)** ; (2) coût
      du flou ~50× la mosaïque/candidat (micro-opt FLOP-bound, sans gain) → **box-blur O(1) /
      compare en résolution réduite** ; (3) explosion combinatoire → **priors (LM P3.2, charset
      depuis le texte visible, fast-path monospace P3.14)**.

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
- [x] **P3.2 — détecteur de cohérence/validité** *(piste utilisateur n°2 — plus fort levier qualité)* :
      `e9615ca` — modèle de langue char-level (n-gram) pur-Go embarqué + wordlist 1400 mots anglais
      (DictionaryPrior, bonus additif par token connu). Double usage : (a) **prior** pour guider/
      élaguer la recherche, (b) **validation a posteriori** + confiance. Combiné à la distance image.
      Sous-tend P3.6/P3.8.
- [~] **P3.4 — auto-détection étendue des paramètres** *(piste n°3)* :
      - [x] **auto-contraste fond sombre** : `New` détecte un fond sombre (bordure) et inverse
            l'image (`InferDarkBackground`), pour les captures *dark-mode* (code !). Chemin clair
            inchangé (no-op) → aucune régression ; round-trip inversé testé.
      - [ ] couleur exacte texte/fond (au-delà du clair/sombre), taille de police (hauteur d'x),
            poids/gras, padding/baseline. Tout dérivé de l'image, surchargeable via `Config`.
      (block-size & offset déjà auto.)
- [~] **P3.3 — police comme dimension de recherche / auto-calibration** *(piste n°1 reformulée)* :
      - [x] **balayage de polices fournies** (v0.3.0) : `RecoverMultiFont` / `--font`(×N) / `--font-dir`
            testent chaque police, scorent par re-pixelisation (`BestTotal`), gardent la meilleure,
            en parallèle. (Pas de DeepFont : CNN exclu en pur-Go.)
      - [x] **bundler des polices redistribuables** (package `fonts`, ~2,2 Mo embarqués) : Liberation
            Sans/Serif/Mono (≈Arial/Times/Courier), Carlito (≈Calibri), Caladea (≈Cambria), Source
            Code Pro + JetBrains Mono (code) ; licences OFL-1.1/Apache-2.0 + `NOTICE.md`. Le CLI les
            balaie **par défaut** (sans `--font`) → vrai zéro-config ; lib `fonts.Renderers()`/`All()`.
      - [ ] **sonde bon-marché** pour élaguer les polices improbables avant la recherche complète
            (le balayage actuel teste toutes les polices ; OK pour ~7, à optimiser pour un gros bundle).

Priorité moyenne :
- [x] **P3.8 — confiance calibrée + abandon honnête** : `8a49453` — `reportConfidence`
      (verdict + `--min-confidence` honest-abort) fusionne distance image + LM + ambiguïté ;
      signale « incertain / probablement irrécupérable » plutôt qu'un best-guess trompeur.
- [x] **P3.5 — localisation auto de la zone caviardée** : `unpixel.LocateRedaction` (gradient de
      bord borné sur zone floutée/mosaïque) + câblage CLI → screenshot complet, pas une zone
      pré-découpée. Corrige le biais σ pleine-image (0,59 vs 5,59 sur la ligne floutée).
- [x] **P3.6 — escalade auto du charset** : `7629a76` — `runEscalation` (tier 1 bundle complet,
      verrouille la meilleure police, puis minuscules → alnum → ascii), déclenchée par la
      confiance ; `--escalate`. Évite l'explosion combinatoire.
- [x] **P3.7 — priors structurés / secrets** : `e9615ca` — Luhn, formats (UUID, token hex/base64,
      digits/PIN), 100+ mots de passe français courants (azerty, motdepasse, …), + wordlist 1400 mots
      anglais. Intégrés dans LM via `WithPriors(...func(string)float64)`. Priors additifs (jamais de
      pénalité) ; matrice 17/17 exact sur secrets.

Priorité basse / exploratoire :
- [ ] **P3.9 — entrée multi-images / vidéo** : exploiter le jitter sous-pixel entre plusieurs
      pixelisations du même texte (information réelle, pas d'hallucination — Positive Security).
- [x] **P3.10 — déconvolution Richardson-Lucy** (`1706ae3`). Spatial-domain RL (noyau Gaussian
      PSF, réutilise la séparable, aucune FFT). API : `pixelate.RichardsonLucy(src, sigma, iterations)` ;
      public `defaults.Deblur(img, sigma, iterations) *image.RGBA` ; CLI `--deblur N` (optionnel,
      off par défaut). Documenté EXPLORATORY : le chemin par défaut (render→blur→compare) plus fort
      pour récupérer redactions pixélisées/floues ; RL optimal pour cas low-noise PSF known.
- [x] **P3.11 — auto Top-K pruning** (`23dbb7e`). Quand LanguageModel défini ET charset ≥40
      runes ET CharsetTopK non pint : auto Top-K=24. Petits charsets inchangés (défaut exact).
      Large-charset GuidedDFS ~10.8× plus vite, −17% B/op. Perf sans perte recall.
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

- [x] **P3.13 — presets de charset** (déjà livré). `unpixel.CharsetLower`, `CharsetAlnum`,
      `CharsetASCII` ; flag `--charset-preset lower|alnum|ascii|code` (via `search.CharsetLower`
      et constants, escalade consciente du code).
- [x] **P3.14 — fast-path monospace** (déjà livré). `search.MonospaceStrategy` derrière
      l'interface `Strategy` ; `--strategy mono` au CLI. Détecte l'avance fixe, classe les cellules
      indépendamment en parallèle — supprime cascade d'erreur polices variable, énorme perf+précision.
- [ ] **P3.15 — cohérence/validité spécifique au code** : étend P3.2 avec (a) un n-gram
      caractère/token entraîné sur du code, (b) une **validation lexicale/syntaxique** — tokeniser
      (chrF, n-grams de tokens) et idéalement parser : Go via `go/scanner`+`go/parser` (dogfooding),
      autres langages via grammaires tree-sitter (GLR + error-recovery). Un candidat qui tokenise/
      parse vaut une confiance bien plus élevée.
- [x] **P3.3+ — polices monospace de code** (`833afbb`). Bundle gain Adwaita Mono (OFL-1.1) +
      Noto Sans Mono (Apache-2.0) ; auto-incluses en `fonts.All()/Renderers()` et balayage (~2 MB).
      Satisfait le sous-axe code avec polices étendues pour capturer plus de cas réels.

### ⚡ Phase 4 — performance de la recherche (mesurée d'abord, prouvée au benchstat)

Profilage CPU (pprof) : le **coût est dominé par la métrique** (`pixelmatch.Compare` ~72 % de la
recherche, `isAntiAliased` 36 % + `colorDelta` 29 %) et, au rendu, par `FillWhite` 22 % + la
rastérisation des glyphes ~30 %. Règle : **mesurer (pprof) → optimiser → benchstat** (gain
significatif, zéro régression alloc/débit) ; **aucune perte de qualité de récupération** (les tests
matrix + round-trip sont le garde-fou).

Faites (gains prouvés, sortie de récupération inchangée) :
- [x] **P4.1 — supprimer `totalScore` du chemin chaud** : c'était une 2e passe pixelmatch
      pleine-image, *display-only*, lue par personne. `Score` (signal d'élagage) inchangé.
      benchstat `DiscoverOffsets` : **−71 % sec/op** (98,6 ms → 28,6 ms), −38 % B/op, −31 % allocs.
- [x] **P4.2 — alignement des structures** (`betteralign`, `mise run align`/`align:check`) :
      empreinte mémoire réduite ; neutre en CPU (pas de régression), `-race` propre.

À faire (par ordre d'impact mesuré ; chacune prouvée au benchstat, récupération inchangée) :
- [x] **P4.3a — détection de grille (phase) + désinclinaison (deskew)** : `InferBlockGrid`
      détecte la taille ET l'origine (PhaseX/PhaseY) de la mosaïque + un score de confiance ;
      `New` redresse automatiquement une mosaïque pivotée (recherche d'angle ±12° maximisant
      l'homogénéité des blocs) **uniquement** sous garde stricte → les images alignées sur les
      axes restent **octet-pour-octet identiques** (matrice 310/310, panel 17/17 exact, deskew
      dormant sur tous les fixtures). C'est le levier honnête « détecte l'alignement & redresse
      l'image » : capacité ajoutée sur grilles hors-origine/pivotées, zéro perte sur le corpus.
      Introspection via `Engine.SkewInfo()`/`DeskewedImage()`.
- [~] **P4.3b — comparaison à la résolution des blocs** : **analysé en profondeur → non adopté**
      (mêmes raisons que le rejet déjà mesuré de la variante décorateur #4). L'idée : images
      constantes par bloc → 1 px/bloc donne le même ratio avec ~`blockSize²`× moins d'opérations.
      ⚠️ **Le métrique pixelmatch par défaut n'est PAS par-pixel** : `isAntiAliased` lit un
      voisinage 3×3, donc l'échantillonnage 1 px/bloc change le compte (faux). Exact seulement
      pour une métrique par-pixel (RGB, non-défaut) ou une réécriture pixelmatch « exact-rim »
      (~7× d'évals/cellule, pas 64×). Et seulement sur crops alignés (phase connue via P4.3a),
      sinon repli pleine résolution — **et la bande de comparaison du DFS fait ≈1 bloc de large**
      (gain marginal), tandis que `TotalScore` (grande région) est rare depuis P4.1. Donc gain
      bout-en-bout marginal sur le chemin par défaut pour un risque hot-path réel → conformément
      à la règle absolue (« prouver au benchstat, ne garder que sur gain significatif »), **le vrai
      levier perf du chemin par défaut est P4.10 (SIMD sur la boucle de comparaison)**, pas
      l'échantillonnage par bloc.
- [~] **P4.4 — métrique sans détection d'anti-aliasing** (`pixelmatch.IncludeAntiAlias`) :
      **MESURÉ** (expérience jetable, revertée). Désactiver `isAntiAliased` donne **Compare −44 %**
      (85→48 µs sur images différentes), **GuidedSearch −12 %** (1,18→1,04 ms), 0 alloc en plus, et
      **la matrice récupère 155/155 à l'identique**. ⚠️ **MAIS** ce n'est PAS exact : les pixels AA
      comptent alors comme des diffs → s'écarte de la sémantique fidèle `Jimp.diff` (pixelmatch
      `includeAA=false`), et 155 fixtures ne prouvent pas l'absence de perte de rappel sur des
      redactions réelles arbitraires. → **décision fidélité/qualité réservée à l'utilisateur** :
      (a) activer par défaut (gain net, matrice OK), (b) exposer en option opt-in (défaut = fidèle),
      ou (c) écarter pour rester fidèle. Recommandation : (b) opt-in `NewPixelmatchFast`.
- [x] **P4.5 — prédiction de la lettre suivante / Top-K charset guidé par le LM** : `629759f` —
      `Config.CharsetTopK` (opt-in, avec `LanguageModel`) n'évalue que les k caractères suivants
      les plus probables au lieu de tout le charset ; `topKChars` dans `evalChildren`/`...Par` ;
      CLI `--charset-topk`. Défaut 0 = neutre (benchstat GuidedSearch inchangé, recall préservé) ;
      compromis recall/vitesse mesuré (cas mono dur : 188 → 55 évals). Réf. originale ci-dessous : trier
      le charset par probabilité, essayer les plus probables d'abord, élaguer tôt ; combiner au
      prior LM (P3.2). Réduit le **nombre** de candidats (chacun = 1 rendu + 1 compare). Réf.
      Prob-Hashcat (RAID'24), hashcat per-position Markov.
- [x] **P4.6 — cache de rendu (`PipelineScorer.render`, clé = texte)** : `bdca2f0`. La découverte
      re-rendait le même texte 64× (offsets) et le `prevGuess` était re-rendu par candidat ; LRU
      256 entrées sous mutex → **discovery −15 %**, −16 % B/op. Exact (rendu indépendant de l'offset).
- [x] **P4.7 — `FillWhite` exponential-copy (memmove)** : `18749c3` — **−97 %** (6334→170 ns),
      render −30 %. Glyph-cache / rendu incrémental restent ouverts (gain marginal vs le cache P4.6
      qui élimine déjà le re-rendu) ; à mesurer avant d'investir.
- [x] **P4.x — `marginColumn` remplace `diffRed`+`Margins`** : `427a141`. `evalFromStage`
      construisait une image-diff pleine par candidat `prevGuess` alors que `Margins` n'en lit que
      la ligne médiane → calcul direct de la 1re colonne différente. **GuidedSearch DFS −16 %**.
      Exact (`marginColumn == Margins(diffRed)`).
- [x] **P4.x — pixelate via indexation directe `dst.Pix` + row-copy** : `9557cab`. Suppression de
      `blockMean`, somme par index `pix[off]`, 1re ligne remplie puis `copy` vers le bas →
      **Pixelate −58 %**, discovery −8 %. Exact.
- [x] **P4.8 — pooling des buffers image** : `d15e68a` — `sync.Pool` pour les scratch buffers non-fuyants
      (grille SSIM, blur temp, FastBlur) → **SSIM −18% sec/op** (allocs 2→0), **FastBlur −8.7%** (−67% B/op),
      **GaussianBlur −5.6%** (−87% B/op), end-to-end **GuidedSearch −2.6%**, **DiscoverOffsets −8.1%**.
- [ ] **P4.9 — PGO** (Go ≥1.21) : `default.pgo` issu d'une récupération représentative ; ~4,5 %+
      CPU-bound, risque quasi nul. Réf. Uber/Google.
- [x] **P4.10 (étape 1) — métrique pixelmatch ré-internalisée sur `*image.RGBA.Pix`** : le profil
      CPU montre que la comparaison `MatchPixel` (externe `orisano/pixelmatch`) = **57,7 %** du CPU,
      dont ~17,6 % de pur surcoût d'abstraction (la lib opère sur `image.Image` via un
      `imageLineReader`). On la réimplémente en interne (`internal/metric/pixelmatch.go`,
      `CountPixels`), **bit-pour-bit identique** (test différentiel ~570 cas + matrice 315/315
      inchangée), opérant directement sur `.Pix` avec une fenêtre glissante 5-lignes poolée.
      benchstat (vs HEAD, `-count 10`) : **Compare −16 à −27 %**, **0 alloc** (−100 %) ;
      end-to-end **DiscoverOffsets −4,4 %** (p=0,003, −47 % allocs, −11 % mémoire),
      **GuidedSearch −2,3 %** (p=0,04). Dépendance externe `orisano/pixelmatch` retirée du runtime
      (gardée test-only pour le différentiel). C'est aussi le **prérequis** pour vectoriser
      `colorDelta` (étape 2). Pur Go, zéro CGO.
- [~] **P4.10 (étape 2) — SIMD `colorDelta`** : **implémenté, mesuré → NON adopté (régression
      prouvée)**, dans la lignée de P4.4/P4.3b. Toute vectorisation impose une disposition SoA :
      pré-calculer Y/I/Q par pixel dans une fenêtre glissante, puis traiter la ligne en lanes. J'ai
      implémenté ce pré-calcul (bit-pour-bit, différentiel ~570 cas + matrice 315/315 inchangés) et
      l'ai benchmarké (`-count 12`, machine de référence) :
      **Pixelmatch/10pct_different +38 %**, **Pixelmatch/gradient +20 %**, **GuidedSearch +10 %**,
      **DiscoverOffsets +3,5 %** — une **régression nette sur tout le spectre**. Cause : le
      fast-path scalaire de `colorDelta` (`if pa==pb return 0`) **saute tout le calcul flottant pour
      les pixels identiques**, qui dominent les crops réels (marges/fond). Le SIMD (comme tout
      pré-calcul SoA) doit traiter **toutes** les lanes, donc refait ce travail évité ; il ne peut
      structurellement pas battre le saut scalaire data-dépendant sur cette charge. Le +3,5 % sur la
      métrique phare est la preuve directe. Conclusion : **pas d'asm AVX2 sur spéculation** — le coût
      (plan9 asm + fallback + détection CPU) violerait la règle « simplicité/moindre mécanisme » pour
      un gain mesuré négatif. Le benchmark représentatif `Pixelmatch_Distance/gradient` (chaque pixel
      diffère, régime bande-de-rédaction) est conservé. Reste ouvert (doc, non implémenté) : un noyau
      AVX2 par blocs de 4 pixels qui **saute les blocs identiques** pourrait préserver le fast-path —
      à n'envisager que sur preuve benchstat (voir `docs/ACCELERATION.md`). Go 1.26 `simd/archsimd`
      reste de toute façon inadapté (⚠️ `GOEXPERIMENT=simd`, AMD64-only, hors promesse de compat).
- [x] **P4.11 — intra-node parallel evalChildren** (`23dbb7e`). Paralléliser enfants d'un nœud
      DFS, capped par intraNodeWorkers (GOMAXPROCS / offset-level) → pas de sur-souscription.
      Large-charset single-offset ~1.5× plus vite ; défaut small-charset neutre ; `-race` propre.

### P5 — Récupération aveugle des redactions réelles (issu de l'échantillon GIMP « Hello World ! »)

Contexte : `testdata/fixtures/text_hello-world.png` (capture GIMP réelle) est **confirmé** par le
modèle direct (pixelmatch **0,0000** avec Noto Sans Mono + `LinearBlockAverage`), mais la
**recherche de bout en bout ne le retrouve pas seule**. Le déblocage clé manquant — la
**pixelisation en lumière linéaire** (GEGL/GIMP/CSS) — est livré (`pixelate.NewLinearBlockAverage`,
`defaults.LinearBlockAverage`, flag `--gamma`). Restent les chantiers d'autonomie suivants, par
ordre d'impact (chacun pur-Go/zéro-CGO, prouvé au benchstat, récupération inchangée) :

- [ ] **P5.1 — auto-détection sRGB vs lumière linéaire.** Choisir automatiquement entre
      `BlockAverage` (sRGB) et `LinearBlockAverage` (GEGL) — p.ex. essayer les deux et garder le
      meilleur score d'image entière, ou détecter la signature « blocs plus clairs ». Aujourd'hui
      l'utilisateur doit passer `--gamma`. *Prérequis du décodage zéro-config de ce type d'image.*
- [ ] **P5.2 — localisation mosaïque-aware + recadrage auto.** `LocateRedaction` est réglé pour le
      **flou** (faible gradient) et **tronque** une mosaïque nette (il rate le « ! » : x≤985 vs
      contenu réel x≤1177). Ajouter un localisateur de bande mosaïque (bbox du contenu aligné sur la
      grille de blocs détectée via `InferBlockGrid`) et recadrer avant la recherche, pour les
      captures plein écran avec grandes marges.
- [ ] **P5.3 — calibrage typographique automatique.** Estimer taille de police, étirement
      horizontal anisotrope (la mise à l'échelle GIMP était ~1,06× plus large que haute) et phase de
      grille à partir de la géométrie de l'image (pas via la réponse). Aujourd'hui fournis à la main
      (≈124 pt, ×1,06, bloc 32). `InferFontSize` sur-estime sur mosaïque très claire (96 px → 104 pt
      au lieu de ~62×2) — à fiabiliser.
- [ ] **P5.4 — stratégie de recherche pour texte long et peu encré.** La DFS guidée/beam
      incrémentale **se piège sur les glyphes fins** (« l », espace, « ! » battent « H ») car le
      signal par-caractère est trop faible sur une mosaïque claire ; le signal discriminant
      n'existe qu'au niveau de la **chaîne entière** (SSIM 0,99 pour la bonne chaîne, vs ~0,007 d'écart
      entre voisins). Pistes : **segmentation en mots** (récupérer chaque mot court séparément),
      **scoring image-entière / ré-classement de candidats** (générer un pool puis classer par score
      global), ou un prior de langue/wordlist dominant. *C'est le verrou principal du décodage
      réellement aveugle des textes de 10+ caractères monospace.*
- [ ] **P5.5 — exposer le pipeline « capture réelle » de bout en bout.** Un helper/CLI qui enchaîne
      localisation (P5.2) → calibrage (P5.3) → choix gamma (P5.1) → recherche adaptée (P5.4), pour
      passer d'une capture brute à la récupération sans paramètres manuels.

## 🧭 Décisions clés

- **Repo public** ; **v0.1.0** (premier module public), **v0.2.0** (Phase 2 + CLI), **v0.3.0**
  (polices custom + balayage), **v0.4.0** (flou gaussien + zéro-config) publiées sur pkg.go.dev.
  API stable pré-1.0, additive (peut évoluer avant 1.0.0). Release auto par goreleaser sur tag
  `v*` (gated sur CI verte).
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
- `6a9e1ab` 2026-06-19 — perf(search): drop per-candidate totalScore (display-only) — ~3.4x faster discovery _(5 fichiers)_
- `598304d` 2026-06-19 — perf(layout): align struct fields with betteralign + add mise align tasks _(6 fichiers)_
- `3ba80c3` 2026-06-19 — docs(progress): add Phase 4 — search performance roadmap _(1 fichiers)_
- `18749c3` 2026-06-19 — perf(imutil): FillWhite via exponential copy — 37x faster, render -30% _(1 fichiers)_
- `1984b52` 2026-06-19 — chore(bench): persist per-commit perf stats (benchmarks/ + mise bench:record) _(4 fichiers)_
- `bdca2f0` 2026-06-19 — perf(search): per-scorer render cache — discovery -15%, exact (P4.6) _(4 fichiers)_
- `9557cab` 2026-06-19 — perf(pixelate): direct Pix indexing + row-copy fill — Pixelate -58%, discovery -8% _(4 fichiers)_
- `427a141` 2026-06-19 — perf(search): marginColumn replaces diffRed+Margins — guided DFS -16% _(7 fichiers)_
- `d5136b5` 2026-06-19 — perf(search): fuse step-9 band+trim into one Crop — -8% allocs (exact) _(4 fichiers)_
- `09b716b` 2026-06-19 — docs(perf): record measured P4.4 (AA-off) tradeoff + session perf summary _(2 fichiers)_
- `177cc2f` 2026-06-19 — perf(imutil): FillWhite exponential-copy fill — ~97% faster (exact) _(2 fichiers)_
- `7c0c2c9` 2026-06-19 — feat(cli): warn when no mosaic grid is detected (likely non-pixelated input) _(3 fichiers)_
- `4d93016` 2026-06-19 — feat(search): never select an all-whitespace guess as the recovery _(4 fichiers)_
- `cbc6769` 2026-06-19 — feat(render): custom fonts + letter-spacing to target real redactions _(6 fichiers)_
- `ff08327` 2026-06-19 — feat(search): whole-image TotalScore to pick the complete answer _(9 fichiers)_
- `e4a1a5c` 2026-06-19 — feat(cli): sweep multiple fonts and keep the best fit _(5 fichiers)_
- `7444a59` 2026-06-19 — perf(cli): run the font sweep in parallel within a core budget _(2 fichiers)_
- `3711b13` 2026-06-19 — perf(cli): run the font sweep in parallel within a core budget _(2 fichiers)_
- `3071b26` 2026-06-19 — feat(api): RecoverMultiFont — library multi-font sweep _(4 fichiers)_
- `b4e6339` 2026-06-19 — refactor(cli): drive the font sweep through unpixel.RecoverMultiFont _(3 fichiers)_
- `620a294` 2026-06-19 — docs(readme): document custom fonts and the font sweep _(2 fichiers)_
- `e6b7f06` 2026-06-19 — docs(progress): record the v0.3.0 release (custom fonts & font sweep) _(1 fichiers)_
- `c279a8e` 2026-06-19 — feat(fonts): bundle redistributable fonts for a zero-config sweep _(16 fichiers)_
- `74b5f1c` 2026-06-19 — feat(pixelate): Gaussian-blur redaction operator (attack blurred text) _(7 fichiers)_
- `78ecfa7` 2026-06-19 — feat(cli): zero-config blur recovery (auto-detect + estimate sigma) _(5 fichiers)_
- `42af230` 2026-06-19 — docs: record blur support and the bf_challenge findings _(2 fichiers)_
- `af45057` 2026-06-19 — test(blur): blur recovery matrix as the quality guard (#7) _(1 fichiers)_
- `0ac9752` 2026-06-19 — perf(pixelate): FastBlur box-approx Gaussian, ~3x cheaper (#3) _(7 fichiers)_
- `b0a0891` 2026-06-19 — feat(locate): region localization for blurred screenshots (#1) _(4 fichiers)_
- `4385452` 2026-06-19 — feat(calibrate): infer font size from content height (#2) _(4 fichiers)_
- `94219bf` 2026-06-19 — feat(lang): char-bigram language prior to break recovery ties (#5) _(10 fichiers)_
- `e9d5763` 2026-06-19 — feat(search): monospace fast-path strategy (#6) _(5 fichiers)_
- `e207275` 2026-06-19 — docs: delta vs current version and vs Bishop Fox unredacter _(1 fichiers)_
- `c25bb16` 2026-06-19 — docs(delta): add performance comparison vs Bishop Fox unredacter _(1 fichiers)_
- `a74f6b3` 2026-06-19 — docs: update README and PROGRESS for v0.4.0 _(2 fichiers)_
- `d7544f2` 2026-06-19 — docs(progress): align stale figures with v0.4.0 _(1 fichiers)_
- `8a49453` 2026-06-19 — feat(confidence): calibrated confidence + honest abort (P3.8) _(5 fichiers)_
- `7629a76` 2026-06-19 — feat(cli): automatic charset escalation (P3.6) _(1 fichiers)_
- `629759f` 2026-06-19 — feat(search): language-guided charset Top-K pruning (P4.5) _(6 fichiers)_
- `e83ba16` 2026-06-19 — docs(progress): mark P3.5/P3.6/P3.8/P4.5 done _(1 fichiers)_
- `e109df5` 2026-06-19 — test(cli): cover runEscalation tier walk (P3.6) _(1 fichiers)_
- `9b160a0` 2026-06-19 — docs: add DELTA.md (v0.4.0 vs v0.3.0 and vs Bishop Fox) _(2 fichiers)_
- `f19c704` 2026-06-19 — feat(bench): recovery quality+speed panel + version tracking + docs-sync hook _(11 fichiers)_
- `d15e68a` 2026-06-19 — perf(hotpath): pool transient scratch buffers (P4.8) _(3 fichiers)_
- `e9615ca` 2026-06-19 — feat(search): candidate plausibility priors — secrets (P3.7) + dictionary (P3.2) _(16 fichiers)_
- `59b6005` 2026-06-19 — docs: record priors (P3.2/P3.7) + pooling (P4.8) + panel version row _(5 fichiers)_
- `1706ae3` 2026-06-19 — feat(pixelate): Richardson-Lucy deconvolution + Deblur API/CLI (P3.10) _(6 fichiers)_
- `23dbb7e` 2026-06-19 — perf(search): auto Top-K pruning (P3.11) + intra-node parallel eval (P4.11) _(5 fichiers)_
- `833afbb` 2026-06-19 — feat(fonts): add Adwaita Mono + Noto Sans Mono to the bundle (P3.3+) _(4 fichiers)_
- `29e1327` 2026-06-19 — test(real): add real-world blurred sample + locate/infer fixture _(2 fichiers)_
- `b991063` 2026-06-19 — feat(grid): block-grid phase detection + skew deskew (P4.3a) _(5 fichiers)_
- `60959e2` 2026-06-19 — perf(metric): in-repo pixelmatch on *image.RGBA.Pix (P4.10 step 1) _(5 fichiers)_
- `12edfde` 2026-06-19 — docs: no-CGO GPU vs SIMD acceleration study + proposals (ACCELERATION.md) _(1 fichiers)_
- `6ed5806` 2026-06-20 — docs: no-CGO GPU vs SIMD acceleration study + proposals (ACCELERATION.md) _(2 fichiers)_
- `959654e` 2026-06-20 — perf(metric): measure SIMD colorDelta prerequisite → not adopted (P4.10 step 2) _(3 fichiers)_
- `f9ce9d4` 2026-06-20 — feat(pixelate): linear-light mosaic + decode real GIMP "Hello World !" sample _(9 fichiers)_
- `f10d3bf` 2026-06-20 — test(fixtures): host the real "Hello World !" sample at the path the user referenced _(5 fichiers)_
