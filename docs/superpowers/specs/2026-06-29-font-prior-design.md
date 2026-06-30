# Spec — #4 Blind font prior (hybrid: heuristic now, ML-ready seam)

Date : 2026-06-29 · Statut : design approuvé (brainstorm) · Sous-projet Tier-2 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Opt-in / additif, cœur de recherche
intouché. #2/#1B/#1/#3/#6 mergés, CI verte.

## 1. Problème & objectif

Le moteur ne sait rendre que **9 polices empaquetées** (`fonts/embed/*`). Quand la police est
inconnue, le balayage multi-polices (`RecoverMultiFont`) décode TOUTES les polices puis classe par
fidélité finale — coûteux, et sans amorçage.

La littérature VFR (DeepFont, DINOv2-PEFT, `Storia-AI/font-classify`) classe les polices depuis du
**texte lisible** ; nos caviardages sont **mosaïqués** (le block-average détruit la forme des glyphes).
Double inadéquation : domaine d'entrée (lisible→mosaïque) **et** jeu de polices (3000→9). Importer un
classifieur pré-entraîné serait inutile. Sources : [DeepFont 2015](https://ar5iv.labs.arxiv.org/html/1507.03196),
[DINOv2-PEFT 2026](https://arxiv.org/pdf/2602.13889), [Reading≠Seeing 2026](https://arxiv.org/pdf/2603.08497).

Le signal adapté au domaine est la **signature pixelisée** : rendre l'exemplaire d'une police →
re-pixeliser à la taille de bloc détectée → comparer la distribution de luminance des blocs au
caviardage. Le dépôt l'implémente déjà en pur-Go (`internal/fontrank.RankFonts`, **déjà aveugle** :
aucune `known_text` requise), mais seulement exposé via l'outil MCP `rank_fonts`, **pas câblé** dans
le chemin de décodage.

**Objectif** : un **prior de police aveugle** qui (a) **réordonne** le balayage multi-polices (essaie
d'abord la police probable → résultat byte-identique, plus rapide par sortie anticipée) et (b)
**élague** optionnellement au top-K (plus rapide encore, peut changer le résultat ⇒ opt-in). Plus une
**couture ML** (interface) pour qu'un classifieur entraîné sur le domaine pixelisé s'y branche plus
tard sans toucher aux appelants — **sans construire le ML maintenant**.

### Non-objectifs
- Pas de classifieur pré-entraîné importé ; pas de jeu 3000 polices ; pas d'entraînement/poids/gonum
  maintenant (YAGNI). Couture seulement.
- Aucune régression : chemin mono-police `Recover` inchangé ⇒ panel 17/17 byte-identique. Le
  réordonnancement seul produit le même `BestGuess` ; seul l'élagage top-K opt-in peut changer le résultat.

## 2. Critères de succès (mesurables)

1. **Réordonnancement byte-identique** : `RecoverWithPrior` en mode réordonnancement renvoie le même
   `BestGuess` que `RecoverMultiFont` sur les mêmes polices (mêmes décodes, ordre différent).
2. **Qualité du prior** : sur des fixtures en-mémoire (rendu→pixelise de chaque police empaquetée), le
   prior classe la **vraie police en top-3** ; taux top-1/top-3 rapportés.
3. **Élagage top-K** : ne régresse pas quand `k` couvre le rang de la vraie police ; documenté qu'un `k`
   trop petit peut perdre la réponse.
4. **Panel 17/17** préservé (chemin par défaut inchangé). Pur-Go, cagé, fixtures en-mémoire, couverture
   ≥ 85 %.

## 3. Architecture & composants

Contrainte de cycle (vérifiée) : `internal/fontrank` importe la racine `unpixel` ; donc `unpixel`
**ne peut pas** importer le prior (cycle). `fonts` importe `defaults` ; donc `defaults` **ne peut pas**
importer `fonts`. Le foyer propre est un **nouveau package public de haut niveau `fontprior`** (comme
`defaults`/`fonts`/`blind`) qui peut importer `unpixel` + `fonts` + `internal/fontrank` sans cycle.

1. **Couture `fontprior` (nouveau package public)** :
   ```go
   package fontprior

   // Ranked is one prior result, best-first (lowest Score).
   type Ranked struct {
       Name  string  // bundled font name, e.g. "Liberation Sans"
       Score float64 // lower = better visual match (in [0,1] for the heuristic)
   }

   // Prior ranks candidate fonts by how well each explains the redaction, blind
   // (no known plaintext). blockSize is the mosaic block side in px (0 = auto-detect).
   type Prior interface {
       Rank(ctx context.Context, img image.Image, blockSize int, fonts []fonts.Font) ([]Ranked, error)
   }
   ```
   - **`Histogram` (impl pur-Go, défaut)** : enveloppe `internal/fontrank`. Mappe `[]fonts.Font` →
     `[]fontrank.NamedFont`, appelle le ranker histogramme (signature de luminance par bloc, déjà
     aveugle), remappe `[]fontrank.FontScore` → `[]Ranked`. Aucun nouvel algorithme.
   - **`Default() Prior`** renvoie `Histogram{}` (sans build tag) ; un fichier `ml.go` derrière
     `//go:build ml` pourra plus tard faire renvoyer un `mlPrior` à la place.

2. **Couture ML différée** (`fontprior/ml.go`, `//go:build ml`) : un stub `mlPrior` qui **documente** le
   contrat d'entraînement (synthétique render→pixelise→label-police, le renderer est le labelleur) et la
   forme de chargement des poids, et renvoie `ErrMLNotBuilt` tant qu'il n'est pas entraîné. **Aucun
   poids, aucun gonum, aucun entraînement maintenant.** Seule l'interface garantit le drop-in.

3. **Helper bloc-aware dans `internal/fontrank`** : ajouter `RankFontsAt(ctx, img, fonts, blockSize int)`
   qui saute la détection quand `blockSize>0` ; `RankFonts` existant délègue avec `0` (comportement
   inchangé). Évite une re-détection quand le moteur connaît déjà la taille de bloc.

4. **Entrée câblée `fontprior.RecoverWithPrior`** :
   ```go
   func RecoverWithPrior(ctx context.Context, img image.Image, opts ...unpixel.Option) ([]unpixel.FontResult, error)
   ```
   - Lit `fonts.All()`, classe via `Default().Rank(...)`, **réordonne TOUJOURS** la liste de polices
     (c'est la raison d'être de l'entrée — pas conditionné à une option).
   - Si `WithFontPriorTopK(k)` avec `0 < k < len`, **tronque** au top-K (sinon réordonnancement seul).
   - Construit les renderers (via `defaults.RendererFromFonts`) dans l'ordre classé, délègue à
     `unpixel.RecoverMultiFont`. Remappe `FontResult.Index` → nom de police pour les notes.
   - Échec du prior (rendu dégénéré, image vide) → repli sur le balayage complet non ordonné (jamais
     pire qu'aujourd'hui) ; 0/1 police → no-op.

5. **Options racine `unpixel`** (juste des champs Config, aucun import → pas de cycle) :
   - `WithFontPrior() Option` → `cfg.fontPrior = true`. **Drapeau de routage** consommé par les
     appelants (CLI `--font-prior`, MCP) pour choisir le chemin `RecoverWithPrior` ; réservé aussi à un
     futur auto-path. `RecoverWithPrior` lui-même réordonne toujours, indépendamment de ce drapeau.
   - `WithFontPriorTopK(k int) Option` → `cfg.fontPriorTopK = k` (élagage ; `k≤0` ou `k≥N` =
     réordonnancement seul, sans troncature).

6. **MCP** : `rank_fonts` gagne un **mode aveugle** (`known_text` devient optionnel → repli sur le prior
   histogramme seul, sans le score métrique qui exige les glyphes connus) ; `unpixel_decode` gagne
   `font_prior_top_k` (optionnel) sur le chemin multi-polices/engine.

7. **CLI** (`cmd/unpixel`) : `--font-prior` et `--font-prior-top-k N` → route vers
   `fontprior.RecoverWithPrior`.

Fichiers : `fontprior/fontprior.go` (interface + Histogram + Default + RecoverWithPrior) ;
`fontprior/ml.go` (`//go:build ml` stub) ; `internal/fontrank/fontrank.go` (`RankFontsAt`) ;
`unpixel.go` (2 options + 2 champs Config) ; `mcp/rankfonts.go` + `mcp/decode.go` (câblage) ;
`cmd/unpixel/main.go` (flags). Tests associés.

## 4. Limites assumées (documentées)

- **9 polices empaquetées seulement.** Le prior classe ce que le moteur peut rendre. Police réellement
  inconnue (hors des 9) → le prior choisit la plus proche des 9 ; la vraie récupération reste limitée
  par le jeu de polices, pas par le prior. (Mur honnête : police inconnue réelle.)
- **Signal histogramme** : sépare bien les grandes catégories (serif/sans/mono) ; le classement
  intra-famille est plus bruité (cf. doc `fontrank`). Le top-K (k≥3) absorbe ce bruit.
- **ML non livré** : la couture existe, l'entraînement domaine-pixelisé (nouveau dans la littérature)
  est un suivi (lié #5/#8).

## 5. Intégration & compatibilité

- Strictement opt-in : `Recover` mono-police et `RecoverMultiFont` inchangés ; `RecoverWithPrior` est une
  nouvelle entrée. Panel 17/17 byte-identique (aucun chemin par défaut modifié).
- Réordonnancement ⇒ même résultat que le balayage complet ; élagage top-K ⇒ opt-in, documenté.
- Pas de cycle d'import (foyer `fontprior` au-dessus de `fonts`/`fontrank`). Pas de CGO, pas de sidecar,
  pas de nouvelle dépendance (gonum non ajouté tant que le ML n'est pas construit).

## 6. Tests & validation (pur-Go, cagé, fixtures en-mémoire)

- `fontprior` : le prior Histogram classe la vraie police en top-3 sur des fixtures rendu→pixelise pour
  plusieurs polices empaquetées (top-1/top-3 rapportés) ; `RecoverWithPrior` réordonnancement-seul =
  même `BestGuess` que `RecoverMultiFont` ; top-K avec k couvrant le rang vrai ne régresse pas ; repli
  sur échec du prior testé.
- `fontrank` : `RankFontsAt(blockSize>0)` saute la détection et donne le même classement que `RankFonts`
  quand la taille passée == taille détectée ; bench de `RankFontsAt` (coût unique pré-recherche, pas la
  boucle interne).
- `mcp` : `rank_fonts` sans `known_text` renvoie un classement (mode aveugle) ; `unpixel_decode` avec
  `font_prior_top_k` route et annote.
- Couture ML : `Default()` renvoie l'impl Histogram en build normal ; un test `//go:build ml` (ou doc)
  vérifie que la couture compile derrière le tag (stub renvoie `ErrMLNotBuilt`).
- Couverture ≥ 85 % ; pur-Go ; panel 17/17 inchangé.

## 7. Découpage (unités isolées)

1. `internal/fontrank/fontrank.go` — `RankFontsAt(ctx, img, fonts, blockSize)` + `RankFonts` délègue 0.
2. `fontprior/fontprior.go` — `Ranked`, `Prior`, `Histogram`, `Default()`, `RecoverWithPrior` + champs
   lus depuis Config.
3. `unpixel.go` — `WithFontPrior`/`WithFontPriorTopK` + champs `fontPrior`/`fontPriorTopK`.
4. `fontprior/ml.go` — stub `//go:build ml` (couture seulement, `ErrMLNotBuilt`).
5. `mcp/` — `rank_fonts` mode aveugle + `unpixel_decode` `font_prior_top_k`.
6. `cmd/unpixel` — flags `--font-prior` / `--font-prior-top-k`.
7. Tests + validation (qualité prior, réordonnancement byte-identique, top-K, repli, bench, couverture).

## 8. Risques & parades

- **Cycle d'import** → foyer `fontprior` au-dessus de `fonts`/`internal/fontrank` ; options racine ne
  font que poser des champs ; vérifié par `go build ./...`.
- **Régression panel** → aucun chemin par défaut touché ; `RecoverWithPrior` est additif ; test
  réordonnancement byte-identique + panel 17/17.
- **Élagage perd la vraie police** → opt-in, off par défaut, k≥3 recommandé ; documenté ; repli complet
  sur échec du prior.
- **Prior trompé (intra-famille)** → top-K absorbe ; réordonnancement seul ne peut jamais perdre la
  réponse (toutes les polices restent décodées).
- **Sur-ingénierie ML** → seule l'interface ; aucun poids/gonum/entraînement ; stub derrière `//go:build ml`.
