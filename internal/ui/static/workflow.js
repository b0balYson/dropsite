// Fills the browser's time zone offset into schedule forms, so the server
// reads <input type="datetime-local"> in the user's local time.
(function () {
  "use strict";
  document.querySelectorAll("[data-tz-form]").forEach(function (form) {
    form.addEventListener("submit", function () {
      var at = form.querySelector("input[type=datetime-local]");
      var off = form.querySelector("[data-tz-offset]");
      if (!at || !off || !at.value) {
        return;
      }
      off.value = String(new Date(at.value).getTimezoneOffset());
    });
  });
})();
