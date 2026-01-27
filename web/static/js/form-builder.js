/**
 * oCMS Form Builder JavaScript
 * Handles form field CRUD operations, sorting, and modal management
 */

// Global form ID - set by template
let _formBuilderId = null;

/**
 * Initialize the form builder with the given form ID
 * @param {number} formId - The ID of the form being edited
 */
function initFormBuilder(formId) {
    _formBuilderId = formId;

    // Initialize sortable for fields
    const fieldsList = document.getElementById('fields-list');
    if (fieldsList) {
        new Sortable(fieldsList, {
            animation: 150,
            handle: '.field-item-handle',
            onEnd: function() {
                saveFieldOrder();
            }
        });
    }

    // Auto-generate slug from name (for new forms)
    const nameInput = document.getElementById('name');
    const slugInput = document.getElementById('slug');

    if (nameInput && slugInput && !slugInput.value) {
        nameInput.addEventListener('input', function() {
            if (!slugInput.dataset.userEdited) {
                slugInput.value = nameInput.value
                    .toLowerCase()
                    .replace(/[^a-z0-9]+/g, '-')
                    .replace(/^-+|-+$/g, '');
            }
        });

        slugInput.addEventListener('input', function() {
            slugInput.dataset.userEdited = 'true';
        });
    }
}

/**
 * Toggle visibility of options field based on field type
 */
function toggleOptionsField() {
    const type = document.getElementById('field-type').value;
    const optionsGroup = document.getElementById('options-group');
    if (type === 'select' || type === 'radio' || type === 'checkbox') {
        optionsGroup.style.display = 'block';
    } else {
        optionsGroup.style.display = 'none';
    }
}

/**
 * Open the add field modal with empty form
 */
function openAddFieldModal() {
    document.getElementById('field-modal-title').textContent = 'Add Field';
    document.getElementById('field-id').value = '';
    document.getElementById('field-form').reset();
    document.getElementById('options-group').style.display = 'none';
    document.getElementById('field-modal').style.display = 'flex';
}

/**
 * Open the edit field modal with existing field data
 */
function openEditFieldModal(id, type, name, label, placeholder, helpText, options, validation, isRequired) {
    document.getElementById('field-modal-title').textContent = 'Edit Field';
    document.getElementById('field-id').value = id;
    document.getElementById('field-type').value = type;
    document.getElementById('field-label').value = label;
    document.getElementById('field-name').value = name;
    document.getElementById('field-placeholder').value = placeholder;
    document.getElementById('field-help-text').value = helpText;
    document.getElementById('field-required').checked = isRequired;

    // Parse options JSON to text
    if (options && options !== '[]') {
        try {
            const optionsArr = JSON.parse(options);
            document.getElementById('field-options').value = optionsArr.join('\n');
        } catch (e) {
            document.getElementById('field-options').value = '';
        }
    } else {
        document.getElementById('field-options').value = '';
    }

    toggleOptionsField();
    document.getElementById('field-modal').style.display = 'flex';
}

/**
 * Close the field modal
 */
function closeFieldModal() {
    document.getElementById('field-modal').style.display = 'none';
}

/**
 * Save field (create or update)
 */
function saveField() {
    const fieldId = document.getElementById('field-id').value;
    const type = document.getElementById('field-type').value;
    const label = document.getElementById('field-label').value;
    const name = document.getElementById('field-name').value;
    const placeholder = document.getElementById('field-placeholder').value;
    const helpText = document.getElementById('field-help-text').value;
    const isRequired = document.getElementById('field-required').checked;

    // Convert options text to JSON array
    const optionsText = document.getElementById('field-options').value;
    let options = '[]';
    if (optionsText.trim()) {
        const optionsArr = optionsText.split('\n').map(o => o.trim()).filter(o => o);
        options = JSON.stringify(optionsArr);
    }

    if (!label.trim()) {
        alert('Label is required');
        return;
    }

    const data = {
        type: type,
        label: label,
        name: name,
        placeholder: placeholder,
        help_text: helpText,
        options: options,
        validation: '{}',
        is_required: isRequired
    };

    const url = fieldId
        ? `/admin/forms/${_formBuilderId}/fields/${fieldId}`
        : `/admin/forms/${_formBuilderId}/fields`;
    const method = fieldId ? 'PUT' : 'POST';

    fetch(url, {
        method: method,
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify(data)
    })
    .then(response => response.json())
    .then(result => {
        if (result.success) {
            window.location.reload();
        } else {
            alert('Error saving field');
        }
    })
    .catch(error => {
        console.error('Error:', error);
        alert('Error saving field');
    });
}

/**
 * Delete a field
 * @param {number} fieldId - The ID of the field to delete
 */
function deleteField(fieldId) {
    if (!confirm('Are you sure you want to delete this field?')) {
        return;
    }

    fetch(`/admin/forms/${_formBuilderId}/fields/${fieldId}`, {
        method: 'DELETE'
    })
    .then(response => response.json())
    .then(result => {
        if (result.success) {
            const fieldEl = document.querySelector(`[data-field-id="${fieldId}"]`);
            if (fieldEl) {
                fieldEl.remove();
            }
            // Show empty state if no fields left
            const remaining = document.querySelectorAll('.field-item');
            if (remaining.length === 0) {
                document.getElementById('fields-list').innerHTML = '<div class="empty-state" id="no-fields-message"><p>No fields added yet. Click "Add Field" to get started.</p></div>';
            }
        } else {
            alert('Error deleting field');
        }
    })
    .catch(error => {
        console.error('Error:', error);
        alert('Error deleting field');
    });
}

/**
 * Save the current field order
 */
function saveFieldOrder() {
    const fieldItems = document.querySelectorAll('.field-item');
    const fieldIds = Array.from(fieldItems).map(item => parseInt(item.dataset.fieldId));

    fetch(`/admin/forms/${_formBuilderId}/fields/reorder`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({ field_ids: fieldIds })
    })
    .then(response => response.json())
    .then(result => {
        if (!result.success) {
            console.error('Error saving field order');
        }
    })
    .catch(error => {
        console.error('Error:', error);
    });
}
