# Spec — #1B Operator-zoo + secured top-2 meta-strategy

Date : 2026-06-29 · Statut : design approuvé (brainstorm) · Sous-projet 2 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Suite directe de **#2
fingerprint-operator** (mergé, `172294b`), dont le descripteur `forensics.Operator` est la
couture.

## 1. Problème & objectif

#2 détecte UN opérateur direct et l'applique avec repli sûr, mais sur les images **ambiguës**
(linéaire-vs-sRGB limite, mosaïque-vs-flou limite) il choisit un seul opérateur qui peut être
le mauvais. On ne peut pas simplement « essayer 2 opérateurs et garder la distance minimale » :
un **mauvais opérateur peut produire une distance faussement basse avec un texte faux** — le mur
« faux avec assurance » (real : conf 1.000 / fidélité 0.000 dans le journal).

**Objectif** : quand le fingerprint est ambigu, essayer les **2 opérateurs candidats les plus
probables** par generate-and-test et choisir par **auto-cohérence physique** (le texte recouvré,
re-pixelisé via l'opérateur candidat, doit reproduire la signature d'opérateur OBSERVÉE) — sinon
**s'abstenir** (repli sûr), jamais une réponse fausse confiante.

### Non-objectifs
- **Filtre anti-bruit impulsionnel** (misroute `hello-world-noisy`) → HORS périmètre : c'est une
  pré-passe de débruitage séparable, suivi distinct.
- Décodeurs eux-mêmes (mosaïque/flou) inchangés — #1B ne fait que *sélectionner* l'opérateur.

## 2. Critères de succès (mesurables)

1. **Invariant** : panel fixtures **17/17 fidélité 1.000** + blur **13/14** inchangés. Garanti
   car les entrées confiantes (conf top-1 ≥ 0.95) gardent le chemin mono-opérateur de #2 ; la
   bande ne se déclenche jamais sur les fixtures (elles sont confiantes).
2. **Gain** : sur une fixture ambiguë construite (mosaïque linéaire qui fingerprinte sRGB de
   justesse), le top-2 + auto-cohérence recouvre la vérité là où le mono-best #2 se trompe.
3. **Zéro faux-confiant** : quand aucun candidat n'est auto-cohérent sous le seuil, le moteur
   **s'abstient** (repli) — jamais une chaîne fausse à haute confiance.
4. **Coût borné ≤ 2× recherche** dans la bande ; le classement du zoo entier est sans recherche.
   Prouvé au benchstat.

## 3. Architecture & composants

Unités à rôle unique, testables isolément :

