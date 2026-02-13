/**
 * oCMS Menu Builder
 * Alpine.js component for menu item CRUD operations and drag-drop sorting.
 *
 * Reads configuration from data-* attributes on the root element:
 *   data-menu-id           - Menu ID (integer)
 *   data-items             - JSON-encoded menu item tree
 *   data-i18n-confirm-delete - Translated "confirm delete" prompt
 *   data-i18n-item-page    - Translated "Page" label
 *   data-i18n-item-custom  - Translated "Custom Link" label
 */

function menuBuilder() {
    return {
        // Properties
        menuId: 0,
        items: [],
        sortIteration: 0,
        customLink: { title: '', url: '', target: '_self', parent_id: 0 },
        pagesParentId: 0,
        selectedPages: [],
        editingItem: null,
        hasChanges: false,
        saving: false,
        tempIdCounter: 0,

        // i18n labels (populated from data attributes in init)
        labels: {
            confirmDelete: 'Are you sure?',
            itemPage: 'Page',
            itemCustom: 'Custom Link'
        },

        init() {
            // Read all dynamic values from data-* attributes
            const el = this.$el;
            this.menuId = parseInt(el.dataset.menuId) || 0;
            this.items = this.parseItems(JSON.parse(el.dataset.items || '[]'));

            // Load i18n strings
            this.labels.confirmDelete = el.dataset.i18nConfirmDelete || 'Are you sure?';
            this.labels.itemPage = el.dataset.i18nItemPage || 'Page';
            this.labels.itemCustom = el.dataset.i18nItemCustom || 'Custom Link';
        },

        // =====================================================================
        // Sort handlers
        // =====================================================================

        /**
         * Handle @alpinejs/sort drag-drop reordering for root items.
         * Uses sortIteration to force Alpine DOM recreation (fixes x-for + x-sort conflict).
         */
        handleSort(itemKey, newPosition) {
            const oldIndex = this.items.findIndex(i => (i.id || i.tempId) == itemKey);
            if (oldIndex !== -1 && oldIndex !== newPosition) {
                const item = this.items.splice(oldIndex, 1)[0];
                this.items.splice(newPosition, 0, item);
                this.sortIteration++;
                this.hasChanges = true;
            }
        },

        /**
         * Handle sorting for level 2 children.
         */
        handleChildSort(parentItem, childKey, newPosition) {
            const parent = this.findItemById(parentItem.id || parentItem.tempId);
            if (!parent || !parent.children) return;
            const oldIndex = parent.children.findIndex(c => (c.id || c.tempId) == childKey);
            if (oldIndex !== -1 && oldIndex !== newPosition) {
                const child = parent.children.splice(oldIndex, 1)[0];
                parent.children.splice(newPosition, 0, child);
                this.sortIteration++;
                this.hasChanges = true;
            }
        },

        /**
         * Handle sorting for level 3 grandchildren.
         */
        handleGrandchildSort(parentChild, grandchildKey, newPosition) {
            const parent = this.findItemById(parentChild.id || parentChild.tempId);
            if (!parent || !parent.children) return;
            const oldIndex = parent.children.findIndex(g => (g.id || g.tempId) == grandchildKey);
            if (oldIndex !== -1 && oldIndex !== newPosition) {
                const grandchild = parent.children.splice(oldIndex, 1)[0];
                parent.children.splice(newPosition, 0, grandchild);
                this.sortIteration++;
                this.hasChanges = true;
            }
        },

        // =====================================================================
        // Data parsing
        // =====================================================================

        /**
         * Parse a single server item into client-side format.
         */
        parseItem(item) {
            let parentId = null;
            if (item.parent_id !== null && item.parent_id !== undefined) {
                if (typeof item.parent_id === 'object' && item.parent_id.Valid) {
                    parentId = item.parent_id.Int64;
                } else if (typeof item.parent_id === 'number') {
                    parentId = item.parent_id;
                }
            }

            return {
                id: item.id,
                title: item.title,
                url: typeof item.url === 'string' ? item.url : (item.url?.String || ''),
                target: typeof item.target === 'string' ? item.target : (item.target?.String || '_self'),
                page_id: (item.page_id !== null && typeof item.page_id === 'object')
                    ? (item.page_id.Valid ? item.page_id.Int64 : null)
                    : item.page_id,
                parent_id: parentId,
                css_class: typeof item.css_class === 'string' ? item.css_class : (item.css_class?.String || ''),
                is_active: item.is_active,
                children: []
            };
        },

        /**
         * Parse server items into client-side format.
         */
        parseItems(serverItems) {
            if (!serverItems) return [];
            return serverItems.map(node => {
                const item = this.parseItem(node.Item);
                item.children = node.Children ? this.parseItems(node.Children) : [];
                return item;
            });
        },

        // =====================================================================
        // Tree operations
        // =====================================================================

        /**
         * Find an item by ID recursively (supports both real IDs and temp IDs).
         */
        findItemById(id, items = this.items) {
            const numId = parseInt(id);
            for (const item of items) {
                if (item.id === numId || item.tempId === id) return item;
                if (item.children && item.children.length > 0) {
                    const found = this.findItemById(id, item.children);
                    if (found) return found;
                }
            }
            return null;
        },

        /**
         * Flatten items tree into a single array with depth for parent dropdown.
         */
        flattenItems() {
            const flatten = (items, depth = 0) => {
                let result = [];
                for (const item of items) {
                    result.push({ id: item.id, title: item.title, depth });
                    if (item.children && item.children.length > 0) {
                        result = result.concat(flatten(item.children, depth + 1));
                    }
                }
                return result;
            };
            return flatten(this.items);
        },

        /**
         * Add an item to the correct location in the tree.
         */
        addItemToTree(item, parentId) {
            if (!parentId) {
                this.items.push(item);
            } else {
                const parent = this.findItemById(parentId);
                if (parent) {
                    if (!parent.children) parent.children = [];
                    parent.children.push(item);
                } else {
                    this.items.push(item);
                }
            }
        },

        /**
         * Remove an item from the tree recursively and return it.
         */
        removeItemFromTree(itemId, items = this.items) {
            for (let i = 0; i < items.length; i++) {
                if (items[i].id === itemId) {
                    return items.splice(i, 1)[0];
                }
                if (items[i].children && items[i].children.length > 0) {
                    const removed = this.removeItemFromTree(itemId, items[i].children);
                    if (removed) return removed;
                }
            }
            return null;
        },

        // =====================================================================
        // Page selection
        // =====================================================================

        isPageSelected(pageId) {
            return this.selectedPages.some(p => p.id === pageId);
        },

        togglePage(pageId, title, slug) {
            const index = this.selectedPages.findIndex(p => p.id === pageId);
            if (index === -1) {
                this.selectedPages.push({ id: pageId, title: title, slug: slug });
            } else {
                this.selectedPages.splice(index, 1);
            }
        },

        // =====================================================================
        // CRUD operations (async API calls)
        // =====================================================================

        async addCustomLink() {
            if (!this.customLink.title || !this.customLink.url) return;

            try {
                const parentId = this.customLink.parent_id ? parseInt(this.customLink.parent_id) : null;
                const response = await fetch(`/admin/menus/${this.menuId}/items`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        title: this.customLink.title,
                        url: this.customLink.url,
                        target: this.customLink.target,
                        parent_id: parentId
                    })
                });

                if (response.ok) {
                    const data = await response.json();
                    const newItem = this.parseItem(data.item);
                    this.addItemToTree(newItem, parentId);
                    this.customLink = { title: '', url: '', target: '_self', parent_id: 0 };
                }
            } catch (e) {
                console.error('Failed to add custom link:', e);
            }
        },

        async addSelectedPages() {
            const parentId = this.pagesParentId ? parseInt(this.pagesParentId) : null;
            for (const page of this.selectedPages) {
                try {
                    const response = await fetch(`/admin/menus/${this.menuId}/items`, {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
                            title: page.title,
                            page_id: page.id,
                            parent_id: parentId,
                            target: '_self'
                        })
                    });

                    if (response.ok) {
                        const data = await response.json();
                        const newItem = this.parseItem(data.item);
                        this.addItemToTree(newItem, parentId);
                    }
                } catch (e) {
                    console.error('Failed to add page:', e);
                }
            }
            this.selectedPages = [];
        },

        editItem(item) {
            const parentId = (item.parent_id === null || item.parent_id === undefined)
                ? "0"
                : String(item.parent_id);

            this.editingItem = { ...item, parent_id: "0" };

            this.$nextTick(() => {
                this.editingItem.parent_id = parentId;
            });
        },

        async saveItem() {
            if (!this.editingItem || !this.editingItem.id) return;

            const newParentId = this.editingItem.parent_id ? parseInt(this.editingItem.parent_id) : 0;

            try {
                const response = await fetch(`/admin/menus/${this.menuId}/items/${this.editingItem.id}`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        title: this.editingItem.title,
                        url: this.editingItem.url,
                        target: this.editingItem.target,
                        css_class: this.editingItem.css_class,
                        is_active: this.editingItem.is_active,
                        page_id: this.editingItem.page_id,
                        parent_id: newParentId
                    })
                });

                if (response.ok) {
                    const itemId = this.editingItem.id;
                    const removedItem = this.removeItemFromTree(itemId);

                    if (removedItem) {
                        Object.assign(removedItem, this.editingItem);
                        removedItem.parent_id = newParentId || null;
                        this.addItemToTree(removedItem, newParentId || null);
                    }

                    this.editingItem = null;
                }
            } catch (e) {
                console.error('Failed to save item:', e);
            }
        },

        async deleteItem(item, index) {
            if (!confirm(this.labels.confirmDelete)) return;

            if (item.id) {
                try {
                    const response = await fetch(`/admin/menus/${this.menuId}/items/${item.id}`, {
                        method: 'DELETE'
                    });

                    if (response.ok) {
                        this.items.splice(index, 1);
                    }
                } catch (e) {
                    console.error('Failed to delete item:', e);
                }
            } else {
                this.items.splice(index, 1);
            }
        },

        async deleteChildItem(parent, childIndex) {
            if (!confirm(this.labels.confirmDelete)) return;

            const child = parent.children[childIndex];
            if (child.id) {
                try {
                    const response = await fetch(`/admin/menus/${this.menuId}/items/${child.id}`, {
                        method: 'DELETE'
                    });

                    if (response.ok) {
                        parent.children.splice(childIndex, 1);
                    }
                } catch (e) {
                    console.error('Failed to delete child item:', e);
                }
            } else {
                parent.children.splice(childIndex, 1);
            }
        },

        async deleteGrandchildItem(parent, grandchildIndex) {
            if (!confirm(this.labels.confirmDelete)) return;

            const grandchild = parent.children[grandchildIndex];
            if (grandchild.id) {
                try {
                    const response = await fetch(`/admin/menus/${this.menuId}/items/${grandchild.id}`, {
                        method: 'DELETE'
                    });

                    if (response.ok) {
                        parent.children.splice(grandchildIndex, 1);
                    }
                } catch (e) {
                    console.error('Failed to delete grandchild item:', e);
                }
            } else {
                parent.children.splice(grandchildIndex, 1);
            }
        },

        async saveOrder() {
            this.saving = true;

            const buildTree = (items) => {
                return items.map(item => ({
                    id: item.id,
                    children: item.children ? buildTree(item.children) : []
                }));
            };

            try {
                const response = await fetch(`/admin/menus/${this.menuId}/reorder`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ items: buildTree(this.items) })
                });

                if (response.ok) {
                    this.hasChanges = false;
                }
            } catch (e) {
                console.error('Failed to save order:', e);
            } finally {
                this.saving = false;
            }
        }
    };
}
