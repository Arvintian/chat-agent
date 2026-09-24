// Message History Storage Module (ES5 compatible)
// Handles saving and loading chat message history using IndexedDB with localStorage fallback

(function(global) {
    'use strict';

    // Message history configuration
    var MAX_HISTORY_MESSAGES = 200;
    var HISTORY_KEY_PREFIX = 'chat_history_';

    // Current chat reference (set externally)
    var currentChat = null;

    // Get storage key for a specific chat
    function getHistoryKey(chatName) {
        return HISTORY_KEY_PREFIX + chatName;
    }

    // Set current chat context
    function setCurrentChat(chatName) {
        currentChat = chatName;
    }

    // Get current chat context
    function getCurrentChat() {
        return currentChat;
    }

    // Save message to IndexedDB (for current chat).
    // NOTE: the chat name is captured SYNCHRONOUSLY at entry — every
    // persistence step below runs in a microtask (.then), by which time
    // currentChat may have been switched to another chat (background saves
    // temporarily swap it). Capturing up front keeps the write on the right
    // chat's history.
    function saveMessageToStorage(message, type, toolData, thinkingContent, files) {
        var chatName = currentChat;
        if (!chatName) {
            return Promise.resolve();
        }

        // Build message object
        var messageObj = {
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
        return Promise.resolve().then(function() {
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.saveMessage(chatName, messageObj)
                    .then(function() {
                        // Also maintain a lightweight index in localStorage for quick access
                        return updateHistoryIndex(chatName, messageObj);
                    });
            } else {
                // Fallback to localStorage if IndexedDB is not available
                saveMessageToLocalStorageFallback(chatName, messageObj);
                return Promise.resolve();
            }
        }).catch(function(e) {
            console.error('Failed to save message to IndexedDB, trying fallback:', e);
            // Fallback to localStorage on error
            saveMessageToLocalStorageFallback(chatName, messageObj);
            return Promise.resolve();
        });
    }

    // Update lightweight index in localStorage
    function updateHistoryIndex(chatName, messageObj) {
        var key = getHistoryKey(chatName);
        var index = [];

        try {
            var stored = localStorage.getItem(key);
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
        var indexEntry = {
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

        return Promise.resolve();
    }

    // Fallback to localStorage (without files to avoid quota issues)
    function saveMessageToLocalStorageFallback(chatName, messageObj) {
        var key = getHistoryKey(chatName);
        var history = [];

        try {
            var stored = localStorage.getItem(key);
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
        var messageObjNoFiles = {};
        for (var prop in messageObj) {
            if (messageObj.hasOwnProperty(prop)) {
                messageObjNoFiles[prop] = messageObj[prop];
            }
        }

        if (messageObjNoFiles.files) {
            // Store only metadata, not the actual image data
            messageObjNoFiles.files = messageObj.files.map(function(img) {
                return {
                    name: img.name,
                    type: img.type,
                    size: img.size,
                    isImage: img.isImage,
                    // Mark as unavailable in fallback mode
                    unavailable: true
                };
            });
        }

        history.push(messageObjNoFiles);

        // Trim to max size
        var count = 0;
        var typesToKeep = ['user', 'assistant', 'tool_call'];
        for (var i = 0; i < history.length; i++) {
            if (typesToKeep.indexOf(history[i].type) !== -1) {
                count++;
            }
        }

        if (count > MAX_HISTORY_MESSAGES) {
            var removed = 0;
            var typesToTrim = ['user', 'assistant', 'tool_call'];
            while (removed < count - MAX_HISTORY_MESSAGES) {
                var idx = -1;
                for (var j = 0; j < history.length; j++) {
                    if (typesToTrim.indexOf(history[j].type) !== -1) {
                        idx = j;
                        break;
                    }
                }
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

    // Load message history from IndexedDB (for current chat).
    // The chat name is captured synchronously at entry (the IndexedDB read
    // runs in a microtask, by which time currentChat may have changed).
    function loadMessageHistoryFromStorage() {
        var chatName = currentChat;
        if (!chatName) {
            return Promise.resolve([]);
        }

        return Promise.resolve().then(function() {
            // Try IndexedDB first
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.loadMessages(chatName)
                    .then(function(messages) {
                        if (messages && messages.length > 0) {
                            return messages;
                        }
                        // Fallback to localStorage
                        return loadMessageHistoryFromLocalStorageFallback(chatName);
                    });
            }

            // Fallback to localStorage
            return loadMessageHistoryFromLocalStorageFallback(chatName);
        }).catch(function(e) {
            console.error('Failed to load message history from IndexedDB:', e);
            return loadMessageHistoryFromLocalStorageFallback(chatName);
        });
    }

    // Fallback to localStorage
    function loadMessageHistoryFromLocalStorageFallback(chatName) {
        if (!chatName) {
            return [];
        }

        var key = getHistoryKey(chatName);
        try {
            var stored = localStorage.getItem(key);
            if (stored) {
                var history = JSON.parse(stored);
                if (Array.isArray(history)) {
                    return history;
                }
            }
        } catch (e) {
            console.error('Failed to load message history from localStorage:', e);
        }
        return [];
    }

    // Remove all messages after the last user message (used for regenerate).
    // Chat name captured synchronously (the async steps must not observe a
    // switched currentChat).
    function removeMessagesAfterLastUser() {
        var chatName = currentChat;
        if (!chatName) {
            return Promise.resolve();
        }

        return Promise.resolve().then(function() {
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.loadMessages(chatName)
                    .then(function(messages) {
                        if (!messages || messages.length === 0) {
                            return;
                        }
                        
                        // Find the index of the last user message
                        var lastUserIndex = -1;
                        for (var i = messages.length - 1; i >= 0; i--) {
                            if (messages[i].type === 'user') {
                                lastUserIndex = i;
                                break;
                            }
                        }
                        
                        if (lastUserIndex === -1 || lastUserIndex === messages.length - 1) {
                            // No user message found or it's already the last message
                            return;
                        }
                        
                        // Delete all messages after the last user message one by one
                        var deleteCount = messages.length - 1 - lastUserIndex;
                        var deletePromises = [];
                        for (var d = 0; d < deleteCount; d++) {
                            deletePromises.push(global.ChatDB.deleteLastMessage(chatName));
                        }
                        return Promise.all(deletePromises);
                    })
                    .then(function() {
                        // Also update localStorage fallback
                        return removeMessagesAfterLastUserFromLocalStorage(chatName);
                    });
            } else {
                return removeMessagesAfterLastUserFromLocalStorage(chatName);
            }
        }).catch(function(e) {
            console.error('Failed to remove messages after last user:', e);
        });
    }

    // Remove messages after last user from localStorage fallback
    function removeMessagesAfterLastUserFromLocalStorage(chatName) {
        if (!chatName) {
            return;
        }
        
        var key = getHistoryKey(chatName);
        try {
            var stored = localStorage.getItem(key);
            if (stored) {
                var history = JSON.parse(stored);
                if (Array.isArray(history)) {
                    // Find the index of the last user message
                    var lastUserIndex = -1;
                    for (var i = history.length - 1; i >= 0; i--) {
                        if (history[i].type === 'user') {
                            lastUserIndex = i;
                            break;
                        }
                    }
                    
                    if (lastUserIndex !== -1 && lastUserIndex < history.length - 1) {
                        // Keep only messages up to and including the last user message
                        history = history.slice(0, lastUserIndex + 1);
                        localStorage.setItem(key, JSON.stringify(history));
                    }
                }
            }
        } catch (e) {
            console.error('Failed to update localStorage history:', e);
        }
    }

    // Clear message history for current chat (chat name captured up front)
    function clearMessageHistory() {
        var chatName = currentChat;
        if (!chatName) {
            return Promise.resolve();
        }

        var key = getHistoryKey(chatName);

        return Promise.resolve().then(function() {
            // Clear from IndexedDB
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.deleteMessages(chatName)
                    .catch(function(e) {
                        console.error('Failed to clear message history from IndexedDB:', e);
                    });
            }
        }).then(function() {
            // Always clear from localStorage (index and fallback data)
            try {
                localStorage.removeItem(key);
            } catch (e) {
                console.error('Failed to clear message history from localStorage:', e);
            }
        });
    }

    // Clear all chat histories
    function clearAllChatHistories() {
        return Promise.resolve().then(function() {
            // Clear from IndexedDB
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.deleteAll()
                    .catch(function(e) {
                        console.error('Failed to clear all histories from IndexedDB:', e);
                    });
            }
        }).then(function() {
            // Clear from localStorage
            var prefix = HISTORY_KEY_PREFIX;
            var keysToDelete = [];

            for (var i = 0; i < localStorage.length; i++) {
                var key = localStorage.key(i);
                if (key && key.startsWith(prefix)) {
                    keysToDelete.push(key);
                }
            }

            keysToDelete.forEach(function(key) {
                try {
                    localStorage.removeItem(key);
                } catch (e) {
                    console.error('Failed to clear history:', e);
                }
            });
        });
    }

    // Export public API
    global.MessageHistory = {
        getHistoryKey: getHistoryKey,
        setCurrentChat: setCurrentChat,
        getCurrentChat: getCurrentChat,
        saveMessage: saveMessageToStorage,
        loadHistory: loadMessageHistoryFromStorage,
        clearHistory: clearMessageHistory,
        clearAllHistories: clearAllChatHistories,
        removeMessagesAfterLastUser: removeMessagesAfterLastUser,
        MAX_HISTORY_MESSAGES: MAX_HISTORY_MESSAGES,
        HISTORY_KEY_PREFIX: HISTORY_KEY_PREFIX
    };

})(typeof window !== 'undefined' ? window : this);
