// Configure marked.js
marked.setOptions({
    breaks: true,
    gfm: true,
    headerIds: true,
    langPrefix: 'language-'
});

let ws = null;
let currentChat = null;
let reconnectAttempts = 0;
const maxReconnectAttempts = 5;
let toastTimeout = null;
let isGenerating = false;

// File upload state
let pendingFiles = [];

// Image preview state
let currentPreviewImages = [];
let currentPreviewIndex = 0;

// Input history management
const INPUT_HISTORY_KEY = 'chat_input_history';
const MAX_HISTORY_SIZE = 50;
let inputHistory = [];
let historyIndex = -1;  // -1 means not browsing history, 0 is the newest entry
let lastInputValue = '';  // Store current input before browsing history

// Load input history from localStorage
// History is stored with oldest at index 0, newest at the end
function loadHistory() {
    try {
        const stored = localStorage.getItem(INPUT_HISTORY_KEY);
        if (stored) {
            inputHistory = JSON.parse(stored);
            // Ensure it's an array
            if (!Array.isArray(inputHistory)) {
                inputHistory = [];
            }
        }
    } catch (e) {
        console.error('Failed to load input history:', e);
        inputHistory = [];
    }
    historyIndex = -1;
}

// Save input to history (only non-empty, unique inputs) - newest at the end
function saveToHistory(text) {
    if (!text || !text.trim()) return;

    const trimmed = text.trim();
    // Remove if already exists (to move to end)
    const existingIndex = inputHistory.indexOf(trimmed);
    if (existingIndex !== -1) {
        inputHistory.splice(existingIndex, 1);
    }

    // Add to end
    inputHistory.push(trimmed);

    // Limit size - remove oldest entries if exceeds max
    if (inputHistory.length > MAX_HISTORY_SIZE) {
        inputHistory = inputHistory.slice(-MAX_HISTORY_SIZE);
    }

    // Save to localStorage
    try {
        localStorage.setItem(INPUT_HISTORY_KEY, JSON.stringify(inputHistory));
    } catch (e) {
        console.error('Failed to save input history:', e);
    }

    historyIndex = -1;
}

// Navigate history (up = older, down = newer)
// History is stored with oldest at index 0, newest at index length-1
function navigateHistory(direction) {
    if (inputHistory.length === 0) return;

    const input = document.getElementById('message-input');
    if (!input) return;

    // First key press - save current input
    if (historyIndex === -1) {
        lastInputValue = input.value;
        // Start from newest entry
        historyIndex = inputHistory.length;
    }

    // Calculate new index
    const newIndex = historyIndex + direction;

    // Bounds check
    if (newIndex < 0) {
        // Going up beyond oldest - restore original input
        input.value = lastInputValue;
        historyIndex = -1;
        return;
    } else if (newIndex >= inputHistory.length) {
        // Going down from beyond oldest - restore original input
        input.value = lastInputValue;
        historyIndex = -1;
        return;
    } else {
        historyIndex = newIndex;
    }

    // Apply selected history item
    input.value = inputHistory[historyIndex];
}

// Clear all input history
function clearHistory() {
    inputHistory = [];
    historyIndex = -1;
    try {
        localStorage.removeItem(INPUT_HISTORY_KEY);
    } catch (e) {
        console.error('Failed to clear input history:', e);
    }
}

// Track tool calls for streaming updates
// index -> { name, argsElement, argsText, complete }
let toolCalls = {};

// Track pending approval requests
let pendingApprovals = {};
let currentApprovalId = null;

// ========== File Upload Functions ==========

// Convert Blob to base64 Data URL
function blobToBase64(blob) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onloadend = () => resolve(reader.result);
        reader.onerror = reject;
        reader.readAsDataURL(blob);
    });
}

