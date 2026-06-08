// Configure marked.js with syntax highlighting
const renderer = new marked.Renderer();

// Override link renderer to open links in new window
renderer.link = function (href, title, text) {
    if (typeof href === 'object') {
        // marked.js v4+ passes an object with href, title, text properties
        const { href: url, title: linkTitle, text: linkText } = href;
        return '<a href="' + url + '" target="_blank" rel="noopener noreferrer"' +
            (linkTitle ? ' title="' + linkTitle + '"' : '') + '>' + linkText + '</a>';
    } else {
        // Fallback for older versions
        return '<a href="' + href + '" target="_blank" rel="noopener noreferrer"' +
            (title ? ' title="' + title + '"' : '') + '>' + text + '</a>';
    }
};

marked.setOptions({
    breaks: true,
    gfm: true,
    renderer: renderer
});
const { markedHighlight } = globalThis.markedHighlight;
marked.use(markedHighlight({
    emptyLangClass: 'hljs',
    langPrefix: 'hljs language-',
    highlight(code, lang, info) {
        const language = hljs.getLanguage(lang) ? lang : 'plaintext';
        return hljs.highlight(code, { language }).value;
    }
}));
// Register marked-katex-extension for inline math ($...$ delimiters) only.
// $$...$$ (display math), \[...\] and \(...\) are handled by custom extensions below.
// We rely on our displayKatex for $$ blocks because marked-katex-extension's blockKatex
// has issues with some markdown edge cases.
marked.use(markedKatex({ throwOnError: false, nonStandard: true }));

// Custom block-level tokenizer for $$...$$ display math
// Handles both multi-line ($$\n...\n$$) and inline-ish ($$ ... $$) forms.
// Uses a preprocessor approach: converts $$...$$ to a known safe token,
// then renders via KaTeX in the renderer.
const displayKatex = {
    name: 'displayKatex',
    level: 'block',
    start(src) { return src.indexOf('$$'); },
    tokenizer(src) {
        // Match both forms:
        // 1. $$\ncontent\n$$  (standard multi-line)
        // 2. $$ content $$     (content on same line as delimiters)
        const match = /^\$\$[ \t]*\n?([\s\S]*?)\n?[ \t]*\$\$/.exec(src);
        if (!match) return;
        // Validate that we actually captured content between $$ delimiters
        // Avoid matching empty $$ $$ or edge cases with no content
        const math = match[1].trim();
        if (!math) return;
        return {
            type: 'displayKatex',
            raw: match[0],
            text: math
        };
    },
    renderer(token) {
        try {
            return katex.renderToString(token.text, { displayMode: true, throwOnError: false }) + '\n';
        } catch (e) {
            return '<pre class="katex-error">' + escapeHtml(token.text) + '</pre>';
        }
    }
};

// Custom block-level tokenizer for \[...\] display math
const bracketLatexDisplay = {
    name: 'bracketLatexDisplay',
    level: 'block',
    start(src) { return src.indexOf('\\['); },
    tokenizer(src) {
        const match = /^\\\[([\s\S]*?)\\\]/.exec(src);
        if (match) {
            return {
                type: 'bracketLatexDisplay',
                raw: match[0],
                text: match[1].trim()
            };
        }
    },
    renderer(token) {
        try {
            return katex.renderToString(token.text, { displayMode: true, throwOnError: false }) + '\n';
        } catch (e) {
            return '<pre class="katex-error">' + escapeHtml(token.text) + '</pre>';
        }
    }
};

// Custom inline tokenizer for \(...\) inline math
const bracketLatexInline = {
    name: 'bracketLatexInline',
    level: 'inline',
    start(src) { return src.indexOf('\\('); },
    tokenizer(src) {
        const match = /^\\\(([\s\S]*?)\\\)/.exec(src);
        if (match) {
            return {
                type: 'bracketLatexInline',
                raw: match[0],
                text: match[1].trim()
            };
        }
    },
    renderer(token) {
        try {
            return katex.renderToString(token.text, { displayMode: false, throwOnError: false });
        } catch (e) {
            return '<span class="katex-error">' + escapeHtml(token.text) + '</span>';
        }
    }
};

// Register custom extensions after marked-katex-extension so they take priority.
// Order: displayKatex ($$) and bracketLatexDisplay (\[) handle block math,
// bracketLatexInline (\() handles inline bracket math,
// marked-katex-extension's inlineKatex ($) handles standard inline math.
marked.use({ extensions: [displayKatex, bracketLatexDisplay, bracketLatexInline] });

// Initialize Mermaid
mermaid.initialize({
    startOnLoad: false,
    theme: 'default',
    securityLevel: 'loose',
    flowchart: {
        useMaxWidth: true,
        htmlLabels: false,
        curve: 'basis'
    },
    themeVariables: {
        primaryColor: '#667eea',
        primaryTextColor: '#333',
        primaryBorderColor: '#667eea',
        lineColor: '#666',
        secondaryColor: '#f5f5f5',
        tertiaryColor: '#e8f0fe'
    }
});

// Custom marked.js extension for mermaid code blocks
// Transforms ```mermaid blocks into <pre class="mermaid"> elements
const mermaidExtension = {
    name: 'mermaid',
    level: 'block',
    start(src) { return src.indexOf('```mermaid'); },
    tokenizer(src) {
        const match = /^```mermaid\s*\n([\s\S]*?)\n\s*```/.exec(src);
        if (!match) return;
        const mermaidCode = match[1];
        if (!mermaidCode || !mermaidCode.trim()) return;
        return {
            type: 'mermaid',
            raw: match[0],
            text: mermaidCode
        };
    },
    renderer(token) {
        // Use a unique ID for each mermaid diagram
        const id = 'mermaid-' + Math.random().toString(36).substr(2, 9);
        return '<pre class="mermaid" id="' + id + '">' + escapeHtml(token.text) + '</pre>';
    }
};

marked.use({ extensions: [mermaidExtension] });

// Helper function to render mermaid diagrams in a container element
function renderMermaidDiagrams(container) {
    if (!container) return;
    const mermaidElements = container.querySelectorAll('pre.mermaid');
    if (mermaidElements.length === 0) return;
    
    // Use requestAnimationFrame to ensure DOM is settled
    requestAnimationFrame(async () => {
        // First, add export buttons to any already-rendered mermaid diagrams
        mermaidElements.forEach(el => {
            if (el.querySelector('svg')) {
                addMermaidExportButton(el);
            }
        });
        
        try {
            await mermaid.run({ nodes: Array.from(mermaidElements) });
            // Add export buttons after successful rendering
            mermaidElements.forEach(el => {
                addMermaidExportButton(el);
            });
        } catch (e) {
            console.error('Mermaid rendering error:', e);
            // For each failed element, show error
            mermaidElements.forEach(el => {
                if (el.querySelector('svg')) return; // already rendered
                el.classList.add('mermaid-error');
                el.innerHTML = '<div class="mermaid-error-msg">⚠️ Mermaid rendering failed: ' + escapeHtml(e.message || 'Unknown error') + '</div>';
            });
        }
    });
}

