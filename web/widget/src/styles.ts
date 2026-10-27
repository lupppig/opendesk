// Every rule lives inside the shadow root, so nothing leaks into the host page
// and the host page's CSS can't reach in. White-first, layered surfaces (grey
// chrome behind white panels), soft semantic colour, and the product's one AI
// hue (violet) for everything the agent does. A workspace sets the colour,
// theme, corner style and position: the host carries them as CSS variables
// (--brand, --brand-fg, --r-*, --ox, --oy; see config.ts hostVars).
// Motion: under 250ms, ease-out, off under prefers-reduced-motion. Bot faces
// carry the shared look motion (BOT_LOOK_CSS): an idle blink in the header, a
// bob while the bot writes, one hop when its reply lands.

import { BOT_LOOK_CSS } from "../../src/lib/botlook";

const LIGHT = `
  --bg: #ffffff; --bg-2: #f4f4f5; --bg-3: #fafafa; --line: #e7e7e9; --line-2: #d6d6d9;
  --fg: #0a0a0a; --fg-2: #525252; --fg-3: #737373; --fg-4: #a3a3a3;
  --accent: var(--brand, #0a0a0a); --accent-fg: var(--brand-fg, #ffffff);
  --ai: #6d4bd8; --ai-soft: #f4f0ff; --ai-line: #e4dcff;
  --ok: #15803d; --ok-soft: #ecfdf3; --danger: #dc2626; --danger-soft: #fef2f2;
  --warn: color-mix(in srgb, #d48806 82%, var(--fg)); --warn-soft: color-mix(in srgb, #d48806 8%, var(--bg)); --warn-line: color-mix(in srgb, #d48806 20%, var(--bg));
  --shadow: 0 0 0 1px rgb(10 10 10 / .06), 0 12px 28px -8px rgb(10 10 10 / .18), 0 32px 64px -20px rgb(10 10 10 / .24);
  --l-shadow: 0 0 0 1px rgb(255 255 255 / .08) inset, 0 8px 20px -6px rgb(0 0 0 / .3), 0 2px 4px rgb(0 0 0 / .08);
  --ring: color-mix(in srgb, var(--fg) 8%, transparent);
  color-scheme: light;
`;
const DARK = `
  --bg: #161616; --bg-2: #242426; --bg-3: #1c1c1d; --line: #2c2c2f; --line-2: #3a3a3e;
  --fg: #fafafa; --fg-2: #c4c4c6; --fg-3: #8d8d92; --fg-4: #5e5e63;
  --accent: var(--brand, #fafafa); --accent-fg: var(--brand-fg, #0a0a0a);
  --ai: #b9a3ff; --ai-soft: #231d33; --ai-line: #372d52;
  --ok: #4ade80; --ok-soft: #12261a; --danger: #f87171; --danger-soft: #2a1515;
  --warn: color-mix(in srgb, #f5b53d 82%, var(--fg)); --warn-soft: color-mix(in srgb, #f5b53d 9%, var(--bg)); --warn-line: color-mix(in srgb, #f5b53d 18%, var(--bg));
  --shadow: 0 0 0 1px rgb(255 255 255 / .08), 0 12px 28px -8px rgb(0 0 0 / .6), 0 32px 64px -20px rgb(0 0 0 / .7);
  --l-shadow: 0 0 0 1px rgb(255 255 255 / .14), 0 8px 20px -6px rgb(0 0 0 / .6);
  --ring: color-mix(in srgb, var(--fg) 12%, transparent);
  color-scheme: dark;
`;

