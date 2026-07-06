# Décoder les images — programme d'exécution (2026-07-06)

Objectif (goal) : **réussir à décoder réellement les images muré es** (real / wild / context),
pas seulement discriminer. ~20 points d'étape, séquencés par ROI, ancrés dans le SOTA et les
mesures de cette session. Règle absolue conservée : **no-CGO**. L'ML est admis mais **entraîné
hors-ligne** (Python) et **inféré en pur-Go** (poids shippés, forward-pass gonum/onnx-go —
binaire statique cross-compilable) derrière `//go:build ml` ; le chemin par défaut reste pur-Go,
panel 17/17 préservé.

## Thèse (mesurée + SOTA)

Trois murs distincts, trois familles de solution :
1. **wild** (captures Depix/unredacter) — mur de **fidélité du modèle direct** : mauvaise police +
   **anti-aliasing d'écran** non modélisé (mesuré : meilleure dist 0.67 sur m4 vs 0.0000 attendu).
   Depix les décode (70–90 %) par **matching de blocs contre un rendu de référence** dans la
   police/settings EXACTS + filtre-boîte linéaire (qu'on a déjà). → **pur-Go, ROI le plus proche.**
2. **context/sick haute-entropie** — **plafond info-théorique** : à rendu correct, secrets
   deviennent des **égalités homoglyphes** (0↔O, l↔1) qu'un prior de langue global départage
   incohéremment (mesuré cette session). → exige des **émissions apprises par-glyphe** (Hill-2016
   HMM per-font / CNN glyph-classifier + LM). ML.
3. **real blind + wild police inconnue** — espace de polices trop petit (9) + police exacte hors
   bundle. → **élargir l'espace de polices** (pur-Go) + **font-ID appris** (ML).

Sources : Hill et al. PETS-2016 (émissions HMM apprises per-font) ; Depix (de-Bruijn/bloc-référence,
Consolas/Monaco/Courier) ; DepixHMM/TF pour proportionnel ; CNN-glyph→homoglyph-groups→LM ;
inférence pur-Go via gorgonia/onnx-go/gonum.

## Phase 1 — Casser WILD (pur-Go, fidélité modèle direct) — premier VRAI décodage

- **P1. Harnais de parité Depix.** Reproduire l'approche bloc-référence de Depix sur `wild/m4,m5`
  (vérité connue « Hello from the other side ») pour établir la police + settings EXACTS qui les
  décodent — ground truth de faisabilité. Critère : identifier la config qui atteint dist≈0.
- **P2. Bundler la/les police(s) exactes.** Depix testimages = éditeurs (Consolas/Monaco/…). Bundler
  la police exacte OU une alternative libre métrique-compatible (Cascadia Code / Inconsolata /
  Liberation Mono selon mesure P1) + NOTICE/licence. Critère : rendu qui matche la bande.
- **P3. Modéliser l'anti-aliasing d'écran.** Étendre le modèle direct : rendu **AA gris** (et option
  **ClearType sous-pixel RGB**) AVANT la pixelisation boîte-linéaire, pour reproduire des captures
  d'écran (pas seulement le gris propre GIMP). `Style`/renderer flag, gated byte-identique off.
- **P4. Décodeur bloc-référence pur-Go** (`mosaictext.DecodeDepix`) — matcher chaque bloc observé
  contre un rendu de référence (de-Bruijn/par-cellule) pour texte monospace de capture. Opt-in.
- **P5. Câbler best-config wild** (police exacte + AA + boîte-linéaire + décodeur) → **décoder m4/m5
  exactement**. ⭐ **Premier décodage wild.** Critère : exact-match sur ≥1 image wild.
- **P6. Étendre m1/m2/m3** (unredacter/Depix vérité inconnue) — décoder OU diagnostic actionnable.

## Phase 2 — Élargir l'espace de POLICES (real/wild aveugle, pur-Go)

- **P7. Bundler des centaines de polices libres** (sous-ensemble Google Fonts / OFL) + NOTICE.
- **P8. Pré-filtre fingerprint + LSH** (`internal/fontrank`) pour garder le sweep tractable
  (top-K polices par signature glyphe-pixelisée avant notation image). Benchstat le prefilter.
- **P9. Câbler le sweep multi-police** dans `RecoverFile`/verify best-config ; mesurer le gain
  real/wild. Critère : ≥1 nouveau exact-match OU diagnostic « vraie police hors atteinte → P16 ».

## Phase 3 — Casser le plafond HOMOGLYPHE (tier ML : entraîne hors-ligne, infère pur-Go)

- **P10. Générateur de données d'entraînement.** Rendre (charset × polices × tailles × block ×
  colorspace × AA) → tuiles pixelisées labellisées ; générateur déterministe in-repo (réutilise
  `internal/render` + `internal/pixelate`). Aucune donnée réelle — synthétique, domaine-exact.
- **P11. Pipeline d'entraînement hors-ligne** (`ml/` — Python/PyTorch, NON shippé, non-CGO car
  hors-processus) : petit **CNN d'émission/confusabilité par-glyphe** conditionné police+block ;
  export poids (ONNX ou format tenseur plat). Documenté + reproductible.
- **P12. Inférence forward-pass PUR-GO** (`//go:build ml`) chargeant les poids (gonum/mat ou
  onnx-go ; zéro cgo ; cross-compile statique). Test : parité numérique vs l'entraînement.
- **P13. Émissions apprises dans le trellis DID/HMM** — remplacer l'émission render-pixelate-distance
  par la posterior par-glyphe du modèle → casse la frontière proportionnelle + les ties.