// Add export button to a rendered mermaid diagram
function addMermaidExportButton(preElement) {
    // Skip if already has export button
    if (preElement.querySelector('.mermaid-export-btn')) return;
    
    const svg = preElement.querySelector('svg');
    if (!svg) return;
    
    const exportBtn = document.createElement('button');
    exportBtn.className = 'mermaid-export-btn';
    exportBtn.title = 'Export as PNG';
    exportBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
            <polyline points="7 10 12 15 17 10"></polyline>
            <line x1="12" y1="15" x2="12" y2="3"></line>
        </svg>
    `;
    exportBtn.onclick = function(e) {
        e.stopPropagation();
        e.preventDefault();
        exportMermaidToPNG(preElement);
    };
    
    preElement.style.position = 'relative';
    preElement.appendChild(exportBtn);
}

// Export mermaid diagram as PNG
function exportMermaidToPNG(preElement) {
    const svg = preElement.querySelector('svg');
    if (!svg) {
        showToast('No diagram to export', true);
        return;
    }
    
    try {
        // Clone the SVG to avoid modifying the displayed one
        const svgClone = svg.cloneNode(true);
        
        // Get SVG dimensions - prefer viewBox for accurate sizing
        const viewBox = svg.viewBox?.baseVal;
        let width, height;
        if (viewBox && viewBox.width > 0 && viewBox.height > 0) {
            width = viewBox.width;
            height = viewBox.height;
        } else {
            const svgRect = svg.getBoundingClientRect();
            width = svgRect.width || svg.getAttribute('width') || 800;
            height = svgRect.height || svg.getAttribute('height') || 600;
        }
        
        // Set explicit dimensions and viewBox on clone for canvas rendering
        svgClone.setAttribute('width', width);
        svgClone.setAttribute('height', height);
        svgClone.setAttribute('viewBox', `0 0 ${width} ${height}`);
        
        // Ensure text elements have explicit fill color for proper canvas rendering
        const textElements = svgClone.querySelectorAll('text');
        textElements.forEach(el => {
            if (!el.getAttribute('fill')) {
                el.setAttribute('fill', '#333');
            }
        });
        
        // Serialize SVG to string
        const serializer = new XMLSerializer();
        let svgString = serializer.serializeToString(svgClone);
        
        // Prepend XML declaration for proper encoding
        svgString = '<?xml version="1.0" encoding="UTF-8"?>\n' + svgString;
        
        // Encode SVG string to base64 data URI to avoid tainted canvas issues
        const svgBase64 = btoa(unescape(encodeURIComponent(svgString)));
        const dataUri = 'data:image/svg+xml;base64,' + svgBase64;
        
        // Create canvas and draw
        const canvas = document.createElement('canvas');
        const scale = 2; // 2x for retina quality
        canvas.width = width * scale;
        canvas.height = height * scale;
        const ctx = canvas.getContext('2d');
        ctx.scale(scale, scale);
        
        // Set white background
        ctx.fillStyle = '#ffffff';
        ctx.fillRect(0, 0, width, height);
        
        const img = new Image();
        // Set crossOrigin to anonymous to avoid tainting
        img.crossOrigin = 'anonymous';
        img.onload = function() {
            ctx.drawImage(img, 0, 0, width, height);
            
            // Trigger download
            canvas.toBlob(function(blob) {
                const downloadUrl = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = downloadUrl;
                a.download = 'mermaid-diagram-' + new Date().toISOString().slice(0, 19).replace(/:/g, '-') + '.png';
                document.body.appendChild(a);
                a.click();
                document.body.removeChild(a);
                URL.revokeObjectURL(downloadUrl);
                showToast('Diagram exported as PNG', false);
            }, 'image/png');
        };
        img.onerror = function() {
            showToast('Failed to export diagram', true);
        };
        img.src = dataUri;
    } catch (e) {
        console.error('Mermaid export error:', e);
        showToast('Export failed: ' + (e.message || 'Unknown error'), true);
    }
}

let ws = null;
let currentChat = null;
let sessionId = null;
let reconnectAttempts = 0;
const maxReconnectAttempts = 10;
const reconnectBaseDelay = 1000;  // 1 second
const reconnectMaxDelay = 15000; // 15 seconds
let toastTimeout = null;
let isGenerating = false;
let lastUserMessage = '';
let lastUserFiles = null;
let lastUserMessageElement = null;  // DOM element of the last user message

// Store chat configurations (name -> { hasKeepHook: boolean })
const chatConfigs = {};

// Scroll behavior is now handled by scroll-handler.js module

// Session ID storage key (localStorage - persistent across tabs and browser restarts)
const SESSION_ID_KEY = 'chat_agent_session_id';

// Last used chat storage key (localStorage - shared across tabs, persistent)
// Only updated when user explicitly selects a chat from the dropdown, not on auto-enter/refresh
const LAST_CHAT_KEY = 'chat_agent_last_chat';

// Per-tab chat storage key (sessionStorage - survives refresh, cleared on tab close)
// Used to restore the tab's current chat on refresh
const TAB_CHAT_KEY = 'chat_agent_tab_chat';

// Flag to distinguish between initial page load and user-initiated back-to-selection
let isInitialPageLoad = true;

// Flag: true when startChat() is called from init() auto-start, false when user explicitly clicks start
let isAutoStarting = false;

// Load last used chat from localStorage (shared across tabs, for new tabs only)
function loadLastChat() {
    try {
        return localStorage.getItem(LAST_CHAT_KEY);
    } catch (e) {
        console.error('Failed to load last chat:', e);
        return null;
    }
}

// Save last used chat to localStorage (only on explicit user selection)
function saveLastChat(chatName) {
    try {
        localStorage.setItem(LAST_CHAT_KEY, chatName);
        console.log('Last chat saved:', chatName);
    } catch (e) {
        console.error('Failed to save last chat:', e);
    }
}

// Load per-tab chat from sessionStorage (survives refresh, cleared on tab close)
function loadTabChat() {
    try {
        return sessionStorage.getItem(TAB_CHAT_KEY);
    } catch (e) {
        console.error('Failed to load tab chat:', e);
        return null;
    }
}

// Save per-tab chat to sessionStorage
function saveTabChat(chatName) {
    try {
        if (chatName) {
            sessionStorage.setItem(TAB_CHAT_KEY, chatName);
        } else {
            sessionStorage.removeItem(TAB_CHAT_KEY);
        }
        console.log('Tab chat saved:', chatName);
    } catch (e) {
        console.error('Failed to save tab chat:', e);
    }
}

// Update clear button count display
function updateClearBadge(count) {
    const countEl = document.getElementById('clear-count');
    if (!countEl) return;

    if (count !== undefined && count !== null && count > 0) {
        countEl.textContent = `(${count})`;
        countEl.style.display = 'inline';
    } else {
        countEl.style.display = 'none';
    }
}

// Detect if device is mobile
function isMobileDevice() {
    return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent) ||
        (window.innerWidth <= 768);
}

// Load session ID from localStorage
function loadSessionId() {
    try {
        return localStorage.getItem(SESSION_ID_KEY);
    } catch (e) {
        console.error('Failed to load session ID:', e);
        return null;
    }
}

// Save session ID to localStorage
function saveSessionId(id) {
    try {
        localStorage.setItem(SESSION_ID_KEY, id);
        console.log('Session ID saved:', id);
    } catch (e) {
        console.error('Failed to save session ID:', e);
    }
}

// Clear session ID from localStorage
function clearSessionId() {
    try {
        localStorage.removeItem(SESSION_ID_KEY);
    } catch (e) {
        console.error('Failed to clear session ID:', e);
    }
}

// Image preview functions are now in image-preview.js module
// Access via window.showImagePreviewFromHistory, window.showImagePreviewWithIndex, etc.

// Input history management is now in input-history.js module
// Access via window.InputHistory

// Track tool calls for streaming updates
// index -> { name, argsElement, argsText, complete }
let toolCalls = {};

// Track pending approval requests
let pendingApprovals = {};
let currentApprovalId = null;

// File upload functions are now in file-upload.js module
// Access via window.FileUploadHandler

// Message history is now handled by message-history.js module
// Access via window.MessageHistory

async function init() {
    // Load input history from localStorage
    window.InputHistory.loadHistory();

    // Initialize quick phrases
    window.QuickPhrases.init();

    // Load webui config from server
    try {
        const configResponse = await fetch('/config');
        const configData = await configResponse.json();
        const appTitle = configData.webui?.title || 'Chat-Agent';
        document.title = appTitle;
        document.getElementById('login-header').textContent = '🤖 ' + appTitle;
        document.getElementById('agent-header').textContent = '🤖 ' + appTitle;
    } catch (e) {
        console.error('Failed to load webui config:', e);
    }

    try {
        // Load session ID for passing to /chats endpoint
        const currentSessionId = loadSessionId();
        const chatsUrl = currentSessionId 
            ? '/chats?session_id=' + encodeURIComponent(currentSessionId)
            : '/chats';
        
        const response = await fetch(chatsUrl);
        const data = await response.json();
        const select = document.getElementById('chat-select');
        // Clear existing options to prevent duplicates when re-initializing
        select.innerHTML = '';

        // Clear chat configs
        Object.keys(chatConfigs).forEach(key => delete chatConfigs[key]);

        // Get active chats from server (chats already open in other tabs)
        const activeChats = data.active_chats || {};

        // Determine which chat to auto-select
        let chatToSelect = null;
        
        // Priority 1: Per-tab chat from sessionStorage (survives refresh)
        // This keeps the tab stable when refreshing while multiple tabs are open
        const tabChat = loadTabChat();
        if (tabChat && data.chats.some(c => c.name === tabChat)) {
            if (!activeChats[tabChat]) {
                chatToSelect = tabChat;
                console.log('Will restore tab chat:', chatToSelect);
            } else {
                // Tab's chat is active in another tab - this shouldn't normally happen
                // (could happen if the tab was closed and reopened very quickly)
                console.log('Tab chat is already active in another tab, skipping:', tabChat);
            }
        }
        
        // Priority 2: Last used chat from localStorage (for new tabs/windows)
        // BUT skip if it's already active in another tab
        if (!chatToSelect) {
            const lastUsedChat = loadLastChat();
            if (lastUsedChat && data.chats.some(c => c.name === lastUsedChat)) {
                if (!activeChats[lastUsedChat]) {
                    chatToSelect = lastUsedChat;
                    console.log('Will select last used chat:', chatToSelect);
                } else {
                    console.log('Last used chat is already active in another tab, skipping:', lastUsedChat);
                }
            }
        }

        // Build the option list and store chat configs
        for (const chatInfo of data.chats) {
            const chatName = chatInfo.name;
            const option = document.createElement('option');
            option.value = chatName;
            
            // If chat is already active in another tab, disable it and add indicator
            if (activeChats[chatName]) {
                option.textContent = '🔒 ' + chatName + ' (已在其他标签页打开)';
                option.disabled = true;
                option.style.color = '#999';
            } else {
                option.textContent = chatName;
            }

            select.appendChild(option);

            // Store chat configuration
            chatConfigs[chatName] = {
                hasKeepHook: chatInfo.has_keep_hook || false
            };

            // Select the determined chat
            if (chatToSelect && chatName === chatToSelect) {
                option.selected = true;
            }
        }

        // If no chat was auto-selected (no tab/last chat, or all are active),
        // mark the server's default_chat as selected in the dropdown
        if (!chatToSelect && data.default_chat) {
            for (let i = 0; i < select.options.length; i++) {
                if (select.options[i].value === data.default_chat && !select.options[i].disabled) {
                    select.options[i].selected = true;
                    break;
                }
            }
        }

        // Auto-start chat on initial page load:
        // Only auto-enter if there's a saved chat to restore (tab chat / last used)
        if (isInitialPageLoad && chatToSelect) {
            isAutoStarting = true;
            startChat();
            isAutoStarting = false;
        }
        
        // Mark initial load as complete (subsequent backToChatSelection won't auto-start)
        isInitialPageLoad = false;
    } catch (e) {
        console.error('Failed to load chats:', e);
    }
}

async function startChat() {
    const chatName = document.getElementById('chat-select').value;
    if (!chatName) {
        alert('Please select a chat');
        return;
    }
    currentChat = chatName;
    window.MessageHistory.setCurrentChat(chatName);
    
    // Always save per-tab chat (sessionStorage) for refresh stability
    saveTabChat(chatName);
    
    // Only save last used chat (localStorage) on explicit user selection, not auto-start
    if (!isAutoStarting) {
        saveLastChat(chatName);
    }
    
    // Update document title to reflect current chat name
    document.title = chatName;
    
    document.getElementById('login-header').textContent = chatName;
    document.getElementById('login-panel').style.display = 'none';
    
    // Ensure chat-panel has correct height before displaying (fixes PWA input-area hidden issue)
    const chatPanel = document.getElementById('chat-panel');
    if (chatPanel) {
        const vh = window.visualViewport ? window.visualViewport.height : window.innerHeight;
        chatPanel.style.height = vh + 'px';
    }
    document.getElementById('chat-panel').style.display = 'flex';
    
    // Update agent header with chat name
    const agentHeader = document.getElementById('agent-header');
    const chatNameText = agentHeader.querySelector('.chat-name-text');
    if (chatNameText) {
        chatNameText.textContent = '💬 ' + chatName;
    } else {
        agentHeader.textContent = '💬 ' + chatName;
    }

    // Show or hide keep button based on chat configuration
    const keepBtn = document.getElementById('keep-btn');
    if (keepBtn) {
        const chatConfig = chatConfigs[chatName];
        if (chatConfig && chatConfig.hasKeepHook) {
            keepBtn.style.display = 'inline-block';
        } else {
            keepBtn.style.display = 'none';
        }
    }

    // Load message history from storage (IndexedDB or localStorage)
    await loadMessageHistory();

    // Badge will be updated when we receive chat_selected message from server

    // Load base session ID from localStorage if not already set
    if (!sessionId) {
        sessionId = loadSessionId();
    }

    // Initialize scroll detection for auto-scroll behavior
    initScrollDetection();

    // Check if WebSocket is already connected
    if (ws && ws.readyState === WebSocket.OPEN) {
        // WebSocket already connected, just send select_chat message
        console.log('WebSocket already connected, sending select_chat for:', chatName);
        ws.send(JSON.stringify({ type: 'select_chat', payload: { chat_name: chatName } }));
    } else {
        // WebSocket not connected, establish new connection
        console.log('WebSocket not connected, establishing new connection');
        connectWebSocket();
    }
}

// Navigate back to chat selection page
function backToChatSelection() {
    // Send deselect message to server so the chat is marked as inactive
    if (ws && ws.readyState === WebSocket.OPEN && currentChat) {
        ws.send(JSON.stringify({ type: 'deselect_chat', payload: {} }));
    }

    // Clear current chat state (but keep session ID and WebSocket connection)
    currentChat = null;
    
    // Clear per-tab chat so next refresh shows selection page
    saveTabChat(null);

    // Clear messages display
    const messagesContainer = document.getElementById('messages');
    if (messagesContainer) {
        messagesContainer.innerHTML = '';
    }

    // Reset input area
    const input = document.getElementById('message-input');
    if (input) {
        input.value = '';
        input.disabled = false;
    }

    // Hide chat panel and show login panel
    document.getElementById('chat-panel').style.display = 'none';
    document.getElementById('login-panel').style.display = 'flex';

    // Reset headers and page title
    // login-header after startChat() contains just the chat name (no emoji prefix).
    // The original app title is stored on the agent-header's chat-name-text span.
    const agentHeaderEl = document.getElementById('agent-header');
    const chatNameSpan = agentHeaderEl ? agentHeaderEl.querySelector('.chat-name-text') : null;
    let appTitle = 'Chat-Agent';
    if (chatNameSpan && chatNameSpan.textContent) {
        // Extract original title from "💬 chatName" or "🤖 Title" format
        appTitle = chatNameSpan.textContent.replace(/^[💬🤖] /, '');
    }
    document.title = appTitle;
    document.getElementById('login-header').textContent = '🤖 ' + appTitle;
    
    // Reset agent header to default
    const chatNameText = agentHeaderEl ? agentHeaderEl.querySelector('.chat-name-text') : null;
    if (chatNameText) {
        chatNameText.textContent = '🤖 ' + appTitle;
    } else if (agentHeaderEl) {
        agentHeaderEl.textContent = '🤖 ' + appTitle;
    }

    // Reset clear badge
    updateClearBadge(0);

    // Reset send button
    isGenerating = false;
    updateSendButton();

    // Hide keep button (will be shown again when selecting a chat with keep hook)
    const keepBtn = document.getElementById('keep-btn');
    if (keepBtn) {
        keepBtn.style.display = 'none';
    }

    // Hide regenerate button and reset state
    removeRegenerateFromLastMessage();
    lastUserMessage = '';
    lastUserFiles = null;
    lastUserMessageElement = null;

    // Re-init to reload chat list (keep WebSocket connection alive)
    init();
}

// Load and display message history from storage
async function loadMessageHistory() {
    const history = await window.MessageHistory.loadHistory();

    // Update badge with locally loaded message count (will be refined by server's chat_selected)
    updateClearBadge(history.length);

    history.forEach((msg, msgIndex) => {
        if (msg.type === 'user') {
            // Check if message has files
            if (msg.files && msg.files.length > 0) {
                // Skip messages marked as unavailable (fallback mode)
                const hasUnavailableFiles = msg.files.some(img => img.unavailable);
                if (hasUnavailableFiles) {
                    displayStoredMessage(msg.content || '📎 Image(s) attached (not available in this session)', 'user');
                } else {
                    displayUserMessageWithFiles(msg.content || '', msg.files, msgIndex);
                }
            } else {
                displayStoredMessage(msg.content, 'user');
            }
        } else if (msg.type === 'assistant') {
            // Check if this message has separate thinking content
            if (msg.thinking && msg.thinking.trim()) {
                displayStoredThinkingAndResponse(msg.thinking, msg.content);
            } else {
                displayStoredMessage(msg.content, 'assistant');
            }
        } else if (msg.type === 'tool_call' && msg.toolData) {
            displayStoredToolCall(msg.toolData);
        }
    });

    // Add regenerate button to the last user message after loading
    if (lastUserMessageElement && (lastUserMessage || (lastUserFiles && lastUserFiles.length > 0))) {
        addRegenerateButton(lastUserMessageElement);
    }

    // Scroll to bottom after loading history
    scrollToBottom(true);
}

// Display stored thinking and response message
function displayStoredThinkingAndResponse(thinkingContent, responseContent) {
    // Display thinking message if exists
    if (thinkingContent && thinkingContent.trim()) {
        const div = document.createElement('div');
        div.className = 'message assistant thinking-message';
        div.innerHTML = `
            <div class="thinking-header">
                <span class="thinking-icon">💭</span>
                <span class="thinking-title">Thinking</span>
                <button class="thinking-expand-btn" onclick="toggleThinkingExpand(this)" title="Expand thinking">Expand ▾</button>
            </div>
            <div class="thinking-content thinking-collapsed markdown-body"></div>
            <div class="message-footer">
                <button class="copy-btn" onclick="copyThinkingMessage(this)" title="Copy thinking content">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                    </svg>
                    <span class="copy-text">Copy</span>
                </button>
            </div>
        `;
        const thinkingDiv = div.querySelector('.thinking-content');
        // Store original markdown content for copying
        thinkingDiv.dataset.originalContent = thinkingContent.trim();
        try {
            thinkingDiv.innerHTML = marked.parse(thinkingContent.trim());
        } catch (e) {
            console.error('Markdown parsing error for thinking:', e);
            thinkingDiv.textContent = thinkingContent.trim();
        }
        document.getElementById('messages').appendChild(div);
        scrollThinkingContentToBottom(thinkingDiv);
        addCopyButtonsToCodeBlocks(div);
        renderMermaidDiagrams(div);
    }

    // Display response message if exists
    if (responseContent && responseContent.trim()) {
        const div = document.createElement('div');
        div.className = 'message assistant response-message';
        div.innerHTML = `
            <div class="response-content markdown-body"></div>
            <div class="message-footer">
                <button class="copy-btn" onclick="copyResponseMessage(this)" title="Copy response content">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                    </svg>
                    <span class="copy-text">Copy</span>
                </button>
            </div>
        `;
        const responseDiv = div.querySelector('.response-content');
        // Store original markdown content for copying
        responseDiv.dataset.originalContent = responseContent.trim();
        try {
            responseDiv.innerHTML = marked.parse(responseContent.trim());
        } catch (e) {
            console.error('Markdown parsing error for response:', e);
            responseDiv.textContent = responseContent.trim();
        }
        document.getElementById('messages').appendChild(div);
        addCopyButtonsToCodeBlocks(div);
        renderMermaidDiagrams(div);
    }
}

// Display a stored user message
function displayStoredMessage(content, type) {
    const div = document.createElement('div');
    // For assistant messages, add 'response-message' class for consistent styling
    div.className = 'message ' + type + (type === 'assistant' ? ' response-message' : '');

    const contentDiv = document.createElement('div');
    contentDiv.className = 'message-content' + (type === 'assistant' ? ' markdown-body' : '');

    if (type === 'assistant') {
        // Store original markdown content for copying
        contentDiv.dataset.originalContent = content;
        try {
            contentDiv.innerHTML = marked.parse(content);
        } catch (e) {
            console.error('Markdown parsing error:', e);
            contentDiv.textContent = content;
        }
    } else {
        contentDiv.textContent = content;
    }

    div.appendChild(contentDiv);

    // Add footer with copy button for both user and assistant messages
    if (type === 'assistant' || type === 'user') {
        const footer = document.createElement('div');
        footer.className = 'message-footer';
        const copyFunction = type === 'assistant' ? 'copyMessage' : 'copyUserMessage';
        footer.innerHTML = `
            <button class="copy-btn" onclick="${copyFunction}(this)" title="Copy message">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                </svg>
                <span class="copy-text">Copy</span>
            </button>
        `;
        div.appendChild(footer);
    }

    document.getElementById('messages').appendChild(div);

    // Track last user message and add regenerate button
    if (type === 'user') {
        removeRegenerateFromLastMessage();
        lastUserMessageElement = div;
        // Save last user message content for regenerate
        lastUserMessage = content;
        lastUserFiles = null;
        // Note: regenerate button will be added after response completes or on error
    }

    // Add copy buttons to code blocks
    addCopyButtonsToCodeBlocks(div);
    if (type === 'assistant') {
        renderMermaidDiagrams(div);
    }
}

// Display a stored tool call
function displayStoredToolCall(toolData) {
    const { name, arguments: args } = toolData;

    const div = document.createElement('div');
    div.className = 'message tool-call';
    div.id = 'tool-call-' + Date.now() + '-' + Math.random().toString(36).substr(2, 9);

    // Mark as complete
    div.innerHTML = `
        <div class="tool-call-content">
            <span class="tool-icon">🔧</span>
            <span class="tool-name">${escapeHtml(name)}</span>
        </div>
        <div class="tool-args">
            <pre><code class="language-json">${escapeHtml(args)}</code></pre>
        </div>
        <div class="tool-complete">✓ Complete</div>
    `;

    document.getElementById('messages').appendChild(div);
    // Don't auto-scroll when loading history
}

function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    let wsUrl = protocol + '//' + window.location.host + '/ws';

    // Use shared session ID so tabs share the same chat history/persistence
    if (!sessionId) {
        sessionId = loadSessionId();
    }
    // If still no session ID, let the server generate one (don't pass one)
    if (sessionId) {
        wsUrl += '?session_id=' + encodeURIComponent(sessionId);
    }

    ws = new WebSocket(wsUrl);

    ws.onopen = function () {
        console.log('WebSocket connected');
        reconnectAttempts = 0;
        closeStatus(); // Close any existing status when connected

        // Re-enable input if it was disabled during disconnection
        // Only if we're not currently in the middle of AI generation
        // (the generation state may have been lost during disconnect)
        const input = document.getElementById('message-input');
        if (input && input.disabled && !isGenerating) {
            input.disabled = false;
        }

        // Auto-select current chat
        if (currentChat) {
            ws.send(JSON.stringify({ type: 'select_chat', payload: { chat_name: currentChat } }));
        }
    };

    ws.onmessage = function (event) {
        try {
            const msg = JSON.parse(event.data);

            // Handle session_init message
            if (msg.type === 'session_init') {
                const newSessionId = msg.payload.session_id;
                if (newSessionId && newSessionId !== sessionId) {
                    sessionId = newSessionId;
                    saveSessionId(sessionId);
                    console.log('Received new session ID:', sessionId);
                }
            }

            handleMessage(msg);
        } catch (e) {
            console.error('Failed to parse message:', e);
        }
    };

    ws.onclose = function () {
        console.log('WebSocket disconnected');
        // Disable input while disconnected
        const input = document.getElementById('message-input');
        if (input) {
            input.disabled = true;
        }
        if (reconnectAttempts < maxReconnectAttempts) {
            // Exponential backoff: 1s, 2s, 4s, 8s, 15s, 15s...
            const delay = Math.min(reconnectBaseDelay * Math.pow(2, reconnectAttempts), reconnectMaxDelay);
            const attemptNum = reconnectAttempts + 1;
            console.log(`Reconnecting in ${delay}ms (attempt ${attemptNum}/${maxReconnectAttempts})`);
            // Show reconnecting status to user
            setStatus(`Reconnecting... (${attemptNum}/${maxReconnectAttempts})`, false);
            reconnectAttempts++;
            setTimeout(connectWebSocket, delay);
        } else {
            setStatus('Unable to reconnect. Please refresh the page.', true);
            // Re-enable input so user can at least copy their drafted text before refreshing
            const input = document.getElementById('message-input');
            if (input) {
                input.disabled = false;
            }
        }
    };

    ws.onerror = function (error) {
        console.error('WebSocket error:', error);
    };
}

function handleMessage(msg) {
    switch (msg.type) {
        case 'welcome':
            setStatus(msg.payload.message || 'Connected', false);
            break;
        case 'chat_selected':
            setStatus(msg.payload.message, false);
            // Update badge with message count from server
            if (msg.payload.message_count !== undefined) {
                updateClearBadge(msg.payload.message_count);
            }
            break;
        case 'chunk':
            displayChunk(msg.payload.content, msg.payload.first, msg.payload.last, msg.payload.content_type);
            break;
        case 'tool_call':
            displayToolCall(
                msg.payload.name,
                msg.payload.arguments,
                msg.payload.index,
                msg.payload.streaming
            );
            break;
        case 'complete':
            // 只有在生成中才重置状态（避免重复处理）
            if (isGenerating) {
                // 重新启用输入框
                const input = document.getElementById('message-input');
                if (input) {
                    input.disabled = false;
                    // 桌面端自动聚焦输入框，移动端不聚焦（避免弹出键盘）
                    if (!isMobileDevice()) {
                        input.focus();
                    }
                }
                isGenerating = false;
                updateSendButton();
                // Show regenerate button on the last user message after successful completion
                addRegenerateButton(lastUserMessageElement);
            }
            smartScrollToBottom(true);
            //setStatus('Response completed', false);
            break;
        case 'error':
            setStatus(msg.payload.error, true);
            // If error mentions "already open", refresh chat list and go back to selection
            if (msg.payload.error && msg.payload.error.indexOf('already open') !== -1) {
                // Reset current chat state
                currentChat = null;
                // Go back to selection page to refresh the chat list
                backToChatSelection();
                return;
            }
            // 只有在生成中才重置状态
            if (isGenerating) {
                const inputErr = document.getElementById('message-input');
                if (inputErr) {
                    inputErr.disabled = false;
                    // 桌面端自动聚焦输入框，移动端不聚焦（避免弹出键盘）
                    if (!isMobileDevice()) {
                        inputErr.focus();
                    }
                }
                isGenerating = false;
                updateSendButton();
                // Show regenerate button on error (for retry)
                addRegenerateButton(lastUserMessageElement);
            }
            break;
        case 'stopped':
            // 只有在生成中才重置状态
            if (isGenerating) {
                isGenerating = false;
                updateSendButton();
                const inputStopped = document.getElementById('message-input');
                if (inputStopped) {
                    inputStopped.disabled = false;
                    // 桌面端自动聚焦输入框，移动端不聚焦（避免弹出键盘）
                    if (!isMobileDevice()) {
                        inputStopped.focus();
                    }
                }
                // Show regenerate button after stop (partial response)
                addRegenerateButton(lastUserMessageElement);
            }
            break;
        case 'cleared':
            setStatus(msg.payload.message, false);
            // Update badge with message count from server (should be 0 after clear)
            if (msg.payload.message_count !== undefined) {
                updateClearBadge(msg.payload.message_count);
            }
            break;
        case 'kept':
            setStatus(msg.payload.message, false);
            break;
        case 'approval_request':
            handleApprovalRequest(msg.payload);
            break;
        case 'thinking':
            break;
        case 'message_count':
            // Update badge with message count from server
            if (msg.payload.count !== undefined) {
                updateClearBadge(msg.payload.count);
            }
            break;
        default:
            console.log('Unknown message type:', msg.type);
    }
}

// Handle approval request from server
function handleApprovalRequest(payload) {
    const { approval_id, targets } = payload;
    currentApprovalId = approval_id;

    // Store targets for approval
    pendingApprovals = {};
    targets.forEach(target => {
        pendingApprovals[target.id] = {
            tool: target.tool,
            details: target.details,
            approved: null,  // null = no decision yet, true = approved, false = denied
            reason: ''
        };
    });

    // Show approval modal
    showApprovalModal(targets);
}

// Show approval modal with tool details
function showApprovalModal(targets) {
    const modal = document.getElementById('approval-modal');
    const container = document.getElementById('approval-targets');
    container.innerHTML = '';

    targets.forEach(target => {
        const targetDiv = document.createElement('div');
        targetDiv.className = 'approval-target';
        targetDiv.dataset.targetId = target.id;

        // Format the details - single line (same as tool-call dialog)
        let detailsHtml = '';
        if (target.details) {
            try {
                const detailsObj = typeof target.details === 'string'
                    ? JSON.parse(target.details)
                    : target.details;
                detailsHtml = `<pre>${escapeHtml(JSON.stringify(detailsObj))}</pre>`;
            } catch (e) {
                detailsHtml = `<pre>${escapeHtml(String(target.details))}</pre>`;
            }
        }

        targetDiv.innerHTML = `
            <div class="approval-target-header">
                <span class="approval-tool-icon">🔧</span>
                <span class="approval-tool-name">${escapeHtml(target.tool)}</span>
            </div>
            ${detailsHtml ? `<div class="approval-target-details">${detailsHtml}</div>` : ''}
            <div class="approval-footer">
                <div class="approval-result" id="approval-result-${escapeHtml(target.id)}"></div>
                <div class="approval-actions">
                    <button class="btn-approve" onclick="approveTarget('${escapeHtml(target.id)}')">Approve</button>
                    <button class="btn-deny" onclick="denyTarget('${escapeHtml(target.id)}')">Deny</button>
                </div>
            </div>
        `;

        container.appendChild(targetDiv);
    });

    // Update modal header with count
    document.getElementById('approval-count').textContent = targets.length;

    modal.style.display = 'flex';
    document.body.style.overflow = 'hidden'; // Prevent background scrolling
}

// Approve a specific target
function approveTarget(targetId) {
    if (pendingApprovals[targetId]) {
        pendingApprovals[targetId].approved = true;
        pendingApprovals[targetId].reason = '';

        // Update UI
        const resultEl = document.getElementById(`approval-result-${targetId}`);
        if (resultEl) {
            resultEl.innerHTML = '<span class="approved-text">✅ Approved</span>';
            resultEl.className = 'approval-result approved';
        }

        // Disable buttons
        const targetDiv = document.querySelector(`[data-target-id="${targetId}"]`);
        if (targetDiv) {
            const buttons = targetDiv.querySelectorAll('button');
            buttons.forEach(btn => {
                btn.disabled = true;
                btn.style.opacity = '0.5';
                btn.style.cursor = 'not-allowed';
            });
            targetDiv.classList.add('decided');
        }

        checkAllApprovalsDone();
    }
}

// Deny a specific target
function denyTarget(targetId) {
    if (pendingApprovals[targetId]) {
        pendingApprovals[targetId].approved = false;
        pendingApprovals[targetId].reason = '';

        // Update UI
        const resultEl = document.getElementById(`approval-result-${targetId}`);
        if (resultEl) {
            resultEl.innerHTML = `<span class="denied-text">❌ Denied</span>`;
            resultEl.className = 'approval-result denied';
        }

        // Disable buttons
        const targetDiv = document.querySelector(`[data-target-id="${targetId}"]`);
        if (targetDiv) {
            const buttons = targetDiv.querySelectorAll('button');
            buttons.forEach(btn => {
                btn.disabled = true;
                btn.style.opacity = '0.5';
                btn.style.cursor = 'not-allowed';
            });
            targetDiv.classList.add('decided');
        }

        checkAllApprovalsDone();
    }
}

// Approve all pending targets
function approveAll() {
    Object.keys(pendingApprovals).forEach(targetId => {
        if (pendingApprovals[targetId].approved === null) {
            approveTarget(targetId);
        }
    });
}

// Deny all pending targets
function denyAll() {
    Object.keys(pendingApprovals).forEach(targetId => {
        if (pendingApprovals[targetId].approved === null) {
            denyTarget(targetId);
        }
    });
}

// Approve all and submit immediately
function approveAllAndSubmit() {
    approveAll();
    // Submit immediately
    setTimeout(() => {
        submitApprovals();
    }, 300);
}

// Deny all and submit immediately
function denyAllAndSubmit() {
    denyAll();
    // Submit immediately
    setTimeout(() => {
        submitApprovals();
    }, 300);
}

// Check if all targets have been approved/denied
function checkAllApprovalsDone() {
    // Check if at least one target has a decision (approved or denied)
    const hasDecision = Object.values(pendingApprovals).some(t => t.approved === true || t.approved === false);

    const submitBtn = document.getElementById('btn-submit-approval');
    if (submitBtn) {
        // Enable submit button once user has made at least one decision
        submitBtn.disabled = !hasDecision;
    }
}

// Submit all approval decisions
function submitApprovals() {
    console.log('submitApprovals called, currentApprovalId:', currentApprovalId);
    console.log('pendingApprovals:', pendingApprovals);

    if (!currentApprovalId) {
        console.log('No currentApprovalId, returning');
        return;
    }

    const results = {};
    let decisionCount = 0;
    Object.keys(pendingApprovals).forEach(targetId => {
        const target = pendingApprovals[targetId];
        console.log(`Target ${targetId}: approved=${target.approved}`);
        // Only include targets that have a decision (approved or denied)
        if (target.approved === true || target.approved === false) {
            results[targetId] = {
                approved: target.approved,
                reason: target.reason || ''
            };
            decisionCount++;
        }
    });

    console.log('Submitting approval response:', {
        approval_id: currentApprovalId,
        results: results,
        decisionCount: decisionCount
    });

    // Send response to server
    if (ws && ws.readyState === WebSocket.OPEN) {
        const message = JSON.stringify({
            type: 'approval_response',
            payload: {
                approval_id: currentApprovalId,
                results: results
            }
        });
        console.log('Sending approval_response message:', message);
        ws.send(message);
    } else {
        console.error('WebSocket not open, readyState:', ws ? ws.readyState : 'ws is null');
    }

    // Close modal
    hideApprovalModal();

    // Reset state
    currentApprovalId = null;
    pendingApprovals = {};
}

// Hide approval modal
function hideApprovalModal() {
    document.getElementById('approval-modal').style.display = 'none';
    document.body.style.overflow = ''; // Restore scrolling
    currentApprovalId = null;
    pendingApprovals = {};
}

// Cancel approval (deny all with default reason)
function cancelApprovals() {
    // Deny all pending targets
    Object.keys(pendingApprovals).forEach(targetId => {
        if (pendingApprovals[targetId].approved === null || pendingApprovals[targetId].approved === undefined) {
            pendingApprovals[targetId].approved = false;
            pendingApprovals[targetId].reason = 'Cancelled by user';

            // Update UI
            const resultEl = document.getElementById(`approval-result-${targetId}`);
            if (resultEl) {
                resultEl.innerHTML = '<span class="denied-text">❌ Cancelled</span>';
                resultEl.className = 'approval-result denied';
            }
        }
    });

    // Submit the approvals (all denied)
    submitApprovals();
}

function sendMessage() {
    const input = document.getElementById('message-input');

    // If currently generating, this is a stop action
    if (isGenerating) {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
                type: 'stop',
                payload: {}
            }));
        } else {
            // WebSocket not available, reset state
            isGenerating = false;
            input.disabled = false;
            updateSendButton();
        }
        return;
    }

    const message = input.value.trim();

    // Allow sending files without text
    if ((!message || message.trim() === '') && window.FileUploadHandler.getPendingFiles().length === 0) {
        return;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Save to input history (only if there's text)
    if (message && message.trim()) {
        window.InputHistory.saveToHistory(message);
    }

    // Prepare message payload with optional files
    const payload = {
        message: message || ''
    };

    // Include files if any
    if (window.FileUploadHandler.getPendingFiles().length > 0) {
        payload.files = window.FileUploadHandler.getPendingFiles().map(file => ({
            url: file.url,
            type: file.type,
            name: file.name,
            file_size: file.size
        }));
    }

    // Display user message with files
    displayUserMessageWithFiles(message, window.FileUploadHandler.getPendingFiles());

    // Save last user message for regenerate
    lastUserMessage = message;
    lastUserFiles = window.FileUploadHandler.getPendingFiles().length > 0 
        ? window.FileUploadHandler.getPendingFiles().map(file => ({
            url: file.url,
            type: file.type,
            name: file.name,
            file_size: file.size
        }))
        : null;
    // Remove regenerate button from previous last user message
    removeRegenerateFromLastMessage();

    // Force scroll to bottom when user sends a message
    scrollToBottom(true);

    // Save to local storage history (include files)
    const filesToSave = window.FileUploadHandler.getPendingFiles().length > 0 ? window.FileUploadHandler.getPendingFiles().map(file => ({
        url: file.url,
        type: file.type,
        name: file.name,
        size: file.size,
        isImage: file.isImage || (file.type && file.type.startsWith('image/')),
        compressed: file.compressed || false,
        originalSize: file.originalSize || file.size
    })) : null;
    window.MessageHistory.saveMessage(message, 'user', null, null, filesToSave);

    // Clear input and files
    input.value = '';
    window.FileUploadHandler.clearPendingFiles();

    // 禁用输入框，发送按钮保持可用（用于停止）
    input.disabled = true;
    isGenerating = true;
    updateSendButton();

    // Send message with files
    ws.send(JSON.stringify({
        type: 'chat',
        payload: payload
    }));
}

// Update send button text and style based on state
function updateSendButton() {
    const sendBtn = document.getElementById('send-btn');
    if (!sendBtn) return;

    if (isGenerating) {
        sendBtn.innerHTML = '⏹';
        sendBtn.title = 'Stop current response';
        sendBtn.classList.add('stopping');
    } else {
        sendBtn.innerHTML = '➤';
        sendBtn.title = 'Send message';
        sendBtn.classList.remove('stopping');
    }
}

// Add regenerate button to a user message element (near the copy button)
function addRegenerateButton(messageElement) {
    if (!messageElement) return;
    
    // Don't add if already has a regenerate button
    if (messageElement.querySelector('.regen-btn')) return;
    
    // Only add if we have a valid last message
    const hasMessage = lastUserMessage && lastUserMessage.trim() !== '';
    const hasFiles = lastUserFiles && lastUserFiles.length > 0;
    if (!hasMessage && !hasFiles) return;
    
    const footer = messageElement.querySelector('.message-footer');
    if (!footer) return;
    
    const regenBtn = document.createElement('button');
    regenBtn.className = 'copy-btn regen-btn';
    regenBtn.title = 'Regenerate response';
    regenBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="1 4 1 10 7 10"></polyline>
            <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"></path>
        </svg>
        <span class="copy-text">Redo</span>
    `;
    regenBtn.onclick = function(e) {
        e.stopPropagation();
        regenerate();
    };
    footer.appendChild(regenBtn);
}

