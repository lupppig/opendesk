// Checks the per-subject socket cap against a running gateway (GATEWAY_URL, AUTH_SECRET).
import { createHmac, randomUUID } from "node:crypto";

const URL_ = (process.env.GATEWAY_URL ?? "ws://127.0.0.1:4000/socket") + "/websocket?vsn=2.0.0";
const SECRET = process.env.AUTH_SECRET ?? "development-only-auth-secret-change-me-0000";
const b64 = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
const now = Math.floor(Date.now() / 1000);
const sub = randomUUID();
const head = b64({ alg: "HS256", typ: "JWT" });
const body = b64({ purpose: "ws", kind: "user", sub, wid: randomUUID(), role: "agent", iat: now, exp: now + 300 });
const token = `${head}.${body}.${createHmac("sha256", SECRET).update(`${head}.${body}`).digest("base64url")}`;

const open = () =>
  new Promise((resolve) => {
    // The token rides in Sec-WebSocket-Protocol (Phoenix's auth token), never the URL.
    const ws = new WebSocket(URL_, ["phoenix", "base64url.bearer.phx." + Buffer.from(token).toString("base64").replace(/=/g, "")]);
    ws.onopen = () => resolve(ws);
    ws.onerror = () => resolve(null);
  });

let fails = 0;
const ok = (c, m) => (console.log(`${c ? "PASS" : "FAIL"}  ${m}`), c || fails++);
const held = [];
for (let i = 0; i < 20; i++) held.push(await open());
ok(held.every(Boolean), "20 sockets for one subject open");
ok((await open()) === null, "the 21st socket for that subject is refused");
held.forEach((ws) => ws.close());
await new Promise((r) => setTimeout(r, 300));
const again = await open();
ok(again !== null, "after closing them, the subject can connect again");
again?.close();
process.exit(fails ? 1 : 0);
