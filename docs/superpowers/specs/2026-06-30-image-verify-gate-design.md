# Spec — #7 Porte de vérification d'image restaurée + protocole sidecar

Date : 2026-06-30 · Statut : design approuvé (brainstorm) · Sous-projet Tier-3 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Opt-in / additif, cœur intouché.
#2/#1B/#1/#3/#6/#4/#5 mergés, CI verte.

## 1. Problème & objectif

Un restaurateur par diffusion (DiffTSR/UDiffText/AnyText2) produit une **image** restaurée. Le projet
ne sait vérifier que des **chaînes** (`unpixel.Verify` #3 rend la chaîne puis re-pixelise puis compare).
Il **n'existe aucune intake d'image restaurée** : la porte anti-hallucination que la feuille de route
place au cœur de #7 est **manquante**.

Recherche (cf. [DiffTSR CVPR-2024](https://arxiv.org/abs/2312.08886), [TextCtrl](https://arxiv.org/pdf/2410.10133)
+ [[font-prior-vfr-mismatch]]) : la diffusion-texte est de la **super-résolution / édition** sur du
texte *dégradé mais présent* ; le caviardage mosaïque détruit l'information (plusieurs lettres → même
moyenne de bloc), donc un restaurateur **hallucine** une lecture plausible. La porte physique ne peut
pas trancher un mosaïque ambigu (plusieurs restaurations re-pixelisent vers le même mosaïque — c'est le
but du caviardage). Valeur légitime de la diffusion : régime **flou** (plus discriminant) et petits
blocs. Le **modèle de diffusion lui-même est différé** (sidecar Python/GPU, hors du flux pur-Go).

**Objectif (ce sous-projet)** : livrer la **porte pur-Go manquante** — `unpixel.VerifyImage` — qui
ré-applique l'opérateur direct à une image restaurée fournie et la compare physiquement au caviardage,
plus l'outil MCP `unpixel_verify_image` et un **protocole sidecar documenté** décrivant comment un
restaurateur externe (diffusion ou autre) s'y branche. Pas de modèle, pas de sidecar Go, pas de CGO.

### Non-objectifs
- Pas de modèle de diffusion, pas d'entraînement, pas de sidecar Python/GPU, pas de `os/exec` (le dépôt
  n'a jamais lancé de sous-processus ; on ne commence pas). Le restaurateur est orchestré à l'extérieur,
  comme la boucle propose→vérifie LLM existante.
- Pas de tag `//go:build` (contrairement à #4/#5 dont le modèle est une passe-avant IN-Go) : ici le
  modèle est un processus externe → rien à compiler-conditionner côté Go.
- Aucune régression : `Verify`/`Recover`/cœur/panel 17/17 byte-identiques (refactor de préparation
  prouvé équivalent).

## 2. Critères de succès (mesurables)

1. **Identité de cas particulier** : `VerifyImage(ctx, redacted, render(texte))` ≈ `Verify(ctx, redacted,
   [texte])[0].Distance` (à tolérance près) — la porte image est la moitié basse de la porte chaîne.
2. **La porte accepte le vrai** : rendre le VRAI texte caché comme image « restaurée » ⇒ distance ≈ 0,
   `Match=true`.
3. **La porte rejette l'halluciné** : rendre un texte FAUX comme image « restaurée » ⇒ distance haute,
   `Match=false` (pour un mosaïque non ambigu).
4. **Refactor sans régression** : la suite `Verify` existante reste verte (préparation extraite,
   comportement inchangé) ; panel 17/17 intact.
5. Pur-Go, cagé, fixtures en-mémoire, couverture ≥ 85 %.

## 3. Architecture & composants

`Verify` (verify.go:48) a déjà la structure exacte : un **prologue de préparation** (ToRGBA →
auto-contraste → deskew → auto-crop → inférence bloc → applyDefaults → auto-fingerprint → auto-calibrate
→ wire composants, lignes 56-133), puis une étape de scoring déléguée à `DefaultVerifyCore`.
`VerifyImage` partage le prologue et change l'étape finale.

1. **Refactor (byte-identique) dans `verify.go`** : extraire le prologue 56-133 en
   `func prepareVerify(img image.Image, opts []Option) (*image.RGBA, Config, error)`. `Verify` devient :
   `rgba,cfg,err := prepareVerify(img,opts) ; … ; DefaultVerifyCore(ctx,rgba,cfg,capped)`. Comportement
   de `Verify` STRICTEMENT inchangé (mêmes étapes, même ordre, mêmes hooks).

2. **`unpixel.VerifyImage`** (verify.go) :
   ```go
   // ImageVerdict is the result of physically verifying a restored image against
   // a redaction by re-applying the forward operator.
   type ImageVerdict struct {
       Distance float64 // whole-image distance in [0,1], lower = more consistent
       Match    bool    // Distance < VerifyMatchThreshold
   }

   func VerifyImage(ctx context.Context, redacted, restored image.Image, opts ...Option) (ImageVerdict, error)
   ```
   - nil `redacted` ou `restored` ⇒ `ErrNilImage` ; hook absent ⇒ `ErrNoComponents`.
   - `rgba,cfg,err := prepareVerify(redacted, opts)` (même prologue) ; puis délègue à
     `DefaultVerifyImageCore(ctx, rgba, ToRGBA(restored), cfg)`.
   - Doc : `Verify(texte) ≡ VerifyImage(render(texte))` ; flou via `WithPixelator` comme `Verify`.

3. **Hook `DefaultVerifyImageCore`** (root, comme `DefaultVerifyCore`) :
   ```go
   var DefaultVerifyImageCore func(ctx context.Context, redacted, restored *image.RGBA, cfg Config) (ImageVerdict, error)
   ```
   Implémenté dans `defaults` (qui peut importer imutil/pixelate ; pas de cycle).

4. **`defaults.verifyImageCore`** (impl du hook, pur-Go) :
   - Redimensionne `restored` aux bornes de `redacted` (pur-Go `golang.org/x/image/draw`, CatmullRom)
     si tailles différentes.
   - Cherche la **phase de grille** : pour `ox,oy ∈ [0, blockSize)` (borné ; blockSize petit),
     `reMosaic := cfg.Pixelator.Pixelate(restoredRGBA, ox, oy)` ; `d := cfg.Metric.Compare(reMosaic, redacted)` ;
     garder le **min** (meilleure phase) — cohérent avec « best grid offset » de `Verify`. Pour un
     pixelateur flou (offsets ignorés) la boucle renvoie une valeur unique (inoffensif).
   - `ImageVerdict{Distance: min, Match: min < unpixel.VerifyMatchThreshold}`.

5. **MCP `unpixel_verify_image`** (`mcp/`) : entrée `{redaction_path, restored_path, block_size?}` →
   `ImageVerifyReport{Distance float64, Match bool}`. Cœur testable `VerifyImageMCP(ctx, redacted,
   restored, blockSize)` appelant `unpixel.VerifyImage`. (Pas de charset : aucun rendu de chaîne.)

6. **Protocole sidecar documenté** (`docs/`) : un fichier décrivant le contrat JSON/fichier qu'un
   restaurateur externe (diffusion ou autre) suit — entrée (image caviardée + indices `propose_hints`),
   sortie (N images restaurées candidates) — et la **boucle anti-hallucination** : sidecar restaure →
   `unpixel_verify_image` (ou `VerifyImage`) garde-fou physique → ne garder que les `Match`. Inclut les
   **limites assumées** (§4) et précise que le modèle est différé.

Fichiers : `verify.go` (prepareVerify + ImageVerdict + VerifyImage + hook) ; `defaults/defaults.go`
(verifyImageCore + wiring init) ; `mcp/verify_image.go` (outil + cœur) ; `docs/sidecar-protocol.md`
(protocole). Tests associés.

## 4. Limites assumées (documentées)

- **Mosaïque ambigu** : la porte ne peut PAS distinguer des restaurations qui re-pixelisent vers le même
  mosaïque (le caviardage est conçu pour ça). Elle rejette les hallucinations qui NE matchent PAS, pas
  celles qui matchent par ambiguïté.
- **Flou > mosaïque** : la porte est plus discriminante en régime flou (moins many-to-one).
- **Recherche de phase = optimiste** : prendre le min sur les phases peut flatter une restauration mal
  alignée ; cohérent avec le « best offset » de `Verify`, documenté.
- **Modèle différé** : ce sous-projet livre la PORTE + le protocole, pas un recouvreur. Aucun gain de
  décodage tant qu'un restaurateur externe n'est pas branché.

## 5. Intégration & compatibilité

- Strictement additif : `VerifyImage` est une nouvelle entrée ; `Verify`/`Recover`/cœur inchangés ;
  prologue extrait = refactor byte-identique (test de non-régression `Verify`). Panel 17/17 intact.
- Pas de cycle (`verifyImageCore` dans `defaults`, hook dans root, comme `DefaultVerifyCore`). Pas de
  CGO, pas de `os/exec`, pas de dépendance nouvelle au-delà de `golang.org/x/image/draw` (déjà présent
  via x/image). Réutilise l'opérateur direct + métrique existants.
- Orchestration externe identique à propose→vérifie : le restaurateur (hors dépôt) propose des images,
  le Go les garde-fou via `VerifyImage`.

## 6. Tests & validation (pur-Go, cagé, fixtures en-mémoire)

- `verify.go` : identité `VerifyImage(redacted, render(texte))` ≈ `Verify(redacted,[texte])` (même
  distance à tolérance) ; vrai-texte ⇒ Match=true, faux-texte ⇒ Match=false (mosaïque non ambigu) ;
  nil images ⇒ ErrNilImage ; `restored` de taille différente redimensionné (toujours un verdict).
- `defaults` : `verifyImageCore` choisit la meilleure phase (min) ; un `restored` désaligné d'une
  phase est toujours accepté via la recherche de phase.
- `Verify` non-régression : suite `Verify` existante verte après extraction de `prepareVerify`.
- `mcp` : `unpixel_verify_image` renvoie Distance/Match sur fixture en-mémoire ; vrai vs faux.
- Couverture ≥ 85 % ; pur-Go ; panel 17/17 inchangé.

## 7. Découpage (unités isolées)

1. `verify.go` — extraire `prepareVerify` (refactor byte-identique de `Verify`) + tests de non-régression.
2. `verify.go` + root — `ImageVerdict`, `VerifyImage`, hook `DefaultVerifyImageCore`.
3. `defaults/defaults.go` — `verifyImageCore` (resize + recherche de phase + métrique) + wiring init + tests.
4. `mcp/verify_image.go` — outil `unpixel_verify_image` + cœur `VerifyImageMCP` + tests + enregistrement.
5. `docs/sidecar-protocol.md` — contrat JSON + boucle anti-hallucination + limites.
6. Validation : identité Verify≡VerifyImage∘render, accept/reject, non-régression, couverture, CI.

## 8. Risques & parades

- **Refactor casse `Verify`** → extraction mécanique, test de non-régression `Verify` + panel 17/17.
- **Cycle d'import** → `verifyImageCore` dans `defaults` derrière le hook `DefaultVerifyImageCore` (même
  schéma que `DefaultVerifyCore`) ; `go build ./...` le vérifie.
- **Recherche de phase optimiste / coûteuse** → bornée à `blockSize²` (petit) ; documentée comme « best
  offset » à la `Verify`.
- **Attentes irréalistes** → §4 explicite : porte, pas recouvreur ; mosaïque ambigu non tranchable ;
  modèle différé. Aucune sur-promesse dans README/MCP.
- **Introduire `os/exec`** → explicitement HORS périmètre ; le restaurateur est externe/LLM-orchestré.
