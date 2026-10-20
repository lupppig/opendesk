// End-to-end check of the realtime gateway with the real `phoenix` JS client.
//
//   GATEWAY_URL   ws://127.0.0.1:4000/socket   (agents' socket; the widget uses GATEWAY_URL + "/widget")
//   GATEWAY_URL_B optional second node of a cluster: Ben, the widget and Ana's
//                 second socket connect there, so presence, typing, events and
//                 disconnect are checked across nodes
//   AUTH_SECRET   the secret the Go API signs with (dev default below)
//   E2E_PUBLISH   a command that publishes through the Go outbox:
//                   $E2E_PUBLISH -wid <wid> -type <t> -visibility <v> -topics <a,b>
//                   $E2E_PUBLISH -disconnect <user_id>
//
// Run: npm install && node e2e.mjs
import { createHmac, randomUUID } from "node:crypto";
import { execFileSync } from "node:child_process";
import { Socket, Presence } from "phoenix";

const GATEWAY_URL = process.env.GATEWAY_URL ?? "ws://127.0.0.1:4000/socket";
const GATEWAY_URL_B = process.env.GATEWAY_URL_B ?? GATEWAY_URL;
const SECRET = process.env.AUTH_SECRET ?? "development-only-auth-secret-change-me-0000";
const PUBLISH = process.env.E2E_PUBLISH;
if (!PUBLISH) throw new Error("set E2E_PUBLISH");

const b64 = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
function jwt(claims, { alg = "HS256", secret = SECRET } = {}) {
  const now = Math.floor(Date.now() / 1000);
  const head = b64({ alg, typ: "JWT" });
  const body = b64({ iat: now, exp: now + 300, jti: randomUUID(), ...claims });
  const hash = { HS256: "sha256", HS512: "sha512" }[alg];
  const sig = createHmac(hash, secret).update(`${head}.${body}`).digest("base64url");
  return `${head}.${body}.${sig}`;
}

let failures = 0;
const ok = (cond, msg) => {
  console.log(`${cond ? "PASS" : "FAIL"}  ${msg}`);
  if (!cond) failures++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms = 3000) => {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (fn()) return true;
    await sleep(25);
  }
  return false;
};

function connect(token, url = GATEWAY_URL) {
  const socket = new Socket(url, {
    // The token rides in Sec-WebSocket-Protocol, never the URL.
    authToken: token,
    reconnectAfterMs: () => 60_000,
    // Silence the client's own logging.
    logger: () => {},
  });
  const state = { opened: false, closed: false, errored: false };
  socket.onOpen(() => (state.opened = true));
  socket.onClose(() => (state.closed = true));
  socket.onError(() => (state.errored = true));
  socket.connect();
  return { socket, state };
}

function join(socket, topic, setup = () => {}) {
  const channel = socket.channel(topic, {});
  const inbox = [];
  setup(channel);
  for (const ev of ["event", "typing", "customer_typing", "presence_state", "presence_diff"]) {
    channel.on(ev, (payload) => inbox.push({ ev, payload }));
  }
  return new Promise((resolve) => {
    channel
      .join()
      .receive("ok", (reply) => resolve({ channel, inbox, reply }))
      .receive("error", (reply) => resolve({ channel, inbox, error: reply }))
      .receive("timeout", () => resolve({ channel, inbox, error: "timeout" }));
  });
}

function publish(args) {
  return execFileSync(PUBLISH, args, { encoding: "utf8" }).trim();
}

const wid = randomUUID();
const otherWid = randomUUID();
const tid = randomUUID();
const ana = { sub: randomUUID(), name: "Ana" };
const ben = { sub: randomUUID(), name: "Ben" };
const cid = randomUUID();
const ws = (u) =>
  jwt({ purpose: "ws", kind: "user", wid, role: "agent", avatar: "https://e.test/a.png", ...u });

// 1. Tokens the gateway must refuse.
for (const [label, token] of [
  ["purpose access", jwt({ purpose: "access", sub: ana.sub })],
  ["HS512", jwt({ purpose: "ws", kind: "user", wid, role: "agent", sub: ana.sub }, { alg: "HS512" })],
  ["expired", jwt({ purpose: "ws", kind: "user", wid, role: "agent", sub: ana.sub, exp: 1 })],
  ["wrong secret", jwt({ purpose: "ws", kind: "user", wid, role: "agent", sub: ana.sub }, { secret: "x".repeat(40) })],
]) {
  const c = connect(token);
  await until(() => c.state.errored || c.state.opened, 2000);
  ok(!c.state.opened, `refuses a token with ${label}`);
  c.socket.disconnect();
}
for (const [label, token, url] of [
  ["widget_ws token on the agents' socket", jwt({ purpose: "widget_ws", sub: cid, wid }), GATEWAY_URL],
  ["ws token on the widget socket", ws(ana), GATEWAY_URL + "/widget"],
]) {
  const c = connect(token, url);
  await until(() => c.state.errored || c.state.opened, 2000);
  ok(!c.state.opened, `refuses a ${label}`);
  c.socket.disconnect();
}

// 2. Agents and a contact connect and join.
const a = connect(ws(ana));
const b = connect(ws(ben), GATEWAY_URL_B);
const k = connect(jwt({ purpose: "widget_ws", sub: cid, wid }), GATEWAY_URL_B + "/widget");
ok(await until(() => a.state.opened && b.state.opened && k.state.opened), "agents and widget connect");

