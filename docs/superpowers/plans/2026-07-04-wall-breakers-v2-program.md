# Wall-breakers v2 — programme d'exécution (2026-07-04)

Issu de la revue exhaustive « comment j'aurais fait » (21 points). Objectif unique :
**décodage performant — casser les murs real / wild / sick / context**, en autonomie,
en respectant les règles absolues (no-CGO par défaut, benchstat sur tout changement perf,
gate `/simplify` + revues, caged `go test`, pas de régression panel 17/17 ni journal full-set).

## Thèse stratégique

Deux corpora résolus (fixtures 17/17, blur 13/14), quatre murés à ~0 exact-match. Chaque
mur coïncide avec un endroit où l'état de l'art emploie un composant **appris** — que le
projet a interdit (tous les `//go:build ml` sont des stubs vides). 14 décodeurs livrés,
aucun n'a franchi exact-match sur real/wild/sick. Les gains restants sont dans :
(a) exécuter les expériences flaguées-jamais-lancées, (b) élargir l'espace de polices,
(c) franchir la frontière ML-**sidecar**, (d) consolider pour itérer vite.

Causes-racines mesurées (table `Évolution`) :
- **real** 0/3 — mauvaise police (conf 1.0, faux avec assurance) → espace de polices trop petit (9).
- **wild** 0/5 — échec géométrique amont (grille/offset/crop) → jamais isolé.
- **sick** 1/10 — mauvaise longueur (frontières proportionnelles) → segmentation.
- **context** 0/10 — destruction d'information (mosaïque moyenne-de-bloc) → prior sémantique.

## Séquencement par ROI

### Phase 1 — Diagnostic + gains gratuits (parallélisable, pur-Go, faible risque)
- **P2** Harnais d'isolation géométrique wild/real : mesurer par étage (localise→grille→police)
  *où* wild échoue avant tout décodage. **Keystone** — conditionne P3/P4/P5. Livrable :
  `mise run geomeasure` + `docs/GEOMETRY.md`. Critère : chiffres par-étage par-image.
- **P9** PGO : `default.pgo` depuis une récup représentative, benchstat ~4,5 %, zéro régression.
- **P17/P18** Diagnostic par-étage + taxonomie d'échec structurée (adossé à P2 et au journal).

### Phase 2 — Casser real (le plus rentable sous no-CGO)
- **P3+P12+P15** Élargir l'espace d'hypothèses de polices (centaines de familles libres) +
  pré-filtre `fontrank` (fingerprint glyphe) / LSH pour rester tractable. Benchstat le
  prefilter ; mesurer le gain de récup sur real. Critère : ≥1 exact-match real OU diagnostic
  clair que la police vraie reste hors atteinte (→ P4).

### Phase 3 — Casser sick + valider la thèse sémantique
- **P1** Spike LLM-propose/vérifie sur sick+context (mesure décisive, pas un build).
- **P5** Segmentation+décodage joints (char-LM dans le trellis / CTC contraint largeur) pour
  la frontière proportionnelle de sick.
- **P6** Fixture sample-starved (IBAN, bloc ≥ largeur glyphe) + multi-frame super-résolution
  de bout en bout — fermer le négatif sous-testé.

### Phase 4 — Franchir la frontière ML-sidecar (plafond real/wild)
- **P4+P14** Remplir un seam `//go:build ml` avec un vrai petit CNN font-ID entraîné sur le
  domaine render→pixelise (forward-pass pur-Go OU sidecar hors-processus documenté).
- **P7+P8** Restaurateur de flou externe via la porte `VerifyImage` (régime flou = vrai gain
  SOTA) ; OCR-auto du leak-prepass (caviardage partiel).

### Phase 5 — Perf sans-perte + architecture + produit
- **P10** Branch-and-bound à borne admissible (pruning exact, sans changer le décodage).
- **P11** Métrique honnête *time-to-first-correct* (remplace le budget-timeout du journal).
- **P16** Métrique edge-aware/apprise opt-in derrière l'interface `Metric` — mesurer le rappel.
- **P13** Consolider did/trained-hmm/window-hmm/reference/ensemble en un décodeur block-grid
  unifié à emissions/priors pluggables.
- **P19/P20/P21** Operating-envelope comme contrat produit ; réallouer le budget hors
  fixtures/blur/hot-path ; étendre le gate anti-régression aux ~75 images.

## Journal des findings (exécution)

