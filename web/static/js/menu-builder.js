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
        return {
            id: item.id,
            title: item.title,
            url: typeof item.url === 'string' ? item.url : (item.url?.String || ''),
            target: typeof item.target === 'string' ? item.target : (item.target?.String || '_self'),
            page_id: (item.page_id !== null && typeof item.page_id === 'object')
            ? (item.page_id.Valid ? item.page_id.Int64 : null)
            : item.page_id,
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
     * Find an item by ID (supports both real IDs and temp IDs)
     */
    findItemById(id) {
        const numId = parseInt(id);
        for (const item of this.items) {
            if (item.id === numId || item.tempId === id) return item;
            if (item.children) {
                for (const child of item.children) {
                    if (child.id === numId || child.tempId === id) return child;
                }
            }
        }
        return null;
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
            const response = await fetch(`/admin/menus/${this.menuId}/items`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    title: this.customLink.title,
                    url: this.customLink.url,
                    target: this.customLink.target
                })
            });

            if (response.ok) {
                const data = await response.json();
                this.items.push(this.parseItem(data.item));
                this.customLink = { title: '', url: '', target: '_self' };
            }
        } catch (e) {
            console.error('Failed to add custom link:', e);
        }
    },

    /**
     * Add selected pages as menu items
     */
    async addSelectedPages() {
        for (const page of this.selectedPages) {
            try {
                const response = await fetch(`/admin/menus/${this.menuId}/items`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        title: page.title,
                        page_id: page.id
                    })
                });

                if (response.ok) {
                    const data = await response.json();
                    this.items.push(this.parseItem(data.item));
                }
            } catch (e) {
                console.error('Failed to add page:', e);
            }
        }
        this.selectedPages = [];
    },

    /**
     * Open edit modal for an item
     */
    editItem(item) {
        this.editingItem = { ...item };
    },

    /**
     * Save edited item
     */
    async saveItem() {
        if (!this.editingItem || !this.editingItem.id) return;

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
                    page_id: this.editingItem.page_id
                })
            });

            if (response.ok) {
                // Update local item
                const updateItem = (items) => {
                    for (let i = 0; i < items.length; i++) {
                        if (items[i].id === this.editingItem.id) {
                            items[i] = { ...items[i], ...this.editingItem };
                            return true;
                        }
                        if (items[i].children && updateItem(items[i].children)) {
                            return true;
                        }
                    }
                    return false;
                };
                updateItem(this.items);
                this.editingItem = null;
            }
        } catch (e) {
            console.error('Failed to save item:', e);
        }
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
