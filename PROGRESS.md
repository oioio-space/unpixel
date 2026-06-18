# UnPixel — Suivi de projet

Fichier de continuité : objectif, état, reste à faire, décisions, historique des
commits. La section **Historique** est alimentée automatiquement par le hook git
`post-commit` ; les autres sections sont maintenues à la main (par Claude) avant
chaque commit.

## 🎯 Objectif

Porter [bishopfox/unredacter](https://github.com/bishopfox/unredacter) en **Go** :
un outil qui reconstruit du texte caché derrière une pixelisation (cf.
[l'article Bishop Fox](https://bishopfox.com/blog/unredacter-tool-never-pixelation)).

## 📍 État actuel

Outillage qualité en place ; **portage pas encore commencé**.

- Skill `go-style-guide` (3 guides Google itemisés) — `.claude/skills/go-style-guide/`
- Gate déterministe pre-commit : gofmt + go vet + golangci-lint v2 + build + test
- Revue IA pre-commit (style-guide) : `.claude/hooks/commit-style-review.sh`
- Gate `/simplify` pre-commit : `.claude/hooks/pre-commit-simplify.sh`
- Tracking commits : ce fichier + hook `.githooks/post-commit`

## ✅ Reste à faire

- [ ] Étudier l'algo d'unredacter (brute-force des combinaisons de caractères,
      re-pixelisation, comparaison de distance d'image).
- [ ] Choisir les libs Go (rendu de police/texte, manipulation d'image).
- [ ] Structurer le code (`internal/…`) et écrire les tests de caractérisation.
- [ ] Implémenter le cœur de l'attaque, puis une CLI.

## 🧭 Décisions clés

- Module : `github.com/mathieu/unpixel`, Go 1.26.
- Deux couches de garde-fou : linters (objectif) + revue IA (subjectif).
- Hooks scindés git-natif (universel) / Claude Code (revue & gates pilotés par Claude).

## 🗂️ Historique des commits

<!-- Lignes ajoutées automatiquement par .githooks/post-commit (ne pas éditer à la main) -->

- `705a884` 2026-06-18 — chore: bootstrap Go style-guide skill + pre-commit hooks
