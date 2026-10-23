import { join } from "node:path";
import { QA_DIR, ROOT } from "./git.ts";

export { QA_DIR, ROOT };
export const ARTIFACTS = join(QA_DIR, ".artifacts");
export const QA_RUNS = join(ARTIFACTS, "qa");
