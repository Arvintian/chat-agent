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

// Track tool calls for streaming updates
// index -> { name, argsElement, argsText, complete }
let toolCalls = {};

async function init() {
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
            //setStatus('Response completed', false);
            break;
        case 'error':
            setStatus(msg.payload.error, true);
            break;
        case 'cleared':
            setStatus(msg.payload.message, false);
            break;
        default:
            console.log('Unknown message type:', msg.type);
    }
}

function sendMessage() {
    const input = document.getElementById('message-input');
    const message = input.value.trim();
    if (!message || !ws || ws.readyState !== WebSocket.OPEN) return;

    addMessage(message, 'user');
    input.value = '';
    autoResize(input);
    input.focus();

    // 直接发送 message，后端已缓存 chat session
    ws.send(JSON.stringify({
        type: 'chat',
        payload: { message: message }
    }));
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
    document.getElementById('messages').appendChild(div);
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
    }
}

function scrollToBottom() {
    const messages = document.getElementById('messages');
    requestAnimationFrame(() => {
        messages.scrollTop = messages.scrollHeight;
    });
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
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
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