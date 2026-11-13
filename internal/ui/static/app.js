// Progressive enhancements for the dropsite interface: archive upload with
// progress and copy-to-clipboard buttons. Every page works without it.
(function () {
  'use strict';

  var ARCHIVE_NAME = /\.(zip|tar\.gz|tgz)$/i;
  var COPY_RESET_MS = 2000;

  function csrfToken() {
    try {
      return JSON.parse(document.body.getAttribute('hx-headers'))['X-CSRF-Token'] || '';
    } catch (e) {
      return '';
    }
  }

  // ---- copy to clipboard -------------------------------------------------

  function fallbackCopy(text) {
    var area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.className = 'offscreen';
    document.body.appendChild(area);
    area.select();
    var ok = false;
    try {
      ok = document.execCommand('copy');
    } catch (e) {
      ok = false;
    }
    document.body.removeChild(area);
    return ok;
  }

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).then(function () { return true; }, function () {
        return fallbackCopy(text);
      });
    }
    return Promise.resolve(fallbackCopy(text));
  }

  function bindCopy(btn) {
    if (btn.dataset.bound) { return; }
    btn.dataset.bound = '1';
    btn.addEventListener('click', function () {
      var target = document.querySelector(btn.dataset.copy);
      if (!target) { return; }
      var label = btn.textContent;
      copyText(target.textContent.replace(/\s+$/, '')).then(function (ok) {
        if (!ok) { return; }
        btn.textContent = btn.dataset.copied || label;
        btn.classList.add('is-done');
        window.setTimeout(function () {
          btn.textContent = label;
          btn.classList.remove('is-done');
        }, COPY_RESET_MS);
      });
    });
  }

  function initCopy(root) {
    root.querySelectorAll('[data-copy]').forEach(bindCopy);
  }

  // ---- connect code countdown -----------------------------------------------

  function pad(n) { return (n < 10 ? '0' : '') + n; }

  function initCountdown(root) {
    root.querySelectorAll('[data-countdown]').forEach(function (box) {
      if (box.dataset.bound) { return; }
      box.dataset.bound = '1';
      var end = Date.parse(box.dataset.countdown);
      var live = box.querySelector('[data-countdown-live]');
      var expired = box.querySelector('[data-countdown-expired]');
      if (isNaN(end) || !live) { return; }
      var timer;
      function tick() {
        if (!document.body.contains(box)) { window.clearInterval(timer); return; }
        var left = Math.max(0, Math.round((end - Date.now()) / 1000));
        var clock = pad(Math.floor(left / 60)) + ':' + pad(left % 60);
        live.textContent = (box.dataset.countdownLeft || '{t}').replace('{t}', clock);
        if (left === 0) {
          window.clearInterval(timer);
          box.classList.add('is-expired');
          if (expired) { expired.hidden = false; }
        }
      }
      tick();
      timer = window.setInterval(tick, 1000);
    });
  }

  // ---- archive upload ------------------------------------------------------

  function hostOf(url) {
    return url.replace(/^https?:\/\//, '').replace(/\/$/, '');
  }

  function Uploader(form) {
    this.form = form;
    this.input = form.querySelector('[data-file]');
    this.status = form.querySelector('[data-status]');
    this.progress = form.querySelector('[data-progress]');
    this.track = form.querySelector('[data-bar-wrap]');
    this.bar = form.querySelector('[data-bar]');
    this.pct = form.querySelector('[data-pct]');
    this.busy = false;
  }

  Uploader.prototype.msg = function (name) {
    return this.form.dataset['msg' + name] || '';
  };

  Uploader.prototype.setProgress = function (fraction) {
    var p = Math.max(0, Math.min(1, fraction));
    this.bar.style.transform = 'scaleX(' + p + ')';
    var value = Math.round(p * 100);
    this.track.setAttribute('aria-valuenow', String(value));
    this.pct.textContent = value + '%';
  };

  Uploader.prototype.setStatus = function (kind, text, link) {
    this.status.textContent = '';
    this.status.className = 'upload-status upload-status--' + kind;
    var p = document.createElement('p');
    p.textContent = text;
    this.status.appendChild(p);
    if (link) {
      var a = document.createElement('a');
      a.href = link;
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      a.className = 'btn btn--ok upload-status__link';
      a.textContent = this.msg('Open') + ' ' + hostOf(link) + ' ↗';
      this.status.appendChild(a);
    }
  };

  Uploader.prototype.finish = function () {
    this.busy = false;
    this.form.classList.remove('is-busy');
    this.input.disabled = false;
    this.input.value = '';
  };

  Uploader.prototype.send = function (file) {
    if (this.busy) { return; }
    if (!ARCHIVE_NAME.test(file.name)) {
      this.setStatus('error', this.msg('Wrongtype'));
      return;
    }
    var self = this;
    var token = csrfToken();
    var data = new FormData();
    data.append('_csrf', token);
    var draft = this.form.querySelector('[data-draft]');
    if (draft && draft.checked) { data.append('draft', '1'); }
    data.append('archive', file, file.name);
    var xhr = new XMLHttpRequest();
    xhr.open('POST', this.form.action);
    xhr.setRequestHeader('Accept', 'application/json');
    xhr.setRequestHeader('X-CSRF-Token', token);
    xhr.upload.addEventListener('progress', function (e) {
      if (e.lengthComputable) { self.setProgress(e.loaded / e.total); }
    });
    xhr.upload.addEventListener('load', function () {
      self.setProgress(1);
      self.setStatus('busy', self.msg('Processing'));
    });
    xhr.addEventListener('load', function () { self.done(xhr); });
    xhr.addEventListener('error', function () {
      self.setStatus('error', self.msg('Failed'));
      self.finish();
    });
    this.busy = true;
    this.form.classList.add('is-busy');
    this.input.disabled = true;
    this.progress.hidden = false;
    this.setProgress(0);
    this.setStatus('busy', this.msg('Uploading') + ' ' + file.name);
    xhr.send(data);
  };

  Uploader.prototype.done = function (xhr) {
    var reply = null;
    try {
      reply = JSON.parse(xhr.responseText);
    } catch (e) {
      reply = null;
    }
    if (xhr.status === 200 && reply && reply.ok && reply.reload) {
      // A draft: the page shows it in its own block (the flash is set).
      window.location.reload();
      return;
    }
    if (xhr.status === 200 && reply && reply.ok) {
      this.setStatus('ok', reply.message, reply.url);
      if (window.htmx && this.form.dataset.history) {
        window.htmx.ajax('GET', this.form.dataset.history, { target: '#history', swap: 'innerHTML' });
      }
    } else {
      this.progress.hidden = true;
      this.setStatus('error', (reply && reply.message) || this.msg('Failed'));
    }
    this.finish();
  };

  Uploader.prototype.bind = function () {
    var self = this;
    var form = this.form;
    var nojs = form.querySelector('[data-nojs]');
    if (nojs) { nojs.hidden = true; }
    this.input.removeAttribute('required');
    this.input.addEventListener('change', function () {
      if (self.input.files.length) { self.send(self.input.files[0]); }
    });
    ['dragenter', 'dragover'].forEach(function (name) {
      form.addEventListener(name, function (e) {
        e.preventDefault();
        if (!self.busy) { form.classList.add('is-over'); }
      });
    });
    form.addEventListener('dragleave', function (e) {
      if (!form.contains(e.relatedTarget)) { form.classList.remove('is-over'); }
    });
    form.addEventListener('drop', function (e) {
      e.preventDefault();
      form.classList.remove('is-over');
      var files = e.dataTransfer && e.dataTransfer.files;
      if (files && files.length) { self.send(files[0]); }
    });
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      if (self.input.files.length) { self.send(self.input.files[0]); }
    });
  };

  // ---- wiring --------------------------------------------------------------

  function focusIssued(root) {
    var issued = root.querySelector && root.querySelector('.issued');
    if (issued) {
      issued.setAttribute('tabindex', '-1');
      issued.focus();
    }
  }

  document.addEventListener('DOMContentLoaded', function () {
    document.documentElement.classList.add('js');
    initCopy(document);
    initCountdown(document);
    document.querySelectorAll('[data-upload]').forEach(function (form) {
      new Uploader(form).bind();
    });
    document.body.addEventListener('htmx:load', function (e) {
      initCopy(e.detail.elt);
      initCountdown(e.detail.elt);
      focusIssued(e.detail.elt);
    });
  });
}());