const aWs = await join(a.socket, `workspace:${wid}`);
const aUser = await join(a.socket, `user:${ana.sub}`);
// Presence tracking is set up before join so presence_state is not missed.
const sync = (store) => (channel) => {
  const p = new Presence(channel);
  p.onSync(() => {
    for (const k of Object.keys(store)) delete store[k];
    p.list((id, { metas }) => (store[id] = metas[0]));
  });
};
const seen = {};
const bSeen = {};
const aTicket = await join(a.socket, `ticket:${wid}:${tid}`, sync(seen));
const bWs = await join(b.socket, `workspace:${wid}`);
const bUser = await join(b.socket, `user:${ben.sub}`);
const kContact = await join(k.socket, `contact:${wid}:${cid}`);
ok(
  [aWs, aUser, aTicket, bWs, bUser, kContact].every((j) => j.reply?.server_time),
  "joins reply {server_time}",
);

// 3. Forbidden joins.
const bad = [
  await join(a.socket, `workspace:${otherWid}`),
  await join(a.socket, `user:${ben.sub}`),
  await join(a.socket, `ticket:${wid}:not-a-uuid`),
  await join(k.socket, `contact:${wid}:${randomUUID()}`),
];
ok(bad.every((j) => j.error?.reason === "forbidden"), "unauthorized joins reply {reason: forbidden}");
const unrouted = [await join(k.socket, `ticket:${wid}:${tid}`), await join(k.socket, `workspace:${wid}`)];
ok(unrouted.every((j) => j.error?.reason === "unmatched topic"), "the widget socket routes no agent topics");
const noContact = await join(a.socket, `contact:${wid}:${cid}`);
ok(noContact.error?.reason === "unmatched topic", "the agents' socket routes no contact topics");

// 4. Presence between two agents.
const bTicket = await join(b.socket, `ticket:${wid}:${tid}`, sync(bSeen));
ok(await until(() => bSeen[ana.sub]?.state === "viewing"), "Ben's presence_state lists Ana");
ok(await until(() => seen[ben.sub]?.state === "viewing"), "Ana sees Ben viewing the ticket");
ok(seen[ana.sub]?.name === "Ana" && seen[ana.sub]?.avatar === "https://e.test/a.png", "presence meta carries name and avatar");
bTicket.channel.push("presence", { state: "replying" });
ok(await until(() => seen[ben.sub]?.state === "replying"), "Ben's presence update to replying reaches Ana");
const invalid = await new Promise((r) =>
  bTicket.channel.push("presence", { state: "away" }).receive("error", r).receive("ok", () => r(null)),
);
ok(invalid?.reason === "invalid", "invalid presence state is refused");

// 5. Typing.
aTicket.channel.push("typing", { contact_id: cid });
ok(
  await until(() => bTicket.inbox.some((m) => m.ev === "typing" && m.payload.user_id === ana.sub && m.payload.name === "Ana")),
  "agent typing reaches the other agent on the ticket",
);
ok(
  await until(() =>
    kContact.inbox.some((m) => m.ev === "typing" && m.payload.ticket_id === tid && m.payload.user?.id === ana.sub),
  ),
  "agent typing with contact_id reaches the widget as {ticket_id, user}",
);
ok(!aTicket.inbox.some((m) => m.ev === "typing"), "typing is not echoed to the sender");
kContact.channel.push("typing", { ticket_id: tid });
ok(
  await until(() =>
    [aTicket, bTicket].every((t) =>
      t.inbox.some((m) => m.ev === "customer_typing" && m.payload.contact_id === cid && m.payload.ticket_id === tid),
    ),
  ),
  "widget typing reaches both agents as customer_typing",
);

// 6. Events through the Go outbox -> relay -> NATS -> gateway.
const events = (j) => j.inbox.filter((m) => m.ev === "event").map((m) => m.payload);
console.log("     ", publish(["-wid", wid, "-type", "ticket.updated", "-visibility", "agents", "-topics", `user:${ana.sub},contact:${wid}:${cid}`]));
ok(await until(() => events(aWs).length === 1 && events(bWs).length === 1), "agents event reaches workspace channels of both agents");
ok(await until(() => events(aUser).length === 1), "agents event reaches the listed user:<uid>");
await sleep(300);
ok(events(bUser).length === 0, "agents event does not reach an unlisted user");
ok(events(kContact).length === 0, "agents event does not reach the contact even when listed");
const first = events(aWs)[0];
ok(first && !("topics" in first) && first.workspace_id === wid && first.type === "ticket.updated", "payload is the envelope minus topics");

console.log("     ", publish(["-wid", wid, "-type", "message.created", "-visibility", "public", "-topics", `contact:${wid}:${cid}`]));
ok(await until(() => events(kContact).length === 1 && events(aWs).length === 2), "public event reaches the contact and the workspace");
ok(events(aWs)[1]?.seq === first.seq + 1, `seq is contiguous (${first?.seq} -> ${events(aWs)[1]?.seq})`);
await sleep(300);
ok(events(aUser).length === 1, "public event without user topic does not reach user:<uid>");

// 7. Disconnect control message closes Ana's sockets only.
const a2 = connect(ws(ana), GATEWAY_URL_B);
ok(await until(() => a2.state.opened), "Ana opens a second socket");
console.log("     ", publish(["-disconnect", ana.sub]));
ok(await until(() => a.state.closed && a2.state.closed), "od.control.disconnect closes all of Ana's sockets");
await sleep(300);
ok(!b.state.closed && b.socket.isConnected(), "Ben stays connected");
ok(await until(() => !bSeen[ana.sub] && bSeen[ben.sub]), "Ben sees Ana leave the ticket after the disconnect");

for (const c of [a, a2, b, k]) c.socket.disconnect();
console.log(failures === 0 ? "\nALL PASSED" : `\n${failures} FAILED`);
process.exit(failures === 0 ? 0 : 1);