export const css = `
:host { all: initial; }
.root {
  ${LIGHT}
  --ease: cubic-bezier(.16, 1, .3, 1);
  --font: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Hiragino Sans", "Hiragino Kaku Gothic ProN", "Yu Gothic UI", Meiryo, "PingFang SC", "Microsoft YaHei", "Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans CJK SC", "Noto Sans CJK JP", "Noto Sans CJK KR", "Segoe UI Arabic", "Geeza Pro", "Noto Sans Arabic", sans-serif;
  --mono: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  --gap-launcher: calc(var(--oy, 20px) + 66px);
  font-family: var(--font); font-size: 14px; line-height: 20px; color: var(--fg); -webkit-font-smoothing: antialiased;
}
.root[data-theme="dark"] { ${DARK} }
@media (prefers-color-scheme: dark) { .root[data-theme="auto"] { ${DARK} } }
* { box-sizing: border-box; margin: 0; padding: 0; }
button, textarea, input { font: inherit; color: inherit; }
button { cursor: pointer; background: none; border: 0; }
:focus { outline: none; }
:focus-visible { outline: 2px solid color-mix(in srgb, var(--fg) 30%, transparent); outline-offset: 2px; }
textarea:focus-visible, input:focus-visible { outline: none; }
img { display: block; }

/* Launcher: icon, or a pill with the icon and a label. */
.launcher {
  position: fixed; right: var(--ox, 20px); bottom: var(--oy, 20px); z-index: 1;
  height: 54px; min-width: 54px; padding: 0 15px; border-radius: var(--r-launcher, 16px);
  background: var(--accent); color: var(--accent-fg); display: flex; align-items: center; justify-content: center; gap: 0;
  box-shadow: var(--l-shadow); pointer-events: auto;
  transition: transform .2s var(--ease), box-shadow .2s, padding .22s var(--ease), gap .22s var(--ease);
  animation: launcher-in .24s var(--ease) both;
}
.pos-left .launcher { right: auto; left: var(--ox, 20px); }
.launcher:hover { transform: translateY(-1px) scale(1.03); }
.launcher:active { transform: scale(.95); transition-duration: .08s; }
.launcher.pill { height: 48px; padding: 0 18px 0 15px; gap: 8px; }
.launcher.pill[aria-expanded="true"] { padding: 0 14px; gap: 0; }
.l-ic { position: relative; width: 24px; height: 24px; flex: none; }
.pill .l-ic { width: 20px; height: 20px; }
.l-ic .ic { position: absolute; inset: 0; display: grid; place-items: center; transition: transform .22s var(--ease), opacity .15s; }
.l-ic img { width: 100%; height: 100%; object-fit: cover; border-radius: 5px; }
.launcher .ic-x { opacity: 0; transform: rotate(-60deg) scale(.6); }
.launcher[aria-expanded="true"] .ic-open { opacity: 0; transform: rotate(60deg) scale(.6); }
.launcher[aria-expanded="true"] .ic-x { opacity: 1; transform: none; }
.l-label { display: block; max-width: 220px; overflow: hidden; white-space: nowrap; font-size: 14px; font-weight: 600; letter-spacing: -.01em; transition: max-width .22s var(--ease), opacity .15s; }
.launcher[aria-expanded="true"] .l-label { max-width: 0; opacity: 0; }
@keyframes launcher-in { from { opacity: 0; transform: translateY(8px) scale(.9); } to { opacity: 1; transform: none; } }
.badge {
  position: absolute; top: -5px; right: -5px; min-width: 20px; height: 20px; padding: 0 6px; border-radius: 999px;
  background: var(--danger); color: #fff; font-size: 11px; font-weight: 600; line-height: 20px; text-align: center;
  font-variant-numeric: tabular-nums; box-shadow: 0 0 0 2px var(--bg); animation: pop .24s var(--ease) both;
}
.pos-left .badge { right: auto; left: -5px; }
@keyframes pop { 0% { transform: scale(.5); opacity: 0; } 60% { transform: scale(1.12); opacity: 1; } 100% { transform: none; } }

/* Panel: rises from the launcher's corner, eases out on close. */
.panel {
  position: fixed; right: var(--ox, 20px); bottom: var(--gap-launcher); width: 384px;
  height: min(640px, calc(100vh - var(--gap-launcher) - 24px));
  display: flex; flex-direction: column; overflow: hidden; pointer-events: auto;
  background: var(--bg); border-radius: var(--r-panel, 16px); box-shadow: var(--shadow);
  transform-origin: bottom right; animation: panel-in .24s var(--ease) both;
}
.pos-left .panel { right: auto; left: var(--ox, 20px); transform-origin: bottom left; }
.preview .panel { height: min(640px, calc(100% - var(--gap-launcher) - 16px)); max-width: calc(100% - 2 * var(--ox, 20px)); }
.panel.leaving { animation: panel-out .16s cubic-bezier(.4, 0, 1, 1) both; pointer-events: none; }
@keyframes panel-in { from { opacity: 0; transform: translateY(12px) scale(.96); } to { opacity: 1; transform: none; } }
@keyframes panel-out { from { opacity: 1; transform: none; } to { opacity: 0; transform: translateY(8px) scale(.98); } }
@media (max-width: 479px) {
  .root:not(.preview) .panel { inset: 0; width: auto; height: 100%; border-radius: 0; transform-origin: bottom center; }
  .root:not(.preview) .launcher.hide-mobile { display: none; }
}

/* Headers */
.head { padding: 12px 12px 11px; display: flex; align-items: center; gap: 10px; border-bottom: 1px solid var(--line); background: var(--bg); flex: none; }
.head.home {
  --head-bg: var(--bg-3);
  padding: 16px 16px 20px; flex-direction: column; align-items: stretch; gap: 0;
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent) 7%, var(--bg-3)) 0%, var(--bg-3) 100%);
}
.head-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.head-main { flex: 1; min-width: 0; }
.ws { display: flex; align-items: center; gap: 9px; min-width: 0; font-size: 13.5px; font-weight: 600; letter-spacing: -.01em; }
.ws-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ws-glyph { width: 26px; height: 26px; flex: none; border-radius: var(--r-av, 6px); overflow: hidden; background: var(--accent); color: var(--accent-fg); display: grid; place-items: center; font-size: 12px; font-weight: 700; }
.ws-glyph img { width: 100%; height: 100%; object-fit: cover; }
.greet { margin-top: 18px; font-size: 22px; line-height: 28px; font-weight: 600; letter-spacing: -.025em; white-space: pre-line; animation: rise .22s .03s var(--ease) both; }
.sub-row { margin-top: 8px; display: flex; align-items: center; gap: 8px; animation: rise .22s .06s var(--ease) both; }
.sub { color: var(--fg-3); font-size: 13px; line-height: 18px; }
.title { font-size: 14px; font-weight: 600; letter-spacing: -.01em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.title-sub { font-size: 12px; color: var(--fg-3); margin-top: 1px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.icon-btn { width: 30px; height: 30px; flex: none; border-radius: var(--r-ctl, 8px); display: grid; place-items: center; color: var(--fg-3); transition: background .12s, color .12s, transform .12s; }
.icon-btn:hover { background: var(--bg-2); color: var(--fg); }
.icon-btn:active { transform: scale(.92); }
.icon-btn:disabled { opacity: .4; cursor: default; background: none; }

/* Avatars */
.av { width: 18px; height: 18px; flex: none; border-radius: var(--r-av, 6px); background: var(--bg-2); color: var(--fg-2); font-size: 8.5px; font-weight: 600; display: grid; place-items: center; overflow: hidden; }
.av img { width: 100%; height: 100%; object-fit: cover; }
/* A bot's look draws its own shape; an uploaded picture is a circle. */
.bot-av { background: transparent; overflow: visible; }
.bot-av.is-pic { border-radius: 50%; overflow: hidden; background: var(--ai-soft); }
.face { display: block; width: 100%; height: 100%; }
.stack { display: inline-flex; align-items: center; flex: none; }
.stack .av { box-shadow: 0 0 0 2px var(--head-bg, var(--bg)); }
.stack .av + .av { margin-inline-start: -7px; }
.stack .bot-av:not(.is-pic) { box-shadow: none; }
.stack .bot-av:not(.is-pic) + .av { margin-inline-start: 1px; }

/* Home */
.body { position: relative; flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin; }
.home-body { padding: 16px; display: flex; flex-direction: column; gap: 12px; }
.card { border: 1px solid var(--line); border-radius: var(--r-card, 10px); background: var(--bg); overflow: hidden; animation: rise .22s .06s var(--ease) both; }
.card-h { padding: 12px 14px 6px; font-size: 11px; font-weight: 600; letter-spacing: .06em; text-transform: uppercase; color: var(--fg-4); }
.conv { width: 100%; display: flex; gap: 10px; align-items: flex-start; padding: 10px 14px; text-align: start; border-top: 1px solid var(--line); transition: background .12s; }
.card-h + .conv { border-top: 0; }
.conv:hover { background: var(--bg-3); }
.conv-main { flex: 1; min-width: 0; }
.conv-top { display: flex; justify-content: space-between; gap: 8px; }
.conv-subject { font-size: 13px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.conv-prev { display: block; font-size: 12.5px; color: var(--fg-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-top: 1px; }
.time { font-family: var(--mono); font-size: 10.5px; color: var(--fg-4); flex: none; }
.dot { width: 7px; height: 7px; border-radius: 99px; margin-top: 6px; flex: none; background: var(--line-2); }
.dot.open { background: var(--accent); }
.primary {
  display: flex; align-items: center; justify-content: center; gap: 8px; height: 40px; border-radius: var(--r-ctl, 8px); width: 100%;
  background: var(--accent); color: var(--accent-fg); font-size: 13.5px; font-weight: 500; transition: opacity .12s, transform .12s, box-shadow .15s;
}
.primary:hover { opacity: .92; }
.primary:active { transform: scale(.985); }
.primary:disabled { opacity: .5; cursor: default; }
.primary.start { height: 44px; justify-content: space-between; padding: 0 16px; box-shadow: 0 1px 2px rgb(0 0 0 / .08); animation: rise .2s var(--ease) both; }
.start-ic { display: grid; place-items: center; transition: transform .18s var(--ease); }
.start:hover .start-ic { transform: translateX(2px); }
[dir="rtl"] .start:hover .start-ic { transform: translateX(-2px); }
.suggest { display: flex; flex-wrap: wrap; gap: 6px; }
.sugg {
  min-height: 32px; padding: 6px 12px; border-radius: var(--r-ctl, 8px); border: 1px solid var(--line); background: var(--bg);
  font-size: 13px; line-height: 18px; text-align: start; color: var(--fg);
  transition: border-color .12s, background .12s, transform .12s var(--ease), box-shadow .12s;
  animation: rise .2s calc(var(--i, 0) * 30ms + 40ms) var(--ease) both;
}
.sugg:hover { border-color: var(--line-2); background: var(--bg-3); transform: translateY(-1px); box-shadow: 0 2px 6px -2px rgb(0 0 0 / .08); }
.sugg:active { transform: scale(.97); transition-duration: .06s; }
.sugg:disabled { opacity: .5; cursor: default; transform: none; }
.link-row { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; font-size: 13px; font-weight: 500; color: var(--fg); text-decoration: none; transition: background .12s; }
.link-row:hover { background: var(--bg-3); }
.link-row svg { color: var(--fg-3); transition: transform .16s var(--ease); }
.link-row:hover svg { transform: translate(1px, -1px); }

/* Topics: one list of rows, on the home screen and before a new conversation's first message. */
.topics { display: flex; flex-direction: column; }
.topic {
  width: 100%; min-height: 42px; display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 10px 14px;
  text-align: start; font-size: 13.5px; line-height: 19px; font-weight: 500; color: var(--fg); border-top: 1px solid var(--line);
  transition: background .12s, color .12s; animation: rise .2s calc(var(--i, 0) * 25ms + 30ms) var(--ease) both;
}
.topic:first-child { border-top: 0; }
.card-h + .topics .topic:first-child { border-top: 0; }
.topic:hover { background: var(--bg-3); }
.topic:focus-visible { outline-offset: -2px; border-radius: var(--r-card, 10px); }
.topic:active { background: var(--bg-2); }
.topic:disabled { opacity: .5; cursor: default; background: none; }
.topic-ic { display: grid; place-items: center; color: var(--fg-4); flex: none; transition: transform .16s var(--ease), color .12s; }
.topic:hover .topic-ic, .topic:focus-visible .topic-ic { color: var(--fg-2); transform: translateX(2px); }
[dir="rtl"] .topic:hover .topic-ic, [dir="rtl"] .topic:focus-visible .topic-ic { transform: translateX(-2px); }
.picker { padding: 18px 14px 12px; display: flex; flex-direction: column; animation: rise .22s var(--ease) both; }
.picker b { font-size: 15px; line-height: 21px; font-weight: 600; letter-spacing: -.015em; }
.picker p { margin: 2px 0 12px; font-size: 12.5px; line-height: 18px; color: var(--fg-3); }
.queued { padding-bottom: 0; }
.topic-row { display: flex; justify-content: center; padding: 14px 14px 0; }
.topic-chip {
  display: inline-flex; align-items: center; gap: 4px; max-width: 100%; height: 26px; padding: 0 4px 0 10px; border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 10%, var(--bg)); color: var(--fg); border: 1px solid color-mix(in srgb, var(--accent) 22%, var(--bg));
  font-size: 12px; font-weight: 500; animation: pop .22s var(--ease) both;
}
.topic-chip span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.topic-chip button { width: 20px; height: 20px; flex: none; display: grid; place-items: center; border-radius: 999px; color: var(--fg-3); transition: background .12s, color .12s; }
.topic-chip button:hover { background: color-mix(in srgb, var(--fg) 8%, transparent); color: var(--fg); }

/* Closed: the offline message, on the home screen and above the composer. */
.away { display: flex; align-items: flex-start; gap: 10px; padding: 11px 12px; border-radius: var(--r-card, 10px); background: var(--warn-soft); border: 1px solid var(--warn-line); animation: rise .2s var(--ease) both; }
.away-ic { width: 26px; height: 26px; flex: none; display: grid; place-items: center; border-radius: 999px; background: var(--bg); color: var(--warn); box-shadow: 0 0 0 1px var(--warn-line); }
.away-text { font-size: 12.5px; line-height: 18px; color: var(--fg-2); white-space: pre-line; overflow-wrap: anywhere; align-self: center; }
.away.bar { margin: 0 12px; padding: 7px 10px; gap: 8px; align-items: center; border-radius: var(--r-ctl, 8px); animation: none; }
.away.bar .away-ic { width: 20px; height: 20px; }
.away.bar .away-text { font-size: 12px; line-height: 17px; white-space: normal; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.away.bar + .composer { padding-top: 8px; }

/* Thread */
.body-inner { min-height: 100%; display: flex; flex-direction: column; }
.thread { padding: 16px 14px 8px; display: flex; flex-direction: column; gap: 2px; }
.day { align-self: center; font-family: var(--mono); font-size: 10.5px; color: var(--fg-4); margin: 10px 0 6px; }
.row { display: flex; flex-direction: column; max-width: 84%; animation: msg-in .2s var(--ease) both; }
.row.me { align-self: flex-end; align-items: flex-end; transform-origin: bottom right; }
.row.them { align-self: flex-start; align-items: flex-start; transform-origin: bottom left; }
[dir="rtl"] .row.me { transform-origin: bottom left; } [dir="rtl"] .row.them { transform-origin: bottom right; }
.row.no-enter { animation: none; }
.row.gap { margin-top: 12px; }
.row.gap:first-child, .day + .row.gap { margin-top: 0; }
@keyframes msg-in { from { opacity: 0; transform: translateY(6px) scale(.98); } to { opacity: 1; transform: none; } }
@keyframes rise { from { opacity: 0; transform: translateY(5px); } to { opacity: 1; transform: none; } }
.who { font-size: 11.5px; color: var(--fg-3); margin: 0 2px 4px; display: flex; align-items: center; gap: 6px; }
.who-name { font-weight: 500; color: var(--fg-2); }
.who-role { color: var(--fg-4); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.msg { padding: 8px 12px; border-radius: var(--r-bubble, 14px); font-size: 13.5px; line-height: 19.5px; overflow-wrap: anywhere; max-width: 100%; }
.me .msg { background: var(--accent); color: var(--accent-fg); border-end-end-radius: 4px; white-space: pre-wrap; transition: opacity .15s; }
.is-pending .msg { opacity: .72; }
.them .msg { background: var(--bg-2); color: var(--fg); border-end-start-radius: 4px; }
.row.me:not(.gap) .msg { border-start-end-radius: 4px; }
.row.them:not(.gap) .msg { border-start-start-radius: 4px; }
.msg p + p, .msg p + ul, .msg p + ol, .msg ul + p, .msg ol + p, .msg pre, .msg blockquote { margin-top: 6px; }
.msg ul, .msg ol { padding-inline-start: 18px; }
.msg li + li { margin-top: 2px; }
.msg a { color: inherit; text-decoration: underline; text-underline-offset: 2px; text-decoration-color: color-mix(in srgb, currentColor 40%, transparent); }
.msg a:hover { text-decoration-color: currentColor; }
.msg code { font-family: var(--mono); font-size: 12px; padding: 0 3px; border-radius: 3px; background: color-mix(in srgb, var(--fg) 7%, transparent); }
.msg pre { font-family: var(--mono); font-size: 12px; white-space: pre-wrap; }
.msg pre code { padding: 0; background: none; }
.msg blockquote { border-inline-start: 2px solid var(--line-2); padding-inline-start: 8px; color: var(--fg-2); }
.meta { font-family: var(--mono); font-size: 10px; color: var(--fg-4); margin: 3px 4px 0; display: flex; gap: 6px; align-items: center; }
.meta.failed { color: var(--danger); font-family: var(--font); font-size: 11.5px; }
.meta button { text-decoration: underline; color: inherit; font-size: inherit; }
.files { display: flex; flex-direction: column; gap: 4px; margin-top: 4px; }
.me .files { align-items: flex-end; }
.thumb { display: block; max-width: 220px; max-height: 160px; border-radius: var(--r-card, 10px); border: 1px solid var(--line); object-fit: cover; }
.chip { display: inline-flex; align-items: center; gap: 6px; max-width: 240px; height: 30px; padding: 0 10px; border-radius: var(--r-ctl, 8px); border: 1px solid var(--line); background: var(--bg); font-size: 12px; color: var(--fg); text-decoration: none; transition: border-color .12s; }
a.chip:hover { border-color: var(--line-2); }
.chip span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chip small { color: var(--fg-4); font-family: var(--mono); font-size: 10.5px; flex: none; }

/* The agent: violet kept to its avatar, the AI pill, tool steps and the caret. */
.ai .msg { background: var(--bg-3); border: 1px solid var(--line); }
.ai-pill { display: inline-flex; align-items: center; height: 16px; padding: 0 5px; border-radius: 4px; background: var(--ai-soft); color: var(--ai); font-size: 9.5px; font-weight: 600; letter-spacing: .04em; }
.greeting .msg { white-space: pre-line; }
.live .msg { min-width: 52px; transition: min-height .2s var(--ease); }
.live.leaving { animation: fade-out .2s ease-out forwards; }
@keyframes fade-out { to { opacity: 0; transform: translateY(-2px); } }
.typing-dots { display: inline-flex; align-items: center; height: 19.5px; color: var(--ai); }
.md.writing > p:last-child::after, .md.writing > ul:last-child > li:last-child::after, .md.writing > ol:last-child > li:last-child::after, .md.writing > blockquote:last-child::after {
  content: ""; display: inline-block; width: 2px; height: 14px; margin-inline-start: 2px; vertical-align: -2px; border-radius: 1px;
  background: var(--ai); animation: caret 1s ease-in-out infinite;
}
@keyframes caret { 0%, 100% { opacity: 1; } 50% { opacity: .25; } }
.tools { display: flex; flex-direction: column; gap: 4px; }
.tools + .md, .tools + .typing-dots { margin-top: 6px; }
.tools + .typing-dots { display: flex; }
.tool { display: flex; align-items: center; gap: 7px; font-size: 12px; line-height: 18px; color: var(--fg-3); animation: rise .18s var(--ease) both; }
.tool-ic { width: 16px; height: 16px; flex: none; display: grid; place-items: center; border-radius: 99px; color: var(--ai); background: var(--ai-soft); transition: background .2s, color .2s; }
.t-done .tool-ic { color: var(--ok); background: var(--ok-soft); }
.t-done .tool-ic svg, .t-error .tool-ic svg { animation: pop .22s var(--ease) both; }
.t-error .tool-ic { color: var(--fg-3); background: var(--bg-2); }
.t-running span:last-child {
  background: linear-gradient(90deg, var(--fg-3) 0%, var(--fg-3) 35%, var(--fg) 50%, var(--fg-3) 65%, var(--fg-3) 100%) 0 0 / 250% 100%;
  -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent; animation: shimmer 1.8s linear infinite;
}
[dir="rtl"] .t-running span:last-child { animation-direction: reverse; }
@keyframes shimmer { from { background-position: 100% 0; } to { background-position: 0 0; } }
.spin { animation: spin .7s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* Sources */
.sources { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
.src {
  display: inline-flex; align-items: center; gap: 6px; max-width: 240px; height: 26px; padding: 0 10px 0 8px; border-radius: var(--r-ctl, 8px);
  border: 1px solid var(--line); background: var(--bg); color: var(--fg-2); font-size: 12px; text-decoration: none;
  transition: border-color .12s, color .12s, background .12s, transform .12s var(--ease);
}
.src svg { color: var(--ai); flex: none; }
.src span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.src:hover { border-color: var(--ai-line); color: var(--fg); background: var(--ai-soft); }
.src:active { transform: scale(.97); }

/* Action cards */
.action {
  margin-top: 6px; width: 272px; max-width: 100%; padding: 11px 12px 12px; border-radius: var(--r-card, 10px);
  border: 1px solid var(--line); background: var(--bg); box-shadow: 0 1px 2px rgb(0 0 0 / .04);
  transition: border-color .2s, background .2s; animation: rise .2s var(--ease) both;
}
.action.s-pending { border-color: var(--ai-line); box-shadow: 0 0 0 3px color-mix(in srgb, var(--ai-soft) 70%, transparent); }
.action-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-height: 20px; }
.action-tool { display: inline-flex; align-items: center; gap: 5px; min-width: 0; font-size: 11.5px; font-weight: 500; color: var(--ai); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.action-need { font-size: 11px; color: var(--fg-4); white-space: nowrap; }
.action-title { margin-top: 6px; font-size: 13.5px; line-height: 19px; font-weight: 600; letter-spacing: -.01em; overflow-wrap: anywhere; }
.action-sum { margin-top: 2px; font-size: 12.5px; line-height: 18px; color: var(--fg-2); overflow-wrap: anywhere; }
.action-btns { display: grid; grid-template-rows: 0fr; opacity: 0; transition: grid-template-rows .22s var(--ease), opacity .15s, margin-top .22s var(--ease); }
.action-btns[data-open] { grid-template-rows: 1fr; opacity: 1; margin-top: 10px; }
.action-btns > div { min-height: 0; overflow: hidden; display: flex; gap: 6px; justify-content: flex-end; }
.action-btns:not([data-open]) button { visibility: hidden; }
.btn {
  height: 30px; padding: 0 12px; border-radius: var(--r-ctl, 8px); display: inline-flex; align-items: center; justify-content: center; gap: 6px;
  font-size: 12.5px; font-weight: 500; transition: background .12s, border-color .12s, opacity .12s, transform .12s var(--ease);
}
.btn:active:not(:disabled) { transform: scale(.96); }
.btn:disabled { cursor: default; }
.btn.solid { background: var(--accent); color: var(--accent-fg); }
.btn.solid:hover:not(:disabled) { opacity: .9; }
.btn.ghost { border: 1px solid var(--line-2); color: var(--fg); }
.btn.ghost:hover:not(:disabled) { background: var(--bg-2); }
.btn:disabled:not([aria-busy="true"]) { opacity: .5; }
.pill { display: inline-flex; align-items: center; gap: 4px; height: 20px; padding: 0 8px; border-radius: 999px; font-size: 11px; font-weight: 500; white-space: nowrap; animation: pop .22s var(--ease) both; }
.p-confirmed { background: var(--ok-soft); color: var(--ok); }
.p-declined, .p-expired { background: var(--bg-2); color: var(--fg-3); }
.p-failed { background: var(--danger-soft); color: var(--danger); }

/* Ratings: quiet until hover or focus; always there on touch screens. */
.meta-row { display: flex; align-items: center; gap: 4px; min-height: 22px; margin-top: 2px; }
.meta-row .meta { margin-top: 0; }
.rate { display: inline-flex; gap: 1px; opacity: 0; transition: opacity .15s; }
.row.ai:hover .rate, .row.ai:focus-within .rate, .rate.rated { opacity: 1; }
@media (hover: none) { .rate { opacity: 1; } }
.rate-btn { width: 24px; height: 22px; display: grid; place-items: center; border-radius: 6px; color: var(--fg-4); transition: color .12s, background .12s, transform .12s var(--ease); }
.rate-btn:hover { color: var(--fg-2); background: var(--bg-2); }
.rate-btn:active { transform: scale(.88); }
.rate-btn.on { color: var(--ai); background: var(--ai-soft); }
.rate-btn.on svg { animation: pop .24s var(--ease) both; }

/* Handoff */
.offer { margin-top: 6px; display: flex; flex-direction: column; align-items: flex-start; }
.handoff {
  display: inline-flex; align-items: center; gap: 6px; height: 30px; padding: 0 11px; border-radius: var(--r-ctl, 8px);
  border: 1px solid var(--line-2); background: var(--bg); font-size: 12.5px; font-weight: 500; color: var(--fg);
  transition: background .12s, border-color .12s, opacity .12s, transform .12s; animation: rise .2s .08s var(--ease) both;
}
.handoff:hover { background: var(--bg-2); }
.handoff:active { transform: scale(.97); }
.handoff:disabled { opacity: .6; cursor: default; }
.handed { margin: 6px 14px 14px; display: flex; align-items: flex-start; gap: 10px; padding: 11px 12px; border-radius: var(--r-card, 10px); background: var(--bg-3); border: 1px solid var(--line); animation: rise .22s var(--ease) both; }
.handed-ic { width: 28px; height: 28px; flex: none; display: grid; place-items: center; border-radius: var(--r-av, 6px); background: var(--bg); border: 1px solid var(--line); color: var(--fg-2); }
.handed-text { display: flex; flex-direction: column; gap: 1px; font-size: 12px; line-height: 17px; color: var(--fg-3); }
.handed-text b { font-size: 13px; line-height: 18px; font-weight: 600; color: var(--fg); }

/* A person typing */
.typing { align-self: flex-start; display: flex; align-items: center; gap: 8px; padding: 2px 14px 12px; color: var(--fg-3); font-size: 12px; animation: rise .18s var(--ease) both; }
.typing .dots { padding: 11px 12px; border-radius: var(--r-bubble, 14px); border-end-start-radius: 4px; background: var(--bg-2); }
.dots { display: inline-flex; align-items: center; gap: 3px; }
.dots span { width: 5px; height: 5px; border-radius: 99px; background: currentColor; opacity: .35; animation: wave 1.1s infinite ease-in-out; }
.dots span:nth-child(2) { animation-delay: .14s; } .dots span:nth-child(3) { animation-delay: .28s; }
@keyframes wave { 0%, 70%, 100% { opacity: .35; transform: none; } 35% { opacity: 1; transform: translateY(-2px); } }

.jump {
  position: sticky; bottom: 10px; margin: -42px auto 10px; width: 32px; height: 32px; display: grid; place-items: center; z-index: 2;
  border-radius: 999px; background: var(--bg); color: var(--fg-2); border: 1px solid var(--line); box-shadow: 0 4px 12px -4px rgb(0 0 0 / .18);
  animation: pop .2s var(--ease) both; transition: color .12s, transform .12s;
}
.jump:hover { color: var(--fg); transform: translateY(1px); }

.empty { padding: 32px 24px; text-align: center; color: var(--fg-3); font-size: 13px; animation: rise .22s var(--ease) both; }
.empty b { display: block; color: var(--fg); font-weight: 600; font-size: 14px; margin-bottom: 4px; }
.empty .intro { white-space: pre-line; }

/* Composer: one inset field with its actions inside. */
.composer { padding: 10px 12px 12px; display: flex; flex-direction: column; gap: 6px; background: var(--bg); flex: none; }
.pending-files { display: flex; flex-wrap: wrap; gap: 6px; }
.pending-files .chip { height: 26px; animation: rise .16s var(--ease) both; }
.pending-files .chip button { color: var(--fg-3); display: grid; place-items: center; border-radius: 3px; }
.pending-files .chip button:hover { color: var(--fg); }
/* Voice: the recording bar inside the field, a pending recording above it, and the player. */
.pending-voice { display: flex; animation: rise .16s var(--ease) both; }
.pending-voice .voice { flex: 1; width: auto; padding-inline-end: 4px; }
.rec-bar { flex: 1; min-width: 0; height: 30px; display: flex; align-items: center; gap: 8px; font-size: 12.5px; animation: fade .12s ease-out; }
.rec-dot { width: 8px; height: 8px; flex: none; border-radius: 50%; background: var(--danger); animation: rec-pulse 1.2s ease-in-out infinite; }
@keyframes rec-pulse { 0%, 100% { opacity: 1; } 50% { opacity: .3; } }
.rec-time, .rec-max { font-family: var(--mono); font-variant-numeric: tabular-nums; flex: none; }
.rec-time { color: var(--fg); }
.rec-max { color: var(--fg-4); }
/* Newest bar on the right; each moves left as the next comes in. */
.rec-wave { flex: 1; min-width: 24px; height: 22px; display: flex; align-items: center; justify-content: space-between; gap: 2px; overflow: hidden; }
.rec-wave span { flex: 1; max-width: 3px; min-height: 3px; border-radius: 2px; background: var(--fg-3); transition: height .1s linear; }
.rec-track, .voice-track { flex: 1; min-width: 24px; height: 4px; border-radius: 2px; background: var(--bg-2); overflow: hidden; }
.rec-track span, .voice-track span { display: block; height: 100%; border-radius: inherit; background: var(--danger); transition: width .1s linear; }
.rec-stop { width: 30px; height: 30px; flex: none; border-radius: var(--r-ctl, 8px); display: grid; place-items: center; background: var(--danger); color: #fff; transition: transform .12s, filter .12s; }
.rec-stop:hover { filter: brightness(1.08); }
.rec-stop:active { transform: scale(.92); }
.voice { display: flex; align-items: center; gap: 8px; width: 220px; max-width: 100%; height: 36px; padding-inline: 4px 12px; border-radius: 999px; border: 1px solid var(--line); background: var(--bg); }
.voice-del { width: 28px; height: 28px; flex: none; border-radius: 50%; display: grid; place-items: center; color: var(--fg-3); transition: background .12s, color .12s; }
.voice-del:hover { background: var(--danger-soft); color: var(--danger); }
.voice-play { width: 28px; height: 28px; flex: none; border-radius: 50%; display: grid; place-items: center; background: var(--accent); color: var(--accent-fg); transition: transform .12s; }
.voice-play:active { transform: scale(.92); }
.voice-track { cursor: pointer; outline: none; }
.voice-track:focus-visible { box-shadow: 0 0 0 2px var(--accent); }
.voice-track span { background: var(--accent); }
.voice-time { font-family: var(--mono); font-size: 11px; color: var(--fg-3); font-variant-numeric: tabular-nums; flex: none; }
.voice audio { display: none; }
.cbar {
  display: flex; align-items: flex-end; gap: 4px; padding: 5px; padding-inline-start: 12px;
  border: 1px solid var(--line); border-radius: var(--r-bar, 10px); background: var(--bg);
  transition: border-color .15s, box-shadow .15s;
}
.cbar:hover { border-color: var(--line-2); }
.cbar:focus-within { border-color: var(--fg-4); box-shadow: 0 0 0 3px var(--ring); }
.cbar.off { background: var(--bg-3); }
.cbar .icon-btn { width: 30px; height: 30px; }
.cbar.lead { padding-inline-start: 5px; }
.cbar.lead .input-wrap { margin-inline-start: 2px; }
.cbar.rec { border-radius: 999px; padding-inline-start: 5px; align-items: center; }
.cbar.rec .icon-btn, .cbar.rec .rec-stop { border-radius: 50%; }
.input-wrap { position: relative; flex: 1; min-width: 0; display: flex; }
textarea.input {
  flex: 1; width: 100%; resize: none; border: 0; background: transparent; font-size: 14px; line-height: 20px; padding: 5px 0;
  max-height: 120px; min-height: 30px; color: var(--fg); transition: height .12s var(--ease); scrollbar-width: thin;
}
textarea.mirror { position: absolute; inset-inline: 0; top: 0; height: 0 !important; min-height: 0; visibility: hidden; pointer-events: none; overflow: hidden; transition: none; }
textarea.input:disabled { cursor: not-allowed; }
textarea.input::placeholder, input.field::placeholder { color: var(--fg-4); }
.send {
  width: 30px; height: 30px; flex: none; border-radius: var(--r-ctl, 8px); display: grid; place-items: center;
  background: var(--accent); color: var(--accent-fg); opacity: .28; transform: scale(.9);
  transition: opacity .16s, transform .18s var(--ease);
}
.send.ready { opacity: 1; transform: none; }
.send.ready:hover { transform: translateY(-1px); }
.send.ready:active { transform: scale(.9); transition-duration: .06s; }
.send:disabled { cursor: default; }
.drop { position: absolute; inset: 0; z-index: 3; display: grid; place-items: center; background: color-mix(in srgb, var(--bg) 88%, transparent); border: 1.5px dashed var(--line-2); border-radius: inherit; font-size: 13px; font-weight: 500; color: var(--fg-2); animation: fade .12s ease-out; }
@keyframes fade { from { opacity: 0; } to { opacity: 1; } }
.capture { margin: 12px 14px 4px; padding: 14px; border: 1px solid var(--line); border-radius: var(--r-card, 10px); display: flex; flex-direction: column; gap: 8px; background: var(--bg-3); animation: rise .2s var(--ease) both; }
.capture p { font-size: 13px; color: var(--fg-2); }
input.field { height: 36px; border: 1px solid var(--line); border-radius: var(--r-ctl, 8px); padding: 0 12px; background: var(--bg); font-size: 13.5px; color: var(--fg); transition: border-color .15s, box-shadow .15s; }
input.field:hover { border-color: var(--line-2); }
input.field:focus { border-color: var(--fg-4); box-shadow: 0 0 0 3px var(--ring); }
.err { color: var(--danger); font-size: 12px; margin-top: 6px; }
.foot { display: flex; align-items: center; justify-content: center; gap: 5px; height: 30px; font-size: 11px; color: var(--fg-4); border-top: 1px solid var(--line); text-decoration: none; flex: none; transition: color .12s; }
.foot:hover { color: var(--fg-2); }
[dir="rtl"] .flip svg { transform: scaleX(-1); }
[dir="rtl"] .link-row:hover .flip svg { transform: scaleX(-1) translate(1px, -1px); }
.time, .day, .meta { unicode-bidi: plaintext; }
.sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
.skel { height: 12px; border-radius: 4px; margin: 10px 14px; background: linear-gradient(90deg, var(--bg-2) 0%, var(--bg-3) 50%, var(--bg-2) 100%) 0 0 / 200% 100%; animation: skel 1.4s ease-in-out infinite; }
@keyframes skel { from { background-position: 100% 0; } to { background-position: -100% 0; } }

${BOT_LOOK_CSS}

/* Touch screens: 16px fields, the size below which iOS Safari zooms in on focus. */
@media (pointer: coarse) { textarea.input, input.field { font-size: max(16px, 1em); } }
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation: none !important; transition: none !important; scroll-behavior: auto !important; }
  .t-running span:last-child { -webkit-text-fill-color: currentColor; background: none; }
  .dots span { opacity: .55; }
}
`;
