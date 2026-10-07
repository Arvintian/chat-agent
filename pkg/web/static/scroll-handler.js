// Scroll behavior control for chat messages
//
// Sticky-scroll model:
// - While "pinned", every content update (streaming chunks, tool calls,
//   thinking blocks) keeps the view at the bottom on the next animation
//   frame — no throttle, so fast streams cannot outrun the scroll.
// - Pinning is only released by REAL user input (wheel / touch / keyboard).
//   Content growth pushing the viewport away from the bottom does NOT count
//   as user scrolling, and programmatic scrolls are ignored by the scroll
//   handler entirely.
// - The "scroll to bottom" button shows only after the user deliberately
//   scrolled up, and disappears as soon as the view reaches the bottom.

(function() {
    var isUserScrolling = false;   // user deliberately scrolled up (reading history)
    var isPinned = true;           // auto-follow the bottom (true unless user scrolled up)
    var SCROLL_THRESHOLD = 50;     // pixels from bottom to consider "at bottom"
    // Snap-to-bottom tolerance while pinned. Must be SMALL: with a large
    // value the view can rest tens of px above the bottom after the last
    // chunk, and the last bubble's bottom edge gets clipped behind the
    // input area (its gap to the input is only ~23px of padding/margin).
    var STICK_THRESHOLD = 2;

    // Scroll to bottom button element
    var scrollToBottomBtn = null;

    // Coalesce multiple stick requests per frame
    var stickFrame = 0;

    // Late-growth watchers (see init). init() runs again every time a chat is
    // entered, so these must be created once per observed element instead of
    // piling up one instance per switch.
    var messagesObserver = null;
    var messagesObserverTarget = null;

    // Last seen scrollTop, used to tell "scrolling down" from "scrolling up".
    // Lives here (not inside the scroll listener) so the listener can be a
    // named function — repeated init() calls then reuse the SAME registration
    // instead of stacking one anonymous listener per chat switch.
    var lastScrollTop = 0;

    function getMessages() {
        return document.getElementById('messages');
    }

    function distanceFromBottom(el) {
        return el.scrollHeight - el.scrollTop - el.clientHeight;
    }

    // Keep the view at the bottom. Runs on the next frame so freshly
    // appended DOM (chunks, tool args, thinking) is measured correctly.
    function stickToBottom() {
        if (stickFrame) return;
        stickFrame = requestAnimationFrame(function () {
            stickFrame = 0;
            // User may have broken the pin (wheel/touch) between the
            // request and this frame — don't snap the view back.
            if (isUserScrolling || !isPinned) return;
            // Don't write scrollTop while a finger is down or right after it
            // lifted (momentum): on mobile that fights the touch scroll.
            if (touchActive || Date.now() - lastTouchEndTime < TOUCH_GRACE_MS) return;
            var el = getMessages();
            if (!el) return;
            // Pinned state is set before any programmatic scrollTop write,
            // and scroll events dispatch asynchronously — the scroll handler
            // sees isPinned and ignores the event of our own scroll.
            if (distanceFromBottom(el) > STICK_THRESHOLD) {
                el.scrollTop = el.scrollHeight;
            }
            // The button hides itself as soon as the bottom is reachable
            updateButton();
        });
    }

    // Real user input. While pinned, any UPWARD input immediately breaks the
    // pin — the browser has not scrolled yet at event time, so the
    // threshold check would still say "at bottom" and the next frame's
    // stick would snap the view back, fighting the user's scroll.
    function breakPin() {
        if (!isPinned) return;
        isPinned = false;
        isUserScrolling = true;
        updateButton();
    }

    // The (innermost) NESTED scrollable element (e.g. the collapsed thinking
    // content, tool args) that the event target sits inside, or null when the
    // target is directly in the outer messages list.
    function innermostScrollable(t, container) {
        var el = t && t.nodeType === 1 ? t : null;
        var found = null;
        while (el && el !== container && el !== document.body) {
            var cs = window.getComputedStyle(el);
            var oy = cs.overflowY;
            if ((oy === 'auto' || oy === 'scroll') && el.scrollHeight > el.clientHeight) {
                found = el;
            }
            el = el.parentElement;
        }
        return found;
    }

    // True when the event target sits inside a NESTED scrollable element.
    function isInnerScrollTarget(t, container) {
        return innermostScrollable(t, container) !== null;
    }

    function onUserWheel(e) {
        var el = getMessages();
        if (!el) return;
        if (isInnerScrollTarget(e.target, el)) return;
        if (isPinned && e.deltaY < 0) breakPin();
    }

    // While a finger is down — and briefly after it lifts (momentum
    // scrolling) — programmatic scrollTop writes fight the browser's touch
    // scroll on mobile and interrupt momentum. Track the gesture so
    // stickToBottom can stay out of the way.
    var touchActive = false;
    var lastTouchEndTime = 0;
    var TOUCH_GRACE_MS = 400;

    function onTouchStart(e) {
        // Only track single-finger gestures; reset on pinch
        lastTouchY = (e.touches.length === 1) ? e.touches[0].clientY : null;
        touchActive = true;
    }
    function onTouchMove(e) {
        if (e.touches.length !== 1) {
            lastTouchY = null;
            touchActive = true;
            return;
        }
        var el = getMessages();
        var y = e.touches[0].clientY;
        if (el && lastTouchY !== null) {
            var dy = y - lastTouchY;
            // A NESTED scrollable element (collapsed thinking block, tool
            // args) under the finger can ABSORB the swipe while the outer
            // list does not move — that must not break the pin. But once the
            // nested element reaches the swipe-direction boundary, the
            // gesture chains to the outer list, so break the pin exactly
            // like a real outer scroll (this is what makes the
            // "scroll to bottom" button appear while streaming over the
            // collapsed thinking block / tool args at the bottom).
            if (dy !== 0) {
                var inner = innermostScrollable(e.target, el);
                if (inner) {
                    var absorbs = dy > 0
                        ? inner.scrollTop > 0
                        : (inner.scrollTop + inner.clientHeight) < inner.scrollHeight;
                    if (absorbs) {
                        lastTouchY = y;
                        return;
                    }
                }
            }
        }
        // Finger moved down = scrolling up (reading history)
        if (isPinned && lastTouchY !== null && y > lastTouchY) breakPin();
        lastTouchY = y;
    }
    // On touchend, e.touches holds the fingers STILL down (the released one
    // is in e.changedTouches); no fingers left means the gesture is over.
    function onTouchEnd(e) {
        lastTouchY = null;
        if (e.touches.length === 0) {
            touchActive = false;
            lastTouchEndTime = Date.now();
            // Gesture ended near the bottom (e.g. the user only peeked a
            // little): re-attach the pin so streaming resumes following.
            var el = getMessages();
            if (el && !isPinned && distanceFromBottom(el) <= SCROLL_THRESHOLD) {
                isUserScrolling = false;
                isPinned = true;
                updateButton();
            }
        }
    }
    function onUserKey(e) {
        if (isPinned && (e.key === 'PageUp' || e.key === 'Home' || e.key === 'ArrowUp')) {
            breakPin();
        }
    }

    // While pinned we are already following the bottom (our own programmatic
    // scrolls also arrive while pinned and are ignored here); only while
    // UNPINNED can a real user scroll re-attach the pin — and only when
    // scrolling DOWN back to the bottom.
    function onMessagesScroll(e) {
        var el = e.currentTarget;
        if (!el) return;
        var top = el.scrollTop;
        var scrollingDown = top >= lastScrollTop;
        lastScrollTop = top;

        if (isPinned) return;
        if (scrollingDown && distanceFromBottom(el) <= SCROLL_THRESHOLD) {
            // User scrolled back down to the bottom: resume auto-follow
            isUserScrolling = false;
            isPinned = true;
        }
        updateButton();
    }

    // DOM grew/mutated inside the message list (mermaid SVG rendered, final
    // re-render rewrote innerHTML, ...). Keep following only while pinned.
    function onMessagesMutated() {
        if (isPinned && !isUserScrolling) stickToBottom();
    }

    // An image inside a reply finishes loading: its height was 0 until now
    // and no DOM mutation happens, so it needs its own hook. 'load' does not
    // bubble, hence the capture-phase listener. Registered once (a named
    // function, so a repeated addEventListener call is a no-op).
    function onMessagesImageLoad(e) {
        if (e.target && e.target.tagName === 'IMG' && isPinned && !isUserScrolling) {
            stickToBottom();
        }
    }

    // Initialize scroll detection
    function init() {
        var messagesContainer = getMessages();
        if (!messagesContainer) return;

        // Get scroll to bottom button
        scrollToBottomBtn = document.getElementById('scroll-to-bottom-btn');

        // Reset scroll state
        isUserScrolling = false;
        isPinned = true;
        updateButton();

        // Reset the scroll-direction baseline (same as the old per-init
        // closure variable did).
        lastScrollTop = 0;
        // Named function (see onMessagesScroll): re-registering the same
        // listener on a repeated init() is a no-op, so listeners no longer
        // pile up once per chat switch.
        messagesContainer.addEventListener('scroll', onMessagesScroll, { passive: true });

        // The last message can grow AFTER the view has settled at the bottom:
        // a mermaid SVG replaces its placeholder, the final re-render rewrites
        // innerHTML, an image inside the reply loads (its height is 0 until
        // 'load' fires). None of these re-triggers the per-chunk scroll, so
        // the view would rest above the (new) bottom and the last bubble
        // gets clipped behind the input area. Watch for DOM changes and keep
        // following while the pin is attached; when the user has scrolled up
        // (unpinned) the stick call is a no-op.
        // init() is called again on every chat entry, so both hooks are kept
        // idempotent: the observer only for the element it is not already
        // watching, the listener as a named function (re-adding the same
        // listener is a no-op).
        if (typeof MutationObserver !== 'undefined' && messagesObserverTarget !== messagesContainer) {
            if (messagesObserver) messagesObserver.disconnect();
            messagesObserver = new MutationObserver(onMessagesMutated);
            messagesObserver.observe(messagesContainer, { childList: true, subtree: true });
            messagesObserverTarget = messagesContainer;
        }
        messagesContainer.addEventListener('load', onMessagesImageLoad, true);

        // Detect deliberate user scrolling
        messagesContainer.addEventListener('wheel', onUserWheel, { passive: true });
        messagesContainer.addEventListener('touchstart', onTouchStart, { passive: true });
        messagesContainer.addEventListener('touchmove', onTouchMove, { passive: true });
        messagesContainer.addEventListener('keydown', onUserKey, { passive: true });
        // End/cancel may happen outside the container — listen on document
        document.addEventListener('touchend', onTouchEnd, { passive: true });
        document.addEventListener('touchcancel', onTouchEnd, { passive: true });
    }

    // Auto-follow the bottom; skipped while the user is reading history.
    function smartScrollToBottom() {
        if (isUserScrolling && !isPinned) return;
        stickToBottom();
    }

    // Force scroll to bottom (e.g., when the user sends a message):
    // also re-attaches the pin so subsequent streaming keeps following.
    function scrollToBottom(force) {
        force = force || false;
        var el = getMessages();
        if (!el) return;

        if (!force && isUserScrolling && !isPinned) return;

        // Set state BEFORE writing scrollTop so the async scroll event of
        // this write is classified as "ours" (pinned) and ignored.
        isUserScrolling = false;
        isPinned = true;
        if (distanceFromBottom(el) > STICK_THRESHOLD) {
            el.scrollTop = el.scrollHeight;
        }
        updateButton();
    }

    // Update scroll to bottom button visibility
    function updateButton() {
        if (!scrollToBottomBtn) return;

        // Show only when the user has deliberately scrolled up
        var show = isUserScrolling && !isPinned;

        // No scrollable room at all (e.g. after clearing the list) — nothing
        // to scroll back to, so the button must not appear. NOTE: must be the
        // TOTAL overflow, not the distance from the bottom — at breakPin time
        // the browser has not scrolled yet, so a short-list check on distance
        // would silently re-pin the moment the user starts dragging up.
        if (show) {
            var el = getMessages();
            if (el && (el.scrollHeight - el.clientHeight) <= SCROLL_THRESHOLD) {
                show = false;
                isUserScrolling = false;
                isPinned = true;
            }
        }

        if (show) {
            scrollToBottomBtn.classList.add('visible');
            scrollToBottomBtn.style.display = 'flex';
        } else {
            scrollToBottomBtn.classList.remove('visible');
            scrollToBottomBtn.style.display = 'none';
        }
    }

    // Reset scroll state and hide the button (call when the message list is
    // cleared or switched, since no scroll event will fire on its own)
    function reset() {
        isUserScrolling = false;
        isPinned = true;
        updateButton();
    }

    // Global scroll to bottom function for button click
    window.scrollToBottom = function() {
        var el = getMessages();
        if (!el) return;

        // Re-attach auto-follow BEFORE writing scrollTop (see scrollToBottom),
        // then force scroll (no smooth to ensure it works)
        isUserScrolling = false;
        isPinned = true;
        el.scrollTop = el.scrollHeight;
        updateButton();
    };

    // Expose functions to global scope
    window.ScrollHandler = {
        init: init,
        scrollToBottom: scrollToBottom,
        smartScrollToBottom: smartScrollToBottom,
        stickToBottom: stickToBottom,
        isUserScrolling: function () { return isUserScrolling; },
        isAtBottom: function () { return isPinned; },
        setUserScrolling: function (state) {
            isUserScrolling = !!state;
            if (!state) isPinned = true;
            updateButton();
        },
        setIsAtBottom: function (state) {
            isPinned = !!state;
            if (state) isUserScrolling = false;
            updateButton();
        },
        reset: reset
    };
})();
