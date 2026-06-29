# Spec — #6 Checksum pruning in the trellis

Date : 2026-06-29 · Statut : design approuvé (brainstorm) · Sous-projet 5 du programme
« débloquer le décodage » (cf. `PROGRESS.md` → 🗺️ Programme). Additif/opt-in, cœur intouché.
#2/#1B/#1/#3 mergés, CI verte.

## 1. Problème & objectif

Les secrets structurés (cartes bancaires, IBAN, dates, téléphones, IDs numériques) ont des
contraintes de format fortes. Aujourd'hui `internal/secrets` les exprime seulement comme un PRIOR
de plausibilité post-hoc (`secrets.Prior`, bonus mou via `WithPriors`) — la recherche explore
quand même tout l'espace, et les digits échouent souvent (« digits decode fails » dans
`mcp-decode-campaign-gaps`).

**Objectif** : élaguer les candidats infaisables **pendant** le beam via le mécanisme
`search.Constraint` existant (`AllowedAt(pos, prefix) []rune`), réduisant l'espace (~10× sur
formats structurés) et corrigeant la récupération. Mettre le **checksum DANS le trellis** : pour
un format Luhn à longueur connue, `AllowedAt(dernière position)` ne renvoie QUE le(s) chiffre(s)
de contrôle qui valident le préfixe.

### Non-objectifs
- Aucune régression sur le texte libre : la contrainte est OPT-IN (`WithExpectedFormat`) ; non
  posée ⇒ comportement byte-identique.
- Pas de grammaires/per-pays exhaustives (cf. §4 limites assumées).

## 2. Critères de succès (mesurables)

1. `WithExpectedFormat(FormatNone)` ou option absente ⇒ décodage **byte-identique** (panel 17/17
   trivialement préservé ; texte libre jamais élagué).
2. Sur une fixture digits forgée, `FormatDigits` recouvre là où le décodage non contraint échoue.
3. Sur une fixture carte bancaire forgée, la chaîne recouvrée **passe Luhn par construction**
   (contrainte de dernière position) et la recherche visite **moins de nœuds** que sans contrainte
   (assertion de comptage).
4. Pur-Go, cagé, fixtures en-mémoire, couverture ≥ 85 %.

## 3. Architecture & composants

1. **Modèle de format dans `internal/secrets`** :
   ```go
   type Format uint8
   const (
       FormatNone Format = iota
       FormatDigits      // IDs numériques / PIN : 0-9, longueur variable
       FormatCreditCard  // 0-9, longueur {13,15,16,19}, Luhn
       FormatIBAN        // A-Z A-Z 0-9 0-9 + alphanum ; len 15-34 ; mod-97 (générique)
       FormatDate        // layouts courants (YYYY-MM-DD, DD/MM/YYYY) + bornes de champ
       FormatPhoneFR     // 10 chiffres, [0][1-9]…  (ou +33 + 9)
       FormatPhoneUS     // 10 chiffres NANP : indicatif & central [2-9]XX
       FormatPhoneE164   // + puis 7-15 chiffres (générique, sans plan national)
   )
   // AllowedRunesAt renvoie les runes faisables à pos pour un préfixe de longueur connue
   // totalLen (0 = inconnue). Renvoie nil pour « aucune contrainte à cette position ».
   func AllowedRunesAt(f Format, pos int, prefix string, totalLen int) []rune
   // Valid valide une chaîne COMPLÈTE pour le format (Luhn, mod-97, bornes date, plan tel).
   func Valid(f Format, s string) bool
   ```
   Réutilise `Luhn` ; ajoute `ibanValid` (mod-97), `dateValid`, `phoneValid(region)`.

2. **`search.Constraint` impl — `formatConstraint`** (nouveau fichier `internal/search`) qui adapte
   un `secrets.Format` (+ longueur cible) en `AllowedAt(pos, prefix)` :
   - **Digits / NumericID** : `0-9` à chaque position.
   - **CreditCard** : `0-9` ; à longueur connue L, `AllowedAt(L-1)` = chiffre(s) Luhn-valides du
     préfixe (checksum-in-trellis) ; longueur inconnue ⇒ digits + filtre Luhn au leaf.
   - **IBAN** (générique) : `A-Z` pos 0–1, `0-9` pos 2–3, alphanum ensuite, len 15–34 ; filtre
     mod-97 au leaf.
   - **Date** : positions chiffre/séparateur + bornes (mois ≤12, jour ≤31) pour layouts courants.
   - **Phone** : par région — **FR** `[0][1-9]` + 8 chiffres (ou `+33`+9) ; **US/NANP** 10 chiffres,
     indicatif et central commençant par `2-9` ; **E164** `+` puis 7–15 chiffres.

