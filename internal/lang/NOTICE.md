# Language data attribution

`words.txt`, `words_fr.txt`, `freq_en.txt`, `freq_fr.txt` are frequency-ordered
word lists (most-frequent first; line number = frequency rank) derived from the
**FrequencyWords** project by Hermit Dave, which is published under the **MIT
License**.

- Source: https://github.com/hermitdave/FrequencyWords (content/2018/{en,fr})
- Built from the OpenSubtitles 2018 frequency lists (`en_50k.txt`, `fr_50k.txt`).
- Processing: kept the top 10,000 entries per language, lowercased, restricted
  to alphabetic tokens (French keeps accents, apostrophes and hyphens), the
  per-line frequency count stripped, duplicates removed preserving rank order.

The original MIT license text governs reuse of this derived data.
