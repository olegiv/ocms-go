// Developer Theme JavaScript

(function() {
    'use strict';

    // Mobile menu toggle
    const menuToggle = document.getElementById('menu-toggle');
    const mainNav = document.getElementById('main-nav');

    if (menuToggle && mainNav) {
        menuToggle.addEventListener('click', function() {
            mainNav.classList.toggle('active');
            this.setAttribute('aria-expanded',
                this.getAttribute('aria-expanded') === 'true' ? 'false' : 'true'
            );
        });

        // Close menu when clicking outside
        document.addEventListener('click', function(e) {
            if (!menuToggle.contains(e.target) && !mainNav.contains(e.target)) {
                mainNav.classList.remove('active');
                menuToggle.setAttribute('aria-expanded', 'false');
            }
        });

        // Close menu on escape key
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && mainNav.classList.contains('active')) {
                mainNav.classList.remove('active');
                menuToggle.setAttribute('aria-expanded', 'false');
                menuToggle.focus();
            }
        });
    }

    // Smooth scroll for anchor links
    document.querySelectorAll('a[href^="#"]').forEach(anchor => {
        anchor.addEventListener('click', function(e) {
            const targetId = this.getAttribute('href');
            if (targetId === '#') return;

            const target = document.querySelector(targetId);
            if (target) {
                e.preventDefault();
                target.scrollIntoView({
                    behavior: 'smooth',
                    block: 'start'
                });

                // Update URL without scrolling
                history.pushState(null, null, targetId);
            }
        });
    });

    // Add copy button to code blocks
    document.querySelectorAll('.dev-prose pre').forEach(pre => {
        const wrapper = document.createElement('div');
        wrapper.className = 'dev-code-wrapper';
        pre.parentNode.insertBefore(wrapper, pre);
        wrapper.appendChild(pre);

        const copyBtn = document.createElement('button');
        copyBtn.className = 'dev-copy-btn';
        copyBtn.innerHTML = 'Copy';
        copyBtn.setAttribute('aria-label', 'Copy code to clipboard');
        wrapper.appendChild(copyBtn);

        copyBtn.addEventListener('click', async function() {
            const code = pre.querySelector('code') || pre;
            try {
                await navigator.clipboard.writeText(code.textContent);
                this.innerHTML = 'Copied!';
                this.classList.add('copied');
                setTimeout(() => {
                    this.innerHTML = 'Copy';
                    this.classList.remove('copied');
                }, 2000);
            } catch (err) {
                console.error('Failed to copy:', err);
            }
        });
    });

    // Add styles for copy button dynamically
    const style = document.createElement('style');
    style.textContent = `
        .dev-code-wrapper {
            position: relative;
        }
        .dev-copy-btn {
            position: absolute;
            top: 8px;
            right: 8px;
            font-family: var(--dev-font-mono);
            font-size: 0.75rem;
            padding: 4px 8px;
            background-color: var(--dev-bg-tertiary);
            border: 1px solid var(--dev-border);
            border-radius: 4px;
            color: var(--dev-text-muted);
            cursor: pointer;
            opacity: 0;
            transition: all 0.2s ease;
        }
        .dev-code-wrapper:hover .dev-copy-btn {
            opacity: 1;
        }
        .dev-copy-btn:hover {
            color: var(--dev-accent);
            border-color: var(--dev-accent);
        }
        .dev-copy-btn.copied {
            color: var(--dev-success);
            border-color: var(--dev-success);
        }
    `;
    document.head.appendChild(style);

    // Keyboard shortcuts
    document.addEventListener('keydown', function(e) {
        // Focus search on '/' key
        if (e.key === '/' && !isInputFocused()) {
            e.preventDefault();
            const searchInput = document.querySelector('.dev-search-input');
            if (searchInput) {
                searchInput.focus();
            }
        }

        // Go home on 'h' key
        if (e.key === 'h' && !isInputFocused()) {
            window.location.href = '/';
        }
    });

    function isInputFocused() {
        const active = document.activeElement;
        return active && (
            active.tagName === 'INPUT' ||
            active.tagName === 'TEXTAREA' ||
            active.isContentEditable
        );
    }

    // Typing effect for terminal hero (if on home page)
    const heroCommand = document.querySelector('.dev-hero .dev-command');
    if (heroCommand) {
        const text = heroCommand.textContent;
        heroCommand.textContent = '';
        let i = 0;

        function typeWriter() {
            if (i < text.length) {
                heroCommand.textContent += text.charAt(i);
                i++;
                setTimeout(typeWriter, 50);
            }
        }

        // Start typing after a short delay
        setTimeout(typeWriter, 500);
    }

    // Add line numbers to code blocks if theme setting is enabled
    if (document.documentElement.dataset.lineNumbers === 'yes') {
        document.querySelectorAll('.dev-prose pre code').forEach(code => {
            const lines = code.textContent.split('\n');
            const numbered = lines.map((line, i) => {
                return `<span class="line-number">${i + 1}</span>${line}`;
            }).join('\n');
            code.innerHTML = numbered;
        });
    }

    // Lazy load images
    if ('IntersectionObserver' in window) {
        const imageObserver = new IntersectionObserver((entries, observer) => {
            entries.forEach(entry => {
                if (entry.isIntersecting) {
                    const img = entry.target;
                    if (img.dataset.src) {
                        img.src = img.dataset.src;
                        img.removeAttribute('data-src');
                    }
                    img.classList.add('loaded');
                    observer.unobserve(img);
                }
            });
        }, {
            rootMargin: '50px 0px'
        });

        document.querySelectorAll('img[data-src]').forEach(img => {
            imageObserver.observe(img);
        });
    }

    // Console easter egg
    console.log('%c⚡ Developer Theme', 'font-size: 24px; font-weight: bold; color: #10b981;');
    console.log('%cBuilt for developers, by developers.', 'font-size: 14px; color: #8b949e;');
    console.log('%cKeyboard shortcuts: "/" to search, "h" to go home', 'font-size: 12px; color: #6e7681;');
})();