### P2 / P2b — grille (livré, commits 3b029e2, 9e0c2e9)
Le harnais `geomeasure` a **corrigé l'hypothèse de départ** : real ne casse pas d'abord à la
police mais à la **grille** (marx : `InferBlockGrid` → Size=0 sur bloc 19px proportionnel à
offset (5,5)). Fix livré (garde sous-harmonique + phase non-nulle) → marx passe grille→police,
panel 17/17 byte-identique, et **2 tests mono-digits pré-existants réparés** (window-hmm timeout
300s, trained-hmm ErrNoContent). Wild n'est PAS un échec de localisation.

### P3a — pourquoi `real/hello-world.png` échoue en aveugle (root-cause, investigation)
Image la plus tractable (« Hello World ! », 13 glyphes monospace) : géométrie+police saines, le
**modèle direct** la reproduit à pixelmatch 0.0000, mais zéro/best-config ne l'atteint pas. **5
bloqueurs cumulés** identifiés (aucun résoluble par plus de recherche — l'élagage tue avant la
profondeur 1) :
1. **Pas de crop du contenu** — marges blanches → score trivial 0.0 à x=0, `DiscoverOffsets`
   laisse tout passer, le DFS cherche du bruit.
2. **Police par défaut fausse** — Liberation Sans ≠ Noto Sans Mono (formes de bols divergentes).
3. **Mode de pixelisation** — GEGL moyenne en **lumière linéaire**, le défaut moyenne en gamma.
4. **XScale 1.06 non modélisé** — GIMP a appliqué ~6 % d'étirement horizontal *au niveau pixel*
   avant mosaïque ; `LetterSpacing` ajoute de l'espace inter-glyphe mais **ne redistribue pas
   l'encre intra-glyphe** → le score du 'H' (~0.375) dépasse le seuil (0.25), DFS élague tout.
   **C'est la primitive réellement manquante** du modèle direct (aucun des 14 décodeurs ne
   modélise l'anisotropie), et le vrai levier pour atteindre le 0.0000 en aveugle.
5. **PaddingLeft** — mauvaise phase d'encre dans le bloc 0.

Conséquence : hello-world blind exige (a) la capacité `Style.XScale` (anisotropie) + (b) le
câblage best-config (crop/police/linéaire/padding auto). Prochaine action contrôlée : lander
`Style.XScale` proprement (gated byte-identique sur zéro-value, benchstat, test oracle prouvant
que le modèle direct atteint l'image), séparé du câblage auto-calibration (futur, via l'axe
varfont existant). Un premier essai a churné le hot-path sans vérification → **jeté** ; ré-abordé
en pass minimale vérifiée.

### P3b — décodage blind de hello-world (déféré, statut honnête)
Le modèle direct est **vérifié** : `TestRealMosaic_HelloWorld` (linear=0.0000, sRGB=0.2986)
et `TestXScale_HelloWorld_directModel` (XScale=1.06 → 0.0000, XScale=1.0 → 0.0972) passent
réellement — le modèle reproduit la redaction ET **discrimine** (bonne config=0, mauvais
stretch=0.097). Donc le décodage blind n'est **pas** un mur fondamental : c'est un problème de
**câblage best-config** (linéaire + Noto Sans Mono + XScale=1.06 + crop correct) + **convergence
de recherche**, pas un mismatch de modèle.

⚠️ **Faux finding écarté** : une sonde a conclu à un « mur de style de renderer » (encre
x/image sombre R≈50 vs GIMP claire R≈200 empêchant tout match). C'est **contredit** par l'oracle
qui atteint 0.0000 avec exactement le même `defaults.RendererFromFonts` — la sonde utilisait un
wrapper `inkAlignRenderer` biaisé. Conclusion et fichiers de sonde jetés (non committés).

Reste à faire (P3b, non churné) : câbler {LinearBlockAverage, Noto Sans Mono, XScale, autoCrop}
dans le best-config du corpus real (le best-config a le droit d'utiliser les hints du manifeste
— c'est la borne-haute atteignable) et vérifier que le DFS/monospace converge sur les 13 glyphes.
Cible : premier exact-match real. À faire en une passe contrôlée dédiée (pas d'exploration
tentaculaire du hot path).

## Règle transverse

Chaque item : TDD → implémentation (go-dev/algo-architect) → benchstat (si perf) → doc →
commit via les gates (`mise run ci`, `/simplify`, caged test). Aucun décodeur nouveau retenu
s'il ne bouge que le *mean-similarity* : critère = franchir exact-match sur un corpus muré,
OU produire un diagnostic actionnable. Pas de régression panel 17/17 ni journal full-set.