// Remove regenerate button from the last user message
function removeRegenerateFromLastMessage() {
    if (lastUserMessageElement) {
        const regenBtn = lastUserMessageElement.querySelector('.regen-btn');
        if (regenBtn) {
            regenBtn.remove();
        }
    }
}

// Regenerate response - resend the last user message
function regenerate() {
    if (isGenerating) {
        // If currently generating, stop first
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
                type: 'stop',
                payload: {}
            }));
        }
        return;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) {
        showToast('WebSocket not connected', true);
        return;
    }

    // Check if we have a last message to regenerate
    const hasMessage = lastUserMessage && lastUserMessage.trim() !== '';
    const hasFiles = lastUserFiles && lastUserFiles.length > 0;

    if (!hasMessage && !hasFiles) {
        showToast('No message to regenerate', true);
        return;
    }

    // Remove old assistant response from DOM (all elements after last user message)
    if (lastUserMessageElement) {
        let nextEl = lastUserMessageElement.nextElementSibling;
        while (nextEl) {
            const toRemove = nextEl;
            nextEl = nextEl.nextElementSibling;
            toRemove.remove();
        }
    }

    // Reset streaming state variables since old DOM elements are removed
    thinkingBlock = null;
    responseBlock = null;
    currentChunk = '';
    currentThinkingChunk = '';
    currentAssistantMessage = '';
    currentThinkingMessage = '';
    currentContentType = '';
    chunkElement = null;
    thinkingElement = null;

    // Hide regenerate button
    removeRegenerateFromLastMessage();

    // Remove old assistant/tool_call messages from local storage
    window.MessageHistory.removeMessagesAfterLastUser();

    // Prepare payload
    const payload = {
        message: lastUserMessage || ''
    };
    if (hasFiles) {
        payload.files = lastUserFiles;
    }

    // Force scroll to bottom
    scrollToBottom(true);

    // Disable input and set generating state
    const input = document.getElementById('message-input');
    if (input) {
        input.value = '';  // Clear current input since we're resending last message
        input.disabled = true;
    }
    isGenerating = true;
    updateSendButton();

    // Send regenerate message
    ws.send(JSON.stringify({
        type: 'regenerate',
        payload: payload
    }));
}

