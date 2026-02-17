// IndexedDB wrapper for chat message storage
// This module handles storing large data (files) that exceed localStorage limits

const DB_NAME = 'ChatAgentDB';
const DB_VERSION = 1;
const STORE_NAME = 'messages';

let db = null;
let dbInitPromise = null;

// Initialize IndexedDB
function initIndexedDB() {
    if (dbInitPromise) {
        return dbInitPromise;
    }

    dbInitPromise = new Promise((resolve, reject) => {
        const request = indexedDB.open(DB_NAME, DB_VERSION);

        request.onerror = () => {
            console.error('Failed to open IndexedDB:', request.error);
            reject(request.error);
        };

        request.onsuccess = () => {
            db = request.result;
            console.log('IndexedDB opened successfully');
            resolve(db);
        };

        request.onupgradeneeded = (event) => {
            const database = event.target.result;
            
            // Create object store if it doesn't exist
            if (!database.objectStoreNames.contains(STORE_NAME)) {
                const store = database.createObjectStore(STORE_NAME, { keyPath: 'id', autoIncrement: true });
                store.createIndex('chatName', 'chatName', { unique: false });
                store.createIndex('timestamp', 'timestamp', { unique: false });
                store.createIndex('messageType', 'type', { unique: false });
            }
        };
    });

    return dbInitPromise;
}

// Get storage key for a specific chat
function getHistoryKey(chatName) {
    return 'chat_history_' + chatName;
}

// Save message with files to IndexedDB
async function saveMessageWithFiles(chatName, messageObj) {
    try {
        await initIndexedDB();
        
        const transaction = db.transaction([STORE_NAME], 'readwrite');
        const store = transaction.objectStore(STORE_NAME);
        
        const record = {
            chatName: chatName,
            message: messageObj,
            timestamp: messageObj.timestamp || Date.now(),
            type: messageObj.type
        };
        
        return new Promise((resolve, reject) => {
            const request = store.add(record);
            
            request.onsuccess = () => {
                resolve(request.result);
            };
            
            request.onerror = () => {
                console.error('Failed to save message to IndexedDB:', request.error);
                reject(request.error);
            };
        });
    } catch (error) {
        console.error('Error saving message to IndexedDB:', error);
        throw error;
    }
}

// Load all messages for a chat from IndexedDB
async function loadMessagesForChat(chatName) {
    try {
        await initIndexedDB();
        
        const transaction = db.transaction([STORE_NAME], 'readonly');
        const store = transaction.objectStore(STORE_NAME);
        const index = store.index('chatName');
        
        return new Promise((resolve, reject) => {
            const request = index.getAll(chatName);
            
            request.onsuccess = () => {
                const records = request.result || [];
                // Sort by timestamp
                records.sort((a, b) => a.timestamp - b.timestamp);
                // Extract just the message objects
                const messages = records.map(r => r.message);
                resolve(messages);
            };
            
            request.onerror = () => {
                console.error('Failed to load messages from IndexedDB:', request.error);
                reject(request.error);
            };
        });
    } catch (error) {
        console.error('Error loading messages from IndexedDB:', error);
        return [];
    }
}

// Delete all messages for a chat from IndexedDB
async function deleteMessagesForChat(chatName) {
    try {
        await initIndexedDB();
        
        const transaction = db.transaction([STORE_NAME], 'readwrite');
        const store = transaction.objectStore(STORE_NAME);
        const index = store.index('chatName');
        
        return new Promise((resolve, reject) => {
            // Get all keys for this chat
            const getKeyRequest = index.getAllKeys(chatName);
            
            getKeyRequest.onsuccess = () => {
                const keys = getKeyRequest.result || [];
                
                if (keys.length === 0) {
                    resolve();
                    return;
                }
                
                // Delete each key
                let deleted = 0;
                keys.forEach(key => {
                    const deleteRequest = store.delete(key);
                    deleteRequest.onsuccess = () => {
                        deleted++;
                        if (deleted === keys.length) {
                            resolve();
                        }
                    };
                    deleteRequest.onerror = () => {
                        console.error('Failed to delete message:', deleteRequest.error);
                    };
                });
            };
            
            getKeyRequest.onerror = () => {
                console.error('Failed to get keys for deletion:', getKeyRequest.error);
                reject(getKeyRequest.error);
            };
        });
    } catch (error) {
        console.error('Error deleting messages from IndexedDB:', error);
        throw error;
    }
}

// Delete all messages from IndexedDB
async function deleteAllMessages() {
    try {
        await initIndexedDB();
        
        const transaction = db.transaction([STORE_NAME], 'readwrite');
        const store = transaction.objectStore(STORE_NAME);
        
        return new Promise((resolve, reject) => {
            const request = store.clear();
            
            request.onsuccess = () => {
                resolve();
            };
            
            request.onerror = () => {
                console.error('Failed to clear all messages:', request.error);
                reject(request.error);
            };
        });
    } catch (error) {
        console.error('Error clearing all messages from IndexedDB:', error);
        throw error;
    }
}

// Get estimated storage usage
async function getStorageUsage() {
    try {
        await initIndexedDB();
        
        const transaction = db.transaction([STORE_NAME], 'readonly');
        const store = transaction.objectStore(STORE_NAME);
        
        return new Promise((resolve, reject) => {
            const request = store.count();
            
            request.onsuccess = () => {
                resolve({
                    messageCount: request.result,
                    // Note: IndexedDB doesn't provide exact size, this is just count
                });
            };
            
            request.onerror = () => {
                reject(request.error);
            };
        });
    } catch (error) {
        console.error('Error getting storage usage:', error);
        return { messageCount: 0 };
    }
}

// Check if IndexedDB is supported
function isIndexedDBSupported() {
    return typeof indexedDB !== 'undefined';
}

// Export functions
window.ChatDB = {
    init: initIndexedDB,
    saveMessage: saveMessageWithFiles,
    loadMessages: loadMessagesForChat,
    deleteMessages: deleteMessagesForChat,
    deleteAll: deleteAllMessages,
    getUsage: getStorageUsage,
    isSupported: isIndexedDBSupported
};

// Auto-initialize on load
if (isIndexedDBSupported()) {
    initIndexedDB().catch(err => {
        console.warn('IndexedDB initialization failed, falling back to localStorage:', err);
    });
} else {
    console.warn('IndexedDB is not supported in this browser');
}
