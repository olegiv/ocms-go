/**
 * oCMS Admin Core JavaScript
 * Global utilities for keyboard shortcuts, HTMX handlers, toast notifications, and form validation
 */

// Keyboard shortcuts
document.addEventListener('keydown', function(e) {
    // Ctrl+S or Cmd+S to save forms
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        const form = document.querySelector('form[data-saveable], form.saveable-form');
        if (form) {
            e.preventDefault();
            const submitBtn = form.querySelector('button[type="submit"], input[type="submit"]');
            if (submitBtn) {
                submitBtn.click();
            } else {
                form.submit();
            }
        }
    }

    // Escape to close modals
    if (e.key === 'Escape') {
        const modal = document.querySelector('.modal.active, [x-data*="modal"][x-show="true"]');
        if (modal) {
            e.preventDefault();
            const closeBtn = modal.querySelector('.modal-close, [x-on\\:click*="close"]');
            if (closeBtn) closeBtn.click();
        }
    }
});

// HTMX loading state handlers
document.body.addEventListener('htmx:beforeRequest', function(e) {
    // Add loading class to buttons
    const trigger = e.detail.elt;
    if (trigger.tagName === 'BUTTON' || trigger.classList.contains('btn')) {
        trigger.classList.add('btn-loading');
    }
});

document.body.addEventListener('htmx:afterRequest', function(e) {
    // Remove loading class from buttons
    const trigger = e.detail.elt;
    if (trigger.tagName === 'BUTTON' || trigger.classList.contains('btn')) {
        trigger.classList.remove('btn-loading');
    }
});

// HTMX error response handler - show error messages as alerts
document.body.addEventListener('htmx:responseError', function(e) {
    const xhr = e.detail.xhr;
    let message = 'An error occurred';

    // Try to get error message from response body
    if (xhr.responseText && xhr.responseText.trim()) {
        message = xhr.responseText.trim();
    }

    // Show error alert
    showToast(message, 'error');
});

// Toast notification helper
function showToast(message, type) {
    type = type || 'info';

    // Create toast container if it doesn't exist
    let container = document.getElementById('toast-container');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.style.cssText = 'position:fixed;top:1rem;right:1rem;z-index:9999;display:flex;flex-direction:column;gap:0.5rem;';
        document.body.appendChild(container);
    }

    // Create toast element
    const toast = document.createElement('div');
    toast.className = 'toast toast-' + type;
    toast.style.cssText = 'padding:0.75rem 1rem;border-radius:0.375rem;color:white;font-size:0.875rem;max-width:400px;box-shadow:0 4px 6px rgba(0,0,0,0.1);animation:slideIn 0.3s ease;';
    toast.style.background = type === 'error' ? '#dc2626' : type === 'success' ? '#16a34a' : '#2563eb';
    toast.textContent = message;

    container.appendChild(toast);

    // Auto-remove after 5 seconds
    setTimeout(function() {
        toast.style.animation = 'slideOut 0.3s ease';
        setTimeout(function() {
            toast.remove();
        }, 300);
    }, 5000);
}

// Add toast animations
if (!document.getElementById('toast-styles')) {
    const style = document.createElement('style');
    style.id = 'toast-styles';
    style.textContent = '@keyframes slideIn{from{transform:translateX(100%);opacity:0}to{transform:translateX(0);opacity:1}}@keyframes slideOut{from{transform:translateX(0);opacity:1}to{transform:translateX(100%);opacity:0}}';
    document.head.appendChild(style);
}

// Form validation helpers
function showFieldError(input, message) {
    const group = input.closest('.form-group');
    if (group) {
        group.classList.add('has-error');
        group.classList.remove('has-success');

        // Remove existing error
        const existing = group.querySelector('.form-error');
        if (existing) existing.remove();

        // Add error message
        const error = document.createElement('div');
        error.className = 'form-error';
        error.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>' + message;
        input.parentNode.insertBefore(error, input.nextSibling);
    }
}

function clearFieldError(input) {
    const group = input.closest('.form-group');
    if (group) {
        group.classList.remove('has-error');
        const error = group.querySelector('.form-error');
        if (error) error.remove();
    }
}

function showFieldSuccess(input) {
    const group = input.closest('.form-group');
    if (group) {
        group.classList.remove('has-error');
        group.classList.add('has-success');
    }
}

// Password strength checker for Alpine.js
function passwordStrength() {
    return {
        password: '',
        strengthPercent: 0,
        strengthClass: '',
        strengthText: '',

        checkStrength() {
            const pwd = this.password;
            let score = 0;

            if (pwd.length === 0) {
                this.strengthPercent = 0;
                this.strengthClass = '';
                this.strengthText = '';
                return;
            }

            // Length checks
            if (pwd.length >= 8) score += 1;
            if (pwd.length >= 12) score += 1;
            if (pwd.length >= 16) score += 1;

            // Character variety checks
            if (/[a-z]/.test(pwd)) score += 1;
            if (/[A-Z]/.test(pwd)) score += 1;
            if (/[0-9]/.test(pwd)) score += 1;
            if (/[^a-zA-Z0-9]/.test(pwd)) score += 1;

            // Common patterns (reduce score)
            if (/^[a-zA-Z]+$/.test(pwd)) score -= 1;
            if (/^[0-9]+$/.test(pwd)) score -= 1;
            if (/(.)\1{2,}/.test(pwd)) score -= 1; // Repeated chars

            // Normalize score
            score = Math.max(0, Math.min(score, 7));
            this.strengthPercent = Math.round((score / 7) * 100);

            // Set class and text based on score
            if (score <= 2) {
                this.strengthClass = 'strength-weak';
                this.strengthText = 'Weak';
            } else if (score <= 4) {
                this.strengthClass = 'strength-fair';
                this.strengthText = 'Fair';
            } else if (score <= 5) {
                this.strengthClass = 'strength-good';
                this.strengthText = 'Good';
            } else {
                this.strengthClass = 'strength-strong';
                this.strengthText = 'Strong';
            }
        }
    };
}

// Add password strength styles
if (!document.getElementById('password-strength-styles')) {
    const style = document.createElement('style');
    style.id = 'password-strength-styles';
    style.textContent = `
        .password-strength { margin-top: 0.5rem; }
        .password-strength-bar { height: 4px; background: var(--gray-200, #e5e7eb); border-radius: 2px; overflow: hidden; }
        .password-strength-fill { height: 100%; transition: width 0.3s ease, background 0.3s ease; }
        .password-strength-text { font-size: 0.75rem; margin-top: 0.25rem; display: block; }
        .strength-weak { background: #dc2626; color: #dc2626; }
        .strength-fair { background: #f59e0b; color: #f59e0b; }
        .strength-good { background: #10b981; color: #10b981; }
        .strength-strong { background: #059669; color: #059669; }
    `;
    document.head.appendChild(style);
}