// Display user message with files/files
function displayUserMessageWithFiles(text, files, msgIndex = -1) {
    const div = document.createElement('div');
    div.className = 'message user';
    div.dataset.msgIndex = msgIndex;

    const contentDiv = document.createElement('div');
    contentDiv.className = 'message-content';

    let html = '';

    // Add text if present
    if (text && text.trim()) {
        html += `<div style="white-space: pre-wrap;">${escapeHtml(text)}</div>`;
    }

    // Add files/files if present
    if (files && files.length > 0) {
        html += '<div class="user-files" style="display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px;">';
        files.forEach((file, idx) => {
            if (file.isImage || (file.type && file.type.startsWith('image/'))) {
                // For new messages (msgIndex < 0), use showImagePreview with files array and index
                if (msgIndex < 0) {
                    // Store files in a data attribute and pass index
                    const filesJson = encodeURIComponent(JSON.stringify(files));
                    html += `<img src="${file.url}" alt="${file.name}" style="max-width: 200px; max-height: 200px; border-radius: 8px; object-fit: cover; cursor: zoom-in;" onclick="showImagePreviewWithIndex('${filesJson}', ${idx})" />`;
                } else {
                    html += `<img src="${file.url}" alt="${file.name}" style="max-width: 200px; max-height: 200px; border-radius: 8px; object-fit: cover; cursor: zoom-in;" onclick="showImagePreviewFromHistory(${msgIndex}, ${idx}); return false;" />`;
                }
            } else {
                const icon = window.FileUploadHandler.getFileIcon(file.type, file.name);
                const sizeStr = window.FileUploadHandler.formatFileSize(file.size);
                html += `
                    <div class="user-file-item" style="display: flex; flex-direction: column; align-items: center; padding: 10px; background: #f5f5f5; border-radius: 8px; min-width: 100px;">
                        <span style="font-size: 32px;">${icon}</span>
                        <span style="font-size: 11px; color: #333; text-align: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100px; margin-top: 4px;" title="${file.name}">${file.name}</span>
                        <span style="font-size: 10px; color: #888; margin-top: 2px;">${sizeStr}</span>
                    </div>
                `;
            }
        });
        html += '</div>';
    }

    contentDiv.innerHTML = html;
    div.appendChild(contentDiv);

    // Add footer with copy button for user messages
    const footer = document.createElement('div');
    footer.className = 'message-footer';
    footer.innerHTML = `
        <button class="copy-btn" onclick="copyUserMessage(this)" title="Copy message">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
            </svg>
            <span class="copy-text">Copy</span>
        </button>
    `;
    div.appendChild(footer);

    document.getElementById('messages').appendChild(div);

    // Update last user message element reference
    removeRegenerateFromLastMessage();
    lastUserMessageElement = div;
    // Set lastUserMessage/lastUserFiles for history loading
    if (msgIndex >= 0) {
        lastUserMessage = (text && text.trim()) ? text.trim() : '';
        lastUserFiles = (files && files.length > 0) ? files.map(file => ({
            url: file.url,
            type: file.type,
            name: file.name,
            file_size: file.size
        })) : null;
    }
    // When called from loadMessageHistory (msgIndex >= 0), regenerate button is added after loading
    // When called from sendMessage/regenerate (msgIndex < 0), add now
    if (msgIndex < 0) {
        // Don't add regenerate button here - it will be added on complete/error/stopped
        // addRegenerateButton(div);
    }
    // Don't auto-scroll here - the forced scroll in sendMessage() handles it
}

