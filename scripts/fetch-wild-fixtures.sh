#!/usr/bin/env bash
# fetch-wild-fixtures.sh — downloads the 10 real-world test images and their
# ground-truth counterparts into testdata/wild/. Idempotent: existing files are
# skipped. Fail-soft per file: a download error is printed but does not abort
# the rest of the batch.
#
# Usage:
#   scripts/fetch-wild-fixtures.sh
#
# The script reads URL/filename pairs from the manifest table below rather than
# from manifest.json so it has no runtime dependency on a JSON parser. Both
# sources are maintained in sync (manifest.json is the machine-readable truth;
# this script is the human-runnable downloader).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$REPO_ROOT/testdata/wild"
mkdir -p "$DEST"

# fetch FILE URL — downloads URL to DEST/FILE if not already present.
fetch() {
    local file="$1"
    local url="$2"
    local dest="$DEST/$file"
    if [[ -f "$dest" ]]; then
        echo "skip  $file (already present)"
        return 0
    fi
    echo "fetch $file ..."
    if curl -sSL --max-time 30 -o "$dest" "$url"; then
        echo "ok    $file"
    else
        echo "FAIL  $file (curl error; skipping)" >&2
        rm -f "$dest"   # remove partial download
    fi
}

# ── mosaic images ──────────────────────────────────────────────────────────────
fetch m1_unredacter_secret.png \
    "https://raw.githubusercontent.com/BishopFox/unredacter/main/secret.png"

fetch m2_depix_testimage1_pixels.png \
    "https://raw.githubusercontent.com/spipm/Depixelization_poc/main/images/testimages/testimage1_pixels.png"

fetch m3_depix_testimage2_pixels.png \
    "https://raw.githubusercontent.com/spipm/Depixelization_poc/main/images/testimages/testimage2_pixels.png"

fetch m4_depix_testimage3_pixels.png \
    "https://raw.githubusercontent.com/spipm/Depixelization_poc/main/images/testimages/testimage3_pixels.png"

fetch m4_gt_testimage3.png \
    "https://raw.githubusercontent.com/spipm/Depixelization_poc/main/images/testimages/testimage3.png"

fetch m5_depix_sublime_pixels.png \
    "https://raw.githubusercontent.com/spipm/Depixelization_poc/main/images/testimages/sublime_screenshot_pixels_gimp.png"

fetch m5_gt_sublime.png \
    "https://raw.githubusercontent.com/spipm/Depixelization_poc/main/images/testimages/sublime_screenshot.png"

# ── blur images ────────────────────────────────────────────────────────────────
fetch b1_deepdeblur_blurrytest1.png \
    "https://raw.githubusercontent.com/meijianhan/DeepDeblur/master/TestImage/BlurryTest1.png"

fetch b2_deepdeblur_testing1.png \
    "https://raw.githubusercontent.com/meijianhan/DeepDeblur/master/TestImage/Testing_1.png"

fetch b3_blurtext_images1.png \
    "https://raw.githubusercontent.com/potru-sujitha/text-images-deblurring/main/images1.png"

fetch b3_gt_images1.png \
    "https://raw.githubusercontent.com/potru-sujitha/text-images-deblurring/main/images1_CLEAR_TEXT.png"

fetch b4_blurtext_images3.jpg \
    "https://raw.githubusercontent.com/potru-sujitha/text-images-deblurring/main/images3.jpg"

fetch b4_gt_images3.png \
    "https://raw.githubusercontent.com/potru-sujitha/text-images-deblurring/main/images3_CLEAR_TEXT.png"

fetch b5_blurtext_images5.jpg \
    "https://raw.githubusercontent.com/potru-sujitha/text-images-deblurring/main/images5.jpg"

fetch b5_gt_images5.png \
    "https://raw.githubusercontent.com/potru-sujitha/text-images-deblurring/main/images5_CLEAR_TEXT.png"

echo ""
echo "done — files in $DEST"
