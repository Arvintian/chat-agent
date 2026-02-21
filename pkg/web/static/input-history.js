// Input History Management Module (ES5 compatible)
// Handles saving, loading, and navigating input history using localStorage

(function() {
    'use strict';

    // Configuration
    var INPUT_HISTORY_KEY = 'chat_input_history';
    var MAX_HISTORY_SIZE = 50;

    // State
    var inputHistory = [];
    var historyIndex = -1;  // -1 means not browsing history, 0 is the newest entry
    var lastInputValue = '';  // Store current input before browsing history

    // Load input history from localStorage
    // History is stored with oldest at index 0, newest at the end
    function loadHistory() {
        try {
            var stored = localStorage.getItem(INPUT_HISTORY_KEY);
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

        var trimmed = text.trim();
        // Remove if already exists (to move to end)
        var existingIndex = inputHistory.indexOf(trimmed);
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

        var input = document.getElementById('message-input');
        if (!input) return;

        // First key press - save current input
        if (historyIndex === -1) {
            lastInputValue = input.value;
            // Start from newest entry
            historyIndex = inputHistory.length;
        }

        // Calculate new index
        var newIndex = historyIndex + direction;

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

    // Reset history navigation state
    function resetHistoryNavigation() {
        historyIndex = -1;
        lastInputValue = '';
    }

    // Get current history index (for debugging)
    function getHistoryIndex() {
        return historyIndex;
    }

    // Get history length (for debugging)
    function getHistoryLength() {
        return inputHistory.length;
    }

    // Check if currently browsing history
    function isBrowsingHistory() {
        return historyIndex !== -1;
    }

    // Export functions to global scope
    window.InputHistory = {
        loadHistory: loadHistory,
        saveToHistory: saveToHistory,
        navigateHistory: navigateHistory,
        clearHistory: clearHistory,
        resetHistoryNavigation: resetHistoryNavigation,
        getHistoryIndex: getHistoryIndex,
        getHistoryLength: getHistoryLength,
        isBrowsingHistory: isBrowsingHistory
    };

})();
