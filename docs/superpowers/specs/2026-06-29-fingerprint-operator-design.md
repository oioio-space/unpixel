# Spec — #2 Fingerprint-operator (auto-détection du modèle direct)

Date : 2026-06-29 · Statut : design approuvé (brainstorm) · Sous-projet 1 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme, et mémoire
`decode-techniques-research-2026`).

## 1. Problème & objectif

UnPixel récupère bien le synthétique (fixtures 17/17, blur 13/14) mais échoue sur les images
réelles. Le constat de la recherche : **ce n'est pas un échec de recherche, c'est un mismatch
du modèle direct (forward operator)** — si le ré-opérateur (gamma, origine/taille de grille,
mosaïque-vs-flou, σ, famille de noyau) ne reproduit pas exactement les blocs observés,
`pixelmatch` n'atteint jamais 0 et le moteur conclut « faux avec assurance » (conf 1.000 /
fidélité 0.000 dans le journal `real`).

**Objectif** : détecter automatiquement le bon opérateur direct pour une image donnée, l'appliquer
au chemin generate-and-test existant, **sans jamais régresser** ce qui marche déjà.

### Non-objectifs (hors périmètre de #2)
- L'**operator-zoo par-outil** + la méta-stratégie top-2 → sous-projet **#1B** (le descripteur
  `Operator` défini ici est la couture pour l'y brancher).
- La déconvolution / le décodage du flou eux-mêmes (déjà présents : `pixelate/deconv.go`,
  `RecoverBlurred`) — #2 ne fait que *détecter et sélectionner* le bon opérateur, pas réécrire les
  décodeurs.

## 2. Critères de succès (mesurables)

1. **Invariant préservé** : panel fixtures **17/17 fidélité 1.000** et blur **13/14** strictement
   inchangés. Garanti par le repli sûr (cf. §5).
2. **Round-trip synthétique** : pour une matrice d'opérateurs connus
   {gamma ∈ (sRGB, linéaire)} × {kind ∈ (mosaïque, box-3, vraie-gaussienne)} × {σ, taille de bloc},
   `Fingerprint` retrouve les bons attributs avec confiance ≥ seuil sur **≥ 90 %** des cas générés.
3. **Auto-flou câblé** : `Recover` avec `WithAuto()` (sans appel manuel `RecoverBlurred`) atteint
   le **même** résultat que `RecoverBlurred` manuel sur tout le corpus `blur` (aujourd'hui le flou
   exige un chemin séparé).
4. **Perf** : `Fingerprint` et le nouveau code de détection de flou portent des `Benchmark…` et tout
   changement perf-affectant est prouvé au benchstat (règle hot-path du projet).

## 3. Architecture

Nouveau package `internal/forensics` qui **agrège** les détecteurs existants et **ajoute** la
détection de flou, exposant un descripteur unique.

```go
package forensics

type Kind uint8   // KindUnknown, KindMosaic, KindBlur
type Gamma uint8  // GammaUnknown, GammaSRGB, GammaLinear
type Kernel uint8 // KernelUnknown, KernelBox3, KernelTrueGauss
type Edge uint8   // EdgeUnknown, EdgeClamp, EdgeReflect, EdgeWrap

// Operator décrit le modèle direct détecté pour une image caviardée.
type Operator struct {
    Kind      Kind
    Gamma     Gamma
    Block     int          // taille de bloc (mosaïque)
    GridPhase image.Point  // origine de grille
    Stretch   float64      // anisotropie x (1.0 = aucune)
    Sigma     float64      // écart-type du flou (Kind == KindBlur)
    Kernel    Kernel
    Edge      Edge         // best-effort
    Tool      string       // étiquette informative best-effort ("GEGL", "Photoshop"…)
    Conf      Conf         // confiance par attribut, dans [0,1]
}

type Conf struct{ Kind, Gamma, Grid, Sigma, Kernel float64 }

// Hint porte ce que l'appelant sait déjà (évite de re-détecter).
type Hint struct{ Block int /* 0 = inconnu */ }

// Fingerprint analyse img et renvoie l'opérateur direct le plus probable.
// Ne nécessite ni police, ni charset, ni rendu candidat.
func Fingerprint(img image.Image, hint Hint) Operator

// Pixelator construit l'opérateur forward correspondant. ok=false si la confiance
// de l'attribut décisif est sous le seuil (l'appelant retombe alors sur le défaut).
func (o Operator) Pixelator(threshold float64) (unpixel.Pixelator, bool)
```

**Couture #1B** : une évolution future `FingerprintN(img, hint) []Operator` (classés par
confiance) permettra à #1B d'essayer le top-2 ; #2 n'implémente que le singulier.

## 4. Méthodes de détection

