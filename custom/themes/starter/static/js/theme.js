// Starter Theme JavaScript
(function() {
    'use strict';

    // ---- Header scroll effect ----
    var header = document.getElementById('site-header');
    if (header) {
        window.addEventListener('scroll', function() {
            if (window.scrollY > 10) {
                header.classList.add('st-header--scrolled');
            } else {
                header.classList.remove('st-header--scrolled');
            }
        }, { passive: true });
    }

    // ---- Mobile menu toggle ----
    var menuToggle = document.getElementById('menu-toggle');
    var mainNav = document.getElementById('main-nav');

    if (menuToggle && mainNav) {
        menuToggle.addEventListener('click', function() {
            var isOpen = mainNav.classList.toggle('active');
            menuToggle.classList.toggle('active');
            menuToggle.setAttribute('aria-expanded', isOpen);
        });

        // Close on click outside
        document.addEventListener('click', function(e) {
            if (!menuToggle.contains(e.target) && !mainNav.contains(e.target)) {
                mainNav.classList.remove('active');
                menuToggle.classList.remove('active');
                menuToggle.setAttribute('aria-expanded', 'false');
            }
        });

        // Close on escape
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape' && mainNav.classList.contains('active')) {
                mainNav.classList.remove('active');
                menuToggle.classList.remove('active');
                menuToggle.setAttribute('aria-expanded', 'false');
                menuToggle.focus();
            }
        });

        // Close menu on Alpine.js event (for custom integrations)
        window.addEventListener('close-mobile-menu', function() {
            mainNav.classList.remove('active');
            menuToggle.classList.remove('active');
            menuToggle.setAttribute('aria-expanded', 'false');
        });
    }

    // ---- Mobile accordion for sub-menus ----
    var parentItems = document.querySelectorAll('.st-nav__item--parent');
    parentItems.forEach(function(item) {
        var link = item.querySelector(':scope > a');
        if (!link) return;

        link.addEventListener('click', function(e) {
            // Only activate accordion on mobile (when toggle is visible)
            if (menuToggle && getComputedStyle(menuToggle).display !== 'none') {
                e.preventDefault();
                // Close sibling menus
                var siblings = item.parentElement.querySelectorAll(':scope > .st-nav__item--parent');
                siblings.forEach(function(sib) {
                    if (sib !== item) sib.classList.remove('open');
                });
                item.classList.toggle('open');
            }
        });
    });

    // ---- Search toggle ----
    var searchBtn = document.querySelector('.st-search-toggle__btn');
    var searchForm = document.querySelector('.st-search-form');

    if (searchBtn && searchForm) {
        searchBtn.addEventListener('click', function() {
            var isOpen = searchForm.classList.toggle('active');
            searchBtn.setAttribute('aria-expanded', isOpen);
            if (isOpen) {
                searchForm.querySelector('input').focus();
            }
        });

        document.addEventListener('click', function(e) {
            if (!searchBtn.contains(e.target) && !searchForm.contains(e.target)) {
                searchForm.classList.remove('active');
                searchBtn.setAttribute('aria-expanded', 'false');
            }
        });
    }

    // ---- Desktop flyout position detection ----
    var level2Items = document.querySelectorAll('.st-nav__sub > .st-nav__item--parent');
    level2Items.forEach(function(item) {
        item.addEventListener('mouseenter', function() {
            if (menuToggle && getComputedStyle(menuToggle).display !== 'none') return;
            var flyout = item.querySelector(':scope > .st-nav__sub');
            if (!flyout) return;
            flyout.classList.remove('flip-left');
            var rect = item.getBoundingClientRect();
            var flyoutWidth = flyout.offsetWidth || 200;
            if (rect.right + flyoutWidth > window.innerWidth - 20) {
                flyout.classList.add('flip-left');
            }
        });
    });

    // ---- Smooth scroll for anchor links ----
    document.querySelectorAll('a[href^="#"]').forEach(function(anchor) {
        anchor.addEventListener('click', function(e) {
            var id = this.getAttribute('href');
            if (id === '#') return;
            var target = document.querySelector(id);
            if (target) {
                e.preventDefault();
                target.scrollIntoView({ behavior: 'smooth', block: 'start' });
                history.pushState(null, null, id);
            }
        });
    });

    // ---- External links in new tab ----
    document.querySelectorAll('.st-prose a').forEach(function(link) {
        if (link.hostname && link.hostname !== window.location.hostname) {
            link.setAttribute('target', '_blank');
            link.setAttribute('rel', 'noopener noreferrer');
        }
    });
})();
