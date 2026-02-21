// File Upload Handler - ES5 compatible
// Handles file selection, validation, compression, and preview rendering

(function() {
    // File upload state
    var pendingFiles = [];

    // Supported file types and their icons
    var SUPPORTED_FILE_TYPES = {
        // Images
        'image/': { icon: '🖼️', category: 'image' },
        // Videos
        'video/': { icon: '🎬', category: 'video' },
        // Audios
        'audio/': { icon: '🎵', category: 'audio' },
        // Documents
        'application/pdf': { icon: '📄', category: 'document' },
        'text/plain': { icon: '📝', category: 'document' },
        'application/msword': { icon: '📘', category: 'document' },
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document': { icon: '📘', category: 'document' },
        'application/vnd.ms-excel': { icon: '📊', category: 'document' },
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': { icon: '📊', category: 'document' },
        'application/vnd.ms-powerpoint': { icon: '📽️', category: 'document' },
        'application/vnd.openxmlformats-officedocument.presentationml.presentation': { icon: '📽️', category: 'document' },
        'text/csv': { icon: '📊', category: 'document' }
    };

    // Maximum file size (50MB)
    var MAX_FILE_SIZE = 50 * 1024 * 1024;

    // Check if a file type is supported
    function isFileTypeSupported(fileType, fileName) {
        // Check by MIME type
        for (var typePrefix in SUPPORTED_FILE_TYPES) {
            if (fileType.startsWith(typePrefix)) {
                return true;
            }
        }
        // Check by file extension for office documents
        var ext = fileName.toLowerCase().split('.').pop();
        var supportedExtensions = ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'csv', 'txt'];
        return supportedExtensions.includes(ext);
    }

    // Get file icon based on type
    function getFileIcon(fileType, fileName) {
        // Check by MIME type
        for (var typePrefix in SUPPORTED_FILE_TYPES) {
            if (fileType.startsWith(typePrefix)) {
                return SUPPORTED_FILE_TYPES[typePrefix].icon;
            }
        }
        // Default icon
        return '📎';
    }

    // Convert Blob to base64 Data URL
    function blobToBase64(blob) {
        return new Promise(function(resolve, reject) {
            var reader = new FileReader();
            reader.onloadend = function() { resolve(reader.result); };
            reader.onerror = reject;
            reader.readAsDataURL(blob);
        });
    }

    // Format file size for display
    function formatFileSize(bytes) {
        if (bytes === 0) return '0 B';
        var k = 1024;
        var sizes = ['B', 'KB', 'MB', 'GB'];
        var i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
    }

    // Handle file selection
    async function handleFiles(files) {
        if (!files || files.length === 0) return;

        // Show loading indicator if compressing images
        var compressionStarted = false;

        for (var i = 0; i < files.length; i++) {
            var file = files[i];

            // Validate file type
            if (!isFileTypeSupported(file.type, file.name)) {
                showToast('Unsupported file type: ' + file.name, true);
                continue;
            }

            // Validate file size (max 50MB)
            if (file.size > MAX_FILE_SIZE) {
                showToast('File size must be less than 50MB: ' + file.name, true);
                continue;
            }

            // Compress images before adding to pending files
            if (file.type.startsWith('image/')) {
                if (!compressionStarted) {
                    setStatus('Compressing images...', false);
                    compressionStarted = true;
                }

                try {
                    // Check if compression is needed
                    var needsCompression = ImageCompressor.needsCompression(file, 500); // 500KB threshold

                    if (needsCompression) {
                        // Compress the image
                        var result = await ImageCompressor.compress(file, {
                            maxWidth: 1920,
                            maxHeight: 1080,
                            quality: 0.85,
                            maxSizeKB: 500,
                            minQuality: 0.6,
                            mimeType: 'image/jpeg'
                        });

                        console.log('Image compressed: ' + file.name + ', ' + result.ratio + '% reduction (' + formatFileSize(result.originalSize) + ' -> ' + formatFileSize(result.compressedSize) + ')');

                        // Convert Blob to base64 for transmission
                        var base64Url = await blobToBase64(result.blob);

                        // Add compressed image to pending files
                        pendingFiles.push({
                            url: base64Url,
                            name: file.name.replace(/\.(png|gif|webp|bmp)$/i, '.jpg'),
                            type: result.mimeType,
                            size: result.compressedSize,
                            isImage: true,
                            originalSize: result.originalSize,
                            compressed: true
                        });

                        // Render previews immediately after adding compressed file
                        renderFilePreviews();
                    } else {
                        // Image is already small enough, no compression needed
                        var reader = new FileReader();
                        reader.onload = (function(f) {
                            return function(e) {
                                pendingFiles.push({
                                    url: e.target.result,
                                    name: f.name,
                                    type: f.type,
                                    size: f.size,
                                    isImage: true,
                                    compressed: false
                                });
                                renderFilePreviews();
                            };
                        })(file);
                        reader.onerror = function() {
                            showToast('Failed to read file: ' + file.name, true);
                        };
                        reader.readAsDataURL(file);
                        continue;
                    }
                } catch (error) {
                    console.error('Failed to compress image:', file.name, error);
                    showToast('Failed to compress image: ' + file.name, true);
                    // Fallback to original file
                    var fallbackReader = new FileReader();
                    fallbackReader.onload = (function(f) {
                        return function(e) {
                            pendingFiles.push({
                                url: e.target.result,
                                name: f.name,
                                type: f.type,
                                size: f.size,
                                isImage: true,
                                compressed: false,
                                compressionError: true
                            });
                            renderFilePreviews();
                        };
                    })(file);
                    fallbackReader.readAsDataURL(file);
                    continue;
                }
            } else {
                // Non-image file, add directly
                var nonImageReader = new FileReader();
                nonImageReader.onload = (function(f) {
                    return function(e) {
                        pendingFiles.push({
                            url: e.target.result,
                            name: f.name,
                            type: f.type,
                            size: f.size,
                            isImage: false
                        });
                        renderFilePreviews();
                    };
                })(file);
                nonImageReader.onerror = function() {
                    showToast('Failed to read file: ' + file.name, true);
                };
                nonImageReader.readAsDataURL(file);
            }
        }

        // Render previews after processing all files
        if (compressionStarted) {
            renderFilePreviews();
            closeStatus(); // Close compression status
        }

        // Clear the input so the same file can be selected again
        var fileInput = document.getElementById('file-input');
        if (fileInput) {
            fileInput.value = '';
        }
    }

    // Render file previews
    function renderFilePreviews() {
        var container = document.getElementById('image-preview-container');

        if (pendingFiles.length === 0) {
            container.style.display = 'none';
            container.innerHTML = '';
            return;
        }

        container.style.display = 'flex';
        var html = '';
        for (var idx = 0; idx < pendingFiles.length; idx++) {
            var file = pendingFiles[idx];
            if (file.isImage) {
                html += '<div class="image-preview-item">' +
                    '<img src="' + file.url + '" alt="' + file.name + '" />' +
                    '<button onclick="removeFile(' + idx + ')" title="Remove file">×</button>' +
                    '</div>';
            } else {
                var icon = getFileIcon(file.type, file.name);
                var sizeStr = formatFileSize(file.size);
                html += '<div class="image-preview-item file-preview-item">' +
                    '<div class="file-preview-content">' +
                    '<span class="file-icon">' + icon + '</span>' +
                    '<span class="file-name" title="' + file.name + '">' + file.name + '</span>' +
                    '<span class="file-size">' + sizeStr + '</span>' +
                    '</div>' +
                    '<button onclick="removeFile(' + idx + ')" title="Remove file">×</button>' +
                    '</div>';
            }
        }
        container.innerHTML = html;
    }

    // Remove a file from pending list
    function removeFile(index) {
        if (index >= 0 && index < pendingFiles.length) {
            pendingFiles.splice(index, 1);
            renderFilePreviews();
        }
    }

    // Clear all pending files
    function clearPendingFiles() {
        pendingFiles = [];
        renderFilePreviews();
    }

    // Get pending files
    function getPendingFiles() {
        return pendingFiles;
    }

    // Export functions to global scope
    window.FileUploadHandler = {
        handleFiles: handleFiles,
        removeFile: removeFile,
        clearPendingFiles: clearPendingFiles,
        getPendingFiles: getPendingFiles,
        renderFilePreviews: renderFilePreviews,
        isFileTypeSupported: isFileTypeSupported,
        getFileIcon: getFileIcon,
        formatFileSize: formatFileSize
    };

    // Also expose functions globally for onclick handlers
    window.handleFiles = handleFiles;
    window.removeFile = removeFile;

})();
