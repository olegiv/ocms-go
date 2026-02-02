/**
 * oCMS Unified Media Dropzone Component
 * Supports both "select" mode (choose from library) and "upload" mode (upload new files)
 */

function mediaDropzone() {
    return {
        // Common state
        mode: 'select',
        dragover: false,

        // Select mode state
        selectedImage: null,
        tempSelected: null,
        showModal: false,
        loading: false,
        mediaItems: [],
        searchQuery: '',
        filterType: '',
        currentPage: 1,
        totalPages: 1,
        perPage: 12,

        // Upload mode state
        files: [],
        uploading: false,
        progress: 0,
        maxSize: 0,
        allowedTypes: [],
        uploadUrl: '',
        redirectUrl: '',
        multiple: false,
        csrfToken: '',

        init() {
            const container = this.$el;
            this.mode = container.dataset.mode || 'select';

            if (this.mode === 'select') {
                this.initSelectMode(container);
            } else {
                this.initUploadMode(container);
            }
        },

        initSelectMode(container) {
            const initialImage = container.dataset.initialImage;
            if (initialImage && initialImage !== 'null') {
                try {
                    this.selectedImage = JSON.parse(initialImage);
                } catch (e) {
                    console.error('Failed to parse initial image:', e);
                }
            }
        },

        initUploadMode(container) {
            this.maxSize = parseInt(container.dataset.maxSize) || 10485760;
            this.uploadUrl = container.dataset.uploadUrl || '';
            this.redirectUrl = container.dataset.redirectUrl || '';
            this.multiple = container.dataset.multiple === 'true';

            const typesStr = container.dataset.allowedTypes || '';
            if (typesStr) {
                this.allowedTypes = typesStr.split(',').map(t => t.trim());
            }

            const csrfInput = document.querySelector('input[name="gorilla.csrf.Token"]');
            if (csrfInput) {
                this.csrfToken = csrfInput.value;
            }
        },

        // Common methods
        isImageType(mimetype) {
            return mimetype && mimetype.startsWith('image/');
        },

        formatSize(bytes) {
            if (bytes === 0) return '0 Bytes';
            const k = 1024;
            const sizes = ['Bytes', 'KB', 'MB', 'GB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
        },

        handleClick() {
            if (this.mode === 'select') {
                if (!this.selectedImage) {
                    this.openPicker();
                }
            } else {
                if (this.files.length === 0) {
                    this.$refs.fileInput.click();
                }
            }
        },

        handleDrop(event) {
            this.dragover = false;
            if (this.mode === 'upload') {
                const newFiles = Array.from(event.dataTransfer.files);
                this.addFiles(newFiles);
            }
        },

        // Select mode methods
        openPicker() {
            this.showModal = true;
            this.tempSelected = this.selectedImage;
            this.loadMedia();
        },

        closeModal() {
            this.showModal = false;
            this.tempSelected = null;
        },

        selectItem(item) {
            this.tempSelected = item;
        },

        confirmSelection() {
            this.selectedImage = this.tempSelected;
            this.closeModal();
        },

        clearSelection() {
            this.selectedImage = null;
        },

        async loadMedia() {
            this.loading = true;
            try {
                let url = `/admin/media/api?page=${this.currentPage}&limit=${this.perPage}`;
                if (this.searchQuery) {
                    url += `&q=${encodeURIComponent(this.searchQuery)}`;
                }
                if (this.filterType) {
                    url += `&type=${encodeURIComponent(this.filterType)}`;
                }

                const response = await fetch(url);
                if (response.ok) {
                    const data = await response.json();
                    this.mediaItems = data.items || [];
                    this.totalPages = data.totalPages || 1;
                } else {
                    this.mediaItems = [];
                }
            } catch (e) {
                console.error('Failed to load media:', e);
                this.mediaItems = [];
            } finally {
                this.loading = false;
            }
        },

        prevPage() {
            if (this.currentPage > 1) {
                this.currentPage--;
                this.loadMedia();
            }
        },

        nextPage() {
            if (this.currentPage < this.totalPages) {
                this.currentPage++;
                this.loadMedia();
            }
        },

        // Upload mode methods
        get hasErrors() {
            return this.files.some(f => f.error);
        },

        handleFiles(event) {
            const newFiles = Array.from(event.target.files);
            this.addFiles(newFiles);
        },

        addFiles(newFiles) {
            if (!this.multiple && newFiles.length > 0) {
                this.files = [];
            }

            for (const file of newFiles) {
                const fileData = {
                    file: file,
                    name: file.name,
                    size: file.size,
                    type: file.type,
                    preview: null,
                    error: null,
                    success: false
                };

                if (file.size > this.maxSize) {
                    fileData.error = 'File too large (max ' + this.formatSize(this.maxSize) + ')';
                }

                if (this.allowedTypes.length > 0 && !this.allowedTypes.includes(file.type)) {
                    fileData.error = 'Unsupported file type';
                }

                if (file.type.startsWith('image/') && !fileData.error) {
                    const reader = new FileReader();
                    reader.onload = (e) => {
                        fileData.preview = e.target.result;
                    };
                    reader.readAsDataURL(file);
                }

                this.files.push(fileData);
            }
        },

        removeFile(index) {
            this.files.splice(index, 1);
            if (this.files.length === 0 && this.$refs.fileInput) {
                this.$refs.fileInput.value = '';
            }
        },

        clearFiles() {
            this.files = [];
            if (this.$refs.fileInput) {
                this.$refs.fileInput.value = '';
            }
        },

        async upload() {
            if (this.files.length === 0 || this.hasErrors || !this.uploadUrl) return;

            this.uploading = true;
            this.progress = 0;

            const formData = new FormData();

            if (this.csrfToken) {
                formData.append('gorilla.csrf.Token', this.csrfToken);
            }

            const folderSelect = document.getElementById('folder_id');
            if (folderSelect && folderSelect.value) {
                formData.append('folder_id', folderSelect.value);
            }

            const validFiles = this.files.filter(f => !f.error);
            validFiles.forEach(f => {
                formData.append('files', f.file);
            });

            try {
                const xhr = new XMLHttpRequest();

                xhr.upload.onprogress = (e) => {
                    if (e.lengthComputable) {
                        this.progress = Math.round((e.loaded / e.total) * 100);
                    }
                };

                xhr.onload = () => {
                    if (xhr.status >= 200 && xhr.status < 300) {
                        this.files.forEach(f => f.success = true);
                        if (this.redirectUrl) {
                            window.location.href = this.redirectUrl;
                        }
                    } else {
                        let errorMsg = 'Upload failed';
                        try {
                            const response = JSON.parse(xhr.responseText);
                            if (response.error) errorMsg = response.error;
                        } catch (e) {
                            if (xhr.responseText) errorMsg = xhr.responseText;
                        }
                        if (typeof showToast === 'function') {
                            showToast(errorMsg, 'error');
                        } else {
                            alert(errorMsg);
                        }
                        this.uploading = false;
                    }
                };

                xhr.onerror = () => {
                    if (typeof showToast === 'function') {
                        showToast('Upload failed. Please try again.', 'error');
                    } else {
                        alert('Upload failed. Please try again.');
                    }
                    this.uploading = false;
                };

                xhr.open('POST', this.uploadUrl);
                xhr.send(formData);
            } catch (error) {
                if (typeof showToast === 'function') {
                    showToast('Upload failed: ' + error.message, 'error');
                } else {
                    alert('Upload failed: ' + error.message);
                }
                this.uploading = false;
            }
        }
    };
}
