// Image Lightbox System
document.addEventListener('DOMContentLoaded', function() {
  // Create lightbox overlay element
  const overlay = document.createElement('div');
  overlay.className = 'lightbox-overlay';
  overlay.innerHTML = `
    <div class="lightbox-content">
      <button class="lightbox-close" aria-label="Close lightbox">&times;</button>
      <img class="lightbox-image" src="" alt="" />
      <div class="lightbox-caption"></div>
    </div>
  `;
  document.body.appendChild(overlay);

  // Get lightbox elements
  const lightboxImage = overlay.querySelector('.lightbox-image');
  const lightboxCaption = overlay.querySelector('.lightbox-caption');
  const closeButton = overlay.querySelector('.lightbox-close');

  // Function to open lightbox
  function openLightbox(imageSrc, imageAlt) {
    lightboxImage.src = imageSrc;
    lightboxImage.alt = imageAlt;
    lightboxCaption.textContent = imageAlt;
    overlay.classList.add('active');
    document.body.style.overflow = 'hidden';
  }

  // Function to close lightbox
  function closeLightbox() {
    overlay.classList.remove('active');
    document.body.style.overflow = '';
    // Clear image src after transition
    setTimeout(() => {
      if (!overlay.classList.contains('active')) {
        lightboxImage.src = '';
      }
    }, 300);
  }

  // Add click handlers for images with clickable-image class
  function initClickableImages() {
    const clickableImages = document.querySelectorAll('.clickable-image, [data-lightbox="true"]');

    clickableImages.forEach(img => {
      // Add clickable styling if not already present
      if (!img.classList.contains('clickable-image')) {
        img.classList.add('clickable-image');
      }

      img.addEventListener('click', function() {
        const imgSrc = this.src || this.getAttribute('data-src');
        const imgAlt = this.alt || this.getAttribute('data-caption') || 'Image';
        openLightbox(imgSrc, imgAlt);
      });
    });
  }

  // Event listeners for closing lightbox
  closeButton.addEventListener('click', closeLightbox);

  // Close when clicking on the image itself
  lightboxImage.addEventListener('click', closeLightbox);

  overlay.addEventListener('click', function(e) {
    if (e.target === overlay) {
      closeLightbox();
    }
  });

  document.addEventListener('keydown', function(e) {
    if (e.key === 'Escape' && overlay.classList.contains('active')) {
      closeLightbox();
    }
  });

  // Initialize clickable images
  initClickableImages();

  // Re-initialize when new content is loaded (for dynamic content)
  const observer = new MutationObserver(function(mutations) {
    mutations.forEach(function(mutation) {
      if (mutation.addedNodes.length > 0) {
        initClickableImages();
      }
    });
  });

  observer.observe(document.body, {
    childList: true,
    subtree: true
  });
});