// Supported file types and their icons
const SUPPORTED_FILE_TYPES = {
    // Images
    'image/': { icon: '🖼️', category: 'image' },
    // Videos
    'video/': { icon: '🎬', category: 'video' },
    // Audios
    'audio/': { icon: '🎵', category: 'audio' },
    // Documents
    'application/pdf': { icon: '📄', category: 'document' },
    'text/plain': { icon: '📝', category: 'document' },
    'application/msword': { icon: '📘', category: 'document' },
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document': { icon: '📘', category: 'document' },
    'application/vnd.ms-excel': { icon: '📊', category: 'document' },
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': { icon: '📊', category: 'document' },
    'application/vnd.ms-powerpoint': { icon: '📽️', category: 'document' },
    'application/vnd.openxmlformats-officedocument.presentationml.presentation': { icon: '📽️', category: 'document' },
    'text/csv': { icon: '📊', category: 'document' }
};

// Maximum file size (50MB)
const MAX_FILE_SIZE = 50 * 1024 * 1024;

// Check if a file type is supported
function isFileTypeSupported(fileType, fileName) {
    // Check by MIME type
    for (const typePrefix in SUPPORTED_FILE_TYPES) {
        if (fileType.startsWith(typePrefix)) {
            return true;
        }
    }
    // Check by file extension for office documents
    const ext = fileName.toLowerCase().split('.').pop();
    const supportedExtensions = ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'csv', 'txt'];
    return supportedExtensions.includes(ext);
}

// Get file icon based on type
function getFileIcon(fileType, fileName) {
    // Check by MIME type
    for (const typePrefix in SUPPORTED_FILE_TYPES) {
        if (fileType.startsWith(typePrefix)) {
            return SUPPORTED_FILE_TYPES[typePrefix].icon;
        }
    }
    // Default icon
    return '📎';
}

// Handle file selection
async function handleFiles(files) {
    if (!files || files.length === 0) return;

    // Show loading indicator if compressing images
    let compressionStarted = false;

    for (const file of Array.from(files)) {
        // Validate file type
        if (!isFileTypeSupported(file.type, file.name)) {
            showToast('Unsupported file type: ' + file.name, true);
            continue;
        }

        // Validate file size (max 50MB)
        if (file.size > MAX_FILE_SIZE) {
            showToast('File size must be less than 50MB: ' + file.name, true);
            continue;
        }

        // Compress images before adding to pending files
        if (file.type.startsWith('image/')) {
            if (!compressionStarted) {
                setStatus('Compressing images...', false);
                compressionStarted = true;
            }

            try {
                // Check if compression is needed
                const needsCompression = ImageCompressor.needsCompression(file, 500); // 500KB threshold

                if (needsCompression) {
                    // Compress the image
                    const result = await ImageCompressor.compress(file, {
                        maxWidth: 1920,
                        maxHeight: 1080,
                        quality: 0.85,
                        maxSizeKB: 500,
                        minQuality: 0.6,
                        mimeType: 'image/jpeg'
                    });

                    console.log(`Image compressed: ${file.name}, ${result.ratio}% reduction (${formatFileSize(result.originalSize)} -> ${formatFileSize(result.compressedSize)})`);

                    // Convert Blob to base64 for transmission
                    const base64Url = await blobToBase64(result.blob);

                    // Add compressed image to pending files
                    pendingFiles.push({
                        url: base64Url,
                        name: file.name.replace(/\.(png|gif|webp|bmp)$/i, '.jpg'),
                        type: result.mimeType,
                        size: result.compressedSize,
                        isImage: true,
                        originalSize: result.originalSize,
                        compressed: true
                    });

                    // Render previews immediately after adding compressed file
                    renderFilePreviews();
                } else {
                    // Image is already small enough, no compression needed
                    const reader = new FileReader();
                    reader.onload = (e) => {
                        pendingFiles.push({
                            url: e.target.result,
                            name: file.name,
                            type: file.type,
                            size: file.size,
                            isImage: true,
                            compressed: false
                        });
                        renderFilePreviews();
                    };
                    reader.onerror = () => {
                        showToast('Failed to read file: ' + file.name, true);
                    };
                    reader.readAsDataURL(file);
                    continue;
                }
            } catch (error) {
                console.error('Failed to compress image:', file.name, error);
                showToast('Failed to compress image: ' + file.name, true);
                // Fallback to original file
                const reader = new FileReader();
                reader.onload = (e) => {
                    pendingFiles.push({
                        url: e.target.result,
                        name: file.name,
                        type: file.type,
                        size: file.size,
                        isImage: true,
                        compressed: false,
                        compressionError: true
                    });
                    renderFilePreviews();
                };
                reader.readAsDataURL(file);
                continue;
            }
        } else {
            // Non-image file, add directly
            const reader = new FileReader();
            reader.onload = (e) => {
                pendingFiles.push({
                    url: e.target.result,
                    name: file.name,
                    type: file.type,
                    size: file.size,
                    isImage: false
                });
                renderFilePreviews();
            };
            reader.onerror = () => {
                showToast('Failed to read file: ' + file.name, true);
            };
            reader.readAsDataURL(file);
        }
    }

    // Render previews after processing all files
    if (compressionStarted) {
        renderFilePreviews();
        closeStatus(); // Close compression status
    }

    // Clear the input so the same file can be selected again
    document.getElementById('file-input').value = '';
}