function addMessage(text, type) {
    const div = document.createElement('div');
    div.className = 'message ' + type;

    const contentDiv = document.createElement('div');
    contentDiv.className = 'message-content' + (type === 'assistant' ? ' markdown-body' : '');

    if (type === 'assistant') {
        // Store original markdown content for copying
        contentDiv.dataset.originalContent = text;
        try {
            contentDiv.innerHTML = marked.parse(text);
        } catch (e) {
            console.error('Markdown parsing error:', e);
            contentDiv.textContent = text;
        }
    } else {
        contentDiv.textContent = text;
    }

    div.appendChild(contentDiv);

    // 添加消息页脚复制按钮（assistant 和 user 消息）
    if (type === 'assistant' || type === 'user') {
        const footer = document.createElement('div');
        footer.className = 'message-footer';
        const copyFunction = type === 'assistant' ? 'copyMessage' : 'copyUserMessage';
        footer.innerHTML = `
            <button class="copy-btn" onclick="${copyFunction}(this)" title="Copy message">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                </svg>
                <span class="copy-text">Copy</span>
            </button>
        `;
        div.appendChild(footer);
    }

    document.getElementById('messages').appendChild(div);

    // 为消息中的代码块添加复制按钮
    addCopyButtonsToCodeBlocks(div);

    if (type === 'assistant') {
        renderMermaidDiagrams(div);
    }

    smartScrollToBottom();
}