| Attribut | Méthode | Origine |
|---|---|---|
| Gamma (linéaire/sRGB) | `pixelate.DetectColorspace` (ratio des deltas + Jensen gap) | **réutilisé** |
| Taille de bloc | `pixelate.InferBlockSize` / `grid.go` | **réutilisé** |
| Origine de grille | `InferGridPhase` | **réutilisé** |
| Anisotropie x (stretch) | `InferXStretch` | **réutilisé** |
| **Mosaïque vs flou** | variance haute-fréquence intra-bloc à l'origine optimale : ≈ 0 ⇒ mosaïque ; gradient continu ⇒ flou | **nouveau** |
| **σ du flou** | pente des bords / spectre ; validé en re-floutant et comparant | **nouveau** |
| **Famille de noyau** | box-3 (3 passes, queues plates) vs vraie gaussienne, discriminé au bord du caviardage | **nouveau** |
| Gestion des bords | clamp/reflect/wrap au bord — best-effort | **nouveau, faible enjeu** |
| Tool (étiquette) | dérivé des paramètres (ex. linéaire+box-3 ⇒ CSS/GEGL ; sRGB+mosaïque ⇒ Photoshop) | **nouveau, informatif** |

Aucune détection ne requiert de rendu candidat (toutes opèrent sur l'image caviardée seule).

## 5. Intégration & compatibilité

- **Câblage interne** : les flags `autoColorspace`/`autoCalibrate` et l'ombrelle `WithAuto()`
  routent désormais par `forensics.Fingerprint`. `WithAuto()` est **étendu** pour couvrir aussi la
  détection de flou (écran de provenance inconnue ⇒ mosaïque *ou* flou traités sans flag manuel).
- **Nouvelle option granulaire** : `WithAutoBlur()` (détecter mosaïque-vs-flou + σ et choisir
  l'opérateur de flou). `WithAuto()` l'inclut.
- **Compatibilité ascendante** : `WithAutoColorspace()` / `WithAutoCalibrate()` **restent
  fonctionnels** (délèguent au fingerprint), aucun appelant cassé (CLI `cmd/unpixel`, MCP
  `mcp/decode.go`). Dépréciation douce documentée, pas de suppression.
- **Repli sûr (choix 1)** : `Operator.Pixelator(threshold)` renvoie `ok=false` sous le seuil de
  confiance ⇒ l'appelant garde le défaut actuel exact (sRGB `BlockAverage` / chemin standard).
  **Le fingerprint ne peut qu'aider, jamais nuire.** C'est ce qui garantit l'invariant §2.1.
- **MCP** : `analyze` expose le descripteur `Operator` (le rapport gagne gamma/σ/kernel/tool
  détectés + confiances) ; le chemin `decode` l'utilise via `WithAuto()`.

## 6. Tests & validation

- **Round-trip** (`forensics/*_test.go`) : générer des images avec chaque opérateur connu →
  vérifier que `Fingerprint` retrouve les attributs (≥ 90 %, cf. §2.2). `got` avant `want`.
- **Non-régression** : panel fixtures 17/17 + blur 13/14 via le gate existant (`bench:panel` /
  journal). Le repli sûr doit rendre le chemin par défaut **byte-identique** quand la confiance est
  basse — test dédié.
- **Benchmarks** : `BenchmarkFingerprint`, `BenchmarkDetectBlur` (+ `b.ReportAllocs()`), baseline
  avant / benchstat après.
- **Journal** : une entrée pour tracer l'effet sur `real`/`blur`.

## 7. Découpage (unités isolées)

1. `internal/forensics` — types `Operator`/`Conf`/`Hint`, `Fingerprint`, `Pixelator(threshold)` ;
   agrège les détecteurs existants.
2. `internal/pixelate` (extension) — `DetectBlur(img, hint) (kind, sigma, kernel, edge, conf)` :
   le nouveau cœur de détection mosaïque-vs-flou + σ + noyau.
3. `unpixel.go` (câblage) — router `autoColorspace`/`autoCalibrate`/`WithAuto()` par `forensics` ;
   ajouter `WithAutoBlur()` ; conserver les options existantes en délégation.
4. `mcp/analyze.go` (exposition) — ajouter le descripteur `Operator` au rapport.

Chaque unité a un rôle unique, une interface claire (`Operator` est le contrat), et se teste
indépendamment.

## 8. Risques & parades

- **Faux positif de détection** → repli sûr (seuil) ; le défaut n'est jamais inversé à tort.
- **σ mal estimé** → la validation par re-floutage borne l'erreur ; à confiance basse, repli.
- **Coût perf de la détection** → `Fingerprint` tourne **une fois** par décodage (hors boucle
  interne) ; benchmarké quand même.
- **Cross-metric (leçon de l'ensemble)** : #2 ne compare PAS des distances entre opérateurs
  différents (c'est #1B). Il sélectionne par *confiance de détection*, pas par distance — donc pas
  de piège « faux avec assurance » ici.
