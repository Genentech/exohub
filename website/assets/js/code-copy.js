// Add copy buttons to all code blocks
(function() {
  function addCopyButtons() {
    document.querySelectorAll('pre').forEach(function(pre) {
      // Skip if already has a copy button
      if (pre.querySelector('.copy-code-btn')) return;

      const code = pre.querySelector('code');
      if (!code) return;

      // Create wrapper for positioning
      pre.style.position = 'relative';

      // Create button
      const button = document.createElement('button');
      button.className = 'copy-code-btn';
      button.textContent = 'Copy';
      button.setAttribute('type', 'button');

      Object.assign(button.style, {
        position: 'absolute',
        top: '0.5rem',
        right: '0.5rem',
        padding: '0.25rem 0.5rem',
        fontSize: '0.75rem',
        background: '#374151',
        color: '#fff',
        border: '1px solid #4b5563',
        borderRadius: '0.25rem',
        cursor: 'pointer',
        opacity: '0',
        transition: 'opacity 0.2s'
      });

      // Show button on hover
      pre.addEventListener('mouseenter', function() { button.style.opacity = '1'; });
      pre.addEventListener('mouseleave', function() { button.style.opacity = '0'; });

      // Copy on click
      button.addEventListener('click', function(e) {
        e.preventDefault();
        e.stopPropagation();

        const text = code.textContent;

        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function() {
            button.textContent = 'Copied!';
            setTimeout(function() { button.textContent = 'Copy'; }, 1500);
          }).catch(function() {
            fallbackCopy(text, button);
          });
        } else {
          fallbackCopy(text, button);
        }
      });

      pre.appendChild(button);
    });
  }

  function fallbackCopy(text, button) {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    try {
      document.execCommand('copy');
      button.textContent = 'Copied!';
      setTimeout(function() { button.textContent = 'Copy'; }, 1500);
    } catch (e) {
      button.textContent = 'Failed';
      setTimeout(function() { button.textContent = 'Copy'; }, 1500);
    }
    document.body.removeChild(textarea);
  }

  // Run when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', addCopyButtons);
  } else {
    addCopyButtons();
  }
})();
