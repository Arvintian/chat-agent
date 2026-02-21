// Image Preview Module
// Handles image preview modal functionality

(function () {
    // Image preview state
    var currentPreviewImages = [];
    var currentPreviewIndex = 0;

    // Show image preview modal from history message
    window.showImagePreviewFromHistory = function (msgIndex, imgIndex) {

        window.MessageHistory.loadHistory().then(function (history) {
            var msg = history[msgIndex];

            if (!msg || !msg.files || msg.files.length === 0) return;

            // Check if files are unavailable (fallback mode)
            var hasUnavailableFiles = false;
            for (var i = 0; i < msg.files.length; i++) {
                if (msg.files[i].unavailable) {
                    hasUnavailableFiles = true;
                    break;
                }
            }

            if (hasUnavailableFiles) {
                showToast('Images are not available in this session', true);
                return;
            }

            // Collect all images from this message
            var allImages = [];
            for (var j = 0; j < msg.files.length; j++) {
                var img = msg.files[j];
                if (img.isImage || (img.type && img.type.startsWith('image/'))) {
                    allImages.push(img);
                }
            }

            currentPreviewImages = [];
            for (var k = 0; k < allImages.length; k++) {
                currentPreviewImages.push(allImages[k].url);
            }

            if (currentPreviewImages.length === 0) return;

            // Find the actual index in the filtered images array
            var originalImg = msg.files[imgIndex];
            currentPreviewIndex = -1;
            for (var m = 0; m < allImages.length; m++) {
                if (allImages[m].url === originalImg.url) {
                    currentPreviewIndex = m;
                    break;
                }
            }
            if (currentPreviewIndex < 0) currentPreviewIndex = 0;

            var modal = document.getElementById('image-preview-modal');
            var imgElement = document.getElementById('image-preview-full');
            var counterElement = document.getElementById('image-preview-counter');

            // Set the image source
            imgElement.src = currentPreviewImages[currentPreviewIndex];

            // Update counter
            if (currentPreviewImages.length > 1) {
                counterElement.textContent = (currentPreviewIndex + 1) + ' / ' + currentPreviewImages.length;
                counterElement.style.display = 'block';
            } else {
                counterElement.textContent = '';
                counterElement.style.display = 'none';
            }

            // Show modal
            modal.style.display = 'flex';
            document.body.style.overflow = 'hidden'; // Prevent background scrolling
        });
    };

    // Show image preview modal for newly sent messages
    window.showImagePreviewWithIndex = function (filesJson, index) {
        var modal = document.getElementById('image-preview-modal');
        var imgElement = document.getElementById('image-preview-full');
        var counterElement = document.getElementById('image-preview-counter');

        // Parse the files from JSON
        var files;
        try {
            files = JSON.parse(decodeURIComponent(filesJson));
        } catch (e) {
            console.error('Failed to parse files:', e);
            return;
        }

        // Collect all image URLs from the files array
        currentPreviewImages = [];
        for (var i = 0; i < files.length; i++) {
            var file = files[i];
            if (file.isImage || (file.type && file.type.startsWith('image/'))) {
                currentPreviewImages.push(file.url);
            }
        }

        if (currentPreviewImages.length === 0) return;

        currentPreviewIndex = index;

        // Set the image source
        imgElement.src = currentPreviewImages[currentPreviewIndex];

        // Update counter
        if (currentPreviewImages.length > 1) {
            counterElement.textContent = (currentPreviewIndex + 1) + ' / ' + currentPreviewImages.length;
            counterElement.style.display = 'block';
        } else {
            counterElement.textContent = '';
            counterElement.style.display = 'none';
        }

        // Show modal
        modal.style.display = 'flex';
        document.body.style.overflow = 'hidden'; // Prevent background scrolling
    };

    // Hide image preview modal
    window.hideImagePreview = function () {
        var modal = document.getElementById('image-preview-modal');
        modal.style.display = 'none';
        document.body.style.overflow = ''; // Restore scrolling

        // Clear image source after a delay to avoid flickering
        setTimeout(function () {
            document.getElementById('image-preview-full').src = '';
        }, 200);
    };

    // Navigate through preview images
    window.navigatePreview = function (direction) {
        var newIndex = currentPreviewIndex + direction;

        // Bounds check
        if (newIndex < 0 || newIndex >= currentPreviewImages.length) {
            return;
        }

        currentPreviewIndex = newIndex;
        var imgElement = document.getElementById('image-preview-full');
        var counterElement = document.getElementById('image-preview-counter');

        // Animate image transition
        imgElement.style.opacity = '0';
        imgElement.style.transform = 'scale(0.95)';

        setTimeout(function () {
            imgElement.src = currentPreviewImages[currentPreviewIndex];
            imgElement.style.opacity = '1';
            imgElement.style.transform = 'scale(1)';

            // Update counter
            if (currentPreviewImages.length > 1) {
                counterElement.textContent = (currentPreviewIndex + 1) + ' / ' + currentPreviewImages.length;
            }
        }, 150);
    };

    // Add keyboard navigation for image preview
    document.addEventListener('keydown', function (e) {
        var modal = document.getElementById('image-preview-modal');
        if (modal.style.display === 'flex') {
            if (e.key === 'Escape') {
                window.hideImagePreview();
            } else if (e.key === 'ArrowLeft') {
                window.navigatePreview(-1);
            } else if (e.key === 'ArrowRight') {
                window.navigatePreview(1);
            }
        }
    });
})();