function displayToolCall(name, args, index, streaming) {
    // Get or create the tool call entry
    let toolCall = toolCalls[index];

    if (!toolCall) {
        // First time seeing this tool call - create new element
        toolCall = {
            name: name,
            argsText: args || '',
            complete: false
        };

        const div = document.createElement('div');
        div.className = 'message tool-call';
        div.id = 'tool-call-' + index;

        // Create tool call content with args area
        div.innerHTML = `
            <div class="tool-call-content">
                <span class="tool-icon">🔧</span>
                <span class="tool-name">${escapeHtml(name)}</span>
            </div>
            <div class="tool-args">
                <pre><code class="language-json"></code></pre>
            </div>
        `;

        document.getElementById('messages').appendChild(div);
        toolCall.element = div;
        toolCall.argsElement = div.querySelector('.tool-args pre');
        toolCalls[index] = toolCall;

        // Update args if provided
        if (args !== undefined && args !== null) {
            toolCall.argsText = args;
            toolCall.argsElement.textContent = args;
        }

        smartScrollToBottom();
    } else {
        // Update existing tool call
        if (name !== toolCall.name) {
            // Name changed (shouldn't happen normally)
            toolCall.name = name;
            const nameElement = toolCall.element.querySelector('.tool-name');
            if (nameElement) {
                nameElement.textContent = name;
            }
        }

        // Handle streaming updates - the backend sends accumulated arguments
        // For streaming mode, we receive incremental updates that build up the complete args
        if (streaming === true && args !== undefined && args !== null) {
            // Check if this is incremental content (not a complete replacement)
            // If new args starts with current argsText, it's incremental - append only the new part
            if (args.startsWith(toolCall.argsText)) {
                toolCall.argsText = args;
            } else {
                // Args doesn't contain current text, append incrementally
                toolCall.argsText += args;
            }
            if (toolCall.argsElement) {
                toolCall.argsElement.textContent = toolCall.argsText;
            }

        }

        // If streaming=false, mark as complete and add visual indicator
        if (streaming === false) {
            toolCall.complete = true;
            if (!toolCall.element.querySelector('.tool-complete')) {
                const completeDiv = document.createElement('div');
                completeDiv.className = 'tool-complete';
                completeDiv.textContent = '✓ Complete';
                toolCall.element.appendChild(completeDiv);
            }

            // Save tool call to local storage
            window.MessageHistory.saveMessage(null, 'tool_call', {
                name: name,
                arguments: args || toolCall.argsText
            });

            // Remove from tracking
            delete toolCalls[index];
        }

        smartScrollToBottom();
    }
}

function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

let currentChunk = '';
let chunkElement = null;
let currentThinkingChunk = '';
let thinkingElement = null;
let currentAssistantMessage = '';
let currentThinkingMessage = '';
let currentContentType = '';

// 存储每个消息块（thinking 和 response）
let thinkingBlock = null;
let responseBlock = null;

// Smart scroll to bottom - delegated to scroll-handler.js
function smartScrollToBottom(force) {
    window.ScrollHandler.smartScrollToBottom(force);
}

function displayChunk(content, isFirst, isLast, contentType = 'response') {
    // 检查是否是最后的 final chunk（空内容）
    if (isLast && content === '') {
        // 最终完成处理
        if (thinkingBlock) {
            // 为思考消息添加 footer
            if (!thinkingBlock.querySelector('.message-footer')) {
                const footer = document.createElement('div');
                footer.className = 'message-footer';
                footer.innerHTML = `
                    <button class="copy-btn" onclick="copyThinkingMessage(this)" title="Copy thinking content">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                        </svg>
                        <span class="copy-text">Copy</span>
                    </button>
                `;
                thinkingBlock.appendChild(footer);
            }
            addCopyButtonsToCodeBlocks(thinkingBlock);
            renderMermaidDiagrams(thinkingBlock);
        }

        if (responseBlock) {
            // 为回答消息添加 footer（如果没有）
            if (!responseBlock.querySelector('.message-footer')) {
                const footer = document.createElement('div');
                footer.className = 'message-footer';
                footer.innerHTML = `
                    <button class="copy-btn" onclick="copyResponseMessage(this)" title="Copy response content">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                        </svg>
                        <span class="copy-text">Copy</span>
                    </button>
                `;
                responseBlock.appendChild(footer);
            }
            addCopyButtonsToCodeBlocks(responseBlock);
            renderMermaidDiagrams(responseBlock);
        }

        // 保存完整消息到本地存储（包含思考内容和回答内容）
        const fullContent = (currentAssistantMessage || '').trim();
        const thinkingContent = (currentThinkingChunk || '').trim();
        if (fullContent || thinkingContent) {
            window.MessageHistory.saveMessage(fullContent, 'assistant', null, thinkingContent);
        }

        // 重置状态
        thinkingBlock = null;
        responseBlock = null;
        currentChunk = '';
        currentThinkingChunk = '';
        currentAssistantMessage = '';
        currentThinkingMessage = '';
        currentContentType = '';
        chunkElement = null;
        thinkingElement = null;

        // Use smart scroll to avoid interrupting user reading
        // The copy buttons and any subsequent tool calls will be visible if user is at bottom
        smartScrollToBottom();
        return;
    }

    // 处理实际内容
    if (contentType === 'thinking') {
        // 处理思考消息
        if (isFirst || !thinkingBlock) {
            // 创建新的思考消息块
            thinkingBlock = document.createElement('div');
            thinkingBlock.className = 'message assistant thinking-message';
            thinkingBlock.innerHTML = `
                <div class="thinking-header">
                    <span class="thinking-icon">💭</span>
                    <span class="thinking-title">Thinking</span>
                    <button class="thinking-expand-btn" onclick="toggleThinkingExpand(this)" title="Expand thinking">Expand ▾</button>
                </div>
                <div class="thinking-content thinking-collapsed markdown-body"></div>
            `;
            thinkingElement = thinkingBlock.querySelector('.thinking-content');
            currentThinkingChunk = content;
            // Store original markdown content for copying
            thinkingElement.dataset.originalContent = content;

            document.getElementById('messages').appendChild(thinkingBlock);

            if (thinkingElement) {
                try {
                    thinkingElement.innerHTML = marked.parse(content);
                } catch (e) {
                    thinkingElement.textContent = content;
                }
                scrollThinkingContentToBottom(thinkingElement);
            }
            smartScrollToBottom();
        } else {
            // 追加内容
            currentThinkingChunk += content;
            // Update stored original content
            thinkingElement.dataset.originalContent = currentThinkingChunk;
            if (thinkingElement) {
                try {
                    thinkingElement.innerHTML = marked.parse(currentThinkingChunk);
                } catch (e) {
                    thinkingElement.textContent = currentThinkingChunk;
                }
                scrollThinkingContentToBottom(thinkingElement);
            }
            smartScrollToBottom();
        }
    } else {
        // 处理回答消息
        if (isFirst || !responseBlock) {
            // 创建新的回答消息块
            responseBlock = document.createElement('div');
            responseBlock.className = 'message assistant response-message';
            responseBlock.innerHTML = `
                <div class="response-content markdown-body"></div>
            `;
            chunkElement = responseBlock.querySelector('.response-content');
            currentChunk = content;
            currentAssistantMessage = content;
            // Store original markdown content for copying
            chunkElement.dataset.originalContent = content;

            document.getElementById('messages').appendChild(responseBlock);

            if (chunkElement) {
                try {
                    chunkElement.innerHTML = marked.parse(content);
                } catch (e) {
                    chunkElement.textContent = content;
                }
            }
            smartScrollToBottom();
        } else {
            // 追加内容
            currentChunk += content;
            currentAssistantMessage += content;
            // Update stored original content
            chunkElement.dataset.originalContent = currentChunk;
            if (chunkElement) {
                try {
                    chunkElement.innerHTML = marked.parse(currentChunk);
                } catch (e) {
                    chunkElement.textContent = currentChunk;
                }
            }
            smartScrollToBottom();
        }
    }
}