// Render file previews
function renderFilePreviews() {
    const container = document.getElementById('image-preview-container');

    if (pendingFiles.length === 0) {
        container.style.display = 'none';
        container.innerHTML = '';
        return;
    }

    container.style.display = 'flex';
    container.innerHTML = pendingFiles.map((file, idx) => {
        if (file.isImage) {
            return `
                <div class="image-preview-item">
                    <img src="${file.url}" alt="${file.name}" />
                    <button onclick="removeFile(${idx})" title="Remove file">×</button>
                </div>
            `;
        } else {
            const icon = getFileIcon(file.type, file.name);
            const sizeStr = formatFileSize(file.size);
            return `
                <div class="image-preview-item file-preview-item">
                    <div class="file-preview-content">
                        <span class="file-icon">${icon}</span>
                        <span class="file-name" title="${file.name}">${file.name}</span>
                        <span class="file-size">${sizeStr}</span>
                    </div>
                    <button onclick="removeFile(${idx})" title="Remove file">×</button>
                </div>
            `;
        }
    }).join('');
}

// Format file size for display
function formatFileSize(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

// Remove a file from pending list
function removeFile(index) {
    if (index >= 0 && index < pendingFiles.length) {
        pendingFiles.splice(index, 1);
        renderFilePreviews();
    }
}

// Clear all pending files
function clearPendingFiles() {
    pendingFiles = [];
    renderFilePreviews();
}

// Message history configuration
const MAX_HISTORY_MESSAGES = 200;
const HISTORY_KEY_PREFIX = 'chat_history_';

// ========== Message History Storage (IndexedDB) ==========

// Get storage key for a specific chat
function getHistoryKey(chatName) {
    return HISTORY_KEY_PREFIX + chatName;
}

// Save message to IndexedDB (for current chat)
async function saveMessageToStorage(message, type, toolData = null, thinkingContent = null, files = null) {
    if (!currentChat) return;

    // Build message object
    const messageObj = {
        type: type,
        content: message,
        timestamp: Date.now()
    };

    // Include thinking content if present
    if (thinkingContent) {
        messageObj.thinking = thinkingContent;
    }

    // Include tool call data if present
    if (toolData) {
        messageObj.toolData = toolData;
    }

    // Include files if present
    if (files && files.length > 0) {
        messageObj.files = files;
    }

    // Save to IndexedDB
    try {
        if (window.ChatDB && window.ChatDB.isSupported()) {
            await window.ChatDB.saveMessage(currentChat, messageObj);

            // Also maintain a lightweight index in localStorage for quick access
            // This stores just metadata, not the actual files
            await updateHistoryIndex(currentChat, messageObj);
        } else {
            // Fallback to localStorage if IndexedDB is not available
            saveMessageToLocalStorageFallback(currentChat, messageObj);
        }
    } catch (e) {
        console.error('Failed to save message to IndexedDB, trying fallback:', e);
        // Fallback to localStorage on error
        saveMessageToLocalStorageFallback(currentChat, messageObj);
    }
}

// Update lightweight index in localStorage
async function updateHistoryIndex(chatName, messageObj) {
    const key = getHistoryKey(chatName);
    let index = [];

    try {
        const stored = localStorage.getItem(key);
        if (stored) {
            index = JSON.parse(stored);
            if (!Array.isArray(index)) {
                index = [];
            }
        }
    } catch (e) {
        console.error('Failed to load history index:', e);
        index = [];
    }

    // Add lightweight entry (without files data)
    const indexEntry = {
        type: messageObj.type,
        content: messageObj.content,
        timestamp: messageObj.timestamp,
        hasFiles: messageObj.files && messageObj.files.length > 0,
        hasThinking: !!messageObj.thinking,
        hasToolData: !!messageObj.toolData
    };

    index.push(indexEntry);

    // Trim to max size
    if (index.length > MAX_HISTORY_MESSAGES) {
        index = index.slice(-MAX_HISTORY_MESSAGES);
    }

    try {
        localStorage.setItem(key, JSON.stringify(index));
    } catch (e) {
        console.error('Failed to save history index:', e);
    }
}

// Fallback to localStorage (without files to avoid quota issues)
function saveMessageToLocalStorageFallback(chatName, messageObj) {
    const key = getHistoryKey(chatName);
    let history = [];

    try {
        const stored = localStorage.getItem(key);
        if (stored) {
            history = JSON.parse(stored);
            if (!Array.isArray(history)) {
                history = [];
            }
        }
    } catch (e) {
        console.error('Failed to load message history:', e);
        history = [];
    }

    // Create a copy without files for localStorage fallback
    const messageObjNoFiles = { ...messageObj };
    if (messageObjNoFiles.files) {
        // Store only metadata, not the actual image data
        messageObjNoFiles.files = messageObj.files.map(img => ({
            name: img.name,
            type: img.type,
            size: img.size,
            isImage: img.isImage,
            // Mark as unavailable in fallback mode
            unavailable: true
        }));
    }

    history.push(messageObjNoFiles);

    // Trim to max size
    let count = 0;
    const typesToKeep = ['user', 'assistant', 'tool_call'];
    for (const msg of history) {
        if (typesToKeep.includes(msg.type)) {
            count++;
        }
    }

    if (count > MAX_HISTORY_MESSAGES) {
        let removed = 0;
        const typesToTrim = ['user', 'assistant', 'tool_call'];
        while (removed < count - MAX_HISTORY_MESSAGES) {
            const idx = history.findIndex(msg => typesToTrim.includes(msg.type));
            if (idx >= 0) {
                history.splice(idx, 1);
                removed++;
            } else {
                break;
            }
        }
    }

    try {
        localStorage.setItem(key, JSON.stringify(history));
    } catch (e) {
        console.error('Failed to save message history to localStorage:', e);
    }
}

// Load message history from IndexedDB (for current chat)
async function loadMessageHistoryFromStorage() {
    if (!currentChat) return [];

    try {
        // Try IndexedDB first
        if (window.ChatDB && window.ChatDB.isSupported()) {
            const messages = await window.ChatDB.loadMessages(currentChat);
            if (messages && messages.length > 0) {
                return messages;
            }
        }

        // Fallback to localStorage
        return loadMessageHistoryFromLocalStorageFallback();
    } catch (e) {
        console.error('Failed to load message history from IndexedDB:', e);
        return loadMessageHistoryFromLocalStorageFallback();
    }
}

// Fallback to localStorage
function loadMessageHistoryFromLocalStorageFallback() {
    if (!currentChat) return [];

    const key = getHistoryKey(currentChat);
    try {
        const stored = localStorage.getItem(key);
        if (stored) {
            const history = JSON.parse(stored);
            if (Array.isArray(history)) {
                return history;
            }
        }
    } catch (e) {
        console.error('Failed to load message history from localStorage:', e);
    }
    return [];
}

// Clear message history for current chat
async function clearMessageHistory() {
    if (!currentChat) return;

    const key = getHistoryKey(currentChat);

    try {
        // Clear from IndexedDB
        if (window.ChatDB && window.ChatDB.isSupported()) {
            await window.ChatDB.deleteMessages(currentChat);
        }
    } catch (e) {
        console.error('Failed to clear message history from IndexedDB:', e);
    }

    // Always clear from localStorage (index and fallback data)
    try {
        localStorage.removeItem(key);
    } catch (e) {
        console.error('Failed to clear message history from localStorage:', e);
    }
}

// Clear all chat histories
async function clearAllChatHistories() {
    // Clear from IndexedDB
    try {
        if (window.ChatDB && window.ChatDB.isSupported()) {
            await window.ChatDB.deleteAll();
        }
    } catch (e) {
        console.error('Failed to clear all histories from IndexedDB:', e);
    }

    // Clear from localStorage
    const prefix = HISTORY_KEY_PREFIX;
    const keysToDelete = [];

    for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key && key.startsWith(prefix)) {
            keysToDelete.push(key);
        }
    }

    keysToDelete.forEach(key => {
        try {
            localStorage.removeItem(key);
        } catch (e) {
            console.error('Failed to clear history:', e);
        }
    });
}

