// Image compression utilities for client-side image optimization
// Compresses images while maintaining good quality before upload and storage

/**
 * Image compressor class for handling client-side image compression
 */
class ImageCompressor {
    /**
     * Default compression settings
     */
    static DEFAULT_OPTIONS = {
        maxWidth: 1920,           // Maximum width for resized images
        maxHeight: 1920,          // Maximum height for resized images
        quality: 0.85,            // JPEG quality (0.0 - 1.0)
        maxSizeKB: 500,           // Target maximum file size in KB
        minQuality: 0.6,          // Minimum quality to try during compression
        mimeType: 'image/jpeg',   // Output format
        preserveExif: false       // Whether to preserve EXIF data (not implemented yet)
    };

    /**
     * Compress an image file
     * @param {File|Blob} file - The image file to compress
     * @param {Object} options - Compression options
     * @returns {Promise<{blob: Blob, originalSize: number, compressedSize: number, ratio: number}>}
     */
    static async compress(file, options = {}) {
        const opts = { ...this.DEFAULT_OPTIONS, ...options };
        
        return new Promise((resolve, reject) => {
            const img = new Image();
            const reader = new FileReader();

            reader.onload = (e) => {
                img.src = e.target.result;
            };

            img.onload = () => {
                try {
                    const result = this.compressImage(img, file, opts);
                    resolve(result);
                } catch (error) {
                    reject(error);
                }
            };

            img.onerror = () => {
                reject(new Error('Failed to load image'));
            };

            reader.onerror = () => {
                reject(new Error('Failed to read file'));
            };

            reader.readAsDataURL(file);
        });
    }

    /**
     * Compress an image element
     * @private
     */
    static compressImage(img, originalFile, options) {
        const { maxWidth, maxHeight, quality, maxSizeKB, minQuality, mimeType } = options;
        
        // Calculate new dimensions while maintaining aspect ratio
        let width = img.width;
        let height = img.height;

        if (width > maxWidth || height > maxHeight) {
            const ratio = Math.min(maxWidth / width, maxHeight / height);
            width = Math.floor(width * ratio);
            height = Math.floor(height * ratio);
        }

        // Create canvas for compression
        const canvas = document.createElement('canvas');
        canvas.width = width;
        canvas.height = height;

        const ctx = canvas.getContext('2d');
        
        // Use high-quality rendering settings
        ctx.imageSmoothingEnabled = true;
        ctx.imageSmoothingQuality = 'high';
        
        // Draw image on canvas
        ctx.drawImage(img, 0, 0, width, height);

        // Initial compression
        let currentQuality = quality;
        let blob = this.canvasToBlob(canvas, mimeType, currentQuality);
        let sizeKB = blob.size / 1024;

        // If size exceeds target, reduce quality iteratively
        if (sizeKB > maxSizeKB && currentQuality > minQuality) {
            const step = (currentQuality - minQuality) / 5;
            
            while (sizeKB > maxSizeKB && currentQuality > minQuality) {
                currentQuality = Math.max(minQuality, currentQuality - step);
                blob = this.canvasToBlob(canvas, mimeType, currentQuality);
                sizeKB = blob.size / 1024;
                
                // Break if quality is too low
                if (currentQuality <= minQuality) {
                    break;
                }
            }
        }

        // Create compressed file info
        const compressedFile = new File([blob], originalFile.name, {
            type: mimeType,
            lastModified: Date.now()
        });

        return {
            blob: blob,
            file: compressedFile,
            url: URL.createObjectURL(blob),
            originalSize: originalFile.size,
            compressedSize: blob.size,
            ratio: ((1 - blob.size / originalFile.size) * 100).toFixed(2),
            width: width,
            height: height,
            quality: currentQuality,
            mimeType: mimeType
        };
    }

    /**
     * Convert canvas to blob with proper MIME type handling
     * @private
     */
    static canvasToBlob(canvas, mimeType, quality) {
        // Handle different image types
        if (mimeType === 'image/jpeg' || mimeType === 'image/jpg') {
            // For JPEG, we can use quality parameter
            return this.dataURLToBlob(canvas.toDataURL('image/jpeg', quality));
        } else if (mimeType === 'image/webp') {
            // WebP also supports quality parameter
            return this.dataURLToBlob(canvas.toDataURL('image/webp', quality));
        } else if (mimeType === 'image/png') {
            // PNG doesn't support quality, but we can still convert
            return this.dataURLToBlob(canvas.toDataURL('image/png'));
        } else {
            // Default to JPEG
            return this.dataURLToBlob(canvas.toDataURL('image/jpeg', quality));
        }
    }

    /**
     * Convert data URL to Blob
     * @private
     */
    static dataURLToBlob(dataURL) {
        const byteString = atob(dataURL.split(',')[1]);
        const mimeString = dataURL.split(',')[0].split(':')[1].split(';')[0];
        const ab = new ArrayBuffer(byteString.length);
        const ia = new Uint8Array(ab);

        for (let i = 0; i < byteString.length; i++) {
            ia[i] = byteString.charCodeAt(i);
        }

        return new Blob([ab], { type: mimeString });
    }

    /**
     * Check if image needs compression
     * @param {File} file - Image file to check
     * @param {number} maxSizeKB - Maximum acceptable size in KB
     * @returns {boolean}
     */
    static needsCompression(file, maxSizeKB = 500) {
        const sizeKB = file.size / 1024;
        return sizeKB > maxSizeKB;
    }

    /**
     * Get image dimensions from file
     * @param {File} file - Image file
     * @returns {Promise<{width: number, height: number}>}
     */
    static getImageDimensions(file) {
        return new Promise((resolve, reject) => {
            const img = new Image();
            const reader = new FileReader();

            reader.onload = (e) => {
                img.src = e.target.result;
            };

            img.onload = () => {
                resolve({ width: img.width, height: img.height });
            };

            img.onerror = () => reject(new Error('Failed to load image'));
            reader.onerror = () => reject(new Error('Failed to read file'));

            reader.readAsDataURL(file);
        });
    }

    /**
     * Compress multiple images
     * @param {File[]} files - Array of image files
     * @param {Object} options - Compression options
     * @returns {Promise<Array>}
     */
    static async compressMultiple(files, options = {}) {
        const results = [];
        
        for (const file of files) {
            if (!file.type.startsWith('image/')) {
                // Skip non-image files
                results.push({
                    file: file,
                    url: URL.createObjectURL(file),
                    originalSize: file.size,
                    compressedSize: file.size,
                    ratio: 0,
                    skipped: true,
                    reason: 'Not an image'
                });
                continue;
            }

            try {
                const result = await this.compress(file, options);
                results.push(result);
            } catch (error) {
                console.error('Failed to compress image:', file.name, error);
                results.push({
                    file: file,
                    url: URL.createObjectURL(file),
                    originalSize: file.size,
                    compressedSize: file.size,
                    ratio: 0,
                    error: error.message
                });
            }
        }

        return results;
    }

    /**
     * Clean up object URLs to prevent memory leaks
     * @param {string|string[]} urls - URL or array of URLs to revoke
     */
    static revokeURLs(urls) {
        const urlList = Array.isArray(urls) ? urls : [urls];
        urlList.forEach(url => {
            if (url && url.startsWith('blob:')) {
                URL.revokeObjectURL(url);
            }
        });
    }
}

// Make it available globally
window.ImageCompressor = ImageCompressor;

// Export for module systems
if (typeof module !== 'undefined' && module.exports) {
    module.exports = ImageCompressor;
}
