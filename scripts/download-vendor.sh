#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Download CDN vendor dependencies to pkg/web/static/vendor/
# Only downloads if local files don't already exist.
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
VENDOR_DIR="$PROJECT_DIR/pkg/web/static/vendor"

# Vendor resource definitions: "local_path|cdn_url"
# Paths are relative to VENDOR_DIR
RESOURCES=(
    # highlight.js
    "highlightjs/github.css|https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.11.1/build/styles/github.css"
    "highlightjs/highlight.min.js|https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.11.1/build/highlight.min.js"

    # KaTeX
    "katex/katex.min.css|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/katex.min.css"
    "katex/katex.min.js|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/katex.min.js"
    "katex/fonts/KaTeX_AMS-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_AMS-Regular.woff2"
    "katex/fonts/KaTeX_Caligraphic-Bold.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Caligraphic-Bold.woff2"
    "katex/fonts/KaTeX_Caligraphic-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Caligraphic-Regular.woff2"
    "katex/fonts/KaTeX_Fraktur-Bold.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Fraktur-Bold.woff2"
    "katex/fonts/KaTeX_Fraktur-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Fraktur-Regular.woff2"
    "katex/fonts/KaTeX_Main-Bold.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Main-Bold.woff2"
    "katex/fonts/KaTeX_Main-BoldItalic.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Main-BoldItalic.woff2"
    "katex/fonts/KaTeX_Main-Italic.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Main-Italic.woff2"
    "katex/fonts/KaTeX_Main-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Main-Regular.woff2"
    "katex/fonts/KaTeX_Math-BoldItalic.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Math-BoldItalic.woff2"
    "katex/fonts/KaTeX_Math-Italic.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Math-Italic.woff2"
    "katex/fonts/KaTeX_SansSerif-Bold.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_SansSerif-Bold.woff2"
    "katex/fonts/KaTeX_SansSerif-Italic.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_SansSerif-Italic.woff2"
    "katex/fonts/KaTeX_SansSerif-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_SansSerif-Regular.woff2"
    "katex/fonts/KaTeX_Script-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Script-Regular.woff2"
    "katex/fonts/KaTeX_Size1-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Size1-Regular.woff2"
    "katex/fonts/KaTeX_Size2-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Size2-Regular.woff2"
    "katex/fonts/KaTeX_Size3-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Size3-Regular.woff2"
    "katex/fonts/KaTeX_Size4-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Size4-Regular.woff2"
    "katex/fonts/KaTeX_Typewriter-Regular.woff2|https://cdn.jsdelivr.net/npm/katex@0.16.28/dist/fonts/KaTeX_Typewriter-Regular.woff2"

    # marked
    "marked/marked.umd.js|https://cdn.jsdelivr.net/npm/marked@18.0.5/lib/marked.umd.js"

    # marked-highlight
    "marked-highlight/index.umd.js|https://cdn.jsdelivr.net/npm/marked-highlight@2.2.4/lib/index.umd.js"

    # marked-katex-extension
    "marked-katex-extension/index.umd.js|https://cdn.jsdelivr.net/npm/marked-katex-extension@5.1.7/lib/index.umd.js"

    # mermaid
    "mermaid/mermaid.min.js|https://cdn.jsdelivr.net/npm/mermaid@11.15.0/dist/mermaid.min.js"
)

DOWNLOAD_TOOL=""
if command -v curl &>/dev/null; then
    DOWNLOAD_TOOL="curl"
elif command -v wget &>/dev/null; then
    DOWNLOAD_TOOL="wget"
else
    echo "Error: neither curl nor wget found. Please install one of them."
    exit 1
fi

echo "=== Downloading vendor dependencies to $VENDOR_DIR ==="

download_count=0
skip_count=0

for resource in "${RESOURCES[@]}"; do
    local_path="${resource%%|*}"
    cdn_url="${resource##*|}"

    target_file="$VENDOR_DIR/$local_path"
    target_dir="$(dirname "$target_file")"

    if [[ -f "$target_file" ]]; then
        skip_count=$((skip_count + 1))
        continue
    fi

    echo "  Downloading: $local_path"
    mkdir -p "$target_dir"

    if [[ "$DOWNLOAD_TOOL" == "curl" ]]; then
        curl -sSL --fail "$cdn_url" -o "$target_file"
    else
        wget -q "$cdn_url" -O "$target_file"
    fi

    download_count=$((download_count + 1))
done

echo "=== Done: $download_count downloaded, $skip_count skipped (already present) ==="