async function init() {
    // Load input history from localStorage
    loadHistory();

    // Load webui config from server
    try {
        const configResponse = await fetch('/config');
        const configData = await configResponse.json();
        const title = configData.webui?.title || 'Chat-Agent';
        document.title = title;
        document.getElementById('login-header').textContent = '🤖 ' + title;
        document.getElementById('agent-header').textContent = '🤖 ' + title;
    } catch (e) {
        console.error('Failed to load webui config:', e);
    }

    try {
        const response = await fetch('/chats');
        const data = await response.json();
        const select = document.getElementById('chat-select');
        let defaultSelected = false;

        for (const chat of data.chats) {
            const option = document.createElement('option');
            option.value = chat;
            option.textContent = chat;
            select.appendChild(option);

            // Auto-select default chat if marked as default
            if (data.default_chat && chat === data.default_chat && !defaultSelected) {
                option.selected = true;
                defaultSelected = true;
            }
        }

        // If only one chat exists, auto-select and start chat immediately
        if (data.chats.length === 1) {
            select.value = data.chats[0];
            startChat();
        }
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
    document.getElementById('login-header').textContent = chatName;
    document.getElementById('login-panel').style.display = 'none';
    document.getElementById('chat-panel').style.display = 'flex';
    document.getElementById('agent-header').textContent = chatName;

    // Load message history from storage (IndexedDB or localStorage)
    await loadMessageHistory();

    connectWebSocket();
}

// Load and display message history from storage
async function loadMessageHistory() {
    const history = await loadMessageHistoryFromStorage();

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
            </div>
            <div class="thinking-content markdown-body"></div>
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
        addCopyButtonsToCodeBlocks(div);
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

    // Add footer for assistant messages only
    if (type === 'assistant') {
        const footer = document.createElement('div');
        footer.className = 'message-footer';
        footer.innerHTML = `
            <button class="copy-btn" onclick="copyMessage(this)" title="Copy message">
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

    // Add copy buttons to code blocks
    addCopyButtonsToCodeBlocks(div);
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
    scrollToBottom();
}

function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    let wsUrl = protocol + '//' + window.location.host + '/ws';

    ws = new WebSocket(wsUrl);

    ws.onopen = function () {
        console.log('WebSocket connected');
        reconnectAttempts = 0;
        closeStatus(); // Close any existing status when connected
        // 自动选择当前 chat
        if (currentChat) {
            ws.send(JSON.stringify({ type: 'select_chat', payload: { chat_name: currentChat } }));
        }
    };

    ws.onmessage = function (event) {
        try {
            const msg = JSON.parse(event.data);
            handleMessage(msg);
        } catch (e) {
            console.error('Failed to parse message:', e);
        }
    };

    ws.onclose = function () {
        console.log('WebSocket disconnected');
        if (reconnectAttempts < maxReconnectAttempts) {
            setStatus('Connection lost. Reconnecting in 3 seconds...', true);
            reconnectAttempts++;
            setTimeout(connectWebSocket, 3000);
        } else {
            setStatus('Unable to reconnect. Please refresh the page.', true);
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
                if (input) input.disabled = false;
                if (input) input.focus();
                isGenerating = false;
                updateSendButton();
            }
            //setStatus('Response completed', false);
            break;
        case 'error':
            setStatus(msg.payload.error, true);
            // 只有在生成中才重置状态
            if (isGenerating) {
                const inputErr = document.getElementById('message-input');
                if (inputErr) inputErr.disabled = false;
                isGenerating = false;
                updateSendButton();
                if (inputErr) inputErr.focus();
            }
            break;
        case 'stopped':
            // 只有在生成中才重置状态
            if (isGenerating) {
                isGenerating = false;
                updateSendButton();
                const inputStopped = document.getElementById('message-input');
                if (inputStopped) inputStopped.disabled = false;
                if (inputStopped) inputStopped.focus();
            }
            break;
        case 'cleared':
            setStatus(msg.payload.message, false);
            break;
        case 'approval_request':
            handleApprovalRequest(msg.payload);
            break;
        case 'thinking':
            break
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
    if ((!message || message.trim() === '') && pendingFiles.length === 0) {
        return;
    }

    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Save to input history (only if there's text)
    if (message && message.trim()) {
        saveToHistory(message);
    }

    // Prepare message payload with optional files
    const payload = {
        message: message || ''
    };

    // Include files if any
    if (pendingFiles.length > 0) {
        payload.files = pendingFiles.map(file => ({
            url: file.url,
            type: file.type,
            name: file.name,
            file_size: file.size
        }));
    }

    // Display user message with files
    displayUserMessageWithFiles(message, pendingFiles);

    // Save to local storage history (include files)
    const filesToSave = pendingFiles.length > 0 ? pendingFiles.map(file => ({
        url: file.url,
        type: file.type,
        name: file.name,
        size: file.size,
        isImage: file.isImage || (file.type && file.type.startsWith('image/')),
        compressed: file.compressed || false,
        originalSize: file.originalSize || file.size
    })) : null;
    saveMessageToStorage(message, 'user', null, null, filesToSave);

    // Clear input and files
    input.value = '';
    clearPendingFiles();

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
                const icon = getFileIcon(file.type, file.name);
                const sizeStr = formatFileSize(file.size);
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
    document.getElementById('messages').appendChild(div);
    scrollToBottom();
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

    // 添加消息页脚复制按钮（仅 assistant 消息）
    if (type === 'assistant') {
        const footer = document.createElement('div');
        footer.className = 'message-footer';
        footer.innerHTML = `
            <button class="copy-btn" onclick="copyMessage(this)" title="Copy message">
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

    scrollToBottom();
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

        scrollToBottom();
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
            saveMessageToStorage(null, 'tool_call', {
                name: name,
                arguments: args || toolCall.argsText
            });

            // Remove from tracking
            delete toolCalls[index];
        }

        scrollToBottom();
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
        }

        // 保存完整消息到本地存储（包含思考内容和回答内容）
        const fullContent = (currentAssistantMessage || '').trim();
        const thinkingContent = (currentThinkingChunk || '').trim();
        if (fullContent || thinkingContent) {
            saveMessageToStorage(fullContent, 'assistant', null, thinkingContent);
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

        scrollToBottom();
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
                </div>
                <div class="thinking-content markdown-body"></div>
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
            }
            scrollToBottom();
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
            }
            scrollToBottom();
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
            scrollToBottom();
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
            scrollToBottom();
        }
    }
}