- **P14. Verify/rerank à posterior homoglyphe apprise** → cible **context 6/9 → 8–9/9**, ties digits
  sick résolus. Critère : franchir exact-match sur les images actuellement muré es par tie.
- **P15. Émissions per-font (Hill-2016)** — sélection/entraînement par police depuis le texte
  calibrant visible (déjà : `internal/varfont` calibre le wght ; y brancher les émissions).

## Phase 4 — Font-ID appris (real/wild aveugle, ML)

- **P16. CNN de font-ID** entraîné sur le domaine pixelisation (pas texte lisible — cf. mémoire
  font-prior-vfr-mismatch) → top-K polices pour le sweep aveugle ; inférence pur-Go. Critère :
  la vraie police de `real/wild` dans le top-K.

## Phase 5 — Intégration, preuve, produit

- **P17. Bout-en-bout MCP** : analyze → rank_fonts/font-ID → calibrate → (émissions ML) propose/verify
  → decode ; piloter la récup real/wild/context depuis un client LLM.
- **P18. Benchmarks + gates** : benchstat sur tout changement hot-path ; tier ML derrière build-tag
  (défaut inchangé, panel 17/17, cover ≥85, /simplify, caged).
- **P19. Journal + verifymeasure + quality-history** : suivre les VRAIS décodages exact real/wild/
  context version-over-version (pas seulement discrimination).
- **P20. Docs operating-envelope** : ce que le tier ML décode vs le plancher info-théorique résiduel ;
  contrat de taux-de-récup honnête.

## Séquencement / ROI

1. **P1→P5** d'abord (pur-Go, décodage wild réel probable — la preuve que le programme livre).
2. **P10→P14** ensuite (ML tie-breaker — le mur fondamental context/sick).
3. **P7→P9, P16** en parallèle (espace de polices) ; **P17→P20** en continu.

Chaque point : TDD → impl → benchstat (si perf) → doc → commit via gates. Critère de rétention :
**franchir exact-match sur un corpus muré** OU diagnostic actionnable. Pas de régression panel/journal.

## Findings d'exécution (2026-07-06)

- **P1 — wild = mur de police, CONFIRMÉ (sondes jetées).** Scorer generate-and-test chaîne-entière
  (filtre-boîte linéaire) × toutes polices bundled + NimbusMonoPS (Courier) × tailles 8–26 × gras ×
  block{4..8} : **m4 meilleure dist = 0.3252** (Caladea, faux serif), **m5 = 0.9348**. Ni police
  Courier ni gras ne ferment l'écart. Les polices exactes (Consolas/Notepad, Sublime) sont
  propriétaires/système — **Depix lui-même exige une capture de référence fournie par l'utilisateur
  dans la police exacte**. → P2/P7 (acquérir/élargir polices) est le seul levier ; sans la police
  exacte, m4/m5 ne sont pas décodables en aveugle. **Réoriente l'effort vers le tier ML (ties).**
- **context ties — rerank INSUFFISANT, mesuré (sonde jetée).** VF-renderer (var_font) + prior de
  langue rerank sur tout le corpus context : **physique 6/9 → 4/9 (w=0.05) → 3/9 (w=0.10)** — le
  prior rétrograde plus de wins qu'il n'en récupère (incohérent). De plus `ctx_sameline_mono_token`
  (`a3f9b2`, hex aléatoire) est **info-théoriquement indécodable** : un décoy homoglyphe score
  **physiquement plus bas** que la vérité (0.0651 < 0.0714) → même un modèle appris préférerait le
  décoy. → seul un **modèle d'émission appris par-glyphe** (P10–P14) peut espérer casser wght750/
  crossimg700 ; `a3f9b2` reste hors d'atteinte de toute méthode (perte d'information réelle).
- **Conséquence programme** : les gains de décodage restants sont concentrés dans **P10–P14 (tier
  ML, multi-session)** + **P2/P7 (polices, dont wild exige la police exacte utilisateur-fournie)**.
  Les corpora déjà décodés : fixtures 17/17, blur 13/14, real (hello-world 0.0000 via propose/verify),
  sick 10/10 discrimination.

### ⭐ DÉCODAGE RÉEL cette session — `ctx_crossimg_wght700` → « Secret7 » (pur-Go, sans ML)

Le plafond « info-théorique » était **trop pessimiste pour les secrets contenant un MOT**. Nouvelle
capacité livrée `mcpserver.VerifyVarFontFit` + test permanent `TestVerifyVarFontFit_DecodeCrossImg` :
la chaîne **calibration par-candidat des axes VF (wght) + taille en generate-and-test** (chaque
candidat prend son min sur la grille wght×size) amène la vérité à son **minimum physique** (0.0119,
à égalité homoglyphe avec « Secnet7 »/« Sccret7 » — indistinguables à block 8), puis un **prior de
mot-dictionnaire** (`dictWordBonus` : la partie alphabétique est-elle un mot ? « Secret » oui,
« Secnet »/« Sccret » non) **casse l'égalité** là où le prior char-n-gram échouait. **Best = « Secret7 »,
Match=true — décodé.** C'est le levier P13/P14 sans ML : émission-par-calibration + prior sémantique
au niveau mot. Frontière raffinée : **les ties homoglyphes de secrets *word-like* SONT décodables**
(calibration + dict) ; seul `a3f9b2` (aléatoire pur, décoy physiquement plus bas) reste hors d'atteinte.
Levier généralisable : intégrer VF-fit + dict-prior dans la boucle verify context (viserait wght750 aussi).
