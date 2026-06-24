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
**v0.8.0 publiée** sur pkg.go.dev : récupération mosaïque + flou gaussien + zéro-config auto-détection,
**décodage aveugle bilingue FR/EN** (paquet `blind`), **décodeur monospace zéro-config** (paquet `mosaictext`),
**récupération flou zéro-config** (`RecoverBlurred` : σ-search adaptatif + beam à prior de langue),
**robustesse au bruit** (auto-débruitage médian) + **prior FR pondéré par fréquence**,
et **corpus de samples réels** organisés sous `testdata/real` avec manifeste (paramètres + ground truth).

- **Mosaïque linéaire (GEGL/GIMP) + échantillon réel "Hello World !"** : la plupart des outils
  (GIMP/GEGL Pixelize, CSS, navigateurs) moyennent les blocs en **lumière linéaire**, pas en sRGB —
  la moyenne d'un texte sombre sur fond clair y est nettement plus claire. Ajout de
  `pixelate.NewLinearBlockAverage` / `defaults.LinearBlockAverage` + flag CLI `--gamma` (chemin sRGB
  par défaut **inchangé/fidèle**). Échantillon réel `testdata/real/hello-world.png` (capture GIMP :
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
  complet vs v0.3.0 et vs Bishop Fox (perf + fonctionnalités) : `docs/comparison.md`.
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
      binaire Chrome au runtime/CI ; métriques edge-aware. Cf. `docs/architecture.md`.

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
      à n'envisager que sur preuve benchstat (voir `docs/performance.md`). Go 1.26 `simd/archsimd`
      reste de toute façon inadapté (⚠️ `GOEXPERIMENT=simd`, AMD64-only, hors promesse de compat).
- [x] **P4.11 — intra-node parallel evalChildren** (`23dbb7e`). Paralléliser enfants d'un nœud
      DFS, capped par intraNodeWorkers (GOMAXPROCS / offset-level) → pas de sur-souscription.
      Large-charset single-offset ~1.5× plus vite ; défaut small-charset neutre ; `-race` propre.

### ⚡ Accélération ~35 % — Scorer caches + mosaic AA-skip fidèle

Profiling + benchstat-proven round : **panel de récupération 1495 ms → 919 ms** (17/17 exact, fidelité 1.000), zéro perte qualité, auto-sélection en `defaults.Wire`.

**Caches d'étape** (internes, déjà notés) : `prevGuess` partial-stage cached per `(prevGuess, offset)` ; `BlueMargin` et bande rouge-crop memoïzed dans cache rendu → supprime ~|charset|× travail redondant per-candidat (~20,5 GB RGBA churn/run).

**`PixelmatchFast` — métrique mosaic AA-skip** : la mosaïque par bloc produit des images **block-constantes** (aucune vraie anti-aliasing) → `isAntiAliased` (60% du CPU métrique) est une dépense inutile sur ce chemin. Nouveau `metric.PixelmatchFast` ; auto-sélectionné par `defaults.Wire` selon le pixelator (mosaic=fast, blur=fidèle). **Outcome-identique** : aucune divergence récupération sur corpus entier (155 matrice inchangée, panel 17/17 exact). Le chemin **blur** (cross-render robustesse) garde `Pixelmatch` fidèle.

**Dead-ends honnêtes** : widening per-pixel cache (L1 regress, métrique memory-bound) et `sync.Pool` buffer pooling (contention sous fan-out GOMAXPROCS → regress parallel paths) — tous deux revertis, mesurés/documentés.

### Expérience : récupération sur 10 images réelles d'Internet

Contexte : nous avons testé UnPixel sur **10 images réelles** (5 mosaïques issues de l'outil unredacter/Depix de BishopFox, 5 flou de deepblur + référentiels texte-déflou) sans configuration manuelle. **Résultat zéro-config : 0/10 récupérées** — non pas un bug, mais une révélation des **limites opérationnelles du modèle generate-and-test** pur. Diagnostic approfondi identifié quatre **causes racines** :

1. **Auto-détection mosaïque vs flou** : nos détecteurs initiaux (GCD exact) et inférence σ confondaient les vraies mosaïques rééchantillonnées/compressées JPEG avec du flou, les envoyant au mauvais pipeline. ✅ **Résolu en P-D** : `InferBlockSizeRobust(img) (blockSize, support)` reconnaît les grilles rééchantillonnées/anti-aliasées, routant les vrais pixélés vers le pipeline mosaïque.

2. **Explosion combinatoire sur texte long** : notre DFS incrémentale cherche *par-caractère* ; un texte de 25 glyphes (« Hello from the other side ») = ~70²⁵ candidats en l'absence de constraints — intractable. Même avec prior de langue et charset, le signal de l'image *par-caractère* est trop faible sur les mosaïques claires (cf. P5.4).

3. **Fidélité de police dominant** : la police n'étant pas dans notre bundle (~70 % des captures réelles utilise une typo non-standard), le score d'image entière (render-repixelate-comparer) diverge ; la vraie réponse ne sort pas top-N.

4. **Flou réel n'est pas Gaussian** : la compression JPEG + flou de mouvement/défaut optique produisent une dégradation non-gaussienne dont le score de confiance ne franchit jamais le seuil.

**Plan à 4 phases pour étendre UnPixel SANS perte de qualité** (tous opt-in, pur-Go, régressés au banc) :

- **P-A — Décodeur de texte long monospace guidé par LM (beam search)** (✅ **livré**) : paquet `mosaictext` exposant `DecodeHMM(ctx, img, opts...)` — un décodeur beam gauche-à-droite qui fusionne le modèle de langue bigramme (transitions) avec la distance MSE de rendu exact (émissions par-cellule), évitant le Viterbi exact (les émissions ne sont pas indépendantes : l'interbloc couplage fait que la bigram domine à températures utiles). Largeur de beam par défaut 8, balaie toutes les polices monospace bundlées ET les deux modes sRGB/linéaire, sauf si une police ou fonte exacte est spécifiée. Livré avec options `WithLanguage`, `WithCharset`, `WithEmissionTemperature`, `WithFont` (nom bundlé monospace), `WithFontFile`/`WithFontFileBold` (TTF/OTF utilisateur). **Limitation honnête** : la fidélité de police domine — sur les mosaïques synthétiques dans les polices bundlées, le décodeur retrouve les textes complets ; sur les captures réelles dont la police n'est pas bundlée (m4 Consolas, m5 Sublime), la récupération est partielle/erronée. La fourniture de la police exacte via `--font` devrait récupérer ces cas. **Constatation** : une variante Viterbi-vraie avec approximation d'émissions par-cellule a été tentée et rejetée — le couplage au bloc rend les bigrams écrasants même à températures calibrées, tandis que le beam préserve le signal d'émission en commitant le contexte progressif. Zéro régression : synthétique 17/17 exact, couverture ≥85 %, CLI exposé `--decoder mono-hmm --lang en|fr [--font ttf/otf]`. Pur-Go, zéro-CGO.
- ✅ **P-B — Dépix-style reference-matching** : paquet `mosaictext` exposant `DecodeReference(ctx, img, opts...)` — un décodeur géométrique qui synthétise des références par-police (rendre chaque glyphe, stocker la signature de bloc par-phase), puis matcher greedily colonne-par-colonne à travers le texte. Aucune hypothèse linguistique → récupère du **contenu arbitraire** (mots de passe, code, chaînes aléatoires) exactement quand la police est connue. Fonctionne aussi sur les **polices proportionnelles** (pas seulement monospace), élargissant au-delà du code. Livré avec options `WithRefCharset` (défaut ASCII), `WithRefFont` (nom bundlé), `WithRefFontFile`/`WithRefFontFileBold` (TTF/OTF utilisateur), `WithRefLinear` (tri-état auto/sRGB/linéaire). **Contrat de police** : si `--font ttf/otf` fourni (ou `WithRefFontFile`), seule cette police est utilisée ; sinon balaie toutes les polices bundlées × {sRGB, linéaire} et garde le meilleur match d'image entière. **Exact sur les fixtures auto-cohérentes** (« Pa55w0rd! » Liberation Sans proportionnelle, « X7kQ2mR9 » Liberation Mono → distance ≈0). **Limitation honnête** : la fidélité de police domine — sur les images réelles dont la police n'est pas bundlée (Notepad/Sublime/etc.), la récupération est inexacte. Le chemin exact-police (`--font yourfont.ttf` / `WithRefFontFile`) est la force technique et celui qui récupère quand la police est connue. Zéro régression : synthétique 17/17 exact, couverture ≥85 %, CLI exposé `--decoder ref-match [--font ttf/otf] [--charset chars]`. Pur-Go, zéro-CGO. **Bilan P-A/P-B/P-C/P-D** : les QUATRE phases sont maintenant livrées.
- **P-C — Normalisation d'entrée pour récupération de flou** (✅ **livré**) : paquet `internal/deblur` exposant estimation morphologique du fond via dilatation/ouverture, suppression du fond additif/multiplicatif, inversion automatique dark-theme, et normalisation optionnelle de contraste + déblockage médian + binarisation. Option API `unpixel.WithNormalize(...)` + flag CLI `--normalize` (avec sous-options `--normalize-bg divide|subtract|none`, `--normalize-binarize`, `--deblock N`). Appliquée uniquement dans `RecoverBlurred`, défini `Result.Normalized`. **Insight clé** : la normalisation est le levier, pas la déconvolution — la sharpening du rendu (Wiener/blind L0) lutte contre la boucle generate-and-test, donc P-C normalise l'entrée et alimente la σ-recherche existante inchangée. Wiener/L0 délibérément reportés. Validée sur fixtures synthétiques textured/vignette/dark/JPEG (échoue→récupère) ; sur les images réelles, la normalisation charge et normalise mais ne récupère pas seule b3/b4/b5 — les bloquants restent la fidélité de police (B/--font) et le flou très lourd (b4), adressés par P-B et l'approche cible. **Correction CLI associée** : décodeuse multi-formats (JPEG/GIF/WebP/BMP/TIFF en plus de PNG) ; b4/b5 chargeaient silencieusement avant.
- [ ] **P-D — Fondation & gains rapides** : auto-détection sRGB-vs-linéaire + localisation robuste ; meilleure surfacing des résultats (retourner le meilleur candidat *même en-dessous du seuil* avec `Result.BelowThreshold`, pour analyses exploratoires) ; outils de mesure réelle (`wild_test.go` derrière le build tag `wild` : 10 images réelles, ground truths quand connus, per-image BestGuess/Confidence/BelowThreshold).

### P5 — Récupération aveugle des redactions réelles (issu de l'échantillon GIMP « Hello World ! »)

Contexte : `testdata/real/hello-world.png` (capture GIMP réelle) est **confirmé** par le
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

### P6 — Décodage aveugle guidé par un prior linguistique (le verrou P5.4, conçu en détail)

Confirmé sur un 2ᵉ échantillon réel : `testdata/real/marx.png` (GIMP, Sans-serif 62 px, bloc
**19×19**, décalage **(5,5)**, isotrope) est reproduit par le modèle direct à **98,4 % de fidélité**
(distance linéaire 0,0163 ; near-miss au niveau du **mot** 4,7× pire ; sRGB 8–13× pire → la lumière
linéaire est bien le bon modèle). La **géométrie** se calibre déjà en zéro-config (`LocateRedaction`
→ `InferBlockSize`=19 ✓, `InferFontSize`=62 px ✓ sur le crop). **Seul le texte ne se récupère pas
en aveugle** : brute-force sur ~60 caractères de français (charset ~70, accents/majuscules/ponctuation)
= espace combinatoire impraticable, et le signal discriminant n'existe qu'au niveau de la chaîne entière.

**Idée directrice — fusionner deux signaux complémentaires dans un beam search.** Sur une mosaïque
claire le signal *par-caractère* de l'image est trop faible (c'est l'échec documenté de la DFS), mais
un modèle de langue *connaît* le caractère/mot suivant ; inversement l'image fournit la cohérence
*globale* que le langage seul n'a pas. On score chaque hypothèse partielle `s` par un coût fusionné :

> `cost(s) = α · imageCost(s) + β · (−log P(s))`

où `imageCost` est notre **forward model exact** (render → `LinearBlockAverage` au bloc/décalage
détectés → distance image, balayage phase-grille + glissement — déjà implémenté) et `P(s)` le prior
de langue. C'est l'émission de DepixHMM¹ remplacée par notre modèle direct (bien plus fidèle que son
clustering k-means des blocs), et son émission bigramme apprise remplacée par un prior d'ordre variable.

**Architecture (pure-Go / zéro-CGO, branchée sur les interfaces `Strategy`/`Metric` existantes) :**

- [x] **P6.1 — segmentation en lignes & mots.** ✅ réalisé — `internal/segment` : `Lines`/`Words`/`Segment` + découpe par largeur rendue (k=0,15·H). 5/5 tests, ~24 µs/op.
- [x] **P6.2 — bilingue FR+EN (char + dict).** ✅ réalisé — `internal/lang` : type `Language` (English/French, ParseLanguage), dicos FR (~2021 mots accentués) + EN, infini-gram unicode-safe via `index/suffixarray`, prior fusionné `PriorFor`. Mesuré : FR correct −2,71 > mélangé −3,05 > EN −4,36.
- [x] **P6.3 — décodage mot-par-mot.** ✅ réalisé — `internal/blinddecode.DecodeWord` : candidats = dico filtrés par largeur, scorés par distance image. Récupère « histoire »/« history » en top-1.
- [x] **P6.4 — ré-classement ligne entière.** ✅ réalisé — `internal/blinddecode.DecodeLineWhole` : beam sur combinaisons de mots, chaque hypothèse rendue+pixelisée+comparée (SSIM), signaux globaux. Top-1 exact sur synthétique, distance 0,000.
- [x] **P6.5 — balayage famille de police.** ✅ réalisé — `BundledRenderers(styles…)` + `blinddecode.Recover` balaie sans/serif/mono, garde le meilleur par score ligne-entière.
- [x] **P6.6 — API publique + CLI + bilingue.** ✅ réalisé — paquet public `blind` (`blind.Recover(ctx, img, opts...)`) avec `WithLanguage`/`WithBlock`/`WithOffset`/`WithFontSize`/`WithLinear`/`WithFonts`/`WithMetric`. Auto-détection bloc/police/taille. CLI `unpixel --blind --lang fr|en image.png`. Tests E2E FR/EN, distance 0,000, ~1,5–2 s chacun.

**Réalisé (synthèse)** : pipeline aveugle complet fonctionnel (segmentation → décodage mot-à-mot → ré-classement ligne-entière) + balayage de police + support bilingue FR+EN embarqué. Preuve de concept sur synthétique exact (SSIM 0,000, FR « le chat » + EN « the cat » récupérés de bout en bout, ~1,5–2 s). **Caveats honnêtes** : (1) **police** — validé sur les polices **embarquées** (sans/serif/mono) ; sur une capture réelle dont la police n'est pas dans le lot (p.ex. Noto Sans pour `docs/text-citation-marx.png`) la récupération est partielle. (2) **ponctuation/apostrophe** hors-dico (« l'histoire », « ! ») non gérée. (3) **performance** — le décodage aveugle d'une grande image réelle (marx, 1450×509, 2 lignes longues, balayage de police) **dépasse 600 s** : beam ligne-entière × pool de candidats × balayage de police est coûteux. Optimisations nécessaires avant usage réel : prefiltre de candidats plus agressif, recadrage/sous-échantillonnage, restreindre le balayage de police une fois la famille identifiée. (4) **prochaine brique** P6.2(a) : étage *mot* à fréquences pondérées (Lexique383) — le dico est aujourd'hui non-pondéré.

¹ JonasSchatz/DepixHMM (HMM + Viterbi, émission k-means) ; fondé sur Hill, Zhou, Saul & Shacham,
« On the (In)effectiveness of Mosaicing and Blurring as Tools for Document Redaction ».
² nathan-barry/tiny-infini-gram (infini-gram pur-Go via suffix array, backoff d'ordre variable).

### P7 — Robustesse des entrées : bruit, flou, prior pondéré (tri d'une proposition ONNX externe)

Une proposition externe (super-résolution **ESRGAN** + OCR **EMNIST** via `onnx-go`/`gorgonnx`) a été **évaluée et rejetée** comme architecture : `onnx-go` est archivé (mai 2024) et `gorgonnx` ne couvre que ~25 % des opérateurs ONNX, alors qu'ESRGAN ≈ 1200 opérations → infaisable ; aucun runtime ONNX **réellement pur-Go** n'existe (`yalue` = CGO ; `onnxruntime-purego` charge la `.so` native, instable) → casse la contrainte binaire-unique ; et la **super-résolution hallucine**, elle **n'inverse pas** une mosaïque (moyenne lossy) — l'inverse correct est notre generate-and-test. Bribes réellement utiles retenues + investigation flou :

- [x] **P7.1 — débruitage en prétraitement (filtre médian, pur-Go).** ✅ réalisé — `imutil.Median(src, radius)` (médian par canal, bords clampés, 2 allocs/op) + option opt-in `blind.WithDenoise(radius)` appliquée avant détection (défaut 0 = off, aucun impact existant). Amélioration prouvée : `segment.Lines` 2 (propre) → 1 (3 % poivre) → **2 après Median** ; plumbing −64,5 % de pixels divergents. Bench : r1 ~880 µs, r2 ~2,29 ms.
- [x] **P7.2 — dico FR pondéré par fréquence (= P6.2(a)).** ✅ réalisé — `freq_fr.txt` (321 mots, ordonnés par fréquence, projet-authored, pas de dépendance externe/Lexique) + `FreqWeight`/`(*Dict).WeightedScore` (Zipf `w=(log(F+1)−log r)/log(F+1)`, base 0,15 OOV-in-dico). `PriorFor(French)` pondère le dico (anglais inchangé). Amélioration prouvée : « il est là » vs « il tes là » — dico uniforme **égaux** (1,0=1,0), pondéré **séparés** (+23 %), `PriorFor` +0,26 vers le mot fréquent. Benchstat : aucune régression significative (FR 4,48→4,55 µs, p=0,063 ; allocs identiques).

**Auto-denoise zéro-config (P7 intégration)** : ✅ `unpixel.InferImpulseNoise` détecte le bruit impulsif (poivre-sel) ; `blind.Recover` l'applique **automatiquement par défaut** (optionnel `blind.WithDenoise(-1|0|N)` pour forcer/désactiver) via `imutil.Median`. Résultat enregistré : `Result.Denoise` (rayon appliqué, 0 = aucun). Images nettes lisent ~0 (pas de débruitage) ; bruitées ~5 % lisent ≥0.05 → rayon appliqué. CLI flag `--denoise` pour `--blind` : `-1` = auto (défaut), `0` = off, `N` = force N×N. Perf : InferImpulseNoise ~880 µs zéro allocs ; Median exact (fenêtre glissante, bords clampés, 2 allocs/op).
- [x] **P7.3 — flou zéro-config : σ comme dimension de recherche + prior de langue.** ✅ réalisé —
  - [x] **P7.3a — σ comme dimension de recherche.** Generate-and-test sur σ (comme `block` en mosaïque) : `unpixel.RecoverBlurred(ctx, img, opts...)` injecte `InferBlurSigma` (estimation initiale), puis balaye σ en recherche adaptative — fast-path si la fidélité est bonne au σ₀, fallback balayage borné sinon. Résultat enregistre `Result.BlurSigma`. ~3,5× plus rapide et −78 % allocs vs balayage naïf. Textes courts (4–5 caractères) à σ ∈ {2,3,4,6} : <1,2 s ; 7-char « connect » à σ=3 via beam+prior : ~9 s. σ=6 sur 7-char à la limite théorique (récupère mais marge mince, informatif). Pas de régression perf vs short cases (benchstat p=0,589). CLI `--redaction blur` avec `--blur-sigma` absent = zéro-config σ-search.
  - [x] **P7.3b — prior de langue pour le flou.** Le flou écrase le signal par-caractère comme la mosaïque → réutiliser l'infini-gram (P6) pour départager les chaînes. Beam search + **prior de langue fusionné au classement beam** (`topBeamLM`) : c'est cela qui récupère les mots plus longs (à σ=3, « cennect » score *mieux* sur pixels qu'« connect » car o≈e sous flou, seul le prior récupère la vraie réponse). Paquet `blind` réutilisé/adapté ; `RecoverBlurred` défaut = beam+prior. Fixtures flou synthétiques : `testdata/blur/` (textes {go,cat,hello,connect} × σ ∈ {2,3,4,6}, manifeste + générateur `//go:build ignore`), 13/13 récupérations aveugles. Pure-Go, aucun CGO.
- [ ] **P7.4 — (veille seulement) modèle appris.** À reconsidérer *uniquement* si un runtime **pur-Go, binaire unique** pour un **petit modèle de langue** émerge ; le besoin « vision » (deblur/OCR appris) reste hors de notre contrainte. Notre prior appris reste l'infini-gram, déjà pur-Go/zéro-dépendance.

### P8 — Améliorations Hill-2016 (analyse papier + décodeurs optionnels)

Analyse approfondie de Hill, Zhou, Saul & Shacham, « On the (In)effectiveness of Mosaicing and Blurring as Tools for Document Redaction » (PETS-2016). La majorité du papier décrit un **HMM avec émissions apprises (k-means des blocs) et transitions bigrammes**, nécessitant un **entraînement supervisé multi-font** — hors pur-Go/zéro-dépendance interne. **Caveats honnêtes** : (1) l'HMM complet avec colonne-ancrée aveugle (§4, section clé) requiert une **restructuration architecturale** pour manipuler les observations par colonne, non implémenté ; (2) benchmark d'appairage SICK/MICR (images réelles d'infrastructures) non livré (scope dépasse l'objectif). **Livrés — tous opt-in, pur-Go, zéro-CGO, panel 17/17 inchangé, couverture ≥86 %** :

- [x] **#3 — Remosaïque (§4 : blur→mosaic correctif).** ✅ `pixelate.NewBlurMosaic(sigma, blockSize, linear)` (composite flou + moyennes par bloc), options API `unpixel.WithRemosaic()` / `WithRemosaicGrid(b)` / `WithRemosaicLinear()`, flags CLI `--remosaic` / `--remosaic-grid N` / `--remosaic-linear`. Écroule l'écart σ-mismatch et le bruit JPEG en redonnant une structure par blocs post-flou. Sur synthétique auto-cohérent, le chemin **plain** converge déjà, donc le gain est **flou réel σ-mismatch/JPEG** ; jamais régrédient vs plain. Grille auto-détectée comme `max(2, round(σ))`.
- [x] **#4 — Estimation σ améliorée.** ✅ `InferBlurSigma` usa un percentile de gradient **adapté à la densité** (insight Polyblur/Chen-&-Ma gradient-ratio), précision ~±2 % sur σ∈{1,2,4,8} sur arêtes marches. Amorce la σ-recherche ; balayage adaptatif suite (P7.3a).
- [x] **#1 — Décodeur beam window-HMM.** ✅ `mosaictext.DecodeWindowHMM(ctx, img, opts...)` (variante **grid-window beam**, non le HMM k-means complet). Glisse une fenêtre sur les cellules grille et score chaque candidat caractère par MSE bloc-fenêtre → récupère des **mosaïques proportionnelles** (pas seulement monospace mono-hmm). API options `WithWHMMCharset` / `WithWHMMFont` / `WithWHMMFontFile`/`WithWHMMFontFileBold` / `WithWHMMLinear` / `WithWHMMBeamWidth` / `WithWHMMSeed`, CLI `--decoder window-hmm` avec charset/font/lang. **Limitation honnête** : c'est le **variant beam**, pas l'HMM appris avec k-means — l'HMM complet Hill (colonnes ancrées aveugles) nécessite redesign, donc `window-hmm` est le beam-fenêtre-grille (analogue `mono-hmm`). Récupération images réelles reste **fidelité-police-bornée** ; fournir la police exacte via `--font` recommandé.
- [x] **#5 — Score partial-credit journal.** ✅ `mise run journal` rapporte par-image **Levenshtein-based score** `100·(1−edit/len)` (moyenne + comptage ≥70 % « sensible ») par le seuil papier 70 %. Interne/dev-facing, bref rappel dans docs.
- [x] **#2 (HMM émissions k-means/Viterbi complet).** ✅ **Livré (cas chiffres, limites honnêtes)** — `mosaictext.DecodeTrainedHMM(ctx, img, opts...)` (genuine learned-emission HMM avec k-means clusters + Viterbi), API options `WithTHMMCharset` / `WithTHMMFont` / `WithTHMMFontFile`/`WithTHMMFontFileBold` / `WithTHMMLinear` / `WithTHMMK` / `WithTHMMW` / `WithTHMMCorpus` / `WithTHMMSeed`, CLI `--decoder trained-hmm` avec charset/font. Entraîne sur corpus rendu (chaînes aléatoires ~2000 de longueur variable), quantise fenêtres blocs en k-means (K défaut 128), accumule émissions/transitions/départ lissées Laplace, puis décode en **colonne-ancrée aveugles** (fenêtre glissante sur grille bloc, pas de limites caractères) via un seul pass Viterbi global. **Récupération exacte sur synthétique auto-cohérent** (chiffres/codes PIN rendus et repixelisés sur même grille/décalage, ~100 % sur digits). **Limites honnêtes** : (a) **Cas chiffres/codes seulement** — charset très étroit, émissions k-means modestes (~55 %) compensées par optimale Viterbi global, mais cassé hors-domaine (géométrie différente, police différente) ; Fig-14 sensibilité offset du papier observée (< 5 % sur fixtures parity papier indépendantes). (b) **Fragile à mismatch géométrie** — le modèle est entraîné sur tuple exact `(taille bloc, taille police, visage police, phase bloc)` ; images test indépendantes → précision s'effondre même avec même police. (c) **Pas généralisateur aux images réelles** — fidélité police et dérive géométrie dominent ; fournir la police exacte via `--font` obligatoire pour cas réels. (d) Caveats structuraux du papier conservés : observations colonne-ancrées/aveugles vs l'ancienne découverte offset-par-offset (refonte architecture déjà opérationnelle, pas blocage). **Parity numbers (SICK corpus + check digits)** : zéro-config **sick ≈ 28 %** / **digit ≈ 12 %** ; décodeur matched **sick ≈ 15 %** / **digit ≈ 0 %** — révélant gap robustesse offset/géométrie comme prochaine étape roadmap.
- [x] **#6 (benchmark SICK/MICR parity).** ✅ **Livré (corpus de parité + test journal)** — `testdata/sick/` corpus généré (SICK-corpus phrases FR/EN + chaînes chiffres générées `go generate`) ; `mise run journal` 5ᵉ corpus ajouté ; `paper_parity_test` rapporte défaut vs décodeur matched sur cibles papier. **Nombre actuels honnêtes** : zéro-config SICK ~28 % / digits ~12 % ; décodeurs matchés SICK ~15 % / digits ~0 % — surfaçant gap offset-robustesse/géométrie comme blocage principal P5 suivant.

### 🌐 Vague « décoder tout le testdata » (recherche cross-domaine, v0.12.0) — LIVRÉ

Issu de 4 agents de recherche en parallèle (SOTA depix · problèmes inverses cross-domaine ·
mur alignement/reconnaissance · out-of-the-box). Les 6 items, **tous opt-in, pur-Go, panel 17/17,
couverture ≥85 %, zéro nouvelle dépendance runtime** :

- [x] **#1 — Carte de capacité info-théorique** (`internal/capacity`) : classes de glyphes
      indistinguables après mosaïque, `BitsPerGlyph`, carte de confusion — triage de récupérabilité.
- [x] **#2 — Pré-filtre par chasse (advance-width)** (`internal/blinddecode`) : élague les
      candidats dont la largeur ne tient pas dans la bande ±1 bloc, avant le score image.
      **pool −58 %, DecodeLineWhole ×6.8 plus rapide**. (cf. attaque PDF arXiv 2206.02285 : 81 % via largeur seule).
- [x] **#3 — Viterbi fusionné au modèle de langue** (`internal/windowhmm` + trained-hmm) :
      `WithTHMMLMWeight` ; β=0 byte-identique, gate chiffres exact. Aide marginale sur émissions bruitées.
- [x] **#4 — Décodeur treillis DID** (`internal/did`, `--decoder did`, Kopec-Chou) : DP sur les
      colonnes-de-début, émission = forward model, frontières découvertes. **Récupère exactement
      le monospace ET le proportionnel court** (une première). Mur restant : voir ci-dessous.
- [x] **#5 — Calibrate-from-visible + Nelder-Mead** (`internal/varfont`) : ajuste la police sur le
      texte net visible ; optimiseur **×3 plus rapide** (8 vs 25 évals). Validé en synthétique.
- [x] **#6 — Déconvolution L0 texte** (`internal/deblur.TextL0`, `--l0-deblur`, Pan CVPR-2014) :
      non-aveugle, FFT autonome, MSE 613→45. Opt-in, défaut byte-identique.

### 🔭 Prochaines étapes (post-v0.12.0) — pour *réellement* décoder `real`/`wild`/`sick`

Les 6 items ci-dessus sont du gain **capacité + performance** ; ils ne déplacent **pas encore** les
corpus `real`/`wild`/`sick` du journal. Les murs restants, par ordre de levier (cf. mémoire
`decode-full-corpus-roadmap.md` et la section « Analyse de tendance » de `docs/JOURNAL.md`) :

- [ ] **DID — émission consciente du contexte aux frontières de blocs** *(le vrai déblocage `sick`)*.
      L'émission DID par-glyphe-isolé ne correspond pas à la pixelisation pleine-ligne quand un bloc
      chevauche deux glyphes (le mélange inter-frontières resurgit). Rendre le glyphe **avec ses voisins**
      dans les blocs-frontières (émission dépendante du contexte) ou décoder par paires. `sick` est
      polices embarquées → c'est le seul mur qui le sépare de la récupération.
- [ ] **Multi-frame sur captures réelles** (`internal/multiframe` existe) : ajoute de la *vraie*
      information, mais exige >1 trame ; le testdata est mono-trame → fournir un corpus multi-trames
      (vidéo / re-rendus à phases sous-bloc différentes) pour exploiter la fusion sous-pixel.
- [ ] **Élargir le bundle de polices libres** (DejaVu / Noto / Liberation…) + **calibrate-from-visible
      sur images à texte visible** : les polices GIMP réelles sont des familles libres courantes ;
      `CalibrateFromVisible` (#5) marche dès qu'une cible porte du clair adjacent (aucun fixture actuel n'en a).
- [ ] **Limite info-théorique** : certaines `wild` (mono-trame, gros blocs, contenu inconnu) sont
      proches de l'irrécupérable sans prior fort — utiliser la carte de capacité (#1) pour trier et
      fixer des attentes plutôt que sur-promettre.
- [ ] **Émissions HMM robustes au JPEG/offset** (P8 #2 suite) : généraliser le trained-HMM (alnum +
      augmentation JPEG + balayage de phase) — le gap offset/géométrie reste le blocage `wild`.

### 🧩 Décodage assisté par contexte — exploiter ce qui entoure la rédaction

Le mur dominant sur `real`/`wild` est la **fidélité de police** : on ne possède pas la fonte exacte,
donc le render→re-pixelise→compare ne matche jamais (journal v0.12.0 : `real` conf 1.000 /
fidélité 0.000 = « faux avec assurance »). La parade la plus puissante n'est pas un meilleur
score image, c'est d'**injecter du contexte présent dans l'image** pour *déterminer* ou
*reconstituer* la police, puis verrouiller cette police pour la zone rédigée. Briques déjà en
place à exploiter : `varfont.CalibrateFromVisible` (#5), `varfont.FitAxes`/VarRenderer (B1),
`fontrank` (B3), `internal/capacity` (#1).

**Fondé sur les signaux du journal (`docs/JOURNAL.md`, run v0.12.0) :**
- `real` **conf 1.000 / fidélité 0.000** + échecs `wrong-glyphs ×2` → mauvaise police, pas
  mauvaise géométrie : motive **C1** (déterminer la police par le clair) et **C2** (reconstituer).
- `sick` mode d'échec **`wrong-length ×9`** → frontières mal calées sur phrases proportionnelles :
  motive **C3** (contexte/format) en complément du DID context-aware.
- Table décodeurs : **`ref-match` 4/10 exact sur `sick`** (meilleur, 5 s) alors que le chemin cœur
  fait 0/10 → un **routage par contexte** vers le bon décodeur (et son renforcement) est un levier
  immédiat, traçable via la table décodeurs.
- `wild` **below-threshold ×3** (conf 0.527) → contenu/police inconnus : C2 + C4 (élagage) d'abord.

Propositions, par levier :

- [ ] **C1 — Police déterminée par un échantillon de texte net (calibrate-from-visible).**
      *La demande directe.* `varfont.CalibrateFromVisible` accepte **n'importe quel crop net +
      son texte connu** → deux sources de calibration, même moteur (objectif fort, méthode
      Bishop Fox/Kopec) :
      - **C1a — texte clair ADJACENT** (même image) : libellé/légende à côté du caviardage.
        Pipeline : détecter région nette + région mosaïque (`LocateRedaction`/segmentation) →
        calibrer sur les glyphes nets connus → verrouiller (police/taille/étirement-x/graisse/
        espacement) → décoder. Manque : détection auto des deux régions + CLI `--visible-text`.
      - **C1b — police fournie dans une AUTRE image** (échantillon séparé) : l'utilisateur donne
        une image rendant du texte dans la police cible + son texte ; on calibre dessus puis on
        décode le caviardage d'une image distincte. CLI `--font-sample <img> --font-sample-text`.
        C'est C1a avec un crop d'un fichier séparé — le moteur ne fait pas la différence.
      Manque surtout **un corpus de test** (cf. testdata ci-dessous, dont une paire
      échantillon↔caviardage). Impact : `real`/`wild`. Pur-Go, briques #5 prêtes.
- [ ] **C2 — Reconstitution de la police (font reconstruction).** Quand aucun clair n'est
      disponible mais la police est « courante » : (a) `fontrank` (B3) classe la **famille libre**
      la plus proche par empreinte glyphique ; (b) `FitAxes`/Nelder-Mead (B1/#5) **déforme une
      fonte variable** (wght/wdth/opsz/slnt) pour matcher la rédaction ; (c) on fige l'instance
      reconstituée comme renderer pour le décodage. Étendre : ajouter les axes `opsz`/`slnt`,
      élargir le bundle variable (Roboto Flex / Source Sans 3 VF), et calibrer sur le clair (C1)
      quand il existe. Manque : corpus de polices non-embarquées mais atteignables par axes.
- [ ] **C3 — Contexte linguistique partiel (cleartext partiel → contrainte).** Quand une partie
      du texte est connue (préfixe « https:// », libellé, mots voisins visibles, format
      « IBAN/UUID/date »), contraindre la recherche/le LM : préfixe figé, gabarit de format,
      n-grammes conditionnés au voisinage. Impact : `real` phrases, secrets structurés. Briques :
      `internal/lang` + les priors structurés existants.
- [ ] **C4 — Empreinte glyphique métrique depuis le clair → élagage du bundle (B3 piloté par
      le contexte).** Mesurer x-height/cap-height/chasse/serif sur les glyphes **nets** pour
      classer/élaguer les polices candidates *avant* le fit C2 (probe ~310× moins cher que décoder).

#### 🗂️ Testdata à compléter pour C1/C2 (corpus `context`)

- [ ] **Nouveau corpus `testdata/context/`** : images contenant **du texte clair + une zone
      caviardée dans la MÊME police/taille**, avec un manifeste donnant : texte visible + son
      rectangle, vérité-terrain du secret + son rectangle, police, taille, bloc, gamma. Générateur
      `internal/fixture/gencontext` (sur le modèle de `gensick`). Permet de **mesurer C1** :
      calibrer sur le clair → décoder le caviardé → comparer au secret. Quelques variantes :
      même-ligne (« User: ▓▓▓▓ »), libellé au-dessus, polices embarquées ET une variable
      (Nunito) pour C2.
- [ ] **Ajouter ce corpus au journal** (table décodeurs : `calibrate→context`) pour tracer C1/C2
      dans le temps comme les autres. *(fait : `8e7192a`)*

### ⚡ Optimisations de performance (candidates — audit 3 agents, RÈGLE : prouver au benchstat)

Audit lecture-seule (cœur · concurrence/pool/mémoire · internes décodeurs). **Aucune n'est
appliquée** : chacune doit passer `mise run bench:baseline` → change → `bench:compare`
(`-count≥12 -benchmem`, `-cpu` pour le parallélisme), gain significatif sans régression
alloc/débit, **panel 17/17 + matrix 310/310 inchangés**, `-race` propre. Les déjà-essayées-et-
rejetées (SIMD colorDelta, compare par-bloc, PGO) **ne sont pas** à refaire.

**Prérequis — combler les trous de benchmark (RÈGLE hot-path violée) :** `internal/windowhmm`
(zéro `Benchmark` : Viterbi + KMeans), `internal/did` (pas de `BenchmarkTrellisDP` isolé),
`internal/varfont` (le fitter est un hot loop sans bench). À ajouter AVANT d'optimiser ces zones.

**Tier 1 — fort impact ÷ effort :**
- [ ] **DID = vrai ICP** (`internal/did/did.go`, `mosaictext/did.go`) : aujourd'hui DP brute-force
      (émission complète render+pixelate+MSE pour chaque (glyphe,colonne) ≈ W·|charset| par phase),
      l'ICP annoncé n'est PAS implémenté. Ajouter un coût approché par **moyennes-de-blocs** (borne
      exacte car l'image pixelisée est constante par bloc) en pré-passe, puis rescore exact **le long
      du meilleur chemin seulement** (itération ICP) + élagage de colonnes (beam). Kopec : 3–25×,
      évite jusqu'à 99,9 % des comparaisons. *(F1, ~5–20×, le plus gros levier du code récent.)*
- [ ] **glyphMu → face par-P (sync.Pool)** (`internal/render/render.go`) : un mutex global sérialise
      tout le travail glyphe entre goroutines → goulot Amdahl du fan-out d'offsets. Face empruntée
      par goroutine (pool clé (bold,taille)). Preuve : `BenchmarkXImage_Render_Parallel` (`b.RunParallel`,
      `-cpu=1,4,8,20`). *(plus haute confiance ; le bench prouve la contention directement.)*
- [ ] **CachingScorer câblé dans GuidedStrategy** (`internal/search/strategy.go` vs `beam.go:196`) :
      le chemin PAR DÉFAUT n'a pas le cache stageImage (seul Beam l'a). *(H1 cœur.)*
- [ ] **blinddecode : cache de tuiles par-mot + composition** (`internal/blinddecode/wholeline.go`) :
      `scoreWholeLine` re-rend la phrase jointe entière + 2 scans `isInk` O(sx·imgH) par combinaison
      (≤500k). Rendre chaque mot du pool UNE fois (~470 vs 55k), composer les tuiles côte-à-côte.
      *(F3, ~50–100× de renders sur lignes 2 mots ; valider sur panel — ça gate la sortie.)*
- [ ] **varfont : Face réutilisée** (`internal/varfont/renderer.go`) : `NewFace` à chaque `Render`
      (le fitter fait ~3·axes·iters évals) → réutiliser/poolifier. *(H1 conc.)*
- [ ] **Métrique early-exit** (`internal/metric/pixelmatch.go`, chemin no-AA = défaut mosaïque) :
      plafond `maxDiff` → sortir dès `diff ≥ seuil·total` (la plupart des candidats sont rejetés).
      *(H4 cœur ; uniquement no-AA où le compte est monotone.)*
- [ ] **Câbler `fontrank`** (B3, `internal/fontrank` — code mort, zéro appelant) en pré-élagage
      avant le décodage coûteux (mosaictext/blinddecode), top-k généreux validé au panel. *(C1 conc.)*

**Tier 2 — moyen :**
- [ ] **DID : pixeliser seulement la bande de la chasse** (pas tout le canevas W) *(F2, 5–15×/émission).*
- [ ] **Paralléliser blinddecode** (produit cartésien + balayage de polices) façon `DiscoverOffsets`
      (slots disjoints) — **prérequis** : synchroniser `widthCache` (`blinddecode.go`, sinon data race). *(H3 conc.)*
- [ ] **Paralléliser le balayage `confusion` de mosaictext** (`recover.go`) — prérequis : `renderCache`
      concurrent (shardé). Fusionner les 2 niveaux de fan-out (counts×cells) en un seul budget. *(H2 conc.)*
- [ ] **Viterbi creux + hoist des splits de tuples** (`internal/windowhmm/model.go`) : O(T·S²)→O(T·E),
      table de tuples parsée une fois O(S²)→O(S). *(F4.)*
- [ ] **trainedhmm : supprimer la 2ᵉ passe de rendu du corpus** (enregistrer les spans en passe 1). *(F5, 2×.)*
- [ ] **Dé-verrouiller `bestSeenTracker` global** (atomic pointer) + compteur `evaluated` atomique
      (`internal/search/search.go`, `beam.go`) — la fusion déterministe ne dépend pas du tracker. *(H5.)*
- [ ] **Budget intra-node = min(Workers, offsets survivants)** (`search.go`) : nourrir le parallélisme
      intra-DFS quand peu d'offsets survivent (cœurs sinon oisifs). *(C3.)*
- [ ] **Pixelate : ne blanchir que la bande de padding** + `sync.Pool` du buffer dst (`pixelate.go`). *(H3 cœur.)*

**Tier 3 — micro / froid (barre plus basse) :**
- [ ] Scans directs `Pix[]` + break par-ligne dans `LeftEdge`/`Margins`/`marginColumn`/SSIM (`imutil`,
      `metric`, `scorer.go`) au lieu de `RGBAAt`. *(H2 cœur.)* · `unpixel.toRGBA` → `imutil.ToRGBA` *(C1).*
- [ ] deblur : tables de twiddles précalculées + scratch FFT réutilisé *(F7)* ; puis rfft 2× *(F8, effort élevé, froid).*
- [ ] mini-batch k-means *(F6, 10–100× lit., froid)* ; multiframe écritures `Pix[]` directes *(F10)* ;
      `GOMEMLIMIT≈1.5GiB` dans `scripts/gotest-caged.sh` (suivre le mur cgroup 2 G).
- [ ] préallocation `evalChildren` / nœuds non-boxés *(H5 cœur)* ; re-mesurer PGO (cœur métrique internalisé depuis).

**Déjà correct (ne pas « corriger ») :** deblur précalcule déjà la FFT du noyau (pas par-iter) et est
luma-only ; `capacity` est froid et honnêtement O(n²) borné ; glyphes DID pré-rendus une fois.
Détails + `file:line` + sources : voir [[unpixel-perf-roadmap]].

## 🧭 Décisions clés

- **Repo public** ; **v0.1.0** (premier module public), **v0.2.0** (Phase 2 + CLI), **v0.3.0**
  (polices custom + balayage), **v0.4.0** (flou gaussien + zéro-config), **v0.5.0** (déconvolution RL
  optionnelle + auto Top-K + parallelisme intra-node + bundle de polices élargi),
  **v0.6.0** (décodage aveugle bilingue FR/EN + paquet `mosaictext` zéro-config + samples réels
  organisés sous `testdata/real`), **v0.7.0** (robustesse entrées : prior FR pondéré par fréquence +
  débruitage médian auto-détecté zéro-config + flag `--denoise`), **v0.8.0** (récupération flou
  zéro-config P7.3 : `RecoverBlurred` σ-search adaptatif + beam à prior de langue intégré au tri),
  **v0.8.1** (perf : ~35 % plus rapide — métrique mosaic AA-skip auto-sélectionnée + caches d'étape
  du scorer, résultats bit-identiques, zéro perte de qualité), **v0.8.2** (ergonomie/perf :
  `RecoverBlurredFile`/`RecoverBlurredReader`, sentinelles `blind.DenoiseAuto/Off`, beam nil-LM
  fast-path ; + durcissement du gate de revue pre-commit), **v0.9.0** (initiative « images réelles
  d'Internet » : les quatre phases P-A/P-B/P-C/P-D — décodeur beam LM monospace `--decoder mono-hmm`,
  reference-matching Depix `--decoder ref-match` avec contrat de police utilisateur, normalisation
  d'entrée `--normalize` + décodage multi-formats JPEG/WebP/…, auto-détection mosaïque robuste +
  best-effort ; toutes opt-in, pur-Go, panel 17/17 inchangé, couverture 87 %), **v0.10.0**
  (recommandations du papier Hill-2016 : décodeur grille-fenêtre `--decoder window-hmm` pour polices
  proportionnelles, HMM à émissions apprises décodé en Viterbi aveugle `--decoder trained-hmm`
  (exact sur chiffres, fragile à la géométrie), mode de correction d'erreur par re-mosaïque
  `--remosaic`, meilleure estimation σ, désambiguïsation LM optionnelle sur ref-match (`--lang`),
  journal de tests évolutif `mise run journal` + corpus de parité SICK/MICR. **Constat honnête** :
  la géométrie/offset est robuste, mais la récupération de phrases proportionnelles à blocs grossiers
  reste non résolue ; le mur réel demeure la fidélité de police. Opt-in, panel 17/17, couverture ~86 %), **v0.11.0**
  (vague d'améliorations décodage — 9 fonctionnalités pur-Go opt-in, panel 17/17 inchangé, zéro régression. **Gains rapides (Q1–Q5)** : auto-gamma-selector (`--gamma auto` → sRGB vs linéaire, garde la meilleure distance), rappel de pool mot adaptatif (calibrage budget `effectivePoolK`), dicos bilingues 10k mots fréquence-pondérés (EN + FR via hermitdave/FrequencyWords), calibrage letter-spacing opt-in (`--letter-spacing-search`, enregistre `Result.LetterSpacing`), élision apostrophe français. Tous zero-config `blind`. **Grands paris (B1–B4)** : ajustement police variable (NEW décodeur `--decoder varfont` + descente coordonnées, méthode Bishop-Fox calibration), fusion multi-trames (inversion itérative arrière, sous-résolution bloc), classeur de police (~310× plus rapide que décodage, élagage balayage), enhancements HMM-entraîné (`mosaictext.WithTHMMLanguage` = corpus entraîné structuré-langue → n-grammes réels, `WithTHMMJPEG` = émissions augmentées JPEG), CLI `--thmm-lang`, `--thmm-jpeg`. **Constat honnête** : les gains rapides et grands paris **améliorent récupération de phrases courtes à police connue, robustesse calibrage et sélection police**. Ils ne **cassent pas** les murs réels/wild/sick — ceux-ci exigent de plus profonds bloquants : scoring par-mot single-bande + contexte/beam, variable-fonts couvrant internet réel. Tous opt-in, pur-Go (nouvelle dépendance pur-Go : go-text/typesetting pour varfont), panel 17/17, couverture ≥86 %),
  **vague décodage testdata (post-v0.11.0, unreleased)** (6 items architecturaux + perf : (1) **trieur par capacité** `internal/capacity` — rendus chaque glyphe charset, pixelisation, clustering signatures bloc en classes indistinguishables, rapporte BitsPerGlyph + carte confusabilités (quelles images récupérables) ; *(2) **préfiltre contrainte largeur-avance** `internal/blinddecode` — élaguer candidats mots dont l'avance rendue ne matche pas la bande ±1 bloc avant la notation image, **−58 % pool, 6.8× faster DecodeLineWhole** (21.3s→3.1s) opt-out `DisableWidthFilter` ; (3) **Viterbi bigramme-fusionné** `internal/windowhmm` HMM-entraîné (`WithTHMMLMWeight`, β=0 byte-identique) — marginal sur émissions bruitées ; (4) **décodeur DID** `internal/did` + `mosaictext.DecodeDID` trellis DP (glyph-start DP, émission render-pixelate-distance, transitions LM) — **exact monospace CLEAN + proportionnel court texte** (première fois, beam/HMM pas pu), honnête : mosaïque réelle/mauvaise boundary JPEF=context-aware-emission future ; (5) **calibration-depuis-visible + Nelder-Mead** `internal/varfont` Bishop-Fox méthode sur texte clair visible, Nelder-Mead optimizer no-new-dep, **3× faster** vs descente coordonnées (685µs vs 2.05ms, 6× moins eval) sans testdata image nearby ; synthétique validé ; (6) **déblur L0 non-aveugle** `internal/deblur.TextL0` Pan-CVPR-2014 σ connu auto-contenu FFT no-dep, **MSE synthétique 613→45**, défaut-off byte-identique. **Bilan honnête** : gains capacité + perf sur testdata ; **ne bougent pas réel/wild/sick** — bloquants : boundary-context-aware, visible-text adjacent, limites infothéo. Tous opt-in, pur-Go, panel 17/17, couverture ≥85 %)
  publiées sur pkg.go.dev.
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
- `986facb` 2026-06-20 — docs(progress): add P5 roadmap — blind recovery of real redactions _(1 fichiers)_
- `2b3d20b` 2026-06-20 — docs(progress): fix stale sample path (testdata/real → testdata/fixtures) _(1 fichiers)_
- `3ba57ec` 2026-06-21 — test(real): organize real mosaic samples under testdata/real with a manifest _(13 fichiers)_
- `7ac5d1b` 2026-06-21 — ci: restore Go cache before mise-action to fix tar 'File exists' _(1 fichiers)_
- `de77056` 2026-06-21 — docs(progress): add P7 roadmap — input robustness (noise/blur) + weighted prior _(1 fichiers)_
- `9188ba9` 2026-06-21 — feat(blind): zero-config auto-denoise + --denoise flag (v0.7.0) _(15 fichiers)_
- `6778128` 2026-06-22 — feat(blur): P7.3 zero-config blur recovery — σ-search + LM-blended beam _(28 fichiers)_
- `5c3c925` 2026-06-22 — perf(search): cache prevGuess stage + BlueMargin + redacted crop (bit-identical) _(4 fichiers)_
- `fe633fe` 2026-06-22 — test(coverage): cover empty-image guard + default constructors _(2 fichiers)_
- `3d9ce35` 2026-06-22 — refactor(review): apply retro-review fixes (perf, ergonomics, docs) _(7 fichiers)_
- `a33845d` 2026-06-22 — docs(release): v0.8.2 — blur file/reader helpers + denoise sentinels _(4 fichiers)_
- `63c0085` 2026-06-22 — feat(real-world): P-D foundation — robust mosaic auto-detect + best-effort surfacing _(19 fichiers)_
- `0aaf67c` 2026-06-22 — feat(real-world): P-A — LM-guided beam decoder for long monospace mosaic text _(11 fichiers)_
- `3bd4368` 2026-06-22 — feat(real-world): P-C — input normalization front-end for blur recovery + multi-format decode _(14 fichiers)_
- `97cd460` 2026-06-22 — feat(real-world): P-B — Depix-style reference-matching mosaic decoder (font-supply contract) _(10 fichiers)_
- `30ff9ab` 2026-06-22 — docs(release): v0.9.0 — real-world initiative (P-A/P-B/P-C/P-D) + coverage margin _(15 fichiers)_
- `366aac6` 2026-06-22 — docs(readme): refresh for v0.9.0 — real-world decoders, normalization, honest envelope _(2 fichiers)_
- `3d0374b` 2026-06-23 — feat(journal): evolving test journal over all corpora (zero-config vs best-config) _(10 fichiers)_
- `15a0c4c` 2026-06-23 — fix(search): trim phantom edge spaces from recovered text (journal finding) _(4 fichiers)_
- `986f4ca` 2026-06-23 — docs(journal): second run — edge-space trim improves blur recovery (+1 zero, +1 best) _(2 fichiers)_
- `7dd965a` 2026-06-23 — feat: Hill-2016 quick wins — partial-credit journal score (#5) + better σ estimation (#4) _(6 fichiers)_
- `eb5040b` 2026-06-23 — feat(blur): re-mosaic error-correction mode for blur recovery (Hill-2016 §4, #3) _(7 fichiers)_
- `15020a6` 2026-06-23 — feat(mosaictext): grid-window beam decoder — proportional-font mosaic recovery (Hill-2016 #1) _(8 fichiers)_
- `005c57b` 2026-06-23 — docs: Hill-2016 recommendations — window-hmm, remosaic, σ estimation, journal score _(2 fichiers)_
- `171f6f4` 2026-06-23 — feat(mosaictext): genuine learned-emission Viterbi HMM with blind decode (Hill-2016 #2) _(9 fichiers)_
- `226eb7e` 2026-06-23 — test(parity): SICK + check-number benchmark fixtures vs Hill-2016 (#6) _(20 fichiers)_
- `315ef53` 2026-06-23 — docs: trained-hmm decoder (#2) + SICK parity corpus (#6), with honest limits _(2 fichiers)_
- `b20ab1d` 2026-06-23 — feat(refmatch): opt-in LM-beam disambiguation; diagnosis — geometry is robust, coarse blocks are the wall _(5 fichiers)_
- `6cf50fe` 2026-06-23 — docs(release): v0.10.0 — Hill-2016 decoders, blur error-correction, evolving journal _(5 fichiers)_
- `e0284ed` 2026-06-23 — docs(journal): restore prior evolution rows lost to rm before v0.10.0 run _(2 fichiers)_
- `0501f26` 2026-06-23 — feat(journal): add trend-analysis section + Version column to evolution table _(3 fichiers)_
- `2b51ff2` 2026-06-23 — feat(blind): Q1 auto gamma selection (sRGB vs linear, keep best) _(4 fichiers)_
- `d3d6bcf` 2026-06-23 — feat(lang,blind): Q4 stronger prior + Q2 adaptive pool recall _(16 fichiers)_
- `dd6990d` 2026-06-23 — feat(blind): Q5 letter-spacing auto-calibration (opt-in) _(7 fichiers)_
- `a644eaf` 2026-06-23 — docs(journal): v0.11.0-dev run — quick wins land in blind path, core corpora flat (no regression) _(2 fichiers)_
- `570d77d` 2026-06-23 — feat(render): B1 spike — pure-Go variable-font instancing (go/no-go: GO) _(6 fichiers)_
- `a8a4bbf` 2026-06-23 — feat(varfont): B1 part 1 — variable-font renderer + coordinate-descent axis fitter _(10 fichiers)_
- `9fc6a72` 2026-06-23 — feat(mosaictext): B4 — language-structured + JPEG-robust trained HMM (opt-in) _(4 fichiers)_
- `47d9183` 2026-06-23 — feat(mosaictext): B1 part 2 — variable-font axis-fitting decoder (--decoder varfont) _(8 fichiers)_
- `0d6c0f1` 2026-06-23 — feat(fontrank): B3 — cheap exemplar visual font ranker (pre-decode pruning) _(3 fichiers)_
- `c2fff70` 2026-06-23 — feat(multiframe): B2 — multi-frame sub-pixel fusion (iterative back-projection) _(4 fichiers)_
- `4373fff` 2026-06-23 — feat(blinddecode): Q3 — French apostrophe-elision candidates in the blind path _(6 fichiers)_
- `f2f9513` 2026-06-23 — docs: v0.11.0 — document the decoding-improvement wave (Q1–Q5 + B1–B4) _(2 fichiers)_
- `5be028f` 2026-06-23 — docs(release): v0.11.0 — record recovery panel (17/17, fidelity 1.000) _(3 fichiers)_
- `23cb24a` 2026-06-24 — test: restore coverage ≥85% for the v0.11.0 wave _(7 fichiers)_
- `3eb9945` 2026-06-24 — feat(capacity): #1 information-theoretic capacity / triage map _(3 fichiers)_
- `2ae839b` 2026-06-24 — feat(blinddecode): #2 advance-width constraint pre-filter (quality + 6.8x perf) _(5 fichiers)_
- `84c45b7` 2026-06-24 — refactor: apply /simplify review findings (reuse/dedupe, behavior-preserving) _(14 fichiers)_
- `985ba3c` 2026-06-24 — fix(hooks): add commit-simplify-review so /simplify fires on every commit _(3 fichiers)_
- `60639d7` 2026-06-24 — feat(windowhmm): #3 language-model-fused Viterbi decode (opt-in) _(6 fichiers)_
- `5836600` 2026-06-24 — feat(did): #4 Document-Image-Decoding trellis decoder (--decoder did) _(7 fichiers)_
- `eaf2c7c` 2026-06-24 — feat(varfont): #5 calibrate-from-visible + Nelder-Mead optimizer (opt-in) _(8 fichiers)_
- `4b8e349` 2026-06-24 — feat(deblur): #6 non-blind L0 text deblurring (Pan CVPR-2014, opt-in) _(8 fichiers)_
- `3a6a69c` 2026-06-24 — test: restore coverage ≥85% for the roadmap wave (#1–#6) _(6 fichiers)_
- `9262991` 2026-06-24 — docs: document the decode-all-testdata wave (#1–#6) in README + PROGRESS _(2 fichiers)_
- `3f1be74` 2026-06-24 — docs(release): v0.12.0 — decode-all-testdata wave (#1–#6) + perf _(3 fichiers)_
- `2273387` 2026-06-24 — docs(progress): add forward-looking roadmap — v0.12.0 wave done + remaining blockers _(1 fichiers)_
- `3ddf48a` 2026-06-24 — feat(journal): track opt-in decoders over time (second evolution table) _(5 fichiers)_
- `4fdc373` 2026-06-24 — feat(journal): track failure-mode + confidence/fidelity/timing signals for analysis _(4 fichiers)_
- `d5fc8fa` 2026-06-24 — docs(journal): v0.12.0 run — decoder table + analysis signals populated _(2 fichiers)_
- `c9a914e` 2026-06-24 — docs(progress): propose context-assisted decoding (C1–C4), grounded in the journal _(1 fichiers)_
- `8f09a3a` 2026-06-24 — test(context): add testdata/context corpus for context-assisted decoding (C1/C2) _(16 fichiers)_
- `e29a0f7` 2026-06-24 — docs(progress): split C1 into C1a (adjacent cleartext) + C1b (separate font sample) _(1 fichiers)_
- `3237fec` 2026-06-24 — feat(cli,context): C1b — determine the font from a separate sample image _(8 fichiers)_
- `8e7192a` 2026-06-24 — feat(journal): track context calibrate-from-visible (C1a/C1b) in the decoder matrix _(3 fichiers)_
- `4655e51` 2026-06-24 — docs(progress): performance-optimization roadmap (3-agent audit, benchstat-gated) _(1 fichiers)_
- `8e09cb6` 2026-06-24 — perf(search): wire CachingScorer into GuidedStrategy (default path) — 2.4x warm _(3 fichiers)_
- `0cf2493` 2026-06-24 — perf(render): replace global glyphMu with per-(bold,size) face pool (lock-free hot path) _(3 fichiers)_
- `11cbe81` 2026-06-24 — perf(metric,search): early-exit ceiling on the no-AA pixel metric (3.7x rejected) _(9 fichiers)_
- `73c9206` 2026-06-24 — perf(varfont): reuse the font Face instead of allocating per Render (fitter hot loop) _(4 fichiers)_
- `1cfb4bb` 2026-06-24 — refactor: apply /simplify findings on the post-v0.12.0 changes (reuse/dedupe) _(3 fichiers)_
- `8ed0504` 2026-06-24 — test: restore coverage ≥85% for the perf + C1b wave _(7 fichiers)_
- `916630c` 2026-06-24 — perf(blinddecode): parallelize whole-line Cartesian scoring (-24 to -29% at cpu≥8) _(4 fichiers)_
- `3009962` 2026-06-24 — docs(journal): v0.13.0 run — perf wins, no quality regression; C1/C1b tracked _(2 fichiers)_
- `d8972af` 2026-06-24 — docs(release): v0.13.0 — performance wave + context calibration (C1b) + journal tracking _(3 fichiers)_
- `1cb2b59` 2026-06-24 — test(cover): restore coverage gate margin to 85.4% for the v0.13.0 release _(16 fichiers)_
- `153368e` 2026-06-24 — docs: rationalize documentation — neophyte README + linked concept tree _(18 fichiers)_
- `23f9fe7` 2026-06-24 — docs: adopt a more formal register across the reader-facing documentation _(11 fichiers)_
- `4806c4b` 2026-06-24 — build(hooks): make /simplify a mandatory gate on every commit path _(3 fichiers)_
- `243c865` 2026-06-24 — test(leak): add goroutine-leak gate via uber-go/goleak + a skill _(13 fichiers)_
- `77c2670` 2026-06-24 — fix(journal): main evolution row landed in the decoder table; record v0.13.0(+dev) _(5 fichiers)_
- `e6dcf1e` 2026-06-25 — feat(journal): surface the context corpus per-image + ctx·C1a column _(5 fichiers)_
- `55a13bf` 2026-06-25 — feat(rectify): planar-homography core for perspective-distorted decode (approach B) _(3 fichiers)_
