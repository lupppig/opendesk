// `pnpm qa`: runs the checks CI runs, for the areas this branch changes, against the committed
// HEAD, and records the result in .artifacts/qa/latest.json.
//
//   node scripts/qa.ts            check what the branch changes since it left main
//   node scripts/qa.ts --all      every area, whatever changed
//   node scripts/qa.ts --dirty    allow uncommitted changes
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { QA_RUNS, ROOT } from "../lib/env.ts";
import { baseRef, changedFiles, git } from "../lib/git.ts";

export type CheckResult = { area: string; name: string; cmd: string; ok: boolean; seconds: number; summary?: string; log: string };
export type QaRun = {
  sha: string;
  base: string;
  dirty: boolean;
  result: "pass" | "fail";
  startedAt: string;
  finishedAt: string;
  files: number;
  checks: CheckResult[];
  // Areas the branch changes that `pnpm qa` has no check for.
  unchecked: string[];
};

type Check = { name: string; dir: string; cmd: string[]; env?: Record<string, string>; summarize?: (out: string) => string | undefined; ok?: (code: number, out: string) => boolean };
type Area = { name: string; match: RegExp; checks: (files: string[]) => Check[] };

const flags = new Set(process.argv.slice(2));
for (const f of flags) if (!["--all", "--dirty"].includes(f)) die(`unknown flag ${f}`);

function die(msg: string): never {
  console.error(`qa: ${msg}`);
  process.exit(1);
}

const under = (prefix: string, files: string[]) => files.filter((f) => f.startsWith(prefix)).map((f) => f.slice(prefix.length));
const present = (files: string[]) => files.filter((f) => existsSync(join(ROOT, f)));

// `go test -json` lines, counted into one line for the QA section.
function goTestSummary(out: string): string | undefined {
  const n = { pass: 0, fail: 0, skip: 0 };
  for (const line of out.split("\n")) {
    if (!line.startsWith("{")) continue;
    try {
      const e = JSON.parse(line) as { Action?: string; Test?: string };
      if (e.Test && (e.Action === "pass" || e.Action === "fail" || e.Action === "skip")) n[e.Action]++;
    } catch {
      // A build error line.
    }
  }
  if (!n.pass && !n.fail && !n.skip) return undefined;
  const skipped = n.skip ? `, ${n.skip} skipped${process.env.OPENDESK_TEST_DATABASE_URL || process.env.TEST_DATABASE_URL ? "" : " (database tests need OPENDESK_TEST_DATABASE_URL)"}` : "";
  return `${n.pass} passed, ${n.fail} failed${skipped}`;
}