function scrollToBottom() {
    const messages = document.getElementById('messages');
    requestAnimationFrame(() => {
        messages.scrollTop = messages.scrollHeight;
    });
}

// 复制整个消息内容
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
        await clearMessageHistory();
    }
    // 不勾选时：只发送 clear 消息，不清空展示，不删记录

    hideClearModal();
}

// autoResize function removed - input box height is now fixed via CSS

function handleKeyDown(e) {
    const input = document.getElementById('message-input');
    if (!input) return;

    // Enter without shift = send
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
        return;
    }

    // Arrow Up = navigate to older history (only when input is empty or at cursor start)
    if (e.key === 'ArrowUp') {
        // Only navigate history when:
        // 1. Input is empty, OR
        // 2. Cursor is at the start and user is not selecting text
        const isSelection = window.getSelection().toString().length > 0;
        const hasContent = input.value.length > 0;
        const atStart = input.selectionStart === 0;

        if ((!hasContent || (atStart && !isSelection)) && !e.shiftKey) {
            e.preventDefault();
            navigateHistory(-1);  // -1 = show older entries (decreasing index)
        }
        return;
    }

    // Arrow Down = navigate to newer history
    if (e.key === 'ArrowDown') {
        // Only navigate history when:
        // 1. Input is empty, OR
        // 2. Cursor is at the end and user is not selecting text
        const isSelection = window.getSelection().toString().length > 0;
        const hasContent = input.value.length > 0;
        const atEnd = input.selectionEnd === input.value.length;

        if ((!hasContent || (atEnd && !isSelection)) && !e.shiftKey) {
            e.preventDefault();
            navigateHistory(1);  // 1 = show newer entries (increasing index)
        }
        return;
    }

    // Escape = cancel history navigation, restore last input
    if (e.key === 'Escape') {
        if (historyIndex !== -1) {
            input.value = lastInputValue;
            historyIndex = -1;
        }
        return;
    }

    // When user starts typing, reset history navigation
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) {
        if (historyIndex !== -1) {
            historyIndex = -1;
        }
    }
}

