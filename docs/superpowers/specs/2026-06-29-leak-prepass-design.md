# Spec — #1 Leak pre-pass

Date : 2026-06-29 · Statut : design approuvé (brainstorm) · Sous-projet 3 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Indépendant de #2/#1B (chemin
fichier, pas image).

## 1. Problème & objectif

Beaucoup de caviardages réels « fuient » l'original par les métadonnées / le format du fichier,
avant même toute inversion de pixels (cf. recherche `decode-techniques-research-2026` : EXIF
thumbnail non re-rendue, PDF rectangle noir sur texte non-aplati, Office = zip+XML, doublon
visible). Le « free-lunch » : si l'original fuite, on le récupère sans décodage.

**Objectif** : une **pré-passe pure-Go au niveau FICHIER** qui détecte et extrait l'original via
ces fuites **avant** le pipeline de décodage, et court-circuite le moteur quand une fuite est
trouvée — sinon laisse le pipeline normal s'exécuter inchangé.

### Non-objectifs
- OCR (le détecteur partial est *assisté par texte-visible*, pas OCR — Tier-2 #5).
- Décodeurs pixel (inchangés). Formats avancés (HEIC/TIFF EXIF, PDF chiffrés, PPTX notes) → différés.

## 2. Critères de succès (mesurables)

1. Chaque détecteur récupère sa fuite sur une fixture positive forgée (JPEG à miniature
   embarquée ; PDF texte sous rectangle ; .docx texte sous forme ; image + indice visible).
2. **Zéro faux positif** sur entrées propres : chaque détecteur s'abstient (jamais d'invention).
3. **Le chemin image-decode/`Recover` est INTOUCHÉ** → invariant panel 17/17 trivialement
   préservé (`leak.Scan` opère sur le chemin fichier, pas sur `image.Image` ; le cœur ne l'appelle
   jamais).
4. Pur-Go, `CGO_ENABLED=0`, tests cagés, couverture ≥ 85 % maintenue.

## 3. Architecture & composants

Nouveau package `internal/leak` ; unités à rôle unique, testables isolément.

```go
package leak

// Source identifie le canal de fuite exploité.
type Source string
const (
    SourceEXIFThumbnail Source = "exif-thumbnail"
    SourcePDFText       Source = "pdf-text"
    SourceOfficeText    Source = "office-text"
    SourcePartial       Source = "partial-redaction"
)

// Result porte ce qui a été récupéré. Text OU Image selon la source.
type Result struct {
    Source     Source
    Text       string       // pdf/office/partial
    Image      image.Image  // exif-thumbnail (raster fuité)
    Confidence float64      // [0,1]
    Notes      []string
}

// Options paramètre le scan (le partial consomme VisibleText).
type Options struct {
    VisibleText string // indice de texte visible pour le détecteur partial ; "" = abstention
}

// Scan renifle le fichier à path et tente chaque détecteur applicable.
// found=false quand aucune fuite n'est trouvée (le caller poursuit le pipeline normal).
func Scan(path string, opts Options) (Result, bool, error)
```