function vitestSummary(out: string): string | undefined {
  const m = out.match(/Tests\s+(.+?)\s*(\(\d+\))?\s*$/m);
  return m ? m[1].replace(/\x1b\[[0-9;]*m/g, "").trim() : out.includes("No test files found") ? "no tests cover these files" : undefined;
}

function pnpmTree(dir: string, files: string[], opts: { lint?: boolean; related?: boolean; build?: boolean; typecheck?: boolean } = {}): Check[] {
  const out: Check[] = [];
  if (!existsSync(join(ROOT, dir, "node_modules"))) out.push({ name: "install", dir, cmd: ["pnpm", "install", "--frozen-lockfile"] });
  if (opts.typecheck !== false) out.push({ name: "typecheck", dir, cmd: ["pnpm", "typecheck"] });
  if (opts.lint) out.push({ name: "lint", dir, cmd: ["pnpm", "lint"] });
  const src = under(`${dir}/`, present(files)).filter((f) => /\.(ts|tsx|js|mjs)$/.test(f) && !f.endsWith(".d.ts"));
  if (opts.related && src.length) {
    out.push({ name: "vitest (related)", dir, cmd: ["pnpm", "exec", "vitest", "related", "--run", "--passWithNoTests", ...src], summarize: vitestSummary });
  }
  if (opts.build) out.push({ name: "build", dir, cmd: ["pnpm", "build"] });
  return out;
}

const AREAS: Area[] = [
  {
    name: "go",
    match: /(\.go$|^go\.(mod|sum)$|^migrations\/)/,
    checks: (files) => {
      const gofiles = present(files.filter((f) => f.endsWith(".go")));
      const whole = files.some((f) => /^go\.(mod|sum)$/.test(f));
      const pkgs = whole ? ["./..."] : [...new Set(gofiles.map((f) => `./${f.split("/").slice(0, -1).join("/") || "."}`))];
      const checks: Check[] = [];
      if (gofiles.length) {
        checks.push({ name: "gofmt", dir: ".", cmd: ["gofmt", "-l", ...gofiles], ok: (code, out) => code === 0 && !out.trim() });
      }
      checks.push({ name: "go vet", dir: ".", cmd: ["go", "vet", "./..."] }, { name: "go build", dir: ".", cmd: ["go", "build", "./..."] });
      if (pkgs.length) checks.push({ name: "go test", dir: ".", cmd: ["go", "test", "-count=1", "-p", "1", "-json", ...pkgs], summarize: goTestSummary });
      return checks;
    },
  },
  { name: "migrations", match: /^migrations\//, checks: () => [{ name: "migration versions", dir: ".", cmd: ["./scripts/check-migrations.sh"] }] },
  { name: "web", match: /^(web|brand)\//, checks: (f) => pnpmTree("web", f, { lint: true, related: true }) },
  { name: "admin", match: /^admin\//, checks: (f) => pnpmTree("admin", f, { lint: true, related: true }) },
  { name: "site", match: /^(site|brand)\//, checks: (f) => pnpmTree("site", f, { typecheck: false, build: true }) },
  { name: "docs", match: /^docs\//, checks: (f) => pnpmTree("docs", f, { typecheck: false, build: true }) },
  { name: "desktop", match: /^desktop\//, checks: (f) => pnpmTree("desktop", f, { related: true }) },
  { name: "qa", match: /^qa\//, checks: () => [{ name: "typecheck", dir: "qa", cmd: ["pnpm", "typecheck"] }, { name: "unit tests", dir: "qa", cmd: ["pnpm", "test"] }] },
  {
    name: "realtime",
    match: /^realtime\//,
    checks: () => [
      { name: "deps", dir: "realtime", cmd: ["mix", "deps.get"] },
      { name: "mix format", dir: "realtime", cmd: ["mix", "format", "--check-formatted"] },
      { name: "compile", dir: "realtime", cmd: ["mix", "compile", "--warnings-as-errors"] },
      { name: "mix test", dir: "realtime", cmd: ["mix", "test"] },
    ],
  },
  {
    name: "ios",
    match: /^ios\//,
    checks: () => [
      {
        name: "xcodebuild (Debug, simulator)",
        dir: ".",
        cmd: ["xcodebuild", "-project", "ios/Opendesk.xcodeproj", "-scheme", "Opendesk", "-configuration", "Debug", "-sdk", "iphonesimulator", "-destination", "generic/platform=iOS Simulator", "-derivedDataPath", "qa/.artifacts/ios-build", "-quiet", "build"],
        env: { DEVELOPER_DIR: process.env.DEVELOPER_DIR ?? "/Applications/Xcode.app/Contents/Developer" },
      },
    ],
  },
  { name: "terraform", match: /^deploy\/terraform\//, checks: () => [{ name: "fmt and validate", dir: ".", cmd: ["make", "tf-check"] }] },
  { name: "installer", match: /^(deploy\/(install|self-host|caddy)\/|docker-compose\.yml$)/, checks: () => [{ name: "installer checks", dir: ".", cmd: ["make", "installer-check"] }] },
];

// Areas a change can touch that have no local check here; named after the run so nobody assumes they were checked.
const UNCHECKED: [string, RegExp][] = [
  ["e2e flows", /^scripts\/e2e\//],
  ["workflows", /^\.github\//],
  ["cloudflare", /^deploy\/cloudflare\//],
];

function run(area: string, c: Check, logDir: string): CheckResult {
  const log = join(logDir, `${area}-${c.name.replace(/[^a-z0-9]+/gi, "-")}.log`);
  const cmd = c.cmd.join(" ");
  process.stdout.write(`qa: ${area}: ${c.name} ... `);
  const started = Date.now();
  const res = spawnSync(c.cmd[0], c.cmd.slice(1), { cwd: join(ROOT, c.dir), encoding: "utf8", maxBuffer: 256 * 1024 * 1024, env: { ...process.env, CI: "1", NO_COLOR: "1", ...c.env } });
  const seconds = (Date.now() - started) / 1000;
  const out = `${res.stdout ?? ""}${res.stderr ?? ""}${res.error ? `\n${res.error.message}` : ""}`;
  writeFileSync(log, `$ (cd ${c.dir} && ${cmd})\n${out}`);
  const code = res.status ?? 1;
  const ok = c.ok ? c.ok(code, out) : code === 0;
  const summary = c.summarize?.(res.stdout ?? "");
  console.log(`${ok ? "ok" : "FAILED"} (${seconds.toFixed(1)}s)${summary ? `, ${summary}` : ""}`);
  if (!ok) {
    const tail = out.trim().split("\n").filter((l) => !l.startsWith('{"')).slice(-25).join("\n");
    console.log(tail.replace(/^/gm, "    "));
    console.log(`    full log: ${log}`);
  }
  return { area, name: c.name, cmd: c.dir === "." ? cmd : `(cd ${c.dir} && ${cmd})`, ok, seconds, summary, log };
}

function main(): void {
  const dirty = git("status", "--porcelain", "--untracked-files=no").length > 0;
  if (dirty && !flags.has("--dirty")) die("the tree has uncommitted changes. QA vouches for a commit: commit first (or pass --dirty)");
  const sha = git("rev-parse", "HEAD");
  const base = baseRef();
  const files = changedFiles(base);
  const matches = (re: RegExp) => flags.has("--all") || files.some((f) => re.test(f));
  const areas = AREAS.filter((a) => matches(a.match));
  const unchecked = UNCHECKED.filter(([, re]) => files.some((f) => re.test(f))).map(([n]) => n);
  console.log(`qa: ${files.length} file(s) changed since ${base} at ${sha.slice(0, 9)}; checking ${areas.map((a) => a.name).join(", ") || "nothing (no checked area changed)"}`);

  // internal/webui embeds the built app; CI compiles against a placeholder, and so does this.
  const embed = join(ROOT, "internal/webui/dist/index.html");
  if (areas.some((a) => a.name === "go") && !existsSync(embed)) writeFileSync(embed, "<!doctype html><title>opendesk</title>\n");

  const startedAt = new Date().toISOString();
  const logDir = join(QA_RUNS, "logs", sha.slice(0, 12));
  mkdirSync(logDir, { recursive: true });
  const checks: CheckResult[] = [];
  for (const a of areas) for (const c of a.checks(files)) checks.push(run(a.name, c, logDir));

  const result = checks.every((c) => c.ok) ? "pass" : "fail";
  const qa: QaRun = {
    sha,
    base,
    dirty,
    result,
    startedAt,
    finishedAt: new Date().toISOString(),
    files: files.length,
    checks,
    unchecked,
  };
  writeFileSync(join(QA_RUNS, "latest.json"), JSON.stringify(qa, null, 2));
  if (unchecked.length) console.log(`qa: not checked here: ${unchecked.join(", ")}`);
  console.log(`qa: ${result === "pass" ? "passed" : "FAILED"}`);
  process.exit(result === "pass" ? 0 : 1);
}

main();
