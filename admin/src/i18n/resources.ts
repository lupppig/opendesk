// The catalogs, one JSON file per namespace and language. Add a namespace here,
// in NAMESPACES, and as locales/<lng>/<ns>.json for every language; i18next.d.ts
// derives the typed keys from the English ones.

import en_common from "./locales/en/common.json";
import en_shell from "./locales/en/shell.json";
import en_auth from "./locales/en/auth.json";
import en_overview from "./locales/en/overview.json";
import en_workspaces from "./locales/en/workspaces.json";
import en_testWorkspaces from "./locales/en/testWorkspaces.json";
import en_users from "./locales/en/users.json";
import en_jobs from "./locales/en/jobs.json";
import en_health from "./locales/en/health.json";
import en_audit from "./locales/en/audit.json";
import en_admins from "./locales/en/admins.json";
import fr_common from "./locales/fr/common.json";
import fr_shell from "./locales/fr/shell.json";
import fr_auth from "./locales/fr/auth.json";
import fr_overview from "./locales/fr/overview.json";
import fr_workspaces from "./locales/fr/workspaces.json";
import fr_testWorkspaces from "./locales/fr/testWorkspaces.json";
import fr_users from "./locales/fr/users.json";
import fr_jobs from "./locales/fr/jobs.json";
import fr_health from "./locales/fr/health.json";
import fr_audit from "./locales/fr/audit.json";
import fr_admins from "./locales/fr/admins.json";
import de_common from "./locales/de/common.json";
import de_shell from "./locales/de/shell.json";
import de_auth from "./locales/de/auth.json";
import de_overview from "./locales/de/overview.json";
import de_workspaces from "./locales/de/workspaces.json";
import de_testWorkspaces from "./locales/de/testWorkspaces.json";
import de_users from "./locales/de/users.json";
import de_jobs from "./locales/de/jobs.json";
import de_health from "./locales/de/health.json";
import de_audit from "./locales/de/audit.json";
import de_admins from "./locales/de/admins.json";

export const NAMESPACES = ["common", "shell", "auth", "overview", "workspaces", "testWorkspaces", "users", "jobs", "health", "audit", "admins"] as const;
export type Namespace = (typeof NAMESPACES)[number];

export const en = { common: en_common, shell: en_shell, auth: en_auth, overview: en_overview, workspaces: en_workspaces, testWorkspaces: en_testWorkspaces, users: en_users, jobs: en_jobs, health: en_health, audit: en_audit, admins: en_admins } as const;
const fr = { common: fr_common, shell: fr_shell, auth: fr_auth, overview: fr_overview, workspaces: fr_workspaces, testWorkspaces: fr_testWorkspaces, users: fr_users, jobs: fr_jobs, health: fr_health, audit: fr_audit, admins: fr_admins } as const;
const de = { common: de_common, shell: de_shell, auth: de_auth, overview: de_overview, workspaces: de_workspaces, testWorkspaces: de_testWorkspaces, users: de_users, jobs: de_jobs, health: de_health, audit: de_audit, admins: de_admins } as const;

export const resources = { en, fr, de };