**Dispatch par reniflage de contenu (magic bytes, pas l'extension)** :
- **JPEG avec APP1/EXIF** (`FFD8` + segment `APP1` `Exif\0\0`) → `exifThumbnail` :
  parseur **main-levée** APP1→IFD0→IFD1, extrait le JPEG de la miniature (tags
  `JPEGInterchangeFormat`/`Length` de l'IFD1). **Aucune dépendance.**
- **`%PDF-`** → `pdfText` (rsc.io/pdf, pur-Go, BSD) : pour chaque page, `Content()` donne
  `Text[]` (avec X,Y,W,FontSize,S) et `Rect[]` ; renvoie le texte dont la position tombe dans un
  rectangle rempli (= texte sous le caviardage non-aplati).
- **ZIP + `[Content_Types].xml`** (.docx/.pptx) → `officeText` (`archive/zip` + `encoding/xml`,
  **stdlib seul**) : extrait le texte du corps (`w:t` / `a:t`).
- **image simple, sans autre fuite** → `partial` : si `opts.VisibleText != ""`, localise ce texte
  dans l'image (gabarit/refmatch léger) et confirme le doublon ; sinon **abstention**.

Fichiers : `internal/leak/leak.go` (types + `Scan` + sniff/dispatch), `exifthumb.go`, `pdf.go`,
`office.go`, `partial.go`.

## 4. Dépendances

- **`rsc.io/pdf`** — NOUVELLE dépendance. Pur-Go (imports stdlib seulement), **BSD-3-Clause**,
  équipe Go. API `Reader.Page(n).Content().{Text,Rect}` (vérifié sur pkg.go.dev). Caveat assumé :
  « incomplete but works on real-world PDFs », support chiffrement limité → best-effort, l'absence
  de fuite n'est jamais une erreur fatale.
- `archive/zip`, `encoding/xml`, `image/jpeg` : **stdlib**.
- EXIF : **main-levée**, aucune dépendance (on n'a besoin que de la miniature IFD1, pas d'un
  parseur EXIF complet).

## 5. Intégration & compatibilité

- **CLI** (`cmd/unpixel`) : un drapeau `--leak-scan` (actif par défaut quand l'entrée est un
  chemin fichier) exécute `leak.Scan(path, …)` AVANT `loadImage`/décodage ; sur fuite confiante,
  imprime le résultat (« récupéré via <source> ») et **court-circuite** le décodage pixel.
- **MCP** : nouvel outil `unpixel_leak_scan(image_path, visible_text?)` renvoyant `Result` (texte,
  ou image en MCP image-content pour la miniature). `decode` peut, en amont, tenter `leak.Scan` et
  court-circuiter (optionnel — décision au plan).
- **Cœur intouché** : `Recover`, `New`, le panel n'appellent jamais `leak.Scan` → invariant
  préservé par construction.

## 6. Tests & validation

- Fixtures positives forgées **dans le test** (déterministes, pas de fetch réseau) :
  - JPEG avec miniature EXIF embarquée (construire un JFIF minimal avec APP1+IFD1+JPEG-thumb) ;
  - PDF minimal avec texte sous un rectangle rempli (octets PDF littéraux) ;
  - .docx/.pptx minimal (`archive/zip` en mémoire + XML `w:t`) ;
  - image + `VisibleText` → doublon localisé.
- Négatifs : entrées propres (PNG fixture, PDF sans rect, image sans indice) → `found=false`.
- `Scan` sniff : un fichier non reconnu → `found=false, err=nil`.
- Pur-Go, cagé. Couverture du package ≥ 85 %.
- Aucune fixture réseau/gitignored (leçon du fix CI : ne jamais dépendre d'un fichier non commité).

## 7. Découpage (unités isolées)

1. `internal/leak/leak.go` — types, `Scan`, sniff/dispatch.
2. `internal/leak/exifthumb.go` — extracteur miniature APP1/IFD1 (main-levée).
3. `internal/leak/pdf.go` — texte-sous-rectangle via rsc.io/pdf.
4. `internal/leak/office.go` — texte OOXML via archive/zip + encoding/xml.
5. `internal/leak/partial.go` — doublon assisté par texte-visible (abstention sans indice).
6. Intégration CLI (`--leak-scan`) + MCP (`unpixel_leak_scan`).
7. `go.mod`/`go.sum` : ajout `rsc.io/pdf`.

## 8. Risques & parades

- **Nouvelle dépendance** → unique, pur-Go/BSD, vérifiée ; isolée dans `internal/leak/pdf.go` ;
  `cgo:check` garde le pur-Go.
- **Faux positifs** → seuils de confiance + abstention ; tests négatifs explicites.
- **Fixtures fragiles** (leçon CI) → tout forgé en mémoire dans les tests, zéro fetch/gitignored.
- **PDF chiffré / exotique** → best-effort ; erreur de parsing = `found=false`, pas d'échec dur.
- **Sécurité (entrée hostile)** → parseurs sur fichiers fournis par l'utilisateur ; bornes de
  lecture (taille max miniature/zip-bomb) ; `#nosec` justifié sur l'ouverture de chemin
  utilisateur, comme l'existant. Anti-panique validé par fuzz (370 k entrées forgées, 0 panic ;
  revue finale) ; `rsc.io/pdf` ne lance aucune goroutine donc le `recover()` de `pdfText` capture
  toute panique de la lib.
- **Risque résiduel — bombe de décompression PDF** (suivi) : `rsc.io/pdf Content()` construit le
  `[]Text` complet d'une page (flux FlateDecode déflaté) AVANT que nos plafonds
  (`maxGlyphsPerPage`/`maxLeakedBytes`) ne s'appliquent. Borné par `maxReadBytes` (64 MiB entrée) +
  `maxPDFPages`, mais l'amplification mémoire par page reste une propriété de la lib tierce → à
  cadrer plus tard (budget mémoire/temps par page).
- **Pré-passe vs modes explicites** (suivi, CLI) : `--leak-scan` (défaut on) court-circuite avant
  `--rectify`/`--frame`/`--blind` ; si l'utilisateur demande explicitement un mode ET que le fichier
  fuite, la fuite l'emporte silencieusement → ignorer la pré-passe quand un mode explicite est posé,
  ou émettre un avis.
