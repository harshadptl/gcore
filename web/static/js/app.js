// Progressive enhancements. Every feature here has a no-JavaScript fallback:
// forms still POST, links still navigate, the server stays the source of truth.
(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const csrfToken = () => $('input[name="_csrf"]')?.value || '';

  const variants = { success: 'success', error: 'danger', danger: 'danger', warning: 'warning' };
  const toast = (message, kind = '') => {
    if (window.ot?.toast) window.ot.toast(message, undefined, { variant: variants[kind] || '' });
    else window.alert(message);
  };

  // Flash messages from the server → Oat toasts.
  const flashData = $('#flash-data');
  if (flashData) {
    try {
      for (const f of JSON.parse(flashData.dataset.flashes || '[]')) toast(f.Message, f.Kind);
    } catch { /* ignore malformed data */ }
  }

  // Light/dark toggle (theme.js applies the saved value on load).
  document.addEventListener('click', (e) => {
    if (!e.target.closest('[data-theme-toggle]')) return;
    const root = document.documentElement;
    const isDark = root.dataset.theme
      ? root.dataset.theme === 'dark'
      : window.matchMedia('(prefers-color-scheme: dark)').matches;
    const next = isDark ? 'light' : 'dark';
    root.dataset.theme = next;
    try { localStorage.setItem('theme', next); } catch { /* private mode */ }
  });

  // <form data-confirm="Are you sure?"> asks before submitting.
  document.addEventListener('submit', (e) => {
    const msg = e.target.dataset?.confirm;
    if (msg && !window.confirm(msg)) e.preventDefault();
  }, true);

  // Like / bookmark buttons: toggle in place with fetch().
  document.addEventListener('submit', async (e) => {
    const form = e.target.closest('form[data-toggle]');
    if (!form || e.defaultPrevented) return;
    e.preventDefault();

    const kind = form.dataset.toggle;
    const btn = form.querySelector('button');
    btn.disabled = true;
    try {
      const res = await fetch(form.action, {
        method: 'POST',
        body: new FormData(form),
        headers: { 'X-Requested-With': 'fetch', Accept: 'application/json' },
      });
      const data = await res.json().catch(() => ({}));
      if (res.status === 401 && data.login) {
        window.location = data.login + '?next=' + encodeURIComponent(location.pathname);
        return;
      }
      if (!res.ok) throw new Error(data.error || 'Something went wrong.');

      btn.setAttribute('aria-pressed', String(data.active));
      btn.classList.toggle('outline', !data.active);
      const label = btn.querySelector('[data-label]');
      if (label) {
        label.textContent = kind === 'like'
          ? (data.active ? 'Liked' : 'Like')
          : (data.active ? 'Saved' : 'Save');
      }
      const count = btn.querySelector('[data-count]');
      if (count) count.textContent = data.count;
      if (kind === 'bookmark') {
        toast(data.active ? 'Saved to your reading list.' : 'Removed from your reading list.', data.active ? 'success' : '');
      }
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      btn.disabled = false;
    }
  });

  // Copy-to-clipboard buttons.
  document.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-copy]');
    if (!btn) return;
    try {
      await navigator.clipboard.writeText(btn.dataset.copy);
      toast('Link copied.', 'success');
    } catch {
      window.prompt('Copy this link:', btn.dataset.copy);
    }
  });

  // Watch page: flip to "ended" once the stream goes offline.
  const watch = document.querySelector('[data-live-status]');
  if (watch) {
    const timer = setInterval(async () => {
      try {
        const res = await fetch(watch.dataset.liveStatus, { headers: { 'X-Requested-With': 'fetch' } });
        if (!res.ok) return; // server hiccup: keep watching; only an explicit live:false ends it
        if ((await res.json()).live !== false) return;
        watch.querySelector('iframe')?.remove();
        watch.querySelector('[data-live-ended]').hidden = false;
        clearInterval(timer);
      } catch {} // network blip: keep watching
    }, 10000);
  }

  document.addEventListener('click', (e) => {
    if (e.target.closest('[data-history-back]')) history.back();
  });

  // Fallback for browsers without Invoker Commands (commandfor/command),
  // which Oat uses to open and close <dialog>s.
  if (!('commandForElement' in HTMLButtonElement.prototype)) {
    document.addEventListener('click', (e) => {
      const btn = e.target.closest('button[commandfor]');
      const target = btn && document.getElementById(btn.getAttribute('commandfor'));
      if (!target) return;
      const cmd = btn.getAttribute('command');
      if (cmd === 'show-modal') target.showModal?.();
      else if (cmd === 'close') target.close?.();
    });
  }

  const editor = $('form[data-editor]');
  if (editor) setupEditor(editor);

  function setupEditor(form) {
    const content = form.querySelector('textarea[name="content"]');
    const preview = form.querySelector('[data-preview]');
    let dirty = false;
    let submitting = false;
    let lastPreview = null;

    form.addEventListener('input', () => { dirty = true; });
    form.addEventListener('submit', () => { submitting = true; syncTags(); });
    window.addEventListener('beforeunload', (e) => {
      if (dirty && !submitting) e.preventDefault();
    });

    // Ctrl/Cmd+S saves with the first submit button (keeps the current status).
    form.addEventListener('keydown', (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
        e.preventDefault();
        form.requestSubmit(form.querySelector('button[type="submit"]'));
      }
    });

    // Tab key inserts two spaces in the Markdown textarea.
    content?.addEventListener('keydown', (e) => {
      if (e.key !== 'Tab' || e.shiftKey || e.ctrlKey || e.metaKey) return;
      e.preventDefault();
      content.setRangeText('  ', content.selectionStart, content.selectionEnd, 'end');
      dirty = true;
    });

    // Preview tab renders Markdown on the server (same sanitizer as publishing).
    form.querySelector('ot-tabs')?.addEventListener('ot-tab-change', async (e) => {
      if (!e.detail?.tab?.hasAttribute('data-preview-tab') || !content || !preview) return;
      if (content.value === lastPreview) return;
      lastPreview = content.value;
      preview.setAttribute('aria-busy', 'true');
      try {
        const res = await fetch('/preview', {
          method: 'POST',
          body: new URLSearchParams({ content: content.value, _csrf: csrfToken() }),
          headers: { 'X-Requested-With': 'fetch' },
        });
        if (!res.ok) throw new Error(`Preview failed (${res.status}).`);
        preview.innerHTML = await res.text(); // sanitized server-side
      } catch (err) {
        preview.textContent = err.message;
        lastPreview = null;
      } finally {
        preview.removeAttribute('aria-busy');
      }
    });

    // Tag chips: <ot-taginput> keeps tags as badges; mirror them into a hidden
    // "tags" field. Without JS the inner input submits comma-separated text.
    const tagsEl = form.querySelector('ot-taginput[data-tags]');
    let hidden = null;
    if (tagsEl) {
      customElements.whenDefined('ot-taginput').then(() => {
        const inner = tagsEl.querySelector('input');
        inner.removeAttribute('name');
        inner.value = '';
        hidden = Object.assign(document.createElement('input'), { type: 'hidden', name: 'tags' });
        form.append(hidden);
        syncTags();
        tagsEl.addEventListener('input', (e) => {
          if (e.target !== tagsEl) return;
          dirty = true;
          syncTags();
        });
      });
    }

    function syncTags() {
      if (!tagsEl || !hidden) return;
      const inner = tagsEl.querySelector('input');
      if (inner?.value.trim()) tagsEl.add(inner.value); // pending text counts too
      hidden.value = (tagsEl.value || []).map(String).join(', ');
    }
  }
})();
