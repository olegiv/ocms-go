// Shared image lightbox for all oCMS themes
(function() {
    'use strict';

    // Generic selectors that match article body images and hero images across themes
    var SELECTORS = [
        '.dev-prose img', '.dev-page-hero img',          // developer theme (HTML)
        '.prose img', '.page-hero img',                  // default theme (HTML)
        '.st-prose img', '.st-article__hero img',        // starter theme (HTML)
        '.fe-article-body img', '.fe-article-hero img'   // templ-based layout
    ].join(', ');

    var overlay = document.createElement('div');
    overlay.className = 'ocms-lightbox';

    var img = document.createElement('img');
    img.className = 'ocms-lightbox-img';
    img.alt = '';
    overlay.appendChild(img);

    var btn = document.createElement('button');
    btn.className = 'ocms-lightbox-close';
    btn.setAttribute('aria-label', 'Close');
    btn.textContent = '\u00D7';
    overlay.appendChild(btn);

    document.body.appendChild(overlay);

    function open(src, alt) {
        img.src = src;
        img.alt = alt || '';
        overlay.classList.add('active');
        document.body.style.overflow = 'hidden';
    }

    function close() {
        overlay.classList.remove('active');
        document.body.style.overflow = '';
        img.src = '';
    }

    function getBestSrc(el) {
        var srcset = el.getAttribute('srcset');
        if (!srcset) return el.src;
        var best = { w: 0, url: el.src };
        srcset.split(',').forEach(function(entry) {
            var parts = entry.trim().split(/\s+/);
            var w = parseInt(parts[1]) || 0;
            if (w > best.w) best = { w: w, url: parts[0] };
        });
        return best.url;
    }

    document.querySelectorAll(SELECTORS).forEach(function(el) {
        el.style.cursor = 'pointer';
        el.addEventListener('click', function(e) {
            e.preventDefault();
            open(getBestSrc(el), el.alt);
        });
    });

    overlay.addEventListener('click', function(e) {
        if (e.target === overlay) close();
    });
    btn.addEventListener('click', close);
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && overlay.classList.contains('active')) close();
    });

    var s = document.createElement('style');
    s.textContent =
        '.ocms-lightbox{position:fixed;inset:0;z-index:9999;display:flex;align-items:center;' +
        'justify-content:center;background:rgba(0,0,0,.9);opacity:0;pointer-events:none;' +
        'transition:opacity .2s ease}' +
        '.ocms-lightbox.active{opacity:1;pointer-events:auto}' +
        '.ocms-lightbox-img{max-width:90vw;max-height:90vh;object-fit:contain;border-radius:4px}' +
        '.ocms-lightbox-close{position:absolute;top:16px;right:16px;font-size:2rem;' +
        'color:#fff;background:none;border:none;cursor:pointer;line-height:1;padding:8px;' +
        'opacity:.7;transition:opacity .2s}.ocms-lightbox-close:hover{opacity:1}';
    document.head.appendChild(s);
})();
