/* Dropsite visitor session for /_dropsite/proxy/ (no widget, no click):
   solves a SHA-256 proof of work in a Web Worker (this same file) and calls
   endpoints with dropsite.fetch(url, options). */
(function () {
  "use strict";
  var P = "/_dropsite/";
  function hexBytes(h) {
    var a = new Uint8Array(h.length / 2);
    for (var i = 0; i < a.length; i++) a[i] = parseInt(h.substr(i * 2, 2), 16);
    return a;
  }
  async function solve(c) {
    var p = c.parameters, salt = hexBytes(p.salt), nonce = hexBytes(p.nonce), pre = hexBytes(p.keyPrefix);
    var buf = new Uint8Array(salt.length + nonce.length + 4), dv = new DataView(buf.buffer), t = Date.now();
    buf.set(salt);
    buf.set(nonce, salt.length);
    for (var n = 0; n < 4294967295; n++) {
      dv.setUint32(buf.length - 4, n);
      var k = new Uint8Array(await crypto.subtle.digest("SHA-256", buf)), i = 0;
      while (i < pre.length && k[i] === pre[i]) i++;
      if (i === pre.length) {
        var hex = Array.prototype.map.call(k.slice(0, p.keyLength || 32), function (x) {
          return (x < 16 ? "0" : "") + x.toString(16);
        }).join("");
        return btoa(JSON.stringify({ challenge: c, solution: { counter: n, derivedKey: hex, time: Date.now() - t } }));
      }
    }
    throw new Error("unsolvable challenge");
  }
  if (typeof window === "undefined") {
    self.onmessage = function (e) {
      solve(e.data).then(function (r) { self.postMessage(r); });
    };
    return;
  }
  var src = document.currentScript && document.currentScript.src;
  function work(c) {
    return new Promise(function (ok, fail) {
      var inline = function () { solve(c).then(ok, fail); };
      try {
        var w = new Worker(src);
        w.onmessage = function (e) { w.terminate(); ok(e.data); };
        w.onerror = function (e) { e.preventDefault(); w.terminate(); inline(); };
        w.postMessage(c);
      } catch (e) {
        inline();
      }
    });
  }
  function json(r) {
    if (!r.ok) throw new Error("dropsite session: HTTP " + r.status);
    return r.json();
  }
  function pow(purpose) {
    return fetch(P + "session/challenge?purpose=" + purpose, { credentials: "same-origin" }).then(json).then(work);
  }
  var tok = null, pending = null;
  function session() {
    if (tok && tok.exp > Date.now() + 10000) return Promise.resolve(tok.token);
    if (!pending) {
      pending = pow("session").then(function (payload) {
        return fetch(P + "session", {
          method: "POST", credentials: "same-origin",
          headers: { "Content-Type": "application/json" }, body: JSON.stringify({ payload: payload })
        });
      }).then(json).then(function (j) {
        tok = { token: j.token, exp: Date.parse(j.expires_at) };
        return j.token;
      }).finally(function () { pending = null; });
    }
    return pending;
  }
  var needPow = {};
  async function dfetch(url, opts, tries) {
    tries = tries || 0;
    var o = Object.assign({}, opts), h = new Headers(o.headers || {});
    var m = /\/_dropsite\/proxy\/([^\/?#]+)/.exec(String(url)), name = m ? m[1] : "";
    h.set("X-Dropsite-Session", await session());
    if (needPow[name]) h.set("X-Dropsite-Pow", await pow("send"));
    o.headers = h;
    var r = await fetch(url, o);
    if (r.status === 401 && tries < 2) {
      var code = "";
      try { code = (await r.clone().json()).error.code; } catch (e) { /* not JSON */ }
      if (code === "proxy_session") tok = null;
      if (code === "proxy_pow") needPow[name] = 1;
      if (code === "proxy_session" || code === "proxy_pow") return dfetch(url, opts, tries + 1);
    }
    return r;
  }
  window.dropsite = Object.assign(window.dropsite || {}, { fetch: dfetch, session: session });
  session().catch(function () { /* retried on the first call */ });
})();
