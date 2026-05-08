// Quick Phrases - 快捷短语管理模块
// 输入为空时按方向键左键触发短语列表，显示在输入框上方
(function() {
    'use strict';

    const STORAGE_KEY = 'chat_agent_quick_phrases';
    const DEFAULT_PHRASES = [];

    let phrases = [];
    let selectedIndex = -1;        // 当前选中的短语索引 (-1 = 未选中)
    let panelVisible = false;
    let inputElement = null;
    let panelElement = null;
    let addInputElement = null;

    // 初始化：从 localStorage 加载短语
    function loadPhrases() {
        try {
            const stored = localStorage.getItem(STORAGE_KEY);
            if (stored) {
                phrases = JSON.parse(stored);
            } else {
                phrases = [...DEFAULT_PHRASES];
            }
        } catch (e) {
            console.error('Failed to load quick phrases:', e);
            phrases = [...DEFAULT_PHRASES];
        }
    }

    // 持久化短语
    function savePhrases() {
        try {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(phrases));
        } catch (e) {
            console.error('Failed to save quick phrases:', e);
        }
    }

    // 创建短语面板 DOM
    function createPanel() {
        if (panelElement) return;

        panelElement = document.createElement('div');
        panelElement.id = 'quick-phrases-panel';
        panelElement.className = 'quick-phrases-panel';
        panelElement.style.display = 'none';
        panelElement.innerHTML = `
            <div class="quick-phrases-header">
                <span class="quick-phrases-title">⚡ Quick Phrases</span>
                <button class="quick-phrases-minimize" id="quick-phrases-minimize-btn" title="Hide panel">−</button>
            </div>
            <div class="quick-phrases-list" id="quick-phrases-list"></div>
            <div class="quick-phrases-add">
                <input type="text" id="quick-phrases-add-input" placeholder="+ New phrase..." />
                <button id="quick-phrases-add-btn" title="Add phrase">+</button>
            </div>
        `;

        // 插入到 input-area 之前
        const inputArea = document.getElementById('input-area');
        if (inputArea && inputArea.parentNode) {
            inputArea.parentNode.insertBefore(panelElement, inputArea);
        }

        // 缓存 add input 引用
        addInputElement = panelElement.querySelector('#quick-phrases-add-input');

        // 绑定新增按钮事件
        const addBtn = panelElement.querySelector('#quick-phrases-add-btn');
        addBtn.addEventListener('click', addPhrase);

        // 绑定新增输入框回车事件
        addInputElement.addEventListener('keydown', function(e) {
            if (e.key === 'Enter') {
                e.preventDefault();
                e.stopPropagation();
                addPhrase();
            }
        });

        // 绑定最小化按钮事件
        const minimizeBtn = panelElement.querySelector('#quick-phrases-minimize-btn');
        minimizeBtn.addEventListener('click', function() {
            hidePanel();
            if (inputElement) inputElement.focus();
        });
    }

    // 渲染短语列表
    function renderList() {
        const listEl = document.getElementById('quick-phrases-list');
        if (!listEl) return;

        listEl.innerHTML = '';

        if (phrases.length === 0) {
            listEl.innerHTML = '<div class="quick-phrases-empty">No phrases yet. Add one below.</div>';
            return;
        }

        phrases.forEach(function(phrase, index) {
            const item = document.createElement('div');
            item.className = 'quick-phrase-item';
            if (index === selectedIndex) {
                item.classList.add('selected');
            }
            item.innerHTML = `
                <span class="quick-phrase-text">${escapeHtml(phrase)}</span>
                <button class="quick-phrase-delete" data-index="${index}" title="Delete phrase">×</button>
            `;
            item.addEventListener('click', function(e) {
                // 如果点击的是删除按钮，不触发选择
                if (e.target.classList.contains('quick-phrase-delete')) return;
                selectAndInsert(index);
            });
            listEl.appendChild(item);
        });

        // 绑定删除按钮事件
        const deleteButtons = listEl.querySelectorAll('.quick-phrase-delete');
        deleteButtons.forEach(function(btn) {
            btn.addEventListener('click', function(e) {
                e.stopPropagation();
                const index = parseInt(btn.getAttribute('data-index'));
                deletePhrase(index);
            });
        });

        // 滚动到选中项
        scrollToSelected();
    }

    // 显示面板
    function showPanel() {
        if (!panelElement) createPanel();
        selectedIndex = -1;
        panelVisible = true;
        panelElement.style.display = 'block';
        renderList();
        // 聚焦新增输入框（移到 addInput 以便快速输入）
        // 不聚焦 addInput，保持主输入框焦点，用键盘导航
    }

    // 隐藏面板
    function hidePanel() {
        panelVisible = false;
        selectedIndex = -1;
        if (panelElement) {
            panelElement.style.display = 'none';
        }
    }

    // 检查面板是否可见
    function isVisible() {
        return panelVisible;
    }

    // 选择并插入短语
    function selectAndInsert(index) {
        if (index < 0 || index >= phrases.length) return;
        const phrase = phrases[index];
        if (inputElement) {
            inputElement.value = phrase;
            // 触发 input 事件以便其他逻辑（如自动调整大小）能响应
            inputElement.dispatchEvent(new Event('input', { bubbles: true }));
            inputElement.focus();
        }
        hidePanel();
    }

    // 新增短语
    function addPhrase() {
        if (!addInputElement) return;
        const text = addInputElement.value.trim();
        if (!text) return;

        // 避免重复
        if (phrases.includes(text)) {
            // 如果已存在，选中并插入
            const existingIndex = phrases.indexOf(text);
            selectAndInsert(existingIndex);
            addInputElement.value = '';
            return;
        }

        phrases.push(text);
        savePhrases();
        addInputElement.value = '';
        renderList();
        // 操作完成后自动聚焦回主输入框，确保快捷键正常工作
        if (inputElement) {
            inputElement.focus();
        }
    }

    // 删除短语
    function deletePhrase(index) {
        if (index < 0 || index >= phrases.length) return;
        phrases.splice(index, 1);
        savePhrases();
        // 调整选中索引
        if (selectedIndex >= phrases.length) {
            selectedIndex = phrases.length - 1;
        }
        renderList();
        // 操作完成后自动聚焦回主输入框，确保快捷键正常工作
        if (inputElement) {
            inputElement.focus();
        }
    }

    // 键盘导航
    function handleKeyDown(e) {
        if (!panelVisible) return false;

        switch (e.key) {
            case 'ArrowUp':
                e.preventDefault();
                if (phrases.length > 0) {
                    if (selectedIndex <= 0) {
                        selectedIndex = phrases.length - 1;
                    } else {
                        selectedIndex--;
                    }
                    renderList();
                }
                return true;

            case 'ArrowDown':
                e.preventDefault();
                if (phrases.length > 0) {
                    if (selectedIndex >= phrases.length - 1) {
                        selectedIndex = 0;
                    } else {
                        selectedIndex++;
                    }
                    renderList();
                }
                return true;

            case 'Enter':
                e.preventDefault();
                if (selectedIndex >= 0 && selectedIndex < phrases.length) {
                    selectAndInsert(selectedIndex);
                }
                return true;

            case 'Delete':
            case 'Backspace':
                // 注意：Backspace 在输入框为空时，不要触发面板的删除
                // 只有当面板可见且 addInput 没有焦点时才处理
                if (e.key === 'Delete' && selectedIndex >= 0 && selectedIndex < phrases.length) {
                    // 检查焦点不在 addInput 上
                    if (document.activeElement !== addInputElement) {
                        e.preventDefault();
                        deletePhrase(selectedIndex);
                        return true;
                    }
                }
                return false;

            case 'Escape':
                e.preventDefault();
                hidePanel();
                inputElement.focus();
                return true;

            case 'ArrowLeft':
                // 左键：未选中项目时隐藏面板
                if (selectedIndex === -1) {
                    e.preventDefault();
                    hidePanel();
                    inputElement.focus();
                    return true;
                }
                return false;

            case 'ArrowRight':
                // 右键：快速关闭面板（和 Esc 一样）
                e.preventDefault();
                hidePanel();
                inputElement.focus();
                return true;

            default:
                // 按其他键：如果焦点在主输入框，隐藏面板并让字符正常输入
                if (document.activeElement === inputElement && e.key.length === 1) {
                    hidePanel();
                    return false;
                }
                return false;
        }
    }

    // 滚动到选中项
    function scrollToSelected() {
        const listEl = document.getElementById('quick-phrases-list');
        if (!listEl) return;
        const selected = listEl.querySelector('.quick-phrase-item.selected');
        if (selected) {
            selected.scrollIntoView({ block: 'nearest' });
        }
    }

    // HTML 转义
    function escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    // 设置关联的输入框
    function setInputElement(el) {
        if (inputElement) {
            // 移除旧输入框的 input 监听
            inputElement.removeEventListener('input', onInputChange);
        }
        inputElement = el;
        if (inputElement) {
            // 监听 input 事件：移动端输入法可能不触发 keydown，用 input 事件兜底
            inputElement.addEventListener('input', onInputChange);
        }
    }

    // 输入框内容变化时自动隐藏面板（移动端兼容）
    // 注意：selectAndInsert 会设置 value 并手动 hidePanel，这里只处理用户手动输入的情况
    function onInputChange() {
        if (panelVisible && inputElement && inputElement.value.length > 0) {
            // 如果焦点在 addInput 上，说明用户正在新增短语，不要关闭
            if (document.activeElement === addInputElement) return;
            hidePanel();
        }
    }

    // 初始化
    function init() {
        loadPhrases();
        createPanel();
        setInputElement(document.getElementById('message-input'));
    }

    // 导出 API
    window.QuickPhrases = {
        init: init,
        show: showPanel,
        hide: hidePanel,
        isVisible: isVisible,
        handleKeyDown: handleKeyDown,
        setInputElement: setInputElement,
        getPhrases: function() { return phrases; }
    };

})();
