# Spec — #8 Étude de fuite d'information (feasibility study)

Date : 2026-06-30 · Statut : design approuvé (brainstorm) · Sous-projet Tier-3 (dernier moonshot) du
programme « débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Additif, pur-Go, cœur intouché.
#2/#1B/#1/#3/#6/#4/#5/#7 mergés, CI verte.

## 1. Problème & objectif

#8 est un **paquet de 4 idées** d'inégale valeur (cf. exploration) :
1. **offset-as-multiframe** — DÉJÀ livré (`internal/multiframe` IBP, `mosaictext.DecodeMultiFrame`).
2. **moiré JPEG⊗mosaïque** & 3. **fuite sub-pixel d'anti-aliasing** — prémisse de RÉCUPÉRATION
   probablement fausse pour un mosaïque à moyenne-de-bloc.
4. **inverse-renderer neuronal amorti** — ML différé (la couture `//go:build ml` existe déjà ;
   borné par le mur d'information comme la diffusion #7).

Recherche : l'exploit de dé-pixelisation connu est l'**« effet d'avalanche manquant »** (mosaïque
déterministe → re-pixeliser des candidats et matcher les valeurs de bloc — ce qu'UnPixel FAIT déjà).
Pour un mosaïque à moyenne-de-bloc : le JPEG n'ajoute qu'un BRUIT dépendant du signal sur des valeurs
de bloc DÉJÀ connues (il ne reconstruit pas le détail intra-bloc moyenné) ; la couverture AA est DÉJÀ
dans la moyenne de bloc ET déjà rendue par le modèle direct. Sources :
[dé-pixelisation vidéo / Depix](https://positive.security/blog/video-depixelation),
[soft-decoding JPEG](https://arxiv.org/pdf/1607.01895). Les « dé-pixeliseurs » IA hallucinent (même
constat que la diffusion #7 ; cf. mémoire [[font-prior-vfr-mismatch]] sur l'inadéquation des modèles
pré-entraînés au domaine mosaïque).

**Objectif** : au lieu de construire un décodeur DCT ou un exploiteur AA sur une prémisse fausse,
**MESURER** rigoureusement (pur-Go, en-mémoire) combien chaque régime fuit réellement d'information
EXPLOITABLE, et **documenter la frontière** dans `docs/JOURNAL.md`. Clôture honnête du tier moonshot
par des chiffres reproductibles. Un exploiteur n'est construit QUE si l'étude révèle une fuite
inattendue et significative (très improbable).

### Non-objectifs
- Pas de décodeur DCT/JPEG, pas d'exploiteur AA, pas de modèle neuronal (Idée 4 reste la couture
  `//go:build ml` différée). Pas de CGO, pas de dépendance nouvelle.
- Aucune régression : additif ; panel 17/17 et cœur intouchés.
- Pas de collision de nom : la tâche mise `leak` est la GARDE goroutine (goleak) ; on utilise le tag
  et la tâche `infoleak`.

## 2. Critères de succès (mesurables)

1. **Primitives correctes** : `Separability(x,x)==0` et `>0` pour des mosaïques différentes ;
   `JPEGRoundTrip` préserve les dimensions et altère des pixels à basse qualité ; `binarizeHardEdge`
   ne produit que deux niveaux de luminance.
2. **Étude reproductible** : le runner `//go:build infoleak` produit, en-mémoire, un tableau par régime
   (AA, JPEG, multi-offset) ; invariants de cohérence vérifiés (séparabilité de mosaïques identiques
   == 0 ; la dérive JPEG croît quand la qualité baisse).
3. **Frontière documentée** : `docs/JOURNAL.md` gagne une section avec les chiffres mesurés et les
   conclusions honnêtes (AA = petit gain déjà capté ; JPEG = bruit/robustesse, pas une fuite ;
   multi-offset = seul levier super-résolution, déjà livré).
4. Pur-Go, cagé, fixtures en-mémoire, couverture ≥ 85 %, panel 17/17 inchangé.

## 3. Architecture & composants

Pas de cycle : nouveau package `internal/infoleak` qui importe `unpixel` (interface `Renderer`,
`Style`), `internal/pixelate`, `internal/imutil`, et stdlib (`image`, `image/jpeg`, `bytes`). Rien ne
l'importe. Le runner d'étude (test) importe en plus `fonts` + `internal/render`.

1. **`internal/infoleak/infoleak.go`** — primitives pur-Go :
   ```go
   // Separability is the mean per-pixel absolute luminance difference between two
   // same-or-overlapping mosaics, normalised to [0,1] (0 = indistinguishable).
   func Separability(a, b *image.RGBA) float64

   // JPEGRoundTrip encodes img as JPEG at the given quality (1..100) and decodes
   // it back to RGBA, simulating a JPEG-compressed capture of a mosaic.
   func JPEGRoundTrip(img *image.RGBA, quality int) (*image.RGBA, error)

   // binarizeHardEdge thresholds an anti-aliased render to two luminance levels
   // (dark text on light), isolating the sub-pixel AA contribution by removing it.
   func binarizeHardEdge(img *image.RGBA, threshold int) *image.RGBA
   ```
   `Separability` compares on the common minimum bounds (confusable pairs have near-equal width);
   uses `imutil.Lum601`.

2. **Mesures (mêmes fichier/paquet, pur-Go, testables)** :
   ```go
   type PairResult struct{ A, B string; AASep, HardSep, Gain float64 }
   type AAReport struct{ Font string; Pairs []PairResult; MeanAASep, MeanHardSep, MeanGain float64 }
   // MeasureAALeak: for each confusable pair, render both (AA), pixelate, Separability =
   // AASep; binarizeHardEdge both, pixelate, Separability = HardSep; Gain = AASep − HardSep.
   func MeasureAALeak(r unpixel.Renderer, fontName string, pairs [][2]string, block int, fontSize float64) (AAReport, error)

   type JPEGPoint struct{ Quality int; Drift float64; TrueStillWins bool }
   type JPEGReport struct{ Text, Wrong string; Points []JPEGPoint }
   // MeasureJPEGImpact: clean = pixelate(render(text)); wrongMosaic = pixelate(render(wrong));
   // per quality q: jpegd = JPEGRoundTrip(clean, q); Drift = Separability(clean, jpegd);
   // TrueStillWins = Separability(jpegd, clean) < Separability(jpegd, wrongMosaic).
   func MeasureJPEGImpact(r unpixel.Renderer, text, wrong string, block int, fontSize float64, qualities []int) (JPEGReport, error)
   ```
   (Idée 1 multi-offset déjà couverte par `internal/multiframe` ; l'étude la CITE/confirme via une
   mesure optionnelle légère ou un renvoi documenté, sans la réimplémenter.)

3. **`internal/infoleak/study_test.go`** (`//go:build infoleak`) — runner lourd, hors chemin par
   défaut (comme `panel_test.go`/`paper_parity_test.go`) : construit un renderer par police empaquetée
   (`fonts.All()` + `render.NewXImageFromFonts`), exécute `MeasureAALeak` sur un jeu de paires
   confusables (`rn/m`, `cl/d`, `vv/w`, `nn/m`, `0/O`, `8/B`, `I/l`) et `MeasureJPEGImpact` sur des
   textes échantillons × qualités {90,75,50,30}, imprime un tableau, et **assert les invariants** de
   cohérence (séparabilité identique == 0 ; dérive JPEG croissante à qualité décroissante). Invoqué :
   `scripts/gotest-caged.sh go test -tags infoleak -run InfoLeak ./internal/infoleak/`.

4. **`internal/infoleak/infoleak_test.go`** (sans tag) — tests unitaires des primitives (couverture
   dans la suite par défaut) : Separability(x,x)==0 / >0 ; JPEGRoundTrip dims+altération ;
   binarizeHardEdge bi-niveau ; un MeasureAALeak/MeasureJPEGImpact minimal sur une police.

5. **`docs/JOURNAL.md`** — section append-only « #8 information-leak study » : tableau des chiffres
   (AA gain moyen par police ; dérive JPEG & true-still-wins par qualité ; rappel multi-offset) +
   conclusions honnêtes + verdict (mur d'information confirmé ; pas d'exploiteur justifié).

6. **`mise.toml`** — tâche `[tasks.infoleak]` (nom DISTINCT de `leak`) : description + run
   `scripts/gotest-caged.sh go test -tags infoleak -run InfoLeak ./internal/infoleak/`. (Discoverability ;
   facultatif mais aligné sur `bench:panel`.)

## 4. Conclusions attendues (à confirmer par les chiffres — l'étude peut surprendre)

- **AA** : petit gain de séparabilité positif (la couverture sub-pixel distingue légèrement plus les
  confusables) — déjà partiellement capté par le modèle direct qui rend en AA. Quantifié, pas exploité.
- **JPEG** : dérive POSITIVE qui croît à basse qualité, `TrueStillWins` qui se dégrade — coût de
  ROBUSTESSE sur captures réelles (lié au mur « 0/10 images réelles »), PAS une fuite intra-bloc.
- **Multi-offset** : seul vrai levier super-résolution, déjà livré (IBP).

Si (contre toute attente) l'AA donnait un gain large et exploitable, l'étude le signalerait comme
candidat à un futur modèle direct non-bloc-constant — différé, hors de ce sous-projet.

## 5. Intégration & compatibilité

- Strictement additif : nouveau package `internal/infoleak` + tests + tâche mise + section JOURNAL.
  Cœur/`Verify`/`Recover`/panel 17/17 intouchés.
- Pas de cycle (`internal/infoleak` au-dessus de pixelate/imutil ; importe l'interface `unpixel.Renderer`).
  Pas de CGO, pas de dépendance nouvelle (stdlib `image/jpeg`). Pas de collision `leak`/`infoleak`.

## 6. Tests & validation (pur-Go, cagé, fixtures en-mémoire)

- Unitaires (suite par défaut) : primitives + une mesure minimale (couverture ≥ 85 %).
- `//go:build infoleak` : étude complète + invariants (identique==0, dérive monotone) — cagé.
- JOURNAL mis à jour avec les chiffres réels produits par le runner.
- `mise run ci` vert ; panel 17/17 inchangé.

## 7. Découpage (unités isolées)

1. `internal/infoleak/infoleak.go` — `Separability`, `JPEGRoundTrip`, `binarizeHardEdge` + unitaires.
2. `internal/infoleak/infoleak.go` (suite) — `MeasureAALeak` + types AAReport/PairResult + unitaires.
3. `internal/infoleak/infoleak.go` (suite) — `MeasureJPEGImpact` + types JPEGReport/JPEGPoint + unitaires.
4. `internal/infoleak/study_test.go` (`//go:build infoleak`) — runner complet + invariants ;
   `mise.toml` tâche `infoleak`.
5. `docs/JOURNAL.md` — section #8 avec les chiffres mesurés + conclusions ; validation (couverture, CI).

## 8. Risques & parades

- **Largeurs différentes des paires confusables** → `Separability` compare sur les bornes minimales
  communes ; paires choisies à largeur proche ; documenté.
- **Collision de nom `leak`** → tag et tâche `infoleak` (distincts de la garde goroutine `leak`).
- **Étude = artefact jetable** → primitives réutilisables + unitaires (couverture) + chiffres versionnés
  dans JOURNAL = artefact durable, pas un script jetable.
- **Cycle d'import** → `internal/infoleak` n'est importé par personne ; importe l'interface `Renderer`.
  `go build ./...` le vérifie.
- **Tentation de sur-construire (DCT/AA exploiteur)** → strictement hors périmètre ; l'étude DÉCIDE,
  par les chiffres, si quoi que ce soit mérite d'être construit (presque sûrement non).