// Mobile keyboard adaptation
let lastViewportHeight = window.innerHeight;

function handleViewportChange() {
    const viewport = window.visualViewport;
    if (!viewport) return;

    const viewportHeight = viewport.height || window.innerHeight;
    const chatPanel = document.getElementById('chat-panel');

    if (chatPanel) {
        chatPanel.style.height = viewportHeight + 'px';
    }

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
                // Also scroll messages to bottom
                scrollToBottom();
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

// Focus input when keyboard is shown on mobile
const messageInput = document.getElementById('message-input');
if (messageInput) {
    messageInput.addEventListener('focus', function () {
        // Small delay to allow keyboard to start appearing
        setTimeout(scrollToBottom, 300);
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
                    handleFiles([file]);
                }
            });

            showToast(`Pasted ${imageItems.length} image(s) from clipboard`, false);
        }
    });
}

// ========== Image Preview Functions ==========

// Show image preview modal from history message
async function showImagePreviewFromHistory(msgIndex, imgIndex) {
    if (msgIndex < 0 || !currentChat) return;

    const history = await loadMessageHistoryFromStorage();
    const msg = history[msgIndex];

    if (!msg || !msg.files || msg.files.length === 0) return;

    // Check if files are unavailable (fallback mode)
    if (msg.files.some(img => img.unavailable)) {
        showToast('Images are not available in this session', true);
        return;
    }

    // Collect all files from this message
    const allImages = msg.files.filter(img => img.isImage || (img.type && img.type.startsWith('image/')));
    currentPreviewImages = allImages.map(img => img.url);

    if (currentPreviewImages.length === 0) return;

    // Find the actual index in the filtered files array
    const originalImg = msg.files[imgIndex];
    currentPreviewIndex = allImages.findIndex(img => img.url === originalImg.url);
    if (currentPreviewIndex < 0) currentPreviewIndex = 0;

    const modal = document.getElementById('image-preview-modal');
    const imgElement = document.getElementById('image-preview-full');
    const counterElement = document.getElementById('image-preview-counter');

    // Set the image source
    imgElement.src = currentPreviewImages[currentPreviewIndex];

    // Update counter
    if (currentPreviewImages.length > 1) {
        counterElement.textContent = `${currentPreviewIndex + 1} / ${currentPreviewImages.length}`;
        counterElement.style.display = 'block';
    } else {
        counterElement.textContent = '';
        counterElement.style.display = 'none';
    }

    // Show modal
    modal.style.display = 'flex';
    document.body.style.overflow = 'hidden'; // Prevent background scrolling
}

