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

    // Save message to IndexedDB (for current chat)
    function saveMessageToStorage(message, type, toolData, thinkingContent, files) {
        if (!currentChat) {
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
                return global.ChatDB.saveMessage(currentChat, messageObj)
                    .then(function() {
                        // Also maintain a lightweight index in localStorage for quick access
                        return updateHistoryIndex(currentChat, messageObj);
                    });
            } else {
                // Fallback to localStorage if IndexedDB is not available
                saveMessageToLocalStorageFallback(currentChat, messageObj);
                return Promise.resolve();
            }
        }).catch(function(e) {
            console.error('Failed to save message to IndexedDB, trying fallback:', e);
            // Fallback to localStorage on error
            saveMessageToLocalStorageFallback(currentChat, messageObj);
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

    // Load message history from IndexedDB (for current chat)
    function loadMessageHistoryFromStorage() {
        if (!currentChat) {
            return Promise.resolve([]);
        }

        return Promise.resolve().then(function() {
            // Try IndexedDB first
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.loadMessages(currentChat)
                    .then(function(messages) {
                        if (messages && messages.length > 0) {
                            return messages;
                        }
                        // Fallback to localStorage
                        return loadMessageHistoryFromLocalStorageFallback();
                    });
            }

            // Fallback to localStorage
            return loadMessageHistoryFromLocalStorageFallback();
        }).catch(function(e) {
            console.error('Failed to load message history from IndexedDB:', e);
            return loadMessageHistoryFromLocalStorageFallback();
        });
    }

    // Fallback to localStorage
    function loadMessageHistoryFromLocalStorageFallback() {
        if (!currentChat) {
            return [];
        }

        var key = getHistoryKey(currentChat);
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

    // Clear message history for current chat
    function clearMessageHistory() {
        if (!currentChat) {
            return Promise.resolve();
        }

        var key = getHistoryKey(currentChat);

        return Promise.resolve().then(function() {
            // Clear from IndexedDB
            if (global.ChatDB && global.ChatDB.isSupported()) {
                return global.ChatDB.deleteMessages(currentChat)
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
        MAX_HISTORY_MESSAGES: MAX_HISTORY_MESSAGES,
        HISTORY_KEY_PREFIX: HISTORY_KEY_PREFIX
    };

})(typeof window !== 'undefined' ? window : this);