// Scroll to bottom - delegated to scroll-handler.js
function scrollToBottom(force) {
    window.ScrollHandler.scrollToBottom(force);
}

// 复制整个消息内容（assistant 消息）
function copyMessage(btn) {
    const messageDiv = btn.closest('.message');
    const contentDiv = messageDiv.querySelector('.message-content');
    // 使用 data 属性存储的原始 markdown 内容，而不是 innerText（渲染后的文本）
    const textToCopy = contentDiv.dataset.originalContent || contentDiv.innerText;

    navigator.clipboard.writeText(textToCopy).then(() => {
        // 显示成功状态
        const copyText = btn.querySelector('.copy-text');
        const originalText = copyText.textContent;
        copyText.textContent = 'Copied!';
        btn.classList.add('copied');

        setTimeout(() => {
            copyText.textContent = originalText;
            btn.classList.remove('copied');
        }, 1500);
    }).catch(err => {
        console.error('Failed to copy:', err);
        showToast('Copy failed', false);
    });
}

// 复制用户消息内容
function copyUserMessage(btn) {
    const messageDiv = btn.closest('.message');
    const contentDiv = messageDiv.querySelector('.message-content');
    // 用户消息包含文本和图片/文件，我们需要提取所有可复制的内容
    let textToCopy = '';
    
    // 获取文本内容
    const textDiv = contentDiv.querySelector('div[style*="white-space: pre-wrap"]');
    if (textDiv && textDiv.textContent) {
        textToCopy = textDiv.textContent;
    } else {
        textToCopy = contentDiv.innerText;
    }

    navigator.clipboard.writeText(textToCopy).then(() => {
        const copyText = btn.querySelector('.copy-text');
        const originalText = copyText.textContent;
        copyText.textContent = 'Copied!';
        btn.classList.add('copied');

        setTimeout(() => {
            copyText.textContent = originalText;
            btn.classList.remove('copied');
        }, 1500);
    }).catch(err => {
        console.error('Failed to copy user message:', err);
        showToast('Copy failed', false);
    });
}

// Scroll thinking content to bottom (for collapsed state showing newest lines)
function scrollThinkingContentToBottom(contentDiv) {
    if (contentDiv && contentDiv.classList.contains('thinking-collapsed')) {
        // Use requestAnimationFrame to ensure DOM is rendered before scrolling
        requestAnimationFrame(() => {
            contentDiv.scrollTop = contentDiv.scrollHeight;
        });
    }
}

// 切换思考内容的展开/收起状态
function toggleThinkingExpand(btn) {
    const thinkingBlock = btn.closest('.thinking-message');
    const contentDiv = thinkingBlock.querySelector('.thinking-content');
    const isCollapsed = contentDiv.classList.contains('thinking-collapsed');
    
    if (isCollapsed) {
        // 展开
        contentDiv.classList.remove('thinking-collapsed');
        btn.textContent = 'Collapse ▴';
        btn.title = 'Collapse thinking';
    } else {
        // 收起
        contentDiv.classList.add('thinking-collapsed');
        btn.textContent = 'Expand ▾';
        btn.title = 'Expand thinking';
        // Scroll to bottom so newest content is visible
        scrollThinkingContentToBottom(contentDiv);
    }
    
    // 展开/收起后滚动到合适位置
    setTimeout(() => {
        if (!isCollapsed) {
            // 收起后，将thinking header滚动到可见区域
            thinkingBlock.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
        smartScrollToBottom();
    }, 100);
}

// 复制思考消息内容
function copyThinkingMessage(btn) {
    const messageDiv = btn.closest('.message');
    const contentDiv = messageDiv.querySelector('.thinking-content');
    // 使用 data 属性存储的原始 markdown 内容
    const textToCopy = contentDiv ? (contentDiv.dataset.originalContent || contentDiv.innerText) : '';

    navigator.clipboard.writeText(textToCopy).then(() => {
        const copyText = btn.querySelector('.copy-text');
        const originalText = copyText.textContent;
        copyText.textContent = 'Copied!';
        btn.classList.add('copied');

        setTimeout(() => {
            copyText.textContent = originalText;
            btn.classList.remove('copied');
        }, 1500);
    }).catch(err => {
        console.error('Failed to copy thinking:', err);
        showToast('Copy failed', false);
    });
}

// 复制回答消息内容
function copyResponseMessage(btn) {
    const messageDiv = btn.closest('.message');
    const contentDiv = messageDiv.querySelector('.response-content');
    // 使用 data 属性存储的原始 markdown 内容
    const textToCopy = contentDiv ? (contentDiv.dataset.originalContent || contentDiv.innerText) : '';

    navigator.clipboard.writeText(textToCopy).then(() => {
        const copyText = btn.querySelector('.copy-text');
        const originalText = copyText.textContent;
        copyText.textContent = 'Copied!';
        btn.classList.add('copied');

        setTimeout(() => {
            copyText.textContent = originalText;
            btn.classList.remove('copied');
        }, 1500);
    }).catch(err => {
        console.error('Failed to copy response:', err);
        showToast('Copy failed', false);
    });
}

// 为消息中的代码块添加复制按钮
function addCopyButtonsToCodeBlocks(messageDiv) {
    const codeBlocks = messageDiv.querySelectorAll('pre');
    codeBlocks.forEach((pre, index) => {
        // Skip mermaid diagrams
        if (pre.classList.contains('mermaid')) {
            return;
        }

        // 检查是否已经添加了复制按钮
        if (pre.querySelector('.code-copy-btn')) {
            return;
        }

        // 获取代码文本
        const codeElement = pre.querySelector('code');
        const codeText = codeElement ? codeElement.innerText : pre.innerText;

        // 创建复制按钮
        const copyBtn = document.createElement('button');
        copyBtn.className = 'code-copy-btn';
        copyBtn.innerHTML = `
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
            </svg>
        `;
        copyBtn.title = 'Copy code';

        // 添加点击事件
        copyBtn.onclick = function (e) {
            e.stopPropagation();
            copyCodeBlock(this, codeText);
        };

        // 将按钮添加到 pre 元素
        pre.style.position = 'relative';
        pre.appendChild(copyBtn);
    });
}

// 复制代码块内容
function copyCodeBlock(btn, codeText) {
    navigator.clipboard.writeText(codeText).then(() => {
        // 显示成功状态
        btn.classList.add('copied');
        btn.innerHTML = `
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12"></polyline>
            </svg>
        `;
        btn.title = 'Copied!';

        setTimeout(() => {
            btn.classList.remove('copied');
            btn.innerHTML = `
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                </svg>
            `;
            btn.title = 'Copy code';
        }, 1500);
    }).catch(err => {
        console.error('Failed to copy code:', err);
        showToast('Copy failed', false);
    });
}

// Toast 提示（复用现有逻辑或创建新函数）
function showToast(message, isError) {
    const container = document.getElementById('toast-container');
    container.innerHTML = '';

    const toast = document.createElement('div');
    toast.className = 'toast' + (isError ? ' error' : '');
    toast.innerHTML = `<span>${message}</span>`;

    container.appendChild(toast);
    container.style.display = 'flex';

    setTimeout(() => {
        closeStatus();
    }, 1500);
}

function setStatus(text, isError) {
    const container = document.getElementById('toast-container');

    // Clear any existing toast
    if (toastTimeout) {
        clearTimeout(toastTimeout);
        toastTimeout = null;
    }
    container.innerHTML = '';
    container.style.display = 'none';

    if (!text) return;

    // Create toast element
    const toast = document.createElement('div');
    toast.className = 'toast' + (isError ? ' error' : '');
    toast.innerHTML = '<span>' + text + '</span><span class="toast-close" onclick="closeStatus()">×</span>';

    container.appendChild(toast);
    container.style.display = 'flex';

    // Auto close based on message type
    // Normal messages: 1.5 seconds, Error messages: 3 seconds
    const displayTime = isError ? 3000 : 1500;
    toastTimeout = setTimeout(() => {
        closeStatus();
    }, displayTime);
}

function closeStatus() {
    if (toastTimeout) {
        clearTimeout(toastTimeout);
        toastTimeout = null;
    }
    document.getElementById('toast-container').style.display = 'none';
}

function showClearModal() {
    if (!currentChat) return;
    document.getElementById('clear-chat-name').textContent = currentChat;
    // Reset the checkbox
    document.getElementById('clear-all-records').checked = false;
    document.getElementById('clear-modal').style.display = 'flex';
}

function hideClearModal() {
    document.getElementById('clear-modal').style.display = 'none';
}

// Quick clear context without modal (Ctrl+K shortcut) - only clears server context, keeps local data
async function quickClearContext() {
    if (!currentChat) {
        showToast('Please select a chat first', true);
        return;
    }

    // Don't allow clearing while AI is generating a response
    if (isGenerating) {
        showToast('Cannot clear context while AI is replying', true);
        return;
    }

    // Send clear message to server (only clears conversation context)
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'clear', payload: {} }));
        showToast('Conversation context cleared (local data preserved)', false);
    } else {
        showToast('WebSocket not connected', true);
    }
}

