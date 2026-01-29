/**
 * oCMS Menu Builder JavaScript
 * Alpine.js methods for menu item CRUD operations and drag-drop sorting
 *
 * Usage: Spread menuBuilderMethods into your Alpine.js component
 * The component must have: menuId, items, i18n.confirmDelete properties
 */

const menuBuilderMethods = {
    /**
     * Parse a single server item into client-side format
     */
    parseItem(item) {
        // Handle parent_id from server (could be null, number, or object with Valid/Int64)
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
     * Parse server items into client-side format
     */
    parseItems(serverItems) {
        if (!serverItems) return [];
        return serverItems.map(node => {
            const item = this.parseItem(node.Item);
            item.children = node.Children ? this.parseItems(node.Children) : [];
            return item;
        });
    },

    /**
     * Find an item by ID recursively (supports both real IDs and temp IDs)
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
     * Flatten items tree into a single array with depth for parent dropdown
     * Returns: [{id, title, depth}, ...]
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
     * Check if a page is selected
     */
    isPageSelected(pageId) {
        return this.selectedPages.some(p => p.id === pageId);
    },

    /**
     * Toggle page selection
     */
    togglePage(pageId, title, slug) {
        const index = this.selectedPages.findIndex(p => p.id === pageId);
        if (index === -1) {
            this.selectedPages.push({ id: pageId, title: title, slug: slug });
        } else {
            this.selectedPages.splice(index, 1);
        }
    },

    /**
     * Add a custom link menu item
     */
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

    /**
     * Add selected pages as menu items
     */
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
                        parent_id: parentId
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

    /**
     * Add an item to the correct location in the tree
     */
    addItemToTree(item, parentId) {
        if (!parentId) {
            // Add to root level
            this.items.push(item);
        } else {
            // Find parent and add to its children
            const parent = this.findItemById(parentId);
            if (parent) {
                if (!parent.children) parent.children = [];
                parent.children.push(item);
            } else {
                // Parent not found, add to root as fallback
                this.items.push(item);
            }
        }
    },

    /**
     * Open edit modal for an item
     */
    editItem(item) {
        // Convert parent_id to string for dropdown binding (select option values are strings)
        // Use "0" for null/undefined (top level)
        const parentId = (item.parent_id === null || item.parent_id === undefined)
            ? "0"
            : String(item.parent_id);

        // First set editingItem with placeholder parent_id to trigger modal render
        this.editingItem = { ...item, parent_id: "0" };

        // Use $nextTick to set correct parent_id after DOM renders the select options
        this.$nextTick(() => {
            this.editingItem.parent_id = parentId;
        });
    },

    /**
     * Save edited item
     */
    async saveItem() {
        if (!this.editingItem || !this.editingItem.id) return;

        // Determine parent_id to send: 0 means root, number means that parent
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
                // Find and remove item from its current location
                const itemId = this.editingItem.id;
                const removedItem = this.removeItemFromTree(itemId);

                if (removedItem) {
                    // Update item properties
                    Object.assign(removedItem, this.editingItem);
                    removedItem.parent_id = newParentId || null;

                    // Add to new location
                    this.addItemToTree(removedItem, newParentId || null);
                }

                this.editingItem = null;
            }
        } catch (e) {
            console.error('Failed to save item:', e);
        }
    },

    /**
     * Remove an item from the tree recursively and return it
     */
    removeItemFromTree(itemId, items = this.items) {
        for (let i = 0; i < items.length; i++) {
            if (items[i].id === itemId) {
                return items.splice(i, 1)[0];
            }
            // Recursively check children at any depth
            if (items[i].children && items[i].children.length > 0) {
                const removed = this.removeItemFromTree(itemId, items[i].children);
                if (removed) return removed;
            }
        }
        return null;
    },

    /**
     * Delete a menu item
     */
    async deleteItem(item, index) {
        if (!confirm(this.i18n.confirmDelete)) return;

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

    /**
     * Delete a child menu item
     */
    async deleteChildItem(parent, childIndex) {
        if (!confirm(this.i18n.confirmDelete)) return;

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

    /**
     * Delete a grandchild (level 3) menu item
     */
    async deleteGrandchildItem(parent, grandchildIndex) {
        if (!confirm(this.i18n.confirmDelete)) return;

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

    /**
     * Save the current menu order
     */
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