3. **Filtre checksum au leaf** : sur candidats complets, `secrets.Valid(format, s)` ; un candidat
   invalide est rejeté (hard) avant le classement (pour les formats à checksum/bornes que la
   feasibility par-position ne capture pas entièrement, et comme garde-fou longueur-inconnue).

4. **Opt-in / câblage** : `WithExpectedFormat(secrets.Format)` (option root) installe le
   `formatConstraint` via le chemin `GuidedDFSConstrained` existant (hook analogue à celui de
   `WithPrefix` / `DefaultConstrainedStrategy`) et active le filtre leaf. Non posé ⇒ inchangé.

Fichiers : `internal/secrets/format.go` (Format + AllowedRunesAt + Valid + validators) ;
`internal/search/formatconstraint.go` (Constraint adapter) ; `unpixel.go` (`WithExpectedFormat`
option + wiring hook) ; `defaults/defaults.go` (hook impl) ; `mcp/decode.go` (`expected_format`).

## 4. Limites assumées (réalité des 5 formats — documentées)

- **Checksums standardisés réels** : Luhn (CreditCard), mod-97 (IBAN).
- **Téléphone par région** : FR et US/NANP ont des plans différents (validé : FR = 10 chiffres
  `0[1-9]…` ou `+33`+9 ; NANP = 10 chiffres, indicatif/central `[2-9]XX` ou `+1`+10). E164 générique
  = `+` puis 7–15 chiffres. Pas de validation d'opérateur/zone fine ; régions au-delà de FR/US/E164
  différées.
- **IBAN générique** : structure + len 15–34 + mod-97, **sans tables BBAN par pays** (différé).
- **Date** : layouts courants seulement (`YYYY-MM-DD`, `DD/MM/YYYY`) ; grammaires exotiques différées.
Ces limites élaguent quand même fort (digits/lettres/séparateurs/longueur + checksum) sans surajuster.

## 5. Intégration & compatibilité

- `WithExpectedFormat` opt-in ; absent ⇒ byte-identique (invariant panel trivial).
- MCP `decode` gagne un champ optionnel `expected_format` (string → secrets.Format) transmis au moteur.
- Cœur de décodage intouché hormis le câblage additif de la contrainte (comme `WithPrefix`).

## 6. Tests & validation

- Fixtures forgées en-mémoire : mosaïque digits recouvrée sous `FormatDigits` ; mosaïque carte
  bancaire dont la récupération passe Luhn ; assertion de comptage de nœuds (contraint < non
  contraint) ; régression sans-format (texte libre inchangé).
- Tests unitaires `secrets` : `AllowedRunesAt` (digits-only ; Luhn dernière position ; IBAN
  positions ; date bornes ; phone FR vs US) ; `Valid` (Luhn, mod-97, bornes date, plan FR/US).
- Cagé ; couverture ≥ 85 % ; pur-Go.

## 7. Découpage (unités isolées)

1. `internal/secrets/format.go` — `Format` + `AllowedRunesAt` + `Valid` + validators (Luhn réutilisé,
   mod-97, date, phone FR/US/E164).
2. `internal/search/formatconstraint.go` — `formatConstraint` implémentant `search.Constraint`.
3. `unpixel.go` + `defaults/defaults.go` — `WithExpectedFormat` option + hook de stratégie contrainte
   + filtre leaf.
4. `mcp/decode.go` — champ `expected_format`.
5. Tests + validation (comptage de nœuds, régression sans-format) + couverture.

## 8. Risques & parades

- **Élagage à tort du texte libre** → strictement opt-in ; `FormatNone`/absent = inchangé ; test de
  régression sans-format.
- **Longueur inconnue + Luhn** → la contrainte dernière-position exige une longueur cible ; sinon
  digits-only + filtre Luhn au leaf (toujours correct, juste moins d'élagage).
- **Faux rejets** (format mal déclaré par l'appelant) → c'est un choix explicite de l'appelant ;
  documenté ; `FormatNone` pour revenir au comportement non contraint.
- **Cross-format / concurrence** → `formatConstraint.AllowedAt` doit être sans état/concurrent-safe
  (comme `PrefixConstraint`/`TemplateConstraint` existants).
- **Invariant panel** → la contrainte ne s'active jamais sans `WithExpectedFormat` ; test sans-format.
