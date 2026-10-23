// Claude Code PreToolUse hook (Bash): leaves merging past the queue and the repository's rules
// to the maintainer. Exit 2 refuses the command and shows the agent why. Pure node, no install needed.
import { readFileSync } from "node:fs";

type Input = { tool_name?: string; tool_input?: { command?: string } };

// A command that runs `tool`: at the start of the line or after a separator, past any `VAR=x`
// prefixes. Text that only mentions it (a heredoc, a commit message, docs) is not one.
function invokes(tool: string, args: string): RegExp {
  return new RegExp(String.raw`(^|[;&|(\n]|\$\()\s*(\w+=\S*\s+)*${tool}\s+${args}`, "m");
}
// The rest of that command, up to the next separator.
const REST = String.raw`[^;&|\n]*`;

const RULES: [RegExp, string][] = [
  [invokes("gh", String.raw`pr\s+merge\b${REST}--admin\b`), "Merging past the merge queue's checks is for the maintainer. Leave the pull request to the queue."],
  [
    invokes("gh", String.raw`api\b(?=${REST}\b(rulesets|protection)\b)(?=${REST}(\s(-X|--method)[=\s]*(PUT|PATCH|POST|DELETE)\b|\s(-f|-F|--field|--raw-field|--input)\b))`),
    "Repository rulesets and branch protection are the maintainer's to change.",
  ],
];

function main(): void {
  let input: Input;
  try {
    input = JSON.parse(readFileSync(0, "utf8")) as Input;
  } catch {
    return;
  }
  const cmd = input.tool_input?.command;
  if (input.tool_name !== "Bash" || !cmd) return;
  for (const [re, why] of RULES) {
    if (re.test(cmd)) {
      process.stderr.write(`Blocked by the QA hook (qa/hooks/pretool.ts): ${why}\n`);
      process.exit(2);
    }
  }
}

main();
