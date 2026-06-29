# Spec — #3 LLM-propose → physics-verify

Date : 2026-06-29 · Statut : design approuvé (brainstorm) · Sous-projet 4 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). S'appuie sur #2 (opérateur
fingerprinté) ; additif MCP, cœur intouché. #2/#1B/#1 mergés, CI verte.

## 1. Problème & objectif

Le mur des phrases longues (mémoire `blind-sentence-scoring-wall`) résiste à la recherche
bottom-up par glyphe. Et l'outil MCP `verify_candidates` actuel **classe sans trancher** :
avec des dictionnaires d'attaquant, la vérité atterrit dans le top-4 mais pas toujours en #1
(`admin→welcome`, `azerty→qwerty`…), car `mosaictext.ScoreCandidates` a une calibration
typographique trop lâche (mémoire `mcp-decode-campaign-gaps` trou #2).

**Objectif** : rendre la vérification **décisive** pour qu'un LLM client (Claude pilotant le MCP)
puisse *proposer* des chaînes entières plausibles (top-down) et les faire **confirmer
physiquement** par le serveur — rendre candidat → opérateur fingerprinté → distance pixelmatch
[0,1] → « match » ssi distance < seuil absolu (dist≈0 = la réponse), via le **modèle direct fidèle
du moteur** (le même qui produit `BestTotal`), pas le scorer lâche. Plus un outil d'**indices**
pour guider les propositions du LLM.

### Non-objectifs
- Le serveur n'appelle PAS de LLM (le LLM est le client) ; la boucle propose→vérifie→raffine est
  pilotée côté client. Le serveur ne fait que vérifier décisivement + fournir des indices.
- Pas d'OCR pur-Go (les indices de contexte viennent de la couche texte PDF/Office via #1, sinon
  vides — OCR auto = Tier-2 #5).

## 2. Critères de succès (mesurables)

1. Nouvelle API publique **`unpixel.Verify`** : distances via le modèle direct FIDÈLE du moteur
   (calibrées comme `BestTotal`, ≈0 sur recouvrement exact).
2. Sur une fixture, la **vraie** chaîne vérifie `match` (dist < τ) et une chaîne plausible-mais-fausse
   **non** (dist ≫ τ) ; le **pick décisif** est correct là où le classeur actuel est ambigu.
3. `propose_hints` renvoie une **estimation du nombre de caractères** (que `analyze` ne donne pas)
   + bloc/police/bbox + contexte fuité (PDF/Office via #1).
4. Additif MCP seul → **cœur/panel intouchés** (17/17 trivialement), pur-Go, cagé, fixtures
   en-mémoire, couverture ≥ 85 %.

## 3. Architecture & composants

1. **`unpixel.Verify(ctx, img, candidates []string, opts ...Option) ([]Verdict, error)`** — nouvelle
   API publique (root). `Verdict{ Text string; Distance float64; Match bool }`. Construit le même
   `Config` que `Recover` (chemin auto : opérateur fingerprinté #2 + bloc inféré + style ; overrides
   via opts existants `WithCharset`/`WithBlockSize`/`WithStyle`…), découvre les offsets de grille
   (`DiscoverOffsets`), puis pour chaque candidat calcule `min sur offsets de TotalScore` via
   `internal/search.NewCachingScorer(NewPipelineScorer(rgba, cfg))`. `Match = Distance < VerifyMatchThreshold`.
   C'est le helper gagné qui comble le trou « ranks-not-picks » (réutilise le scorer fidèle du moteur,
   pas `mosaictext.ScoreCandidates`).
2. **MCP `verify_candidates` rebranché** — appelle `unpixel.Verify` ; `VerifyReport` gagne `match`
   par candidat (`RankedCandidate.Match bool`) + un **pick décisif** (`Pick string` = candidat
   `Match` de plus basse distance, ou `""`/`"none"`), en plus de `Ranked`/`Best`/`Margin`.
   `mosaictext.ScoreCandidates` n'est plus utilisé par cet outil (reste pour `mosaictext` lui-même).
3. **MCP `unpixel_propose_hints`** (nouvel outil) — `{char_count_estimate int, block_size int,
   font_size_pt float64, redaction_bbox []int, charset_hint string, leaked_context string}`. Réutilise
   `Analyze` (bloc/police/bbox), `internal/capacity.Analyze` (estimation du nombre de caractères),
   et `internal/leak.Scan` (#1) pour `leaked_context` quand le fichier est un PDF/Office. Agrégation
   mince, non redondante avec `analyze`.

Fichiers : `verify.go` (root, `Verify`/`Verdict`/`VerifyMatchThreshold`), `mcp/server.go` (upgrade
handleVerify + report), `mcp/propose_hints.go` (nouvel outil) + enregistrement.

## 4. Modèle direct fidèle & seuil décisif

`Verify` utilise le **même** pipeline render→opérateur→métrique que `Recover` (donc une distance qui
signifie la même chose que `BestTotal`). Par candidat : balayer les offsets de grille et prendre la
distance min (meilleur alignement), exactement comme le moteur note une chaîne recouvrée. Le **seuil
de match τ** (absolu, [0,1]) est une const de package calibrée dans la tâche de validation : la vraie
chaîne doit scorer `< τ` et une chaîne clairement fausse `≥ τ` à travers les fixtures (τ attendu
≈ 0,05–0,10, les recouvrements exacts étant à ≈0). Pick décisif = `argmin Distance` parmi
`Match==true`, sinon `"none"` (« proposer davantage »).

## 5. Intégration & compatibilité

- Additif : nouvelle API `unpixel.Verify` (le cœur `Recover`/`New`/panel inchangé) ; MCP
  `verify_candidates` rebranché (sortie enrichie, rétro-compatible — champs ajoutés, pas retirés) ;
  nouvel outil `unpixel_propose_hints` enregistré dans `NewServer`.
- Invariant panel 17/17 : `Verify` ne touche pas le chemin de décodage du panel.
- `propose_hints` consomme #1 (`leak.Scan`) pour le contexte fuité — synergie, pas de duplication.

## 6. Tests & validation

- `unpixel.Verify` (root test) : sur une mosaïque forgée en-mémoire de « go », `Verify(["go","xy"])`
  → "go" Distance≈0 Match=true, "xy" Distance haute Match=false. `got` avant `want`.
- Calibration τ : test qui rend la vraie chaîne (dist < τ) et une fausse (dist ≥ τ) sur ≥ 2 fixtures ;
  fixe `VerifyMatchThreshold` documenté.
- MCP `verify_candidates` : pick décisif correct sur une fixture (la vraie chaîne gagne, Match=true,
  Pick=vraie).
- `propose_hints` : char_count_estimate plausible sur une fixture connue (ex. « go » → ~2) ; sur un
  .docx (via #1) leaked_context non vide.
- Cœur/panel intouchés (grep : aucun nouvel import lourd dans le cœur) ; cagé ; ≥ 85 %.

## 7. Découpage (unités isolées)

1. `verify.go` (root) — `Verify`/`Verdict`/`VerifyMatchThreshold`, modèle direct fidèle + balayage
   offsets via `internal/search`.
2. `mcp/server.go` — `handleVerify` rebranché sur `unpixel.Verify` ; `RankedCandidate.Match` +
   `VerifyReport.Pick`.
3. `mcp/propose_hints.go` — outil `unpixel_propose_hints` + enregistrement `NewServer`.
4. Tests + calibration τ + couverture.

## 8. Risques & parades

- **Seuil τ trop strict/lâche** → calibré sur fixtures (vrai < τ ≤ faux) ; documenté ; le pick
  retombe sur `"none"` plutôt qu'un faux-positif confiant.
- **Cross-metric** (leçon ensemble/#1B) : `Verify` compare des distances du MÊME modèle fidèle entre
  candidats — comparable et calibré comme `BestTotal`, pas de mélange de métriques.
- **Perf** : `Verify` est O(candidats × offsets) avec rendu ; borner le nombre de candidats
  (`maxVerifyCandidates`, ex. 256) et réutiliser `CachingScorer`. Pas dans la boucle de recherche du
  cœur → pas de régression hot-path ; benchmark le scorer si besoin.
- **propose_hints vs analyze** → agrégation mince ; valeur neuve = char-count + contexte fuité.
