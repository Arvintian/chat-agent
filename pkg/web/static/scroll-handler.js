// Scroll behavior control for chat messages
// This module handles auto-scroll and user reading detection

(function() {
    // Scroll behavior control variables
    var isUserScrolling = false;
    var isAtBottom = true;
    var scrollTimeout = null;
    var SCROLL_THRESHOLD = 50; // pixels from bottom to consider "at bottom"
    
    // Scroll to bottom button element
    var scrollToBottomBtn = null;

    // Throttle scroll during streaming to improve performance
    var lastScrollTime = 0;
    var SCROLL_THROTTLE_MS = 150;
    var scrollPending = false;

    // Initialize scroll detection after DOM is ready
    function initScrollDetection() {
        var messagesContainer = document.getElementById('messages');
        if (!messagesContainer) return;

        // Get scroll to bottom button
        scrollToBottomBtn = document.getElementById('scroll-to-bottom-btn');

        // Reset scroll state when starting a new chat
        isUserScrolling = false;
        isAtBottom = true;

        // Hide button initially
        updateScrollToBottomButton();

        // Listen for scroll events to detect user reading behavior
        var scrollTimer = null;
        messagesContainer.addEventListener('scroll', function() {
            var scrollTop = messagesContainer.scrollTop;
            var scrollHeight = messagesContainer.scrollHeight;
            var clientHeight = messagesContainer.clientHeight;

            // Check if user is at bottom (within threshold)
            var wasAtBottom = isAtBottom;
            isAtBottom = (scrollHeight - scrollTop - clientHeight) <= SCROLL_THRESHOLD;

            // If user scrolled up from bottom, mark as scrolling
            if (wasAtBottom && !isAtBottom) {
                isUserScrolling = true;
            }

            // If user scrolled back to bottom, re-enable auto-scroll
            if (!wasAtBottom && isAtBottom) {
                isUserScrolling = false;
            }

            // Update scroll to bottom button visibility
            updateScrollToBottomButton();

            // Clear existing timeout
            if (scrollTimer) {
                clearTimeout(scrollTimer);
            }

            // Set timeout to stabilize scrolling state
            scrollTimer = setTimeout(function() {
                scrollTimer = null;
                // Re-check position after scrolling stops
                var currentScrollTop = messagesContainer.scrollTop;
                var currentScrollHeight = messagesContainer.scrollHeight;
                var currentClientHeight = messagesContainer.clientHeight;
                isAtBottom = (currentScrollHeight - currentScrollTop - currentClientHeight) <= SCROLL_THRESHOLD;

                // If user is at bottom, re-enable auto-scroll
                if (isAtBottom) {
                    isUserScrolling = false;
                }

                // Update scroll to bottom button visibility
                updateScrollToBottomButton();
            }, 150);
        });
    }

    // Smart scroll to bottom - only scrolls if user is not reading history
    function smartScrollToBottom(force) {
        force = force || false;
        
        // Only scroll if user is not reading history or is at bottom
        if (!isUserScrolling || isAtBottom) {
            var now = Date.now();
            if (force) {
                requestAnimationFrame(function() {
                    var messages = document.getElementById('messages');
                    if (messages) {
                        messages.scrollTop = messages.scrollHeight;
                    }
                    lastScrollTime = now;
                    scrollPending = false;
                });
                return;
            }
            if (now - lastScrollTime > SCROLL_THROTTLE_MS && !scrollPending) {
                scrollPending = true;
                requestAnimationFrame(function() {
                    var messages = document.getElementById('messages');
                    if (messages) {
                        messages.scrollTop = messages.scrollHeight;
                    }
                    lastScrollTime = now;
                    scrollPending = false;
                });
            }
        }
    }

    // Force scroll to bottom
    function scrollToBottom(force) {
        force = force || false;
        var messages = document.getElementById('messages');
        if (!messages) return;

        // Force scroll (e.g., when user sends a message) or auto-scroll if not reading history
        if (force || !isUserScrolling || isAtBottom) {
            messages.scrollTop = messages.scrollHeight;
        }
    }

    // Check if user is currently scrolling (reading history)
    function getUserScrollingState() {
        return isUserScrolling;
    }

    // Check if user is at bottom
    function getIsAtBottomState() {
        return isAtBottom;
    }

    // Set user scrolling state (for external use)
    function setUserScrollingState(state) {
        isUserScrolling = state;
    }

    // Set is at bottom state (for external use)
    function setIsAtBottomState(state) {
        isAtBottom = state;
    }

    // Update scroll to bottom button visibility
    function updateScrollToBottomButton() {
        if (!scrollToBottomBtn) return;
        
        // Show button when user is scrolling (not at bottom)
        if (!isAtBottom) {
            scrollToBottomBtn.classList.add('visible');
            scrollToBottomBtn.style.display = 'flex';
        } else {
            scrollToBottomBtn.classList.remove('visible');
            scrollToBottomBtn.style.display = 'none';
        }
    }

    // Global scroll to bottom function for button click
    window.scrollToBottom = function() {
        var messages = document.getElementById('messages');
        if (!messages) return;
        
        // Force scroll to bottom (without smooth to ensure it works)
        messages.scrollTop = messages.scrollHeight;
        
        // Reset scrolling state immediately
        isUserScrolling = false;
        isAtBottom = true;
        
        // Hide button after clicking
        if (scrollToBottomBtn) {
            scrollToBottomBtn.classList.remove('visible');
            scrollToBottomBtn.style.display = 'none';
        }
    };

    // Expose functions to global scope
    window.ScrollHandler = {
        init: initScrollDetection,
        scrollToBottom: scrollToBottom,
        smartScrollToBottom: smartScrollToBottom,
        isUserScrolling: getUserScrollingState,
        isAtBottom: getIsAtBottomState,
        setUserScrolling: setUserScrollingState,
        setIsAtBottom: setIsAtBottomState
    };
})();
