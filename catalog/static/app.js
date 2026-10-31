(function () {
  "use strict";

  function $(id) { return document.getElementById(id); }

  var grid = $("grid");
  var emptyEl = $("empty");
  var countEl = $("count");
  var filterEl = $("filter");
  var sortEl = $("sort");
  var genreEl = $("genre");
  var statusEl = $("status");
  var kindEl = $("kind");
  var rescanBtn = $("rescan");
  var openingEl = $("opening");
  var openingTitle = $("opening-title");

  var editorEl = $("editor");
  var edForm = $("ed-form");
  var edError = $("ed-error");
  var edSave = $("ed-save");

  var DEFAULT_GENRES = [
    "Action", "Adventure", "Animation", "Comedy", "Concert", "Crime", "Documentary", "Drama",
    "Family", "Fantasy", "History", "Horror", "Music", "Mystery", "Romance",
    "Science Fiction", "Thriller", "War", "Western"
  ];

  var allDiscs = [];
  var knownGenres = [];
  var knownSeries = [];
  var mediaDir = "/media";
  var prefs = { sort: "title", genre: "", status: "", kind: "" };
  var firstLoad = true;

  var editing = null;
  var pendingFile = null;
  var removeCover = false;

  // ------------------------------------------------------------ helpers
  function api(path, opts) {
    return fetch(path, opts).then(function (r) {
      return r.json().then(null, function () { return {}; }).then(function (data) {
        if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
        return data;
      });
    });
  }

  function jsonOpts(method, body) {
    return { method: method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  }

  function fmtTime(sec) {
    sec = Math.max(0, Math.floor(sec));
    var h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
    return (h ? h + ":" + (m < 10 ? "0" : "") : "") + m + ":" + (s < 10 ? "0" : "") + s;
  }

  // "Continue at 1:23:10" for a DVD, "Continue: track 7 · 1:02" for a CD
  function resumeText(d) {
    var r = d.resume;
    if (!r) return "";
    return d.kind === "cd" ? "Continue: track " + r.track + " \u00b7 " + fmtTime(r.track_time)
                           : "Continue at " + fmtTime(r.time);
  }

  function formatSize(bytes) {
    return (bytes / 1073741824).toFixed(1) + " GB";
  }

  function loadPrefs() {
    try {
      var p = JSON.parse(localStorage.getItem("streamisoPrefs") || "{}");
      if (p.sort) prefs.sort = p.sort;
      if (typeof p.genre === "string") prefs.genre = p.genre;
      if (typeof p.status === "string") prefs.status = p.status;
      if (typeof p.kind === "string") prefs.kind = p.kind;
    } catch (e) { /* no localStorage: use the defaults */ }
  }

  function savePrefs() {
    try { localStorage.setItem("streamisoPrefs", JSON.stringify(prefs)); } catch (e) { /* ignore */ }
  }

  function findDisc(id) {
    for (var i = 0; i < allDiscs.length; i++) if (allDiscs[i].id === id) return allDiscs[i];
    return null;
  }

  // ------------------------------------------------------------ sorting and filtering
  function cmpText(a, b) {
    return String(a).localeCompare(String(b), "en", { sensitivity: "base", numeric: true });
  }
  function num(v, fallback) { return v === null || v === undefined ? fallback : v; }

  var sorters = {
    "title": function (a, b) { return cmpText(a.title, b.title); },
    "year-desc": function (a, b) {
      return (num(b.year, -1) - num(a.year, -1)) || cmpText(a.title, b.title);
    },
    "year-asc": function (a, b) {
      return (num(a.year, 99999) - num(b.year, 99999)) || cmpText(a.title, b.title);
    },
    "added": function (a, b) { return (b.added - a.added) || cmpText(a.title, b.title); },
    "series": function (a, b) {
      if (!a.series !== !b.series) return a.series ? -1 : 1;  // discs with a series first
      return cmpText(a.series, b.series) ||
        (num(a.season, 0) - num(b.season, 0)) ||
        (num(a.disc, 0) - num(b.disc, 0)) ||
        cmpText(a.title, b.title);
    }
  };

  function visibleList() {
    var q = filterEl.value.trim().toLowerCase();
    var list = allDiscs.filter(function (d) {
      if (prefs.genre && d.genres.indexOf(prefs.genre) === -1) return false;
      if (prefs.kind && d.kind !== prefs.kind) return false;
      if (prefs.status === "watched" && !d.watched) return false;
      if (prefs.status === "unwatched" && d.watched) return false;
      if (q) {
        var hay = (d.title + " " + d.series + " " + (d.artist || "") + " " + d.genres.join(" ")).toLowerCase();
        if (hay.indexOf(q) === -1) return false;
      }
      return true;
    });
    list.sort(sorters[prefs.sort] || sorters.title);
    return list;
  }

  // ------------------------------------------------------------ grid
  function subText(d) {
    var parts = [];
    if (d.kind === "cd") {
      if (d.artist) parts.push(d.artist);
      if (d.year) parts.push(d.year);
      parts.push(d.track_count + (d.track_count === 1 ? " track" : " tracks"));
      return parts.join(" \u00b7 ");
    }
    if (d.year) parts.push(d.year);
    if (d.series) parts.push(d.series);
    var st = [];
    if (d.season !== null && d.season !== undefined) st.push("S" + d.season);
    if (d.disc !== null && d.disc !== undefined) st.push("D" + d.disc);
    if (st.length) parts.push(st.join(" "));
    return parts.join(" · ");
  }

  // "watched" for DVDs, "listened" for audio CDs
  function seenWord(d) { return d.kind === "cd" ? "listened" : "watched"; }
  function cap(s) { return s.charAt(0).toUpperCase() + s.slice(1); }

  function buildItem(d) {
    var item = document.createElement("div");
    item.className = "item" + (d.watched ? " watched" : "") + (d.kind === "cd" ? " cd" : "");
    item.dataset.id = d.id;
    item.setAttribute("role", "listitem");

    var card = document.createElement("button");
    card.type = "button";
    card.className = "card";
    card.title = d.title + (d.synopsis ? "\n\n" + d.synopsis.slice(0, 300) : "") +
      "\n\n" + formatSize(d.size) + " · E: edit · W: " + seenWord(d);

    var coverUrl = "/api/cover/" + d.id + "?v=" + d.cover_v;
    var img = document.createElement("span");
    img.className = "cover";
    var bg = document.createElement("img");
    bg.className = "bg";
    bg.alt = "";
    bg.loading = "lazy";
    bg.src = coverUrl;
    var fg = document.createElement("img");
    fg.className = "fg";
    fg.alt = "";
    fg.loading = "lazy";
    fg.src = coverUrl;
    img.appendChild(bg);
    img.appendChild(fg);

    var title = document.createElement("span");
    title.className = "title";
    title.textContent = d.title;

    var sub = document.createElement("span");
    sub.className = "sub";
    sub.textContent = subText(d) || formatSize(d.size);

    card.appendChild(img);
    card.appendChild(title);
    card.appendChild(sub);
    if (d.genres.length) {
      var g = document.createElement("span");
      g.className = "genres";
      g.textContent = d.genres.slice(0, 3).join(" · ");
      card.appendChild(g);
    }
    if (d.resume) {
      if (d.resume.pct !== null && d.resume.pct !== undefined) {
        var bar = document.createElement("span");
        bar.className = "progress";
        var fill = document.createElement("span");
        fill.style.width = Math.max(3, Math.min(100, d.resume.pct)) + "%";
        bar.appendChild(fill);
        img.appendChild(bar);
      }
      var rs = document.createElement("span");
      rs.className = "resume";
      rs.textContent = "\u25b6 " + resumeText(d);
      card.appendChild(rs);
    }
    card.addEventListener("click", function () { play(d, !!d.resume); });

    var kindTag = document.createElement("span");
    kindTag.className = "kindtag";
    kindTag.textContent = d.kind === "cd" ? "\uD83C\uDFB5 CD" : "\uD83C\uDF9E\uFE0F DVD";

    var ribbon = document.createElement("span");
    ribbon.className = "ribbon";
    ribbon.textContent = "\u2713 " + cap(seenWord(d));

    var tools = document.createElement("div");
    tools.className = "tools";

    var mark = document.createElement("button");
    mark.type = "button";
    mark.className = "tool";
    mark.textContent = "✓";
    mark.title = d.watched ? "Mark as not " + seenWord(d) : "Mark as " + seenWord(d);
    mark.setAttribute("aria-pressed", d.watched ? "true" : "false");
    mark.addEventListener("click", function (e) { e.stopPropagation(); toggleWatched(d); });

    var edit = document.createElement("button");
    edit.type = "button";
    edit.className = "tool";
    edit.textContent = "✎";
    edit.title = "Edit details";
    edit.addEventListener("click", function (e) { e.stopPropagation(); openEditor(d); });

    if (d.resume) {
      var again = document.createElement("button");
      again.type = "button";
      again.className = "tool";
      again.textContent = "\u21ba";
      again.title = "Start over";
      again.addEventListener("click", function (e) { e.stopPropagation(); play(d, false); });
      tools.appendChild(again);
    }
    tools.appendChild(mark);
    tools.appendChild(edit);

    item.appendChild(card);
    item.appendChild(ribbon);
    if (kindTag) img.appendChild(kindTag);
    item.appendChild(tools);
    return item;
  }

  function focusCard(id) {
    var c = grid.querySelector('.item[data-id="' + id + '"] .card');
    if (!c) return false;
    try { c.focus({ preventScroll: true }); } catch (e) { c.focus(); }
    return true;
  }

  // Wording of the status filter: "watched" for DVDs, "listened" for CDs, "played" for a mix.
  function statusLabels() {
    var hasCd = allDiscs.some(function (d) { return d.kind === "cd"; });
    var pair = prefs.kind === "cd" ? ["Not listened", "Listened"]
      : (prefs.kind === "dvd" || !hasCd) ? ["Unwatched", "Watched"] : ["Not played", "Played"];
    statusEl.options[1].textContent = pair[0];
    statusEl.options[2].textContent = pair[1];
    statusEl.setAttribute("aria-label", pair[1] + " status");
  }

  function render() {
    statusLabels();
    var focusId = null;
    var active = document.activeElement;
    if (active && active.closest) {
      var it = active.closest(".item");
      if (it && grid.contains(it)) focusId = it.dataset.id;
    }

    var list = visibleList();
    grid.textContent = "";
    // One section per kind, so DVDs and CDs are not mixed. Headings only matter when both exist.
    var kinds = {};
    allDiscs.forEach(function (d) { kinds[d.kind] = true; });
    var showHeadings = !!(kinds.dvd && kinds.cd);
    [["dvd", "DVDs"], ["cd", "Music CDs"]].forEach(function (g) {
      var items = list.filter(function (d) { return d.kind === g[0]; });
      if (!items.length) return;
      var section = document.createElement("section");
      section.className = "group";
      if (showHeadings) {
        var h = document.createElement("h2");
        h.textContent = g[1];
        var n = document.createElement("span");
        n.className = "count";
        n.textContent = String(items.length);
        h.appendChild(n);
        section.appendChild(h);
      }
      var cards = document.createElement("div");
      cards.className = "cards";
      cards.setAttribute("role", "list");
      items.forEach(function (d) { cards.appendChild(buildItem(d)); });
      section.appendChild(cards);
      grid.appendChild(section);
    });

    if (!allDiscs.length) {
      countEl.textContent = "";
    } else if (list.length === allDiscs.length) {
      countEl.textContent = allDiscs.length + (allDiscs.length === 1 ? " disc" : " discs");
    } else {
      countEl.textContent = list.length + " of " + allDiscs.length;
    }

    if (!allDiscs.length) {
      emptyEl.hidden = false;
      emptyEl.innerHTML =
        "Nothing found. Put DVD <code>.iso</code> files in your DVD folder and audio CD " +
        "<code>.cue</code>/<code>.bin</code> files in your CD folder, then click <b>Refresh</b>. " +
        "Folders in use (inside the container): <code></code>";
      emptyEl.querySelector("code:last-of-type").textContent = mediaDir;
    } else if (!list.length) {
      emptyEl.hidden = false;
      emptyEl.textContent = "Nothing matches these filters.";
    } else {
      emptyEl.hidden = true;
    }

    if (focusId) focusCard(focusId);
  }

  function refreshGenreOptions(genres) {
    var current = prefs.genre;
    genreEl.textContent = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "All genres";
    genreEl.appendChild(all);
    genres.forEach(function (g) {
      var o = document.createElement("option");
      o.value = g;
      o.textContent = g;
      genreEl.appendChild(o);
    });
    if (current && genres.indexOf(current) === -1) {
      prefs.genre = "";  // the filtered genre no longer exists
      savePrefs();
    }
    genreEl.value = prefs.genre;
  }

  function load() {
    return api("/api/discs").then(function (data) {
      allDiscs = data.discs;
      knownGenres = data.genres;
      knownSeries = data.series;
      mediaDir = data.media_dir;
      refreshGenreOptions(knownGenres);
      var dl = $("series-list");
      dl.textContent = "";
      knownSeries.forEach(function (s) {
        var o = document.createElement("option");
        o.value = s;
        dl.appendChild(o);
      });
      render();
      if (firstLoad) {
        firstLoad = false;
        var first = grid.querySelector(".card");
        if (first && !editorOpen()) first.focus();
      }
    }).then(null, function () {
      emptyEl.hidden = false;
      emptyEl.textContent = "Could not load the library. Is the service running?";
    });
  }

  // ------------------------------------------------------------ watched
  function toggleWatched(d) {
    var want = !d.watched;
    d.watched = want;
    render();
    api("/api/disc/" + d.id + "/watched", jsonOpts("POST", { watched: want })).then(null, function () {
      d.watched = !want;
      render();
      alert("Could not save. Please try again.");
    });
  }

  // ------------------------------------------------------------ playback
  // When coming back from the player (TV "Back" button), stop the disc this tab started.
  // Only stops it if this tab started it, so it never cuts off someone watching on another device.
  function stopIfWeStartedIt() {
    try {
      if (sessionStorage.getItem("streamisoPlaying") === "1") {
        sessionStorage.removeItem("streamisoPlaying");
        fetch("/api/stop", { method: "POST" });
      }
    } catch (e) { /* sessionStorage unavailable: fine */ }
  }

  function play(disc, resume) {
    openingTitle.textContent = disc.title;
    openingEl.hidden = false;
    api("/api/play/" + disc.id, jsonOpts("POST", { resume: !!resume })).then(function (res) {
      try { sessionStorage.setItem("streamisoPlaying", "1"); } catch (e) { /* ignore */ }
      // Give VLC a moment to start before opening the stream.
      setTimeout(function () { window.location.href = res.player_url; }, 1200);
    }).then(null, function () {
      openingEl.hidden = true;
      alert("Could not open the disc.");
    });
  }

  // ------------------------------------------------------------ editor
  function editorOpen() { return !editorEl.hidden; }

  function genreList() {
    return $("ed-genres").value.split(",").map(function (s) { return s.trim(); }).filter(Boolean);
  }

  function syncChips() {
    var cur = genreList().map(function (g) { return g.toLowerCase(); });
    var chips = $("ed-chips").querySelectorAll(".chip");
    for (var i = 0; i < chips.length; i++) {
      var on = cur.indexOf(chips[i].textContent.toLowerCase()) !== -1;
      chips[i].setAttribute("aria-pressed", on ? "true" : "false");
    }
  }

  function buildChips() {
    var box = $("ed-chips");
    box.textContent = "";
    var seen = {};
    var names = [];
    DEFAULT_GENRES.concat(knownGenres).forEach(function (g) {
      var k = g.toLowerCase();
      if (!seen[k]) { seen[k] = true; names.push(g); }
    });
    names.sort(cmpText);
    names.forEach(function (g) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "chip";
      b.textContent = g;
      b.addEventListener("click", function () {
        var list = genreList();
        var idx = -1;
        for (var i = 0; i < list.length; i++) if (list[i].toLowerCase() === g.toLowerCase()) idx = i;
        if (idx === -1) list.push(g); else list.splice(idx, 1);
        $("ed-genres").value = list.join(", ");
        syncChips();
      });
      box.appendChild(b);
    });
  }

  function setCoverPreview() {
    var img = $("ed-cover");
    var note = $("ed-cover-note");
    var rm = $("ed-cover-remove");
    note.hidden = true;
    img.className = "";
    if (pendingFile) {
      img.src = URL.createObjectURL(pendingFile);
      note.hidden = false;
      note.textContent = "The new cover will be applied when you save.";
    } else if (removeCover) {
      img.src = "/api/cover/" + editing.id + "?v=" + editing.cover_v;
      img.className = "faded";
      note.hidden = false;
      note.textContent = "The custom cover will be removed when you save.";
    } else {
      img.src = "/api/cover/" + editing.id + "?v=" + editing.cover_v;
    }
    rm.hidden = !((editing.custom_cover && !removeCover) || pendingFile);
  }

  function openEditor(d) {
    editing = d;
    pendingFile = null;
    removeCover = false;
    $("ed-cover-file").value = "";

    var c = d.custom || {};
    var a = d.auto || {};
    $("ed-heading").textContent = "Edit: " + d.title;
    $("ed-info").textContent = d.rel + (d.label ? " · label: " + d.label : "") + " · " + formatSize(d.size);

    $("ed-title").value = c.title || "";
    $("ed-title").placeholder = a.title || "";
    $("ed-year").value = c.year !== undefined && c.year !== null ? c.year : "";
    $("ed-year").placeholder = a.year || "";
    $("ed-series").value = c.series || "";
    $("ed-season").value = c.season !== undefined && c.season !== null ? c.season : "";
    $("ed-disc").value = c.disc !== undefined && c.disc !== null ? c.disc : "";
    $("ed-genres").value = (c.genres || []).join(", ");
    $("ed-genres").placeholder = (a.genres || []).join(", ");
    $("ed-synopsis").value = c.synopsis || "";
    $("ed-synopsis").placeholder = a.synopsis || "";
    $("ed-watched").checked = !!d.watched;
    $("ed-watched-label").textContent = cap(seenWord(d));
    document.querySelector(".ed-cover").classList.toggle("cd", d.kind === "cd");

    buildChips();
    syncChips();
    setCoverPreview();
    edError.hidden = true;
    $("ed-query").value = "";
    $("ed-lookup-note").hidden = true;
    edSave.disabled = false;
    editorEl.hidden = false;
    $("ed-title").focus();
  }

  function closeEditor() {
    var id = editing && editing.id;
    editorEl.hidden = true;
    editing = null;
    pendingFile = null;
    removeCover = false;
    if (id) focusCard(id);
  }

  function showError(err) {
    edError.textContent = err && err.message ? err.message : "Could not save.";
    edError.hidden = false;
  }

  function numOrNull(id) {
    var v = $(id).value.trim();
    return v === "" ? null : Number(v);
  }

  function applyCoverChange(d) {
    if (pendingFile) {
      var fd = new FormData();
      fd.append("cover", pendingFile);
      return api("/api/disc/" + d.id + "/cover", { method: "POST", body: fd });
    }
    if (removeCover) return api("/api/disc/" + d.id + "/cover", { method: "DELETE" });
    return Promise.resolve();
  }

  edForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var d = editing;
    if (!d) return;
    var payload = {
      title: $("ed-title").value,
      year: numOrNull("ed-year"),
      series: $("ed-series").value,
      season: numOrNull("ed-season"),
      disc: numOrNull("ed-disc"),
      genres: genreList(),
      synopsis: $("ed-synopsis").value,
      watched: $("ed-watched").checked
    };
    edSave.disabled = true;
    edError.hidden = true;
    api("/api/disc/" + d.id + "/meta", jsonOpts("PUT", payload))
      .then(function () { return applyCoverChange(d); })
      .then(function () { var id = d.id; closeEditor(); return load().then(function () { focusCard(id); }); })
      .then(null, function (err) { showError(err); edSave.disabled = false; });
  });

  $("ed-cancel").addEventListener("click", closeEditor);
  editorEl.addEventListener("mousedown", function (e) { if (e.target === editorEl) closeEditor(); });
  $("ed-genres").addEventListener("input", syncChips);

  $("ed-cover-file").addEventListener("change", function () {
    var f = this.files && this.files[0];
    if (!f) return;
    pendingFile = f;
    removeCover = false;
    setCoverPreview();
  });

  $("ed-cover-remove").addEventListener("click", function () {
    if (pendingFile) { pendingFile = null; $("ed-cover-file").value = ""; }
    else if (editing.custom_cover) removeCover = true;
    setCoverPreview();
  });

  $("ed-lookup").addEventListener("click", function () {
    var d = editing;
    if (!d) return;
    var btn = $("ed-lookup");
    var note = $("ed-lookup-note");
    btn.disabled = true;
    btn.textContent = "Searching\u2026";
    note.hidden = true;
    edError.hidden = true;
    api("/api/disc/" + d.id + "/lookup", jsonOpts("POST", { query: $("ed-query").value.trim() }))
      .then(function (res) {
        return load().then(function () {
          var fresh = findDisc(d.id);
          if (fresh) {
            editing = fresh;
            setCoverPreview();
            var a = fresh.auto || {};
            $("ed-title").placeholder = a.title || "";
            $("ed-year").placeholder = a.year || "";
            $("ed-genres").placeholder = (a.genres || []).join(", ");
            $("ed-synopsis").placeholder = a.synopsis || "";
          }
          note.textContent = !res.found
            ? "Nothing found. Try the name of the series or album alone."
            : !res.cover
              ? "Found the album and its details, but no cover art exists for it online. You can upload one with \u201cChange cover\u201d."
              : "Found. The cover and details were updated (your own edits still take priority).";
          note.hidden = false;
        });
      })
      .then(null, showError)
      .then(function () { btn.disabled = false; btn.textContent = "Search again"; });
  });

  $("ed-reset").addEventListener("click", function () {
    var d = editing;
    if (!d) return;
    if (!window.confirm("Remove all edits for this disc (including watched status and custom cover)?")) return;
    api("/api/disc/" + d.id + "/meta", jsonOpts("PUT", {}))
      .then(function () { return api("/api/disc/" + d.id + "/cover", { method: "DELETE" }); })
      .then(function () { var id = d.id; closeEditor(); return load().then(function () { focusCard(id); }); })
      .then(null, showError);
  });

  // ------------------------------------------------------------ keyboard (TV remote)
  function move(key) {
    var cards = Array.prototype.slice.call(grid.querySelectorAll(".card"));
    if (!cards.length) return;
    var cur = document.activeElement;
    var item = cur && cur.closest ? cur.closest(".item") : null;
    cur = item ? item.querySelector(".card") : null;
    if (!cur) { cards[0].focus(); return; }

    var r = cur.getBoundingClientRect();
    var cx = r.left + r.width / 2, cy = r.top + r.height / 2;
    var best = null, bestScore = Infinity;

    cards.forEach(function (c) {
      if (c === cur) return;
      var b = c.getBoundingClientRect();
      var dx = b.left + b.width / 2 - cx, dy = b.top + b.height / 2 - cy;
      var ok, main, cross;
      if (key === "ArrowRight") { ok = dx > 10; main = dx; cross = Math.abs(dy); }
      else if (key === "ArrowLeft") { ok = dx < -10; main = -dx; cross = Math.abs(dy); }
      else if (key === "ArrowDown") { ok = dy > 10; main = dy; cross = Math.abs(dx); }
      else { ok = dy < -10; main = -dy; cross = Math.abs(dx); }
      if (!ok) return;
      var score = main + cross * 2.5;
      if (score < bestScore) { bestScore = score; best = c; }
    });

    if (best) {
      best.focus();
      best.scrollIntoView({ block: "center", behavior: "smooth" });
    }
    return best;
  }

  document.addEventListener("keydown", function (e) {
    if (editorOpen()) {
      if (e.key === "Escape") { e.preventDefault(); closeEditor(); }
      return;
    }
    var t = e.target;
    var tag = t && t.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
      if (t === filterEl && e.key === "ArrowDown") {
        e.preventDefault();
        var c = grid.querySelector(".card");
        if (c) c.focus();
      }
      return;
    }

    var itemEl = t && t.closest ? t.closest(".item") : null;
    if (itemEl && (e.key === "e" || e.key === "E")) {
      e.preventDefault();
      var d1 = findDisc(itemEl.dataset.id);
      if (d1) openEditor(d1);
      return;
    }
    if (itemEl && (e.key === "w" || e.key === "W")) {
      e.preventDefault();
      var d2 = findDisc(itemEl.dataset.id);
      if (d2) toggleWatched(d2);
      return;
    }

    if (e.key && e.key.indexOf("Arrow") === 0) {
      e.preventDefault();
      if (e.key === "ArrowUp" && itemEl) {
        if (!move(e.key)) filterEl.focus();  // top of the grid: go up to the filter box
      } else {
        move(e.key);
      }
    }
  });

  // ------------------------------------------------------------ top bar controls
  sortEl.addEventListener("change", function () { prefs.sort = sortEl.value; savePrefs(); render(); });
  genreEl.addEventListener("change", function () { prefs.genre = genreEl.value; savePrefs(); render(); });
  kindEl.addEventListener("change", function () { prefs.kind = kindEl.value; savePrefs(); statusLabels(); render(); });
  statusEl.addEventListener("change", function () { prefs.status = statusEl.value; savePrefs(); render(); });
  filterEl.addEventListener("input", render);

  rescanBtn.addEventListener("click", function () {
    rescanBtn.disabled = true;
    api("/api/rescan", { method: "POST" }).then(load).then(function () { rescanBtn.disabled = false; },
      function () { rescanBtn.disabled = false; });
  });

  // When returning via browser history (bfcache) the page can reappear without reloading.
  window.addEventListener("pageshow", function () { openingEl.hidden = true; stopIfWeStartedIt(); });

  loadPrefs();
  sortEl.value = prefs.sort;
  statusEl.value = prefs.status;
  kindEl.value = prefs.kind;
  stopIfWeStartedIt();
  load();
})();
