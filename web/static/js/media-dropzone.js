/**
 * oCMS Unified Media Dropzone Component
 * Supports both "select" mode (choose from library) and "upload" mode (upload new files)
 */

// safeRedirectTarget normalizes a redirect URL to a same-origin http(s) target,
// returning '' when the input is missing, malformed, or points off-origin. The
// only current call site is `data-redirect-url` in admin templates, but DOM
// attributes are an open surface — defense-in-depth against a future caller
// wiring an attacker-controllable value into the dataset.
function safeRedirectTarget(raw) {
    if (typeof raw !== 'string' || raw.trim() === '') return '';
    try {
        const parsed = new URL(raw, window.location.origin);
        if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return '';
        if (parsed.origin !== window.location.origin) return '';
        return parsed.pathname + parsed.search + parsed.hash;
    } catch (e) {
        return '';
    }
}

document.addEventListener('alpine:init', () => Alpine.data('mediaDropzone', mediaDropzone));

// Shared by Alpine, TinyMCE and theme pickers. All request state stays in this
// closure so closing one picker cannot update another picker's form fields.
function initMediaPickerUpload(root, onUploaded) {
    if (!root) return null;
    if (root.mediaPickerUploader) return root.mediaPickerUploader;
    const input = root.querySelector('[data-upload-file]');
    const allowedFormats = root.dataset.allowedFormats.split(',');
    const choose = root.querySelector('[data-upload-choose]');
    const progress = root.querySelector('[data-upload-progress]');
    const status = root.querySelector('[data-upload-status]');
    let active = null;
    const busy = (value) => {
        choose.disabled = value;
        input.disabled = value;
        progress.hidden = !value;
        root.setAttribute('aria-busy', String(value));
    };
    const reset = () => {
        const previous = active;
        active = null;
        if (previous) previous.abort();
        busy(false);
        input.value = '';
        status.textContent = '';
        status.classList.remove('form-error');
    };
    choose.addEventListener('click', () => { if (!active) input.click(); });
    input.addEventListener('change', () => {
        if (active) return;
        const file = input.files[0];
        input.value = ''; // Choosing the same image after a failure retries it.
        if (!file) return;
        status.classList.add('form-error');
        if (file.size > Number(root.dataset.maxSize)) {
            status.textContent = root.dataset.tooLarge;
            return;
        }
        const extension = file.name.slice(file.name.lastIndexOf('.')).toLowerCase();
        if (!allowedFormats.includes(extension)) {
            status.textContent = root.dataset.invalid;
            return;
        }
        const xhr = new XMLHttpRequest();
        active = xhr;
        busy(true);
        progress.value = 0;
        status.classList.remove('form-error');
        status.textContent = root.dataset.uploading;
        const fail = (message) => {
            if (active !== xhr) return;
            active = null;
            busy(false);
            status.classList.add('form-error');
            status.textContent = message || root.dataset.error;
        };
        xhr.upload.onprogress = (event) => {
            if (active === xhr && event.lengthComputable) progress.value = Math.round(event.loaded / event.total * 100);
        };
        xhr.onerror = () => fail();
        xhr.ontimeout = () => fail();
        xhr.onload = () => {
            if (active !== xhr || !root.isConnected) return;
            const json = xhr.getResponseHeader('Content-Type')?.split(';')[0].trim() === 'application/json';
            const item = json && xhr.response?.item;
            if (xhr.status !== 201 || !item || !Number.isSafeInteger(item.id) || item.id <= 0 ||
                typeof item.filepath !== 'string' || typeof item.filename !== 'string' || !item.mimetype?.startsWith('image/')) {
                // API diagnostics are not UI translations. Keep every picker
                // in the page's language, including server-side refusals.
                const messages = {400: root.dataset.invalid, 413: root.dataset.tooLarge, 403: root.dataset.forbidden};
                fail(messages[xhr.status]);
                return;
            }
            active = null;
            busy(false);
            status.textContent = '';
            onUploaded(item);
        };
        const body = new FormData();
        body.append('file', file);
        xhr.open('POST', '/admin/media/upload');
        xhr.setRequestHeader('Accept', 'application/json');
        xhr.responseType = 'json';
        xhr.send(body);
    });
    root.mediaPickerUploader = { reset };
    return root.mediaPickerUploader;
}

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
        mediaRequest: 0,
        pinnedUpload: null,
        pickerUploader: null,

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
            this.pickerUploader = initMediaPickerUpload(container.querySelector('[data-media-picker-upload]'), item => {
                this.searchQuery = '';
                this.filterType = '';
                this.currentPage = 1;
                this.pinnedUpload = item;
                this.tempSelected = item;
                this.loadMedia();
            });
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
            this.redirectUrl = safeRedirectTarget(container.dataset.redirectUrl);
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
            this.pickerUploader?.reset();
            this.showModal = true;
            this.tempSelected = this.selectedImage;
            this.loadMedia();
        },

        closeModal() {
            this.pickerUploader?.reset();
            this.mediaRequest++;
            this.pinnedUpload = null;
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

        refilter() {
            this.currentPage = 1;
            this.loadMedia(true);
        },

        async loadMedia(refilter = false) {
            const request = ++this.mediaRequest;
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
                    if (request !== this.mediaRequest) return;
                    this.mediaItems = data.items || [];
                    this.totalPages = data.totalPages || 1;
                } else {
                    if (request !== this.mediaRequest) return;
                    this.mediaItems = [];
                }
            } catch (e) {
                if (request !== this.mediaRequest) return;
                console.error('Failed to load media:', e);
                this.mediaItems = [];
            } finally {
                if (request === this.mediaRequest) {
                    // The POST result remains selectable even if refreshing the
                    // library fails or a concurrent upload changes page ordering.
                    if (this.pinnedUpload && !this.searchQuery && !this.filterType && this.currentPage === 1) {
                        this.mediaItems = [this.pinnedUpload, ...this.mediaItems.filter(item => item.id !== this.pinnedUpload.id)];
                    }
                    if (refilter && this.tempSelected && !this.mediaItems.some(item => item.id === this.tempSelected.id)) {
                        this.tempSelected = null;
                    }
                    this.loading = false;
                }
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

            const folderInput = document.querySelector('input[name="folder_id"]');
            if (folderInput && folderInput.value) {
                formData.append('folder_id', folderInput.value);
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
