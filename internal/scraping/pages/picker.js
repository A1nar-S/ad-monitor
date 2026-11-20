// Element picker for price watches. It runs inside the dashboard's sandboxed
// iframe, on a script-free copy of the shop's page: hovering draws the
// element's box model like browser dev tools (margin, border, padding,
// content), clicking picks the element and reports a CSS selector for it
// to the dashboard. The dashboard talks back through postMessage.
//
// In "remove" mode a click hides what is in the way instead (a cookie banner
// the copy can't close, having no scripts). Cookie banners are hidden on
// load too. Hidden elements stay in the document, so the selectors built
// here still match the real page the watch reads.
;(() => {
  'use strict'

  const colors = {
    margin: 'rgba(246, 178, 107, 0.66)',
    border: 'rgba(255, 229, 153, 0.66)',
    padding: 'rgba(147, 196, 125, 0.55)',
    content: 'rgba(111, 168, 220, 0.66)',
  }

  // Our overlay lives in a closed shadow root so the shop's CSS can't touch
  // it, and ignores the pointer so it never gets picked itself.
  const host = document.createElement('x-price-picker')
  host.style.cssText =
    'all:initial;position:fixed;inset:0;pointer-events:none;z-index:2147483647'
  const root = host.attachShadow({ mode: 'closed' })
  root.innerHTML = `<style>
    :host{all:initial}
    div{position:fixed;box-sizing:border-box;pointer-events:none;display:none}
    .margin{border-style:solid;border-color:${colors.margin}}
    .border{border-style:solid;border-color:${colors.border}}
    .padding{border-style:solid;border-color:${colors.padding}}
    .content{background:${colors.content}}
    .tip{max-width:min(420px,calc(100vw - 16px));padding:5px 8px;border-radius:4px;background:#1b1e24;color:#e8eaed;
      font:12px/1.45 'Cascadia Mono',ui-monospace,Menlo,Consolas,monospace;box-shadow:0 2px 10px rgba(0,0,0,.35);
      white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
    .tip .tag{color:#f0abfc}.tip .cls{color:#93c5fd}.tip .dim{color:#a3a9b3}
    .tip .txt{display:block;margin-top:2px;color:#fff;overflow:hidden;text-overflow:ellipsis;
      font-family:'Segoe UI',system-ui,-apple-system,sans-serif}
    .picked{outline:2px solid #0f6cbd;outline-offset:1px;background:rgba(15,108,189,.14);border-radius:2px}
    .badge{padding:1px 6px;border-radius:3px;background:#0f6cbd;color:#fff;
      font:600 11px/16px 'Segoe UI',system-ui,-apple-system,sans-serif}
    .match{outline:2px dashed #e0a23a;outline-offset:1px}
    .zap{outline:2px solid #c42b1c;outline-offset:-1px;background:rgba(196,43,28,.22)}
  </style>`
  const box = (cls) => {
    const d = document.createElement('div')
    d.className = cls
    root.appendChild(d)
    return d
  }
  const marginBox = box('margin')
  const borderBox = box('border')
  const paddingBox = box('padding')
  const contentBox = box('content')
  const tip = box('tip')
  const pickedBox = box('picked')
  const badge = box('badge')
  const zapBox = box('zap')
  let matchBoxes = []
  document.documentElement.appendChild(host)

  let pointer = null // last mouse position in the viewport
  let hovered = null
  let origin = null // what was clicked
  let depth = 0 // how many parents up from origin the pick is
  let picked = null
  let matches = []
  let label = 'Price'
  let frame = 0
  let mode = 'pick' // or 'remove'
  const hidden = [] // {el, style, auto}, newest last

  const post = (msg) => parent.postMessage(msg, '*')
  const px = (v) => Math.max(0, parseFloat(v) || 0)

  function place(d, left, top, width, height, widths) {
    d.style.display = width > 0 || height > 0 ? 'block' : 'none'
    d.style.left = left + 'px'
    d.style.top = top + 'px'
    d.style.width = Math.max(0, width) + 'px'
    d.style.height = Math.max(0, height) + 'px'
    if (widths) d.style.borderWidth = widths.map((w) => w + 'px').join(' ')
  }

  // The four rings dev tools draw: each is a box whose border is exactly
  // that ring's width, so the colors never overlap.
  function drawBoxModel(el) {
    const r = el.getBoundingClientRect()
    const cs = getComputedStyle(el)
    const m = ['Top', 'Right', 'Bottom', 'Left'].map((s) => px(cs['margin' + s]))
    const b = ['Top', 'Right', 'Bottom', 'Left'].map((s) =>
      px(cs['border' + s + 'Width']),
    )
    const p = ['Top', 'Right', 'Bottom', 'Left'].map((s) => px(cs['padding' + s]))
    place(marginBox, r.left - m[3], r.top - m[0], r.width + m[1] + m[3], r.height + m[0] + m[2], m)
    place(borderBox, r.left, r.top, r.width, r.height, b)
    const pl = r.left + b[3]
    const pt = r.top + b[0]
    const pw = r.width - b[1] - b[3]
    const ph = r.height - b[0] - b[2]
    place(paddingBox, pl, pt, pw, ph, p)
    place(contentBox, pl + p[3], pt + p[0], pw - p[1] - p[3], ph - p[0] - p[2])

    tip.textContent = ''
    const tag = document.createElement('span')
    tag.className = 'tag'
    tag.textContent = el.localName
    const cls = document.createElement('span')
    cls.className = 'cls'
    cls.textContent =
      (el.id ? '#' + el.id : '') +
      [...el.classList].slice(0, 4).map((c) => '.' + c).join('')
    const size = document.createElement('span')
    size.className = 'dim'
    size.textContent = `  ${round(r.width)} × ${round(r.height)}`
    tip.append(tag, cls, size)
    const text = textOf(el, 80)
    if (text) {
      const t = document.createElement('span')
      t.className = 'txt'
      t.textContent = text
      tip.append(t)
    }
    tip.style.display = 'block'
    const tw = tip.offsetWidth
    const th = tip.offsetHeight
    let top = r.bottom + m[2] + 6
    if (top + th > innerHeight - 4) top = r.top - m[0] - th - 6
    tip.style.top = Math.max(4, top) + 'px'
    tip.style.left = Math.min(Math.max(4, r.left), innerWidth - tw - 4) + 'px'
  }

  const round = (n) => Math.round(n * 10) / 10

  function hideBoxModel() {
    for (const d of [marginBox, borderBox, paddingBox, contentBox, tip, zapBox]) d.style.display = 'none'
  }

  // Remove mode shows the whole element in red: it is what a click hides.
  function drawZap(el) {
    drawBoxModel(el)
    for (const d of [marginBox, borderBox, paddingBox, contentBox]) d.style.display = 'none'
    const r = el.getBoundingClientRect()
    place(zapBox, r.left, r.top, r.width, r.height)
  }

  function draw() {
    frame = 0
    if (pointer) {
      const el = document.elementFromPoint(pointer.x, pointer.y)
      hovered = el && el !== document.documentElement && el !== document.body ? el : null
    }
    if (hovered && mode === 'remove') drawZap(hovered)
    else if (hovered) {
      zapBox.style.display = 'none'
      drawBoxModel(hovered)
    } else hideBoxModel()

    const r = picked && picked.isConnected ? picked.getBoundingClientRect() : null
    // A hidden field or meta tag has no box to outline.
    if (r && (r.width || r.height)) {
      place(pickedBox, r.left, r.top, r.width, r.height)
      badge.textContent = label
      badge.style.display = 'block'
      badge.style.left = Math.max(2, r.left) + 'px'
      badge.style.top = Math.max(2, r.top - 20) + 'px'
    } else {
      pickedBox.style.display = 'none'
      badge.style.display = 'none'
    }

    matchBoxes.forEach((d, i) => {
      const el = matches[i]
      if (!el || el === picked) {
        d.style.display = 'none'
        return
      }
      const r = el.getBoundingClientRect()
      place(d, r.left, r.top, r.width, r.height)
    })
  }

  const schedule = () => {
    if (!frame) frame = requestAnimationFrame(draw)
  }

  // --- what the dashboard gets about a picked element ----------------------

  function textOf(el, max = 300) {
    const t = (el.textContent || '').replace(/\s+/g, ' ').trim()
    return t.length > max ? t.slice(0, max - 1) + '…' : t
  }

  // Attributes that could hold a machine-readable price (a hidden input's
  // value, a meta tag's content, data-price="1299").
  function numericAttrs(el) {
    const skip = new Set(['class', 'style', 'id', 'href', 'src', 'srcset', 'sizes', 'width', 'height', 'd', 'viewbox'])
    const out = []
    for (const a of el.attributes) {
      if (skip.has(a.name) || a.value.length > 60 || !/\d/.test(a.value)) continue
      out.push({ name: a.name, value: a.value })
    }
    return out.slice(0, 6)
  }

  function describe(el) {
    if (el.id) return el.localName + '#' + el.id
    const c = [...el.classList].filter(plain).slice(0, 2)
    return el.localName + c.map((x) => '.' + x).join('')
  }

  function chainOf(el) {
    const out = []
    for (let cur = el; cur && cur !== document.documentElement && out.length < 12; cur = cur.parentElement)
      out.push(describe(cur))
    return out
  }

  function send() {
    if (!picked) return
    post({
      type: 'picked',
      selector: selectorFor(picked),
      text: textOf(picked),
      tag: picked.localName,
      attrs: numericAttrs(picked),
      chain: chainOf(origin),
      depth,
    })
  }

  // --- selectors -------------------------------------------------------------
  // Short, and readable by the server's CSS engine: plain class names only
  // (no Tailwind "lg:" variants or generated hashes), ids, a few stable
  // attributes, child combinators and :nth-of-type.

  const plain = (s) => /^[A-Za-z_-][A-Za-z0-9_-]*$/.test(s)
  const generated = (s) =>
    /__[\w-]{4,}$/.test(s) ||
    /^(css|sc|jsx|emotion|svelte)-/.test(s) ||
    (/\d/.test(s) && /[A-Z]/.test(s) && s.length > 5)
  const goodClass = (c) => plain(c) && !generated(c)
  const priceish = (c) => /price|amount|cost|sum/i.test(c)
  const stableAttrs = ['itemprop', 'data-testid', 'data-test', 'data-test-id', 'data-qa', 'data-cy', 'name', 'property']

  function unique(sel, el) {
    try {
      const all = document.querySelectorAll(sel)
      return all.length === 1 && all[0] === el
    } catch {
      return false
    }
  }

  function partOf(el) {
    if (el.id && plain(el.id) && !generated(el.id) && unique('#' + el.id, el)) return '#' + el.id
    const classes = [...el.classList].filter(goodClass).sort((a, b) => priceish(b) - priceish(a))
    let part = el.localName + classes.slice(0, 2).map((c) => '.' + c).join('')
    for (const a of stableAttrs) {
      const v = el.getAttribute(a)
      if (v && v.length <= 60 && /^[\w .:/-]+$/.test(v)) {
        part += `[${a}="${v}"]`
        break
      }
    }
    const p = el.parentElement
    if (p && [...p.children].some((c) => c !== el && safeMatches(c, part))) {
      let n = 1
      for (let s = el.previousElementSibling; s; s = s.previousElementSibling)
        if (s.localName === el.localName) n++
      part += `:nth-of-type(${n})`
    }
    return part
  }

  function safeMatches(el, sel) {
    try {
      return el.matches(sel)
    } catch {
      return false
    }
  }

  function selectorFor(el) {
    const own = partOf(el)
    if (unique(own, el)) return own
    // The element under one telling ancestor: "div.product-info span.text-xl".
    let anc = el.parentElement
    for (let i = 0; anc && anc !== document.documentElement && i < 6; i++, anc = anc.parentElement) {
      const sel = partOf(anc) + ' ' + own
      if (unique(sel, el)) return sel
    }
    // Else the exact path down from the nearest id (or the body).
    const parts = []
    for (let cur = el; cur && cur !== document.documentElement; cur = cur.parentElement) {
      const part = partOf(cur)
      parts.unshift(part)
      const sel = parts.join(' > ')
      if (unique(sel, el)) return sel
      if (part.startsWith('#')) break
    }
    return parts.join(' > ')
  }

  // --- hiding what is in the way --------------------------------------------

  function hide(el, auto) {
    if (hidden.some((h) => h.el === el)) return
    try {
      const style = el.getAttribute('style')
      el.style.setProperty('display', 'none', 'important')
      hidden.push({ el, style, auto })
    } catch {
      // An element that can't be styled (an SVG part, say) stays.
    }
  }

  function restore(h) {
    if (h.style === null) h.el.removeAttribute('style')
    else h.el.setAttribute('style', h.style)
  }

  function reportHidden() {
    post({
      type: 'hidden',
      count: hidden.length,
      auto: hidden.filter((h) => h.auto).length,
    })
  }

  // A cookie wall that locked the page's scrolling leaves it locked once
  // hidden. Only vertical scrolling: many pages hide horizontal overflow on
  // purpose.
  function unlockScroll() {
    for (const el of [document.documentElement, document.body]) {
      if (!el) continue
      const cs = getComputedStyle(el)
      // "overflow: hidden auto" is x then y; one value is both.
      const y = cs.overflowY || (cs.overflow || '').split(' ').pop()
      if (/hidden|clip/.test(y)) el.style.setProperty('overflow-y', 'auto', 'important')
      if (el === document.body && cs.position === 'fixed')
        el.style.setProperty('position', 'static', 'important')
    }
  }

  // Consent tools that use neutral names.
  const consentTools = [
    '#onetrust-consent-sdk', '#CybotCookiebotDialog', '#CybotCookiebotDialogBodyUnderlay',
    '#usercentrics-root', '#didomi-host', '#didomi-popup', '#qc-cmp2-container', '#cmpbox', '#cmpbox2',
    '.cmpboxBG', '[id^="sp_message_container"]', '.fc-consent-root', '#truste-consent-track',
    '.truste_overlay', '.truste_box_overlay', '#klaro', '.klaro', '#iubenda-cs-banner', '.cc-window',
    '.cc-banner', '.cc-grower',
  ]
  // Anything else named for cookies or consent counts only when it floats
  // over the page as a banner: a bakery's "cookie-card" product, a sticky
  // header's "Cookie settings" link and a buy box's consent checkbox stay.
  const consentNamed =
    ':is([id*="cookie" i],[class*="cookie" i],[id*="consent" i],[class*="consent" i],' +
    '[id*="gdpr" i],[class*="gdpr" i],[aria-label*="cookie" i],[aria-label*="consent" i])'
  const content = 'main, h1, [itemprop="price"], [itemprop="offers"], [role="main"]'

  // The banner a cookie-named element belongs to: itself or a close
  // ancestor that is position: fixed (banners don't stick, headers do). An
  // ancestor must be banner-sized, not an app shell filling the screen, and
  // nothing holding the page's content or the picked element is a banner.
  function banner(el) {
    for (let cur = el, i = 0; cur && cur !== document.body && i < 4; cur = cur.parentElement, i++) {
      if (getComputedStyle(cur).position !== 'fixed') continue
      const r = cur.getBoundingClientRect()
      const large = r.width * r.height > 0.5 * innerWidth * innerHeight
      if (cur !== el && large) return null
      if (cur.querySelector(content) || (picked && cur.contains(picked))) return null
      return cur
    }
    return null
  }

  function hideCookieBanners() {
    const before = hidden.length
    for (const sel of consentTools) {
      try {
        document.querySelectorAll(sel).forEach((el) => hide(el, true))
      } catch {
        // A selector this browser can't read.
      }
    }
    let named = []
    try {
      named = [...document.querySelectorAll(consentNamed)]
    } catch {
      return
    }
    for (const el of named) {
      if (el === document.documentElement || el === document.body || host.contains(el)) continue
      if (hidden.some((h) => h.el === el || h.el.contains(el))) continue
      const top = banner(el)
      if (top) hide(top, true)
    }
    if (hidden.length !== before) {
      unlockScroll()
      reportHidden()
      schedule()
    }
  }

  // --- events ------------------------------------------------------------------

  addEventListener(
    'mousemove',
    (e) => {
      pointer = { x: e.clientX, y: e.clientY }
      schedule()
    },
    true,
  )
  document.documentElement.addEventListener('mouseleave', () => {
    pointer = null
    hovered = null
    schedule()
  })
  addEventListener('scroll', schedule, { capture: true, passive: true })
  addEventListener('resize', schedule)

  // The copy is for looking, not using: no link, button or form does
  // anything; a click picks instead.
  const block = (e) => {
    e.preventDefault()
    e.stopPropagation()
  }
  for (const type of ['mousedown', 'mouseup', 'pointerdown', 'pointerup', 'dblclick', 'auxclick', 'submit'])
    addEventListener(type, block, true)
  addEventListener(
    'click',
    (e) => {
      block(e)
      const el = document.elementFromPoint(e.clientX, e.clientY) || e.target
      if (!(el instanceof Element) || el === document.documentElement) return
      if (mode === 'remove') {
        if (el === document.body) return
        hide(el, false)
        reportHidden()
        schedule()
        return
      }
      origin = picked = el
      depth = 0
      matches = []
      syncMatchBoxes()
      send()
      schedule()
    },
    true,
  )

  function syncMatchBoxes() {
    while (matchBoxes.length < matches.length) matchBoxes.push(box('match'))
    matchBoxes.forEach((d, i) => {
      if (i >= matches.length) d.style.display = 'none'
    })
  }

  addEventListener('message', (e) => {
    if (e.source !== parent || !e.data || typeof e.data !== 'object') return
    const d = e.data
    if (d.type === 'config') {
      if (typeof d.label === 'string') label = d.label
      schedule()
    } else if (d.type === 'depth' && origin) {
      // Pick a parent of what was clicked (the breadcrumb), or back down.
      let el = origin
      let n = 0
      while (n < d.depth && el.parentElement && el.parentElement !== document.documentElement) {
        el = el.parentElement
        n++
      }
      picked = el
      depth = n
      send()
      schedule()
    } else if (d.type === 'highlight') {
      // Show what a selector (typed, or a suggestion) matches.
      let all = []
      let ok = true
      try {
        all = d.selector ? [...document.querySelectorAll(d.selector)] : []
      } catch {
        ok = false
      }
      matches = all.slice(0, 50)
      if (d.pick && all[0]) {
        origin = picked = all[0]
        depth = 0
      }
      syncMatchBoxes()
      const first = all[0]
      if (first && d.scroll) {
        const r = first.getBoundingClientRect()
        if (r.width || r.height) first.scrollIntoView({ block: 'center', behavior: 'smooth' })
      }
      post({
        type: 'highlighted',
        valid: ok,
        count: all.length,
        text: first ? textOf(first) : '',
        visible: !!first && first.getClientRects().length > 0,
      })
      schedule()
    } else if (d.type === 'clear') {
      origin = picked = null
      matches = []
      syncMatchBoxes()
      schedule()
    } else if (d.type === 'mode') {
      mode = d.mode === 'remove' ? 'remove' : 'pick'
      schedule()
    } else if (d.type === 'unhide') {
      // The last one hidden, or with all: every one.
      const undo = d.all ? hidden.splice(0) : hidden.splice(-1)
      undo.reverse().forEach(restore)
      reportHidden()
      schedule()
    }
  })

  hideCookieBanners()
  // Stylesheets that arrive late can make a banner float only now.
  addEventListener('load', hideCookieBanners)
  post({ type: 'ready', title: document.title })
})()