// Show image preview modal for newly sent messages
function showImagePreviewWithIndex(filesJson, index) {
    const modal = document.getElementById('image-preview-modal');
    const imgElement = document.getElementById('image-preview-full');
    const counterElement = document.getElementById('image-preview-counter');

    // Parse the files from JSON
    let files;
    try {
        files = JSON.parse(decodeURIComponent(filesJson));
    } catch (e) {
        console.error('Failed to parse files:', e);
        return;
    }

    // Collect all image URLs from the files array
    currentPreviewImages = files
        .filter(file => file.isImage || (file.type && file.type.startsWith('image/')))
        .map(file => file.url);

    if (currentPreviewImages.length === 0) return;

    currentPreviewIndex = index;

    // Set the image source
    imgElement.src = currentPreviewImages[currentPreviewIndex];

    // Update counter
    if (currentPreviewImages.length > 1) {
        counterElement.textContent = `${currentPreviewIndex + 1} / ${currentPreviewImages.length}`;
        counterElement.style.display = 'block';
    } else {
        counterElement.textContent = '';
        counterElement.style.display = 'none';
    }

    // Show modal
    modal.style.display = 'flex';
    document.body.style.overflow = 'hidden'; // Prevent background scrolling
}

// Hide image preview modal
function hideImagePreview() {
    const modal = document.getElementById('image-preview-modal');
    modal.style.display = 'none';
    document.body.style.overflow = ''; // Restore scrolling

    // Clear image source after a delay to avoid flickering
    setTimeout(() => {
        document.getElementById('image-preview-full').src = '';
    }, 200);
}

