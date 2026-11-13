// Upload link page (spec upload-link): files dropped or picked here are sent
// one by one with PUT <this address>/<path>, a few at a time. The token is
// in the page address; nothing is stored in the browser.
(function () {
  "use strict";

  var PARALLEL = 3;
  var RETRIES = 2;
  var IGNORED = { ".git": 1, "node_modules": 1, "__MACOSX": 1, ".DS_Store": 1, "Thumbs.db": 1 };

  var root = document.querySelector("[data-upload-link]");
  if (!root) return;
  var drop = root.querySelector("[data-drop]");
  var progress = root.querySelector("[data-progress]");
  var barWrap = root.querySelector("[data-bar-wrap]");
  var bar = root.querySelector("[data-bar]");
  var pct = root.querySelector("[data-pct]");
  var count = root.querySelector("[data-count]");
  var result = root.querySelector("[data-result]");
  var errors = root.querySelector("[data-errors]");
  var base = location.pathname.replace(/\/+$/, "");
  var busy = false;

  function msg(name) { return root.getAttribute("data-msg-" + name) || ""; }

  function fill(text, vars) {
    return text.replace(/\{(\w+)\}/g, function (m, k) { return k in vars ? String(vars[k]) : m; });
  }

  function ignored(path) {
    var segs = path.split("/");
    for (var i = 0; i < segs.length; i++) {
      if (IGNORED[segs[i]]) return true;
    }
    return segs[segs.length - 1].indexOf("._") === 0;
  }

  // stripTop removes a folder name shared by every path ("site/index.html"
  // becomes "index.html") when the user picked or dropped one folder.
  function stripTop(items) {
    if (!items.length) return items;
    var top = items[0].path.split("/")[0];
    for (var i = 0; i < items.length; i++) {
      if (items[i].path.indexOf(top + "/") !== 0) return items;
    }
    return items.map(function (it) { return { path: it.path.slice(top.length + 1), file: it.file }; });
  }

  function readAll(reader) {
    return new Promise(function (resolve, reject) {
      var out = [];
      (function next() {
        reader.readEntries(function (batch) {
          if (!batch.length) return resolve(out);
          out = out.concat(Array.prototype.slice.call(batch));
          next();
        }, reject);
      })();
    });
  }

  function walk(entry, prefix) {
    if (entry.isFile) {
      return new Promise(function (resolve) {
        entry.file(function (f) { resolve([{ path: prefix + entry.name, file: f }]); }, function () { resolve([]); });
      });
    }
    if (!entry.isDirectory) return Promise.resolve([]);
    return readAll(entry.createReader()).then(function (children) {
      return Promise.all(children.map(function (c) { return walk(c, prefix + entry.name + "/"); }));
    }).then(function (lists) { return [].concat.apply([], lists); });
  }

  function fromDrop(dt) {
    var entries = [];
    if (dt.items && dt.items.length && dt.items[0].webkitGetAsEntry) {
      for (var i = 0; i < dt.items.length; i++) {
        var e = dt.items[i].webkitGetAsEntry();
        if (e) entries.push(e);
      }
    }
    if (!entries.length) {
      return Promise.resolve(Array.prototype.map.call(dt.files || [], function (f) { return { path: f.name, file: f }; }));
    }
    return Promise.all(entries.map(function (e) { return walk(e, ""); })).then(function (lists) {
      var items = [].concat.apply([], lists);
      return entries.length === 1 && entries[0].isDirectory ? stripTop(items) : items;
    });
  }

  function fromInput(input, folder) {
    var items = Array.prototype.map.call(input.files || [], function (f) {
      return { path: folder && f.webkitRelativePath ? f.webkitRelativePath : f.name, file: f };
    });
    return folder ? stripTop(items) : items;
  }

  function encodePath(p) { return p.split("/").map(encodeURIComponent).join("/"); }

  function reason(xhr) {
    var code = "", text = "";
    try {
      var body = JSON.parse(xhr.responseText);
      code = body.error.code;
      text = body.error.message;
    } catch (e) { /* not JSON */ }
    if (code === "upload_link_invalid") return { fatal: true, text: msg("expired") };
    if (code === "archive_file_too_large") return { text: msg("too-big") };
    if (code === "archive_too_large") return { text: msg("total") };
    if (code === "archive_too_many_files") return { text: msg("many") };
    if (/secret/.test(text)) return { skip: true, text: msg("secret") };
    return { text: msg("rejected") + (text ? ": " + text : "") };
  }

  function send(item, onProgress) {
    return new Promise(function (resolve) {
      var xhr = new XMLHttpRequest();
      xhr.open("PUT", base + "/" + encodePath(item.path));
      xhr.setRequestHeader("Content-Type", "application/octet-stream");
      xhr.upload.onprogress = function (e) { onProgress(e.loaded); };
      xhr.onload = function () {
        if (xhr.status >= 200 && xhr.status < 300) return resolve({ ok: true });
        if (xhr.status === 429 || xhr.status >= 500) return resolve({ retry: true, wait: Number(xhr.getResponseHeader("Retry-After")) || 2 });
        resolve(reason(xhr));
      };
      xhr.onerror = function () { resolve({ retry: true, wait: 2, text: msg("network") }); };
      xhr.send(item.file);
    });
  }

  function sendWithRetry(item, onProgress) {
    var attempt = 0;
    function once() {
      return send(item, onProgress).then(function (r) {
        if (r.retry && attempt < RETRIES) {
          attempt++;
          onProgress(0);
          return new Promise(function (ok) { setTimeout(ok, Math.min(r.wait, 30) * 1000); }).then(once);
        }
        if (r.retry) return { text: r.text || msg("rejected") };
        return r;
      });
    }
    return once();
  }

  function setProgress(fraction) {
    var p = Math.round(fraction * 100);
    bar.style.transform = "scaleX(" + fraction + ")";
    pct.textContent = p + "%";
    barWrap.setAttribute("aria-valuenow", String(p));
  }

  function addError(path, text) {
    var li = document.createElement("li");
    li.className = "upload-link__error";
    var name = document.createElement("span");
    name.className = "mono";
    name.textContent = path;
    li.appendChild(name);
    li.appendChild(document.createTextNode(" — " + text));
    errors.appendChild(li);
    errors.hidden = false;
  }

  function showResult(text, ok) {
    result.textContent = text;
    result.className = "notice upload-link__result " + (ok ? "notice--ok" : "notice--warn");
    result.hidden = false;
  }

  function upload(items) {
    if (busy) return;
    items = items.filter(function (it) { return it.path && !ignored(it.path); });
    result.hidden = true;
    errors.textContent = "";
    errors.hidden = true;
    if (!items.length) {
      showResult(msg("empty"), false);
      return;
    }
    busy = true;
    drop.classList.add("is-busy");
    progress.hidden = false;
    count.hidden = false;
    var total = items.reduce(function (s, it) { return s + it.file.size; }, 0) || 1;
    var loaded = items.map(function () { return 0; });
    var done = 0, failed = 0, next = 0, stopped = false;
    function tick() {
      setProgress(Math.min(1, loaded.reduce(function (s, n) { return s + n; }, 0) / total));
      count.textContent = fill(msg("counter"), { done: done, total: items.length });
    }
    tick();
    function worker() {
      if (stopped || next >= items.length) return Promise.resolve();
      var i = next++;
      var it = items[i];
      return sendWithRetry(it, function (n) { loaded[i] = n; tick(); }).then(function (r) {
        if (r.ok) {
          loaded[i] = it.file.size;
        } else {
          if (!r.skip) failed++;
          addError(it.path, r.text);
          if (r.fatal) stopped = true;
        }
        done++;
        tick();
        return worker();
      });
    }
    var workers = [];
    for (var w = 0; w < PARALLEL; w++) workers.push(worker());
    Promise.all(workers).then(function () {
      busy = false;
      drop.classList.remove("is-busy");
      if (stopped) {
        showResult(msg("expired"), false);
      } else if (failed) {
        showResult(msg("partial"), false);
      } else {
        setProgress(1);
        showResult(msg("done"), true);
      }
    });
  }

  ["dragenter", "dragover"].forEach(function (t) {
    drop.addEventListener(t, function (e) {
      e.preventDefault();
      drop.classList.add("is-over");
    });
  });
  ["dragleave", "drop"].forEach(function (t) {
    drop.addEventListener(t, function () { drop.classList.remove("is-over"); });
  });
  drop.addEventListener("drop", function (e) {
    e.preventDefault();
    fromDrop(e.dataTransfer).then(upload);
  });
  // A file dropped beside the zone must not navigate away from the page.
  window.addEventListener("dragover", function (e) { e.preventDefault(); });
  window.addEventListener("drop", function (e) { e.preventDefault(); });

  var folder = root.querySelector("[data-pick-folder]");
  var files = root.querySelector("[data-pick-files]");
  folder.addEventListener("change", function () { upload(fromInput(folder, true)); folder.value = ""; });
  files.addEventListener("change", function () { upload(fromInput(files, false)); files.value = ""; });
})();
