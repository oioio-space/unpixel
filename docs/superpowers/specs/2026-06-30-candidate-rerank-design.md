# Spec — #5 Re-rank de candidats (hybride : fusion LM pur-Go maintenant, couture CTC différée)

Date : 2026-06-30 · Statut : design approuvé (brainstorm) · Sous-projet Tier-2 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Opt-in / additif, cœur intouché.
#2/#1B/#1/#3/#6/#4 mergés, CI verte.

## 1. Problème & objectif

La distance image seule ne sépare pas des candidats visuellement quasi identiques (surtout en flou) :
`RankFinal` n'applique le LM qu'en **départage étroit** (bande `lmTieBand=0.01`), et `unpixel.Verify`
(#3) ne renvoie qu'une distance physique. Le moteur a déjà des LMs riches (bigramme, infini-gram,
dict+freq fusionnés via `lang.PriorFor`) et un vérificateur physique fidèle (`Verify`).

La littérature OCR confirme que le **CTC + LM (shallow fusion)** aide — mais c'est exactement la
fusion LM que le projet fait déjà ; et un **CRNN-CTC pré-entraîné** (PP-OCRv4/OnnxTR) est triplement
inadapté : (1) entraîné sur texte LISIBLE → ne transfère pas à la mosaïque (cf. [[font-prior-vfr-mismatch]],
leçon #4) ; (2) CGO/ONNX interdits ; (3) sa part linguistique double l'existant. Sources :
[CTC+n-gram shallow fusion](https://arxiv.org/html/2508.10356v1), [text-line recognition](https://arxiv.org/pdf/2104.07787).

**Objectif** : une **étape de re-rank post-recherche** de premier ordre, opt-in, qui fusionne la
distance physique (`Verify`) avec un score linguistique sur le top-K, généralisant le départage étroit
de `RankFinal` en un composant réglable et inspectable. Plus une **couture CTC** (`//go:build ml`) où
un futur CRNN entraîné sur le domaine render→pixelise pourra s'insérer — sa seule valeur nouvelle
réelle étant de reconnaître des polices **hors des 9 empaquetées** (que le modèle direct ne peut pas
rendre). **Aucun modèle neuronal construit maintenant.**

### Non-objectifs
- Pas de modèle CTC pré-entraîné ; pas d'ONNX/CGO/sidecar ; pas de gonum/poids maintenant (couture seulement).
- Pas de nouveau scoreur physique ni de nouveau LM : réutilise `Verify` (#3) et `lang.PriorFor`.
- Aucune régression : cœur de recherche, `Verify`, `RankFinal`, panel 17/17 inchangés (poids 0 ⇒ ordre physique).
- Pas d'intégration CLI (le balayage CLI fait déjà le départage LM via `RankFinal`) — différé.

## 2. Critères de succès (mesurables)

1. **Poids 0 = ordre physique** : `Linguistic.Rerank(..., weight=0)` préserve exactement l'ordre par
   distance croissante (réductible au comportement actuel ; tri stable).
2. **La fusion départage** : sur un cas forgé où la physique préfère marginalement une chaîne
   implausible mais le LM préfère fortement le vrai mot, `weight>0` fait gagner le vrai mot (et
   `weight=0` non). Test non vacant.
3. **Bout-en-bout** : `rerank.Rerank(ctx, img, candidats)` (Verify + fusion) classe correctement sur
   une fixture en-mémoire.
4. Pur-Go, cagé, fixtures en-mémoire, couverture ≥ 85 %. Panel 17/17 inchangé (chemin par défaut intact).

## 3. Architecture & composants

Contrainte de cycle (vérifiée) : la racine `unpixel` n'importe pas `internal/lang` ; `internal/lang`
n'importe pas la racine. Un **nouveau package public de haut niveau `rerank`** peut importer `unpixel`
(pour `Verify`/`Verdict`/`Option`) + `internal/lang` (pour `PriorFor`) sans cycle. La racine n'importe
pas `rerank`.

1. **Package `rerank` (nouveau, public)** :
   ```go
   // Ranked is one re-ranked candidate.
   type Ranked struct {
       Text     string  // candidate
       Distance float64 // physical Verify distance, [0,1], lower better
       LMScore  float64 // linguistic score, higher better
       Blended  float64 // fused ordering key, lower better
   }

   // Reranker re-orders physically-scored candidates. The image is provided for a
   // future discriminative (CTC) impl; the default ignores it.
   type Reranker interface {
       Rerank(ctx context.Context, img image.Image, verdicts []unpixel.Verdict,
              lm func(string) float64, weight float64) ([]Ranked, error)
   }

   // Linguistic is the pure-Go default: blends physical distance with an LM score.
   type Linguistic struct{}

   func Default() Reranker // !ml → Linguistic{} ; ml → ctcReranker (deferred)

   // Rerank is the one-call convenience: Verify then fuse.
   func Rerank(ctx context.Context, img image.Image, candidates []string,
               opts ...unpixel.Option) ([]Ranked, error)
   ```
   - **Mélange (`Linguistic.Rerank`)** : `bestLM = max(lmScore_i)` sur l'ensemble ; pour chaque
     candidat `blended = distance − weight·(lmScore − bestLM)`. Le candidat le plus plausible reçoit
     un bonus linguistique nul ; les autres sont pénalisés proportionnellement à leur moindre
     plausibilité. Tri ascendant par `blended`, départage déterministe par `(distance, text)`.
     `weight ≤ 0` ⇒ `blended = distance` ⇒ ordre physique (tri stable). `lm == nil` ⇒ traité comme
     score constant ⇒ ordre physique.
   - **`Rerank` (convenience)** : résout une `Config` depuis `opts`, appelle `unpixel.Verify(ctx, img,
     candidates, opts...)`, choisit le LM = `cfg.LanguageModel` si défini sinon `lang.PriorFor(lang.English)`,
     applique `Default().Rerank(ctx, img, verdicts, lm, cfg.RerankWeight)`.

2. **Couture CTC différée** (`rerank/ml.go`, `//go:build ml`) : `var ErrCTCNotBuilt = errors.New(...)` ;
   `Default()` renvoie `ctcReranker{}` dont `Rerank` renvoie `(nil, ErrCTCNotBuilt)`. Documente le
   contrat futur : CRNN-CTC entraîné sur le domaine synthétique render→pixelise (le renderer est le
   labelleur), passe avant pur-Go (conv+BiLSTM+CTC, **sans CGO**), valeur nouvelle = polices hors-9.
   **Aucun poids, aucun gonum, aucun entraînement maintenant.**

3. **Option racine `unpixel`** (champ exporté + option, inertes dans le cœur) :
   - `WithRerankWeight(w float64) Option` → `cfg.RerankWeight = w` ; `Config.RerankWeight float64`
     (lu seulement par le package `rerank` ; `Recover`/`Verify`/`RankFinal` ne le lisent jamais ⇒
     byte-identique par défaut). Défaut 0 = ordre physique ; valeur conseillée de départ ~0.05–0.1.

4. **MCP** : `verify_candidates` gagne `rerank_weight` (optionnel). `VerifyCandidates` gagne un
   paramètre `rerankWeight float64` ; quand `>0`, après `Verify`, réordonne `Ranked` via
   `rerank.Default().Rerank(...)` avec `lang.PriorFor(lang.English)` ; `Best` reflète l'ordre fusionné ;
   **`Pick` reste la décision PHYSIQUE** (un match physique confiant ne doit pas être supplanté par le
   LM). `Margin` recalculé sur l'ordre fusionné. `rerank_weight=0`/absent ⇒ comportement actuel.

Fichiers : `rerank/rerank.go` (Ranked + Reranker + Linguistic + Rerank) ; `rerank/default_noml.go`
(`//go:build !ml` Default) ; `rerank/ml.go` (`//go:build ml` Default + stub) ; `unpixel.go` (option +
champ Config) ; `mcp/server.go` (`rerank_weight` + câblage VerifyCandidates). Tests associés.

## 4. Limites assumées (documentées)

- Ne fait que **réordonner** des candidats fournis ; n'en génère aucun.
- **Chiffres non structurés** : aucun signal LM → l'étape ne peut pas battre la physique (les chiffres
  structurés relèvent de #6). Honnête : pas de gain digits ici.
- Un **poids trop élevé** peut supplanter une physique correcte → opt-in, conservateur par défaut (0).
- La valeur nouvelle d'un vrai CTC (polices hors-9) est **différée** à la couture `//go:build ml` ; le
  re-rank pur-Go ne l'apporte pas.

## 5. Intégration & compatibilité

- Strictement opt-in : nouveau chemin (`rerank.Rerank`, MCP `rerank_weight`) ; cœur/`Verify`/`RankFinal`
  intouchés ⇒ panel 17/17 byte-identique. `RerankWeight` inerte dans le cœur.
- Pas de cycle (`rerank` au-dessus de la racine et de `internal/lang`). Pas de CGO, pas de dépendance
  nouvelle (gonum non ajouté tant que le CTC n'est pas construit).
- Réutilise `Verify` (#3) et les LMs existants — pas de duplication de scoreur.

## 6. Tests & validation (pur-Go, cagé, fixtures en-mémoire)

- `rerank` : poids 0 préserve l'ordre physique (tri stable, byte-identique) ; cas forgé fusion
  départage (vrai mot rescapé à `weight>0`, pas à 0) — non vacant ; `lm==nil`/0/1 candidat = no-op ;
  `Rerank` bout-en-bout (Verify+fusion) sur fixture en-mémoire (rendu→pixelise).
- `mcp` : `verify_candidates` avec `rerank_weight>0` réordonne ; `Pick` reste le match physique ;
  `rerank_weight=0` byte-identique au rapport actuel.
- Couture ML : `Default()` = `Linguistic` en build normal ; test `//go:build ml` vérifie que la couture
  compile et que le stub renvoie `ErrCTCNotBuilt`.
- Couverture ≥ 85 % ; pur-Go ; panel 17/17 inchangé.

## 7. Découpage (unités isolées)

1. `rerank/rerank.go` — `Ranked`, `Reranker`, `Linguistic` (mélange), `Rerank` convenience + tests.
2. `rerank/default_noml.go` — `//go:build !ml` `Default() → Linguistic{}`.
3. `unpixel.go` — `WithRerankWeight` + `Config.RerankWeight` (inerte) + test.
4. `rerank/ml.go` — `//go:build ml` `Default() → ctcReranker` stub + `ErrCTCNotBuilt` + test.
5. `mcp/server.go` — `rerank_weight` champ + câblage `VerifyCandidates` (Best/Margin fusionnés, Pick
   physique) + tests.
6. Validation : poids-0 byte-identique, fusion départage, MCP, couture ml, couverture.

## 8. Risques & parades

- **Cycle d'import** → `rerank` au-dessus de racine/`internal/lang` ; option racine ne fait que poser un
  champ ; `go build ./...` le vérifie.
- **Régression panel/Verify** → aucun chemin par défaut touché ; `rerank` additif ; test poids-0
  byte-identique + panel 17/17.
- **LM supplante la physique** → poids opt-in, défaut 0, conservateur ; `Pick` MCP reste physique ;
  doc claire.
- **Échelle du score LM hétérogène** → le poids absorbe l'échelle (« unités de distance par unité LM ») ;
  le mélange est relatif à `bestLM` (invariant par décalage), documenté.
- **Sur-ingénierie ML** → couture seule ; aucun poids/gonum/entraînement ; stub derrière `//go:build ml`.
