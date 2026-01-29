// Default Theme JavaScript for oCMS
(function() {
    'use strict';

    // Initialize theme functionality when DOM is ready
    document.addEventListener('DOMContentLoaded', function() {
        initHeaderScroll();
        initMobileMenu();
        initMobileAccordion();
        initSearchToggle();
        initSmoothScroll();
        initExternalLinks();
        initFlyoutPosition();
    });

    /**
     * Header scroll effect - glassmorphism on scroll
     */
    function initHeaderScroll() {
        const header = document.getElementById('site-header');
        if (!header) return;

        const scrollThreshold = 20;

        function updateHeaderState() {
            if (window.scrollY > scrollThreshold) {
                header.classList.add('scrolled');
            } else {
                header.classList.remove('scrolled');
            }
        }

        // Initial check
        updateHeaderState();

        // Throttled scroll handler
        let ticking = false;
        window.addEventListener('scroll', function() {
            if (!ticking) {
                window.requestAnimationFrame(function() {
                    updateHeaderState();
                    ticking = false;
                });
                ticking = true;
            }
        }, { passive: true });
    }

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

            // Close all open submenus when closing main menu
            if (isExpanded) {
                closeAllSubmenus();
            }
        });

        // Close menu when clicking outside
        document.addEventListener('click', function(e) {
            if (!siteNav.contains(e.target) && !menuToggle.contains(e.target)) {
                menuToggle.setAttribute('aria-expanded', 'false');
                siteNav.classList.remove('active');
                document.body.classList.remove('menu-open');
                closeAllSubmenus();
            }
        });

        // Close menu on escape key
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && siteNav.classList.contains('active')) {
                menuToggle.setAttribute('aria-expanded', 'false');
                siteNav.classList.remove('active');
                document.body.classList.remove('menu-open');
                closeAllSubmenus();
            }
        });
    }

    /**
     * Mobile accordion for nested menu items
     */
    function initMobileAccordion() {
        const navItems = document.querySelectorAll('.nav-item.has-children > a');

        navItems.forEach(function(link) {
            link.addEventListener('click', function(e) {
                // Only apply accordion behavior on mobile
                if (window.innerWidth > 768) return;

                e.preventDefault();
                const parentItem = this.parentElement;

                // Close siblings at the same level
                const siblings = parentItem.parentElement.querySelectorAll(':scope > .nav-item.has-children');
                siblings.forEach(function(sibling) {
                    if (sibling !== parentItem) {
                        sibling.classList.remove('open');
                    }
                });

                // Toggle current item
                parentItem.classList.toggle('open');
            });
        });
    }

    /**
     * Close all open submenus
     */
    function closeAllSubmenus() {
        document.querySelectorAll('.nav-item.open').forEach(function(item) {
            item.classList.remove('open');
        });
    }

    /**
     * Search toggle functionality
     */
    function initSearchToggle() {
        const searchToggle = document.querySelector('.search-toggle');
        const headerSearch = document.querySelector('.header-search');

        if (!searchToggle || !headerSearch) return;

        searchToggle.addEventListener('click', function() {
            const isExpanded = this.getAttribute('aria-expanded') === 'true';
            this.setAttribute('aria-expanded', !isExpanded);
            headerSearch.classList.toggle('open');

            // Focus search input when opened
            if (!isExpanded) {
                const searchInput = headerSearch.querySelector('input[type="search"]');
                if (searchInput) {
                    setTimeout(function() {
                        searchInput.focus();
                    }, 100);
                }
            }
        });

        // Close search when clicking outside
        document.addEventListener('click', function(e) {
            if (!headerSearch.contains(e.target)) {
                searchToggle.setAttribute('aria-expanded', 'false');
                headerSearch.classList.remove('open');
            }
        });

        // Close search on escape
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && headerSearch.classList.contains('open')) {
                searchToggle.setAttribute('aria-expanded', 'false');
                headerSearch.classList.remove('open');
            }
        });
    }

    /**
     * Flyout position detection - flip to left if near right edge
     */
    function initFlyoutPosition() {
        const subMenuItems = document.querySelectorAll('.sub-menu .nav-item.has-children');

        subMenuItems.forEach(function(item) {
            item.addEventListener('mouseenter', function() {
                const submenu = this.querySelector(':scope > .sub-menu');
                if (!submenu) return;

                // Reset position
                submenu.classList.remove('flip-left');

                // Check if submenu would overflow
                const rect = submenu.getBoundingClientRect();
                const viewportWidth = window.innerWidth;

                if (rect.right > viewportWidth - 20) {
                    submenu.classList.add('flip-left');
                }
            });
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
