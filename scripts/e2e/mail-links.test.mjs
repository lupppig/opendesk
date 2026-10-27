import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { cfg, mailLink } from "./lib.mjs";

test("reads already decoded SMTP links without interpreting the token as quoted-printable", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "opendesk-mail-links-"));
  const before = cfg.mailSink;
  cfg.mailSink = path.join(dir, "mail.jsonl");
  const link = `${cfg.app}/csat#t=AA0123456789abcdefghijklmnop&score=4`;
  try {
    fs.writeFileSync(cfg.mailSink, JSON.stringify({
      to: ["survey@customer.test"],
      data: `Content-Transfer-Encoding: quoted-printable\r\n\r\n${link}`,
    }) + "\n");
    assert.equal(await mailLink("/csat#t=", "survey@customer.test", 0), link);
  } finally {
    cfg.mailSink = before;
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