async function confirmClear() {
    const clearAllRecords = document.getElementById('clear-all-records').checked;

    // 发送 clear 消息到服务器（服务端不再携带 context）
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'clear', payload: {} }));
    }

    // 勾选时：清空消息展示 + 删除本地存储记录
    if (clearAllRecords) {
        // 清空消息展示区
        const messagesContainer = document.getElementById('messages');
        if (messagesContainer) {
            messagesContainer.innerHTML = '';
        }
        // 清除历史记录（IndexedDB + localStorage）
        await window.MessageHistory.clearHistory();
        // Badge will be updated when we receive cleared message from server
    }
    // 不勾选时：只发送 clear 消息，不清空展示，不删记录

    hideClearModal();
}

// Keep session - execute session keep hook
function keepSession() {
    if (!currentChat) {
        showToast('Please select a chat first', true);
        return;
    }

    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'keep', payload: {} }));
        showToast('Executing keep hook...', false);
    } else {
        showToast('WebSocket not connected', true);
    }
}

// autoResize function removed - input box height is now fixed via CSS

function handleKeyDown(e) {
    const input = document.getElementById('message-input');
    if (!input) return;

    // Quick phrases panel keyboard navigation (handles keys when panel is visible)
    if (window.QuickPhrases && window.QuickPhrases.isVisible()) {
        var handled = window.QuickPhrases.handleKeyDown(e);
        if (handled) return;
    }

    // Ctrl+K / Cmd+K = clear conversation context (without deleting local data)
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault();
        quickClearContext();
        return;
    }

    // Enter without shift = send
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
        return;
    }

    // Arrow Left = show quick phrases panel (only when input is empty)
    if (e.key === 'ArrowLeft') {
        var isEmpty = input.value.length === 0;
        if (isEmpty && !e.shiftKey && window.QuickPhrases) {
            e.preventDefault();
            window.QuickPhrases.show();
            return;
        }
    }

    // Arrow Up = navigate to older history (only when input is empty or at cursor start)
    if (e.key === 'ArrowUp') {
        // Only navigate history when:
        // 1. Input is empty, OR
        // 2. Cursor is at the start and user is not selecting text
        var isSelection = window.getSelection().toString().length > 0;
        var hasContent = input.value.length > 0;
        var atStart = input.selectionStart === 0;

        if ((!hasContent || (atStart && !isSelection)) && !e.shiftKey) {
            e.preventDefault();
            window.InputHistory.navigateHistory(-1);  // -1 = show older entries (decreasing index)
        }
        return;
    }

    // Arrow Down = navigate to newer history
    if (e.key === 'ArrowDown') {
        // Only navigate history when:
        // 1. Input is empty, OR
        // 2. Cursor is at the end and user is not selecting text
        var isSelectionDown = window.getSelection().toString().length > 0;
        var hasContentDown = input.value.length > 0;
        var atEnd = input.selectionEnd === input.value.length;

        if ((!hasContentDown || (atEnd && !isSelectionDown)) && !e.shiftKey) {
            e.preventDefault();
            window.InputHistory.navigateHistory(1);  // 1 = show newer entries (increasing index)
        }
        return;
    }

    // Escape = cancel history navigation, restore last input
    if (e.key === 'Escape') {
        if (window.InputHistory.getHistoryIndex() !== -1) {
            window.InputHistory.resetHistoryNavigation();
            input.value = '';
        }
        return;
    }

    // When user starts typing, reset history navigation and hide regenerate button
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) {
        // Hide quick phrases panel when typing
        if (window.QuickPhrases && window.QuickPhrases.isVisible()) {
            window.QuickPhrases.hide();
        }
        if (window.InputHistory.getHistoryIndex() !== -1) {
            window.InputHistory.resetHistoryNavigation();
        }
        // Hide regenerate button when user starts typing new message
        removeRegenerateFromLastMessage();
    }
}

// Mobile keyboard adaptation
let lastViewportHeight = window.innerHeight;

function handleViewportChange() {
    const viewport = window.visualViewport;
    const viewportHeight = viewport ? viewport.height : window.innerHeight;
    const chatPanel = document.getElementById('chat-panel');

    // Always keep chat-panel height in sync with actual viewport
    if (chatPanel && chatPanel.style.display !== 'none') {
        chatPanel.style.height = viewportHeight + 'px';
    }

    if (!viewport) return;

    // Detect keyboard show/hide by comparing viewport height change
    const heightDiff = lastViewportHeight - viewportHeight;
    const isKeyboardShown = heightDiff > 150; // Keyboard typically takes >150px

    if (isKeyboardShown) {
        // Keyboard is shown - scroll to make input visible
        const inputArea = document.getElementById('input-area');
        if (inputArea) {
            // Use setTimeout to allow keyboard animation completes
            setTimeout(() => {
                inputArea.scrollIntoView({ behavior: 'smooth', block: 'end' });
                // Also scroll messages to bottom (force scroll for keyboard)
                scrollToBottom(true);
            }, 100);
        }
    } else if (viewportHeight > lastViewportHeight - 50) {
        // Keyboard is hidden - restore layout
        setTimeout(() => {
            const messages = document.getElementById('messages');
            if (messages) {
                messages.style.height = 'auto';
            }
        }, 100);
    }

    lastViewportHeight = viewportHeight;
}

// Listen for visualViewport changes (mobile keyboard)
if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', handleViewportChange);
    window.visualViewport.addEventListener('scroll', handleViewportChange);
}

// Also listen to window resize for keyboard show/hide
window.addEventListener('resize', handleViewportChange);

// Initialize scroll detection - delegated to scroll-handler.js
function initScrollDetection() {
    window.ScrollHandler.init();
}

// Focus input when keyboard is shown on mobile
const messageInput = document.getElementById('message-input');
if (messageInput) {
    messageInput.addEventListener('focus', function () {
        // Small delay to allow keyboard to start appearing
        setTimeout(() => smartScrollToBottom(), 300);
    });

    // Handle paste event for image upload from clipboard
    messageInput.addEventListener('paste', function (e) {
        const items = e.clipboardData?.items;
        if (!items) return;

        // Find image items in clipboard
        const imageItems = Array.from(items).filter(item => item.type.startsWith('image/'));

        if (imageItems.length > 0) {
            e.preventDefault();

            // Process each image
            imageItems.forEach(item => {
                const file = item.getAsFile();
                if (file) {
                    window.FileUploadHandler.handleFiles([file]);
                }
            });

            showToast(`Pasted ${imageItems.length} image(s) from clipboard`, false);
        }
    });
}

// Image preview functions have been moved to image-preview.js module

// Mobile double-tap to clear context (similar to desktop Ctrl+K)
// Only active on mobile devices, and disabled during AI response generation
// Uses swipe detection: a "tap" is only counted if the finger stays mostly in place
// during the entire touch (touchstart -> touchend movement < SWIPE_THRESHOLD).
// This prevents false double-tap triggers when the user is scrolling quickly.
(function initMobileDoubleTapClear() {
    let lastTapTime = 0;
    const DOUBLE_TAP_THRESHOLD = 400; // ms between taps
    const DOUBLE_TAP_DISTANCE = 30;   // max pixels between tap positions
    const SWIPE_THRESHOLD = 15;       // max movement during a single touch to count as tap

    let lastTapX = 0;
    let lastTapY = 0;
    let touchStartX = 0;
    let touchStartY = 0;
    let touchHasMoved = false;

    // Attach to the messages area - the main content region
    const messagesEl = document.getElementById('messages');
    if (!messagesEl) return;

    messagesEl.addEventListener('touchstart', function(e) {
        // Only handle single-finger taps
        if (e.touches.length !== 1) return;
        const touch = e.touches[0];
        touchStartX = touch.clientX;
        touchStartY = touch.clientY;
        touchHasMoved = false;
    }, { passive: true });

    messagesEl.addEventListener('touchmove', function(e) {
        if (touchHasMoved) return;
        const touch = e.touches[0];
        const dx = Math.abs(touch.clientX - touchStartX);
        const dy = Math.abs(touch.clientY - touchStartY);
        if (dx > SWIPE_THRESHOLD || dy > SWIPE_THRESHOLD) {
            touchHasMoved = true;
        }
    }, { passive: true });

    messagesEl.addEventListener('touchend', function(e) {
        // Only on mobile devices
        if (!isMobileDevice()) return;

        // Skip if this touch was a swipe (not a stationary tap)
        if (touchHasMoved) return;

        // Don't trigger if user is interacting with buttons, links, inputs, etc.
        const target = e.target;
        if (target.closest('button') || target.closest('a') ||
            target.closest('input') || target.closest('textarea') ||
            target.closest('pre') || target.closest('code') ||
            target.closest('img') || target.closest('.user-files') ||
            target.closest('.copy-btn') || target.closest('.code-copy-btn') ||
            target.closest('.regen-btn')) {
            return;
        }

        const now = Date.now();
        const touch = e.changedTouches[0];
        const tapX = touch.clientX;
        const tapY = touch.clientY;

        const timeDiff = now - lastTapTime;
        const distX = Math.abs(tapX - lastTapX);
        const distY = Math.abs(tapY - lastTapY);

        if (timeDiff < DOUBLE_TAP_THRESHOLD && timeDiff > 0 &&
            distX < DOUBLE_TAP_DISTANCE && distY < DOUBLE_TAP_DISTANCE) {
            // Double tap detected
            e.preventDefault();
            quickClearContext();
            // Reset to prevent triple-tap
            lastTapTime = 0;
        } else {
            lastTapTime = now;
            lastTapX = tapX;
            lastTapY = tapY;
        }
    }, { passive: false });
})();

init();