1. **`forensics.FingerprintN(img image.Image, hint Hint) []Operator`** — la couture planifiée de
   #2. Classe **tout le zoo** de profils par cohérence avec les stats observées (ratio gamma,
   variance intra-bloc, lissé du flou). **Sans recherche** (pas de rendu candidat). Renvoie les
   opérateurs triés par confiance décroissante ; `Operator.Conf` porte le score.
   `Fingerprint` (singulier, #2) devient `FingerprintN(...)[0]` pour rester DRY.

2. **Zoo de profils** — `internal/forensics/profiles.go` : table `Profile{ Name string; Gamma;
   Kind; Kernel; Truncation float64; Edge }`. Outils : GEGL, Photoshop, GIMP, CSS, ffmpeg,
   OpenCV (+ « unknown » génériques). `Operator.Build` résout un profil en `pixelate.Pixelator`
   concret ; **les profils de configuration identique dédupliquent vers UN opérateur** (zoo nommé
   sans code dupliqué). La table mappe nom → config ; le builder mappe config → Pixelator.

3. **Nouveaux opérateurs `pixelate`** pour les différences par-outil RÉELLES absentes aujourd'hui,
   et seulement celles qu'un profil utilise (YAGNI) :
   - gestion des bords du flou : `Edge ∈ {Clamp, Reflect, Wrap}` (aujourd'hui `GaussianBlur` =
     clamp seul) → `NewGaussianBlurEdge(sigma, edge)` ;
   - troncature de noyau / nombre de passes box → paramètre sur le flou box.
   Les combinaisons déjà couvertes (mosaïque sRGB/linéaire, gaussienne clamp, box-3) réutilisent
   l'existant.

4. **Méta-stratégie sécurisée** — `internal/forensics/meta.go` (logique pure, testable) + un mince
   point d'appel dans `Recover`. Décision bandée → exécuter le top-2 via le moteur existant →
   contrôle d'auto-cohérence → gagnant | abstention.

## 4. Flot de contrôle (sélection sécurisée)

```
rank := FingerprintN(img, hint)            // pas cher, zoo entier
switch {
case conf(rank[0]) >= 0.95:                // confiant → mono-opérateur (#2, inchangé)
    return rank[0].Build(...)
case conf(rank[0]) < floor (~0.3):         // trop incertain → repli sûr (défaut, inchangé)
    return default
default:                                    // bande ambiguë
    cands := rank[:2]
    for op := range cands {
        text, dist := engine.run(op)                          // <= 2x recherche
        reproduced := Fingerprint(op.apply(render(text)))     // re-fingerprint
        coherent   := signatureMatches(reproduced, observed) && dist < distThreshold
    }
    winner := argmin dist parmi {coherent}  ;  sinon ABSTAIN -> repli
}
```

- **Auto-cohérence** : `signatureMatches` compare la signature de la reconstruction (gamma, kind,
  σ/block à tolérance) à l'opérateur OBSERVÉ. Un mauvais opérateur échoue ce test même à distance
  basse → c'est ce qui bloque le « faux avec assurance ».
- **Abstention** = repli sur le meilleur candidat à basse confiance OU le défaut, AVEC une
  confiance basse dans le `Result` (jamais une fidélité haute trompeuse).
- Le `floor` (~0.3) et `distThreshold` sont des constantes calibrées + commentées, ajustées sur
  les fixtures de round-trip.

## 5. Intégration & compatibilité

- La méta-stratégie bandée s'active sous l'option existante **`WithAuto()`** (pas de nouvelle
  option publique ; le chemin mono-opérateur de #2 reste pour `WithAutoColorspace`/`WithAutoBlur`
  hors bande). `New`/`Run` inchangés ; la méta-logique vit côté `Recover`.
- Couvre le trou « bande Conf 0.95–1.00 » de #2 (la bande l'exerce désormais).
- Repli sûr préservé : confiant → #2 ; très bas → défaut byte-identique.

## 6. Tests & validation

- **FingerprintN** : classement correct sur fixtures à opérateur connu (le vrai opérateur en
  tête), dédup des profils identiques.
- **Round-trip ambigu** : fixture linéaire-vs-sRGB limite + mosaïque-vs-flou limite → le top-2 +
  auto-cohérence élit le bon ; `got` avant `want`.
- **Anti-faux-confiant** : cas où le mauvais opérateur atteint une distance basse avec un texte
  faux → assert **abstention** (pas de fidélité haute fausse).
- **Non-régression** : panel 17/17 + blur 13/14 (les fixtures confiantes ne déclenchent pas la
  bande).
- **Benchmarks** : `BenchmarkFingerprintN` (classement zoo) + le chemin 2× ; benchstat, pas de
  régression hot-path (le classement est sans recherche ; la bande est ≤ 2×).

## 7. Découpage (unités isolées)

1. `internal/forensics/profiles.go` — table de profils nommés + résolution profil→config, dédup.
2. `internal/forensics` (extension) — `FingerprintN` (classement du zoo) ; `Fingerprint` délègue.
3. `internal/pixelate` (extension) — `NewGaussianBlurEdge(sigma, edge)` (+ troncature box si un
   profil l'exige).
4. `internal/forensics/meta.go` — décision bandée + auto-cohérence + abstention (logique pure).
5. `unpixel.go` (câblage) — `Recover` appelle la méta-stratégie sous `WithAuto()` dans la bande ;
   hors bande, chemin #2 inchangé.

## 8. Risques & parades

- **Coût 2×** → borné à la bande seulement ; classement sans recherche ; benchstat.
- **Auto-cohérence trop stricte → abstention excessive** → tolérances calibrées sur round-trip ;
  l'abstention retombe sur le meilleur candidat à basse confiance (pas pire que #2).
- **Zoo dupliqué** → dédup par config à la construction (la décision de design clé).
- **Invariant panel** → la bande ne s'active pas sur entrées confiantes ; test de non-régression.
- **Récursion** (méta appelle le moteur qui pourrait re-fingerprinter) → le moteur est invoqué
  avec un `Pixelator` explicite (comme la délégation blur de #2), ce qui court-circuite l'auto-
  fingerprint — à vérifier au plan.