// Navigate through preview files
function navigatePreview(direction) {
    const newIndex = currentPreviewIndex + direction;

    // Bounds check
    if (newIndex < 0 || newIndex >= currentPreviewImages.length) {
        return;
    }

    currentPreviewIndex = newIndex;
    const imgElement = document.getElementById('image-preview-full');
    const counterElement = document.getElementById('image-preview-counter');

    // Animate image transition
    imgElement.style.opacity = '0';
    imgElement.style.transform = 'scale(0.95)';

    setTimeout(() => {
        imgElement.src = currentPreviewImages[currentPreviewIndex];
        imgElement.style.opacity = '1';
        imgElement.style.transform = 'scale(1)';

        // Update counter
        if (currentPreviewImages.length > 1) {
            counterElement.textContent = `${currentPreviewIndex + 1} / ${currentPreviewImages.length}`;
        }
    }, 150);
}

// Add keyboard navigation for image preview
document.addEventListener('keydown', function (e) {
    const modal = document.getElementById('image-preview-modal');
    if (modal.style.display === 'flex') {
        if (e.key === 'Escape') {
            hideImagePreview();
        } else if (e.key === 'ArrowLeft') {
            navigatePreview(-1);
        } else if (e.key === 'ArrowRight') {
            navigatePreview(1);
        }
    }
});

init();