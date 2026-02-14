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
    autoResize(input);
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
    } catch (e) {
        console.error('Failed to load chats:', e);
    }
}

function startChat() {
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
    connectWebSocket();
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
            displayChunk(msg.payload.content, msg.payload.first, msg.payload.last);
            break;
        case 'tool_call':
            displayToolCall(
                msg.payload.name,
                msg.payload.arguments,
                msg.payload.index,
                msg.payload.streaming
            );
            break;
        case 'thinking':
            const thinkingStatus = msg.payload.status;
            const thinkingEl = document.getElementById('thinking');
            if (thinkingStatus) {
                // Thinking started - insert if not already present
                if (!thinkingEl) {
                    document.getElementById('messages').insertAdjacentHTML('beforeend', '<div id="thinking" class="message thinking">Thinking...</div>');
                    scrollToBottom();
                }
            } else {
                // Thinking ended - remove the element
                if (thinkingEl) {
                    thinkingEl.remove();
                }
            }
            break;
        case 'complete':
            const thinking = document.getElementById('thinking');
            if (thinking) thinking.remove();
            // 重新启用输入框
            const input = document.getElementById('message-input');
            if (input) input.disabled = false;
            if (input) input.focus();
            isGenerating = false;
            updateSendButton();
            //setStatus('Response completed', false);
            break;
        case 'error':
            setStatus(msg.payload.error, true);
            // 重新启用输入框
            const inputErr = document.getElementById('message-input');
            if (inputErr) inputErr.disabled = false;
            isGenerating = false;
            updateSendButton();
            if (inputErr) inputErr.focus();
            break;
        case 'stopped':
            // 流已停止
            isGenerating = false;
            updateSendButton();
            const inputStopped = document.getElementById('message-input');
            if (inputStopped) inputStopped.disabled = false;
            if (inputStopped) inputStopped.focus();
            // 移除 thinking 指示器
            const thinkingStopped = document.getElementById('thinking');
            if (thinkingStopped) thinkingStopped.remove();
            break;
        case 'cleared':
            setStatus(msg.payload.message, false);
            break;
        case 'approval_request':
            handleApprovalRequest(msg.payload);
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
    if (!message || !ws || ws.readyState !== WebSocket.OPEN) return;

    // Save to input history
    saveToHistory(message);

    addMessage(message, 'user');
    input.value = '';
    autoResize(input);

    // 禁用输入框，发送按钮保持可用（用于停止）
    input.disabled = true;
    isGenerating = true;
    updateSendButton();

    // 直接发送 message，后端已缓存 chat session
    ws.send(JSON.stringify({
        type: 'chat',
        payload: { message: message }
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

function addMessage(text, type) {
    const div = document.createElement('div');
    div.className = 'message ' + type;

    const contentDiv = document.createElement('div');
    contentDiv.className = 'message-content';

    if (type === 'assistant') {
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
    // Clean up any existing thinking element
    const thinking = document.getElementById('thinking');
    if (thinking) thinking.remove();

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

        // Update args (accumulate streaming updates)
        // Always update argsText and display when we receive args
        if (args !== undefined && args !== null && streaming === true) {
            toolCall.argsText = args;
            if (toolCall.argsElement) {
                toolCall.argsElement.textContent = args;
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
function displayChunk(content, isFirst, isLast) {
    // 创建新的响应元素
    if (isFirst) {
        currentChunk = content;
        const div = document.createElement('div');
        div.className = 'message assistant';
        div.id = 'current-response';
        div.innerHTML = '<div class="message-content markdown-body"></div>';
        document.getElementById('messages').appendChild(div);
        chunkElement = div.querySelector('.message-content');
        if (chunkElement) {
            try {
                chunkElement.innerHTML = marked.parse(currentChunk);
            } catch (e) {
                chunkElement.textContent = currentChunk;
            }
        }
        scrollToBottom();
        if (isLast) {
            chunkElement = null;
        }
        return;
    }

    // 追加内容到现有响应
    currentChunk += content;
    if (chunkElement) {
        try {
            chunkElement.innerHTML = marked.parse(currentChunk);
        } catch (e) {
            chunkElement.textContent = currentChunk;
        }
    }

    // 滚动到底部（如果有实际内容）
    if (content || !isLast) {
        scrollToBottom();
    }

    // 标记最后一个块完成
    if (isLast) {
        chunkElement = null;
        // 为流式响应完成后的消息添加代码块复制按钮
        const responseDiv = document.getElementById('current-response');
        if (responseDiv) {
            // 添加页脚复制按钮
            if (!responseDiv.querySelector('.message-footer')) {
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
                responseDiv.appendChild(footer);
            }

            // 为代码块添加复制按钮
            addCopyButtonsToCodeBlocks(responseDiv);
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
    const textToCopy = contentDiv.innerText;

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
        copyBtn.onclick = function(e) {
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

    // Auto close after 1 second (1000ms)
    toastTimeout = setTimeout(() => {
        closeStatus();
    }, 1000);
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
    document.getElementById('clear-modal').style.display = 'flex';
}

function hideClearModal() {
    document.getElementById('clear-modal').style.display = 'none';
}

function confirmClear() {
    if (ws && ws.readyState === WebSocket.OPEN) {
        // 清除当前 chat session 的对话记录
        ws.send(JSON.stringify({ type: 'clear', payload: {} }));
    }
    hideClearModal();
}

function autoResize(textarea) {
    textarea.style.height = 'auto';
    textarea.style.height = Math.min(textarea.scrollHeight, 120) + 'px';
}

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
            autoResize(input);
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
}

init();