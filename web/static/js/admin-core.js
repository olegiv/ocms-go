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

function asElement(target) {
    if (target instanceof Element) {
        return target;
    }
    if (target && target.parentElement instanceof Element) {
        return target.parentElement;
    }
    return null;
}

function closestMatch(target, selector) {
    const element = asElement(target);
    if (!element) {
        return null;
    }
    return element.closest(selector);
}

function normalizeBulkScope(scope) {
    return (scope || '').trim();
}

// Declarative helpers for CSP-safe interactions
document.addEventListener('change', function(e) {
    const perPageSelector = closestMatch(e.target, 'select[data-per-page-selector="true"]');
    if (perPageSelector) {
        const selectedValue = Number.parseInt(perPageSelector.value, 10);
        if (!Number.isInteger(selectedValue) || selectedValue <= 0) {
            return;
        }

        const paramName = perPageSelector.getAttribute('data-per-page-param') || 'per_page';
        const nextURL = new URL(window.location.href);
        nextURL.searchParams.set(paramName, String(selectedValue));
        nextURL.searchParams.set('page', '1');
        window.location.assign(nextURL.toString());
        return;
    }

    const autoSubmit = closestMatch(e.target, '[data-auto-submit="true"]');
    if (autoSubmit) {
        const form = autoSubmit.form || autoSubmit.closest('form');
        if (form) {
            form.submit();
        }
        return;
    }

    const syncTarget = closestMatch(e.target, '[data-sync-target]');
    if (syncTarget) {
        const targetId = syncTarget.getAttribute('data-sync-target');
        const target = targetId ? document.getElementById(targetId) : null;
        if (target) {
            target.value = syncTarget.value;
        }
    }

    const syncPicker = closestMatch(e.target, '[data-sync-picker]');
    if (syncPicker) {
        const pickerId = syncPicker.getAttribute('data-sync-picker');
        const picker = pickerId ? document.getElementById(pickerId) : null;
        if (picker) {
            picker.value = syncPicker.value;
        }
        return;
    }

    const bulkItem = closestMatch(e.target, 'input[type="checkbox"][data-bulk-item="true"]');
    if (bulkItem) {
        updateBulkScopeState(bulkItem.getAttribute('data-bulk-scope'));
        return;
    }

    const bulkMaster = closestMatch(e.target, 'input[type="checkbox"][data-bulk-master="true"]');
    if (bulkMaster) {
        setBulkScopeSelection(bulkMaster.getAttribute('data-bulk-scope'), bulkMaster.checked);
    }
});

document.addEventListener('input', function(e) {
    const syncPicker = closestMatch(e.target, '[data-sync-picker]');
    if (!syncPicker) {
        return;
    }
    const pickerId = syncPicker.getAttribute('data-sync-picker');
    const picker = pickerId ? document.getElementById(pickerId) : null;
    if (picker) {
        picker.value = syncPicker.value;
    }
});

document.addEventListener('click', function(e) {
    const clearBtn = closestMatch(e.target, '[data-clear-target]');
    if (clearBtn) {
        e.preventDefault();
        const targetId = clearBtn.getAttribute('data-clear-target');
        const target = targetId ? document.getElementById(targetId) : null;
        if (target) {
            target.value = '';
            target.focus();
        }
        return;
    }

    const historyBackBtn = closestMatch(e.target, '[data-history-back="true"]');
    if (!historyBackBtn) {
        const bulkAction = closestMatch(e.target, '[data-bulk-action]');
        if (!bulkAction) {
            return;
        }
        e.preventDefault();
        const scope = bulkAction.getAttribute('data-bulk-scope');
        const action = bulkAction.getAttribute('data-bulk-action');
        if (!scope || !action) {
            return;
        }

        if (action === 'select-all') {
            setBulkScopeSelection(scope, true);
            return;
        }
        if (action === 'deselect-all') {
            setBulkScopeSelection(scope, false);
            return;
        }
        if (action === 'delete-selected') {
            bulkDeleteSelected(scope);
        }
        return;
    }
    e.preventDefault();
    if (window.history.length > 1) {
        window.history.back();
        return;
    }
    window.location.href = '/admin';
});

function getBulkItemCheckboxes(scope) {
    const allItems = Array.from(document.querySelectorAll('input[type="checkbox"][data-bulk-item="true"]'));
    const normalizedScope = normalizeBulkScope(scope);
    if (!normalizedScope) {
        return allItems;
    }
    const scopedItems = allItems.filter(function(el) {
        return normalizeBulkScope(el.getAttribute('data-bulk-scope')) === normalizedScope;
    });
    if (scopedItems.length > 0) {
        return scopedItems;
    }
    return allItems;
}

function getBulkMasterCheckboxes(scope) {
    const allMasters = Array.from(document.querySelectorAll('input[type="checkbox"][data-bulk-master="true"]'));
    const normalizedScope = normalizeBulkScope(scope);
    if (!normalizedScope) {
        return allMasters;
    }
    const scopedMasters = allMasters.filter(function(el) {
        return normalizeBulkScope(el.getAttribute('data-bulk-scope')) === normalizedScope;
    });
    if (scopedMasters.length > 0) {
        return scopedMasters;
    }
    return allMasters;
}

function getBulkSelectedCountTargets(scope) {
    const allTargets = Array.from(document.querySelectorAll('[data-bulk-selected-count]'));
    const normalizedScope = normalizeBulkScope(scope);
    if (!normalizedScope) {
        return allTargets;
    }
    const scopedTargets = allTargets.filter(function(el) {
        return normalizeBulkScope(el.getAttribute('data-bulk-selected-count')) === normalizedScope;
    });
    if (scopedTargets.length > 0) {
        return scopedTargets;
    }
    return allTargets;
}

function getBulkControls(scope) {
    const controls = Array.from(document.querySelectorAll('[data-bulk-controls="true"]'));
    if (controls.length === 0) {
        return null;
    }
    const normalizedScope = normalizeBulkScope(scope);
    if (!normalizedScope) {
        return controls[0];
    }
    const scopedControls = controls.find(function(el) {
        return normalizeBulkScope(el.getAttribute('data-bulk-scope')) === normalizedScope;
    });
    if (scopedControls) {
        return scopedControls;
    }
    return controls[0];
}

function updateBulkScopeState(scope) {
    if (!scope) {
        return;
    }
    const items = getBulkItemCheckboxes(scope);
    const selectedCount = items.filter(function(item) { return item.checked; }).length;

    const masters = getBulkMasterCheckboxes(scope);
    masters.forEach(function(master) {
        master.checked = items.length > 0 && selectedCount === items.length;
        master.indeterminate = selectedCount > 0 && selectedCount < items.length;
    });

    const countTargets = getBulkSelectedCountTargets(scope);
    countTargets.forEach(function(target) {
        target.textContent = String(selectedCount);
    });
}

function setBulkScopeSelection(scope, checked) {
    if (!scope) {
        return;
    }
    const items = getBulkItemCheckboxes(scope);
    items.forEach(function(item) {
        item.checked = checked;
    });
    updateBulkScopeState(scope);
}

async function bulkDeleteSelected(scope) {
    const controls = getBulkControls(scope);
    if (!controls) {
        showToast('Bulk actions are not configured', 'error');
        return;
    }

    const selectedIDs = getBulkItemCheckboxes(scope)
        .filter(function(item) { return item.checked; })
        .map(function(item) { return Number.parseInt(item.getAttribute('data-bulk-id'), 10); })
        .filter(function(id) { return Number.isInteger(id) && id > 0; });

    const noneMessage = controls.getAttribute('data-bulk-none') || 'No items selected';
    if (selectedIDs.length === 0) {
        showToast(noneMessage, 'error');
        return;
    }

    const confirmMessage = controls.getAttribute('data-bulk-confirm') || 'Delete selected items?';
    if (!window.confirm(confirmMessage)) {
        return;
    }

    const deleteURL = controls.getAttribute('data-bulk-delete-url');
    if (!deleteURL) {
        showToast('Bulk delete URL is missing', 'error');
        return;
    }

    try {
        const response = await fetch(deleteURL, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Accept': 'application/json'
            },
            body: JSON.stringify({ ids: selectedIDs })
        });

        let payload;
        try {
            payload = await response.json();
        } catch (parseError) {
            payload = null;
        }

        if (!response.ok || !payload || payload.success !== true) {
            const errorMessage = payload && payload.error ? payload.error : (controls.getAttribute('data-bulk-error') || 'Bulk delete failed');
            showToast(errorMessage, 'error');
            return;
        }

        const deletedCount = Number.isFinite(payload.deleted) ? Number(payload.deleted) : 0;
        const failedCount = Array.isArray(payload.failed) ? payload.failed.length : 0;

        if (failedCount > 0) {
            const partialMessage = controls.getAttribute('data-bulk-partial') || 'Bulk delete partially completed';
            const totalCount = deletedCount + failedCount;
            showToast(partialMessage + ' (' + deletedCount + '/' + totalCount + ')', 'info');
        } else {
            const successMessage = controls.getAttribute('data-bulk-success') || 'Bulk delete completed';
            showToast(successMessage + ' (' + deletedCount + ')', 'success');
        }

        window.location.reload();
    } catch (error) {
        const errorMessage = controls.getAttribute('data-bulk-error') || 'Bulk delete failed';
        showToast(errorMessage, 'error');
    }
}

function initBulkScopes() {
    const scopes = new Set();
    Array.from(document.querySelectorAll('[data-bulk-scope]')).forEach(function(el) {
        const scope = el.getAttribute('data-bulk-scope');
        if (scope) {
            scopes.add(scope);
        }
    });
    scopes.forEach(function(scope) {
        updateBulkScopeState(scope);
    });
}

initBulkScopes();

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
