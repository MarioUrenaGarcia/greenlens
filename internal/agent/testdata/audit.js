// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Accessibility checks run inside the page by the end-to-end tests. Contrast
// follows WCAG 2.2: text needs 4.5:1, or 3:1 when large, and focus
// indicators 3:1. The background behind an element is composed from the
// solid layers of its ancestors over the page background with every glow of
// the backdrop at full strength, so the ratio is the worst the page can
// show. The blur and saturation of the glass are not modeled.
(() => {
  const parse = (s) => {
    const m = /rgba?\(([^)]+)\)/.exec(s || "");
    if (!m) return null;
    const p = m[1]
      .split(/[\s,/]+/)
      .filter(Boolean)
      .map(parseFloat);
    return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
  };
  const over = (top, bottom) => {
    const a = top.a + bottom.a * (1 - top.a);
    if (a === 0) return { r: 0, g: 0, b: 0, a: 0 };
    const mix = (x, y) => (x * top.a + y * bottom.a * (1 - top.a)) / a;
    return { r: mix(top.r, bottom.r), g: mix(top.g, bottom.g), b: mix(top.b, bottom.b), a };
  };
  const lum = (c) => {
    const f = (v) => {
      const x = v / 255;
      return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
  };
  const ratio = (a, b) => {
    const x = lum(a);
    const y = lum(b);
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  };
  const token = (name) => {
    const probe = document.createElement("span");
    probe.style.color = "var(" + name + ")";
    document.body.appendChild(probe);
    const c = parse(getComputedStyle(probe).color);
    probe.remove();
    return c;
  };
  // Pages without the tokens of the interface, such as the printable
  // reports, have their own plain background.
  const themed = () => getComputedStyle(document.documentElement).getPropertyValue("--bg-deep").trim() !== "";
  const backdrops = () => {
    if (!themed()) {
      const bg = parse(getComputedStyle(document.documentElement).backgroundColor);
      return [bg && bg.a > 0 ? bg : { r: 255, g: 255, b: 255, a: 1 }];
    }
    const deep = token("--bg-deep");
    const eff = over(token("--bg-glow-eff"), deep);
    const load = over(token("--bg-glow-load"), deep);
    return [deep, eff, load, over(token("--bg-glow-load"), eff)];
  };

  // layers returns the solid colors painted behind and on el, bottom first,
  // from the outermost ancestor below body down to el; html holds the
  // backdrop.
  const layers = (el) => {
    const out = [];
    for (let n = el; n && n !== document.body && n !== document.documentElement; n = n.parentElement) {
      const cs = getComputedStyle(n);
      const own = [];
      const bg = parse(cs.backgroundColor);
      if (bg && bg.a > 0) own.push(bg);
      if (cs.backgroundImage !== "none") {
        const colors = cs.backgroundImage.match(/rgba?\([^)]+\)/g) || [];
        // Only gradients of one color, as the glass uses, are solid.
        if (colors.length > 0 && colors.every((c) => c === colors[0])) own.push(parse(colors[0]));
      }
      out.unshift(...own);
    }
    return out;
  };
  const opacity = (el) => {
    let o = 1;
    for (let n = el; n; n = n.parentElement) o *= parseFloat(getComputedStyle(n).opacity);
    return o;
  };
  const worst = (el, color) => {
    let min = Infinity;
    let at = null;
    for (const base of backdrops()) {
      const bg = layers(el).reduce((acc, l) => over(l, acc), base);
      const fg = over({ ...color, a: color.a * opacity(el) }, bg);
      const r = ratio(fg, bg);
      if (r < min) {
        min = r;
        at = bg;
      }
    }
    return { ratio: min, bg: at };
  };
  const rgb = (c) => "rgb(" + [c.r, c.g, c.b].map(Math.round).join(", ") + ")";
  const visible = (el) => {
    const r = el.getBoundingClientRect();
    if (r.width <= 1 || r.height <= 1) return false;
    const cs = getComputedStyle(el);
    return cs.visibility !== "hidden" && cs.display !== "none";
  };
  const disabled = (el) => el.closest("button:disabled, input:disabled, select:disabled, textarea:disabled, fieldset:disabled");

  const contrast = () => {
    const out = [];
    const seen = new Set();
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let t = walker.nextNode(); t; t = walker.nextNode()) {
      const el = t.parentElement;
      if (!el || seen.has(el) || t.textContent.trim() === "") continue;
      seen.add(el);
      if (el.closest("script, style, option, noscript, template") || !visible(el) || disabled(el)) continue;
      const cs = getComputedStyle(el);
      const svg = el instanceof SVGElement;
      const color = parse(svg ? cs.fill : cs.color);
      if (!color) continue;
      const size = parseFloat(cs.fontSize);
      const large = size >= 24 || (size >= 18.66 && parseInt(cs.fontWeight, 10) >= 700);
      const need = large ? 3 : 4.5;
      const w = worst(el, color);
      if (w.ratio < need - 0.005) {
        out.push({
          kind: "contrast",
          text: t.textContent.trim().slice(0, 60),
          ratio: Math.round(w.ratio * 100) / 100,
          need,
          fg: rgb(color),
          bg: rgb(w.bg),
        });
      }
    }
    for (const el of document.querySelectorAll("input:not([type=hidden]):not([type=checkbox]):not([type=radio]), select, textarea")) {
      if (!visible(el) || disabled(el)) continue;
      const w = worst(el, parse(getComputedStyle(el).color));
      if (w.ratio < 4.5 - 0.005) {
        out.push({ kind: "contrast", text: "<" + el.tagName.toLowerCase() + " id=" + el.id + ">", ratio: Math.round(w.ratio * 100) / 100, need: 4.5 });
      }
    }
    return out;
  };

  const structure = (lang) => {
    const out = [];
    if (document.documentElement.lang !== lang) out.push({ kind: "lang", text: document.documentElement.lang });
    const main = document.querySelectorAll("main");
    if (main.length !== 1) out.push({ kind: "main", text: String(main.length) });
    const h1 = document.querySelectorAll("main h1");
    if (h1.length !== 1) out.push({ kind: "h1", text: String(h1.length) });
    let last = 0;
    for (const h of document.querySelectorAll("main h1, main h2, main h3, main h4, main h5, main h6")) {
      const level = Number(h.tagName[1]);
      if (last && level > last + 1) out.push({ kind: "heading-skip", text: h.tagName + " " + h.textContent.trim().slice(0, 40) });
      last = level;
    }
    const ids = new Map();
    for (const el of document.querySelectorAll("[id]")) ids.set(el.id, (ids.get(el.id) || 0) + 1);
    for (const [id, n] of ids) if (n > 1) out.push({ kind: "duplicate-id", text: id });
    for (const el of document.querySelectorAll("img")) if (!el.hasAttribute("alt")) out.push({ kind: "img-alt", text: el.src });
    for (const el of document.querySelectorAll("svg")) {
      if (el.closest("[aria-hidden=true]")) continue;
      const named = el.getAttribute("role") === "img" && (el.getAttribute("aria-label") || el.querySelector("title"));
      if (!named) out.push({ kind: "svg", text: el.outerHTML.slice(0, 80) });
    }
    for (const el of document.querySelectorAll("canvas")) {
      if (!el.closest("[aria-hidden=true], [role=img][aria-label]")) out.push({ kind: "canvas", text: el.className });
    }
    if (!document.title) out.push({ kind: "title", text: "" });
    return out;
  };

  // focus describes the focused element and whether its indicator shows
  // with enough contrast.
  const focus = () => {
    const el = document.activeElement;
    if (!el || el === document.body) return { tag: "BODY" };
    const cs = getComputedStyle(el);
    const outline = cs.outlineStyle !== "none" && parseFloat(cs.outlineWidth) >= 2;
    let indicator = 0;
    if (outline) indicator = worst(el.parentElement || el, parse(cs.outlineColor)).ratio;
    return {
      tag: el.tagName,
      id: el.id,
      href: el.getAttribute("href"),
      text: (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 40),
      visible: visible(el) && el.getBoundingClientRect().bottom > 0,
      outline,
      indicator: Math.round(indicator * 100) / 100,
    };
  };

  window.__glAudit = { contrast, structure, focus };
})();
