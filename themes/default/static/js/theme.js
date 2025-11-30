// Default Theme JavaScript for oCMS
(function() {
    'use strict';

    // Initialize theme functionality when DOM is ready
    document.addEventListener('DOMContentLoaded', function() {
        initMobileMenu();
        initSmoothScroll();
        initExternalLinks();
    });

    /**
     * Mobile menu toggle functionality
     */
    function initMobileMenu() {
        const menuToggle = document.querySelector('.mobile-menu-toggle');
        const siteNav = document.querySelector('.site-nav');

        if (!menuToggle || !siteNav) return;

        menuToggle.addEventListener('click', function() {
            const isExpanded = this.getAttribute('aria-expanded') === 'true';
            this.setAttribute('aria-expanded', !isExpanded);
            siteNav.classList.toggle('active');
            document.body.classList.toggle('menu-open');
        });

        // Close menu when clicking outside
        document.addEventListener('click', function(e) {
            if (!siteNav.contains(e.target) && !menuToggle.contains(e.target)) {
                menuToggle.setAttribute('aria-expanded', 'false');
                siteNav.classList.remove('active');
                document.body.classList.remove('menu-open');
            }
        });

        // Close menu on escape key
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && siteNav.classList.contains('active')) {
                menuToggle.setAttribute('aria-expanded', 'false');
                siteNav.classList.remove('active');
                document.body.classList.remove('menu-open');
            }
        });
    }

    /**
     * Smooth scroll for anchor links
     */
    function initSmoothScroll() {
        document.querySelectorAll('a[href^="#"]').forEach(function(anchor) {
            anchor.addEventListener('click', function(e) {
                const href = this.getAttribute('href');
                if (href === '#') return;

                const target = document.querySelector(href);
                if (target) {
                    e.preventDefault();
                    target.scrollIntoView({
                        behavior: 'smooth',
                        block: 'start'
                    });
                }
            });
        });
    }

    /**
     * Add external link attributes
     */
    function initExternalLinks() {
        document.querySelectorAll('a[href^="http"]').forEach(function(link) {
            // Skip internal links
            if (link.hostname === window.location.hostname) return;

            // Add target and rel attributes for external links
            if (!link.hasAttribute('target')) {
                link.setAttribute('target', '_blank');
            }
            const rel = link.getAttribute('rel') || '';
            if (!rel.includes('noopener')) {
                link.setAttribute('rel', (rel + ' noopener noreferrer').trim());
            }
        });
    }
})();
