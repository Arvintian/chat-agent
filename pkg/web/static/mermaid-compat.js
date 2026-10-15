// Mermaid compatibility layer for common LLM-generated Mermaid syntax.
//
// It fixes invalid subgraph declarations such as:
//   subgraph 模式串 P = "ABABCABD"
//   subgraph 暴力法（文本回退）
//   匹配到第7位失配 --> 暴力法（文本回退）
//
// by converting them to stable IDs with bracket titles:
//   subgraph sg_1[模式串 P = ABABCABD]
//   sg_1 --> sg_2
//
// It also detects Mermaid's rendered "Syntax error in text" SVG and retries
// with a normalized source.
(function () {
    if (window.__mermaidCompatInstalled) return;
    window.__mermaidCompatInstalled = true;

    function safeEscapeHtml(text) {
        if (typeof escapeHtml === 'function') return escapeHtml(text);
        return String(text || '')
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    function escapeRegExp(s) {
        const specials = new Set(['.', '*', '+', '?', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\']);
        return Array.from(s).map(ch => specials.has(ch) ? '\\' + ch : ch).join('');
    }

    function isSimpleId(s) {
        return /^[A-Za-z_][A-Za-z0-9_]*$/.test(s);
    }

    function sanitizeBracketTitle(title, aggressive) {
        let t = title.replace(/["']/g, '').replace(/\]/g, ')').trim();
        if (aggressive) {
            t = t.replace(/[（）()=]/g, '-');
        }
        return t;
    }

    function replaceKnownSubgraphTitles(line, titles, map) {
        const protectedParts = [];
        let safe = line.replace(/"[^"]*"|'[^']*'|\[[^\]]*\]/g, part => {
            protectedParts.push(part);
            return '__MERMAID_PROTECTED_' + (protectedParts.length - 1) + '__';
        });

        for (const title of titles) {
            const id = map.get(title);
            if (!id) continue;

            const body = title.split(/\s+/).map(escapeRegExp).join('\\s+');
            if (!body) continue;

            const wordish = 'A-Za-z0-9_一-鿿㐀-䶿';
            const re = new RegExp('(^|[^' + wordish + '])(' + body + ')(?![' + wordish + '])', 'g');
            safe = safe.replace(re, (match, prefix) => prefix + id);
        }

        return safe.replace(/__MERMAID_PROTECTED_(\d+)__/g, (_, idx) => protectedParts[Number(idx)]);
    }

    function normalizeMermaidSource(code, aggressive) {
        aggressive = aggressive === true;

        if (!code || !/\bsubgraph\b/.test(code)) return code;

        const lines = code.split(/\r?\n/);
        const map = new Map();
        const idPrefix = 'sg_' + Math.random().toString(36).slice(2, 8) + '_';
        let counter = 0;
        const out = [];

        for (const line of lines) {
            const m = /^(\s*)subgraph\s+(.+?)\s*$/.exec(line);
            if (!m) {
                out.push(line);
                continue;
            }

            const indent = m[1];
            const rest = m[2].trim();

            // Already has a valid Mermaid ID, optionally with a bracket title:
            //   subgraph S1
            //   subgraph S1[Title]
            //   subgraph S1["Title"]
            const idTitle = /^([A-Za-z_][A-Za-z0-9_]*)(?:\s*\[(.*)\])?$/.exec(rest);
            if (idTitle) {
                const id = idTitle[1];
                const rawTitle = idTitle[2];

                if (rawTitle === undefined || rawTitle.trim() === '') {
                    out.push(line);
                    continue;
                }

                let title = rawTitle.trim();
                let quoted = false;
                if ((title.startsWith('"') && title.endsWith('"')) || (title.startsWith("'") && title.endsWith("'"))) {
                    quoted = true;
                    title = title.slice(1, -1);
                }

                if (title) {
                    map.set(rawTitle, id);
                    map.set(title, id);
                }

                const needsRewrite = quoted || aggressive || /["']/.test(rawTitle) || rawTitle.includes(']');
                if (!needsRewrite) {
                    out.push(line);
                    continue;
                }

                const safeTitle = sanitizeBracketTitle(title, aggressive);
                if (safeTitle) {
                    out.push(indent + 'subgraph ' + id + '[' + safeTitle + ']');
                    map.set(safeTitle, id);
                } else {
                    out.push(indent + 'subgraph ' + id);
                }
                continue;
            }

            // No valid ID: treat the whole remainder as the display title.
            let title = rest;
            if ((title.startsWith('"') && title.endsWith('"')) || (title.startsWith("'") && title.endsWith("'"))) {
                title = title.slice(1, -1);
            }

            const safeTitle = sanitizeBracketTitle(title, aggressive);
            const id = idPrefix + (++counter);

            map.set(rest, id);
            if (title !== rest) map.set(title, id);
            if (safeTitle) map.set(safeTitle, id);

            if (safeTitle) {
                out.push(indent + 'subgraph ' + id + '[' + safeTitle + ']');
            } else {
                out.push(indent + 'subgraph ' + id);
            }
        }

        if (map.size === 0) return code;

        const titles = Array.from(map.keys())
            .filter(t => t && t.length > 0 && !isSimpleId(t))
            .sort((a, b) => b.length - a.length);

        if (!titles.length) return out.join('\n');

        return out.map(line => replaceKnownSubgraphTitles(line, titles, map)).join('\n');
    }

    function isValidRendered(el) {
        const svg = el.querySelector('svg');
        if (!svg) return false;
        if (svg.querySelector('.error-text')) return false;
        return true;
    }

    function showMermaidError(el, message) {
        el.classList.add('mermaid-error');
        el.innerHTML =
            '<div class="mermaid-error-msg">⚠️ Mermaid rendering failed: ' +
            safeEscapeHtml(message || 'Syntax error in text') +
            '</div>';
    }

    const originalRenderMermaidDiagrams = window.renderMermaidDiagrams;
    if (typeof originalRenderMermaidDiagrams !== 'function') return;

    window.renderMermaidDiagrams = function (container) {
        if (!container) return;

        const mermaidElements = container.querySelectorAll('pre.mermaid');
        if (mermaidElements.length === 0) return;

        requestAnimationFrame(async () => {
            const nodes = Array.from(mermaidElements);

            nodes.forEach(el => {
                if (isValidRendered(el) && typeof addMermaidExportButton === 'function') {
                    addMermaidExportButton(el);
                }
            });

            try {
                if (typeof loadMermaid !== 'function') {
                    throw new Error('loadMermaid is not available');
                }

                await loadMermaid();

                for (const el of nodes) {
                    if (isValidRendered(el)) continue;

                    const original = el.dataset.originalMermaid || el.textContent;
                    el.dataset.originalMermaid = original;

                    const normalized = normalizeMermaidSource(original, false);
                    const aggressive = normalizeMermaidSource(original, true);

                    const attempts = [original];
                    if (normalized !== original) attempts.push(normalized);
                    if (aggressive !== original && aggressive !== normalized) attempts.push(aggressive);

                    let lastError = null;

                    for (const src of attempts) {
                        if (!src || !src.trim()) continue;

                        el.textContent = src;
                        el.removeAttribute('data-processed');

                        try {
                            await mermaid.run({ nodes: [el] });

                            if (isValidRendered(el)) {
                                if (typeof addMermaidExportButton === 'function') {
                                    addMermaidExportButton(el);
                                }
                                lastError = null;
                                break;
                            }
                        } catch (e) {
                            lastError = e;
                        }
                    }

                    if (!isValidRendered(el)) {
                        showMermaidError(el, (lastError && lastError.message) || 'Syntax error in text');
                    }
                }
            } catch (e) {
                nodes.forEach(el => {
                    if (!isValidRendered(el)) {
                        showMermaidError(el, e.message || 'Mermaid load failed');
                    }
                });
            }
        });
    };

    console.log('Mermaid compatibility layer installed');
})();
