import { Database } from "bun:sqlite";
import { runMigrations } from "./run-migrations";

const db = new Database(":memory:");
db.run("PRAGMA foreign_keys = ON");
db.run(`CREATE TABLE users (
  id TEXT PRIMARY KEY NOT NULL,
  username TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  created_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE projects (
  id TEXT PRIMARY KEY NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  flow_mode TEXT NOT NULL DEFAULT 'classic',
  git_path TEXT,
  remote_url TEXT,
  default_branch TEXT DEFAULT 'main',
  startup_script TEXT,
  copy_files TEXT,
  chapter_settings TEXT,
  proxy_domain TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE chapters (
  id TEXT PRIMARY KEY NOT NULL,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  branch TEXT NOT NULL,
  base_branch TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE narrators (
  id TEXT PRIMARY KEY NOT NULL,
  chapter_id TEXT REFERENCES chapters(id),
  type TEXT NOT NULL DEFAULT 'primary',
  fast_mode INTEGER NOT NULL DEFAULT 0,
  traits TEXT NOT NULL DEFAULT '["legacy"]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY NOT NULL, narrator_id TEXT)`);
db.run(`CREATE TABLE narrator_grants (
  id TEXT PRIMARY KEY NOT NULL,
  narrator_id TEXT NOT NULL,
  principal_type TEXT NOT NULL,
  principal_id TEXT NOT NULL,
  access TEXT NOT NULL DEFAULT 'read',
  granted_by TEXT,
  created_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE remote_devices (
  id TEXT PRIMARY KEY NOT NULL,
  name TEXT NOT NULL,
  slug TEXT NOT NULL,
  description TEXT,
  token_hash TEXT NOT NULL,
  token_prefix TEXT NOT NULL,
  connection_mode TEXT NOT NULL DEFAULT 'reverse',
  direct_url TEXT,
  status TEXT NOT NULL DEFAULT 'offline',
  last_seen_at TEXT,
  platform_os TEXT,
  platform_arch TEXT,
  shell_path TEXT,
  default_cwd TEXT,
  agent_version TEXT,
  capabilities_json TEXT,
  scope TEXT NOT NULL DEFAULT 'global',
  project_id TEXT REFERENCES projects(id),
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  revoked_at TEXT
)`);
db.run(`CREATE TABLE user_preferences (
  id TEXT PRIMARY KEY NOT NULL,
  user_id TEXT NOT NULL UNIQUE,
  fast_mode_default INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE knowledge_collections (
  id TEXT PRIMARY KEY NOT NULL,
  name TEXT NOT NULL,
  slug TEXT NOT NULL,
  project_id TEXT REFERENCES projects(id),
  default_level TEXT NOT NULL DEFAULT 'public',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`);
db.run(`CREATE TABLE knowledge_grants (
  id TEXT PRIMARY KEY NOT NULL,
  collection_id TEXT,
  principal_type TEXT NOT NULL,
  principal_id TEXT NOT NULL,
  grant_type TEXT NOT NULL,
  clearance_level TEXT,
  tag_id TEXT,
  can_write INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
)`);
const now = "2026-08-19T00:00:00.000Z";
db.run("INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)", [
  "u-1", "sentinel", "x", "user", now,
]);
db.run("INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", [
  "p-1", "sentinel project", "keep-project", now, now,
]);
db.run("INSERT INTO chapters (id, project_id, title, branch, base_branch, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)", [
  "ch-1", "p-1", "sentinel chapter", "main", "main", now, now,
]);
db.run("INSERT INTO narrators (id, chapter_id, traits, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", [
  "n-1", "ch-1", "not-json", now, now,
]);
db.run("INSERT INTO narrator_messages (id, narrator_id) VALUES (?, ?)", ["msg-1", "n-1"]);
db.run("INSERT INTO narrator_grants (id, narrator_id, principal_type, principal_id, access, created_at) VALUES (?, ?, ?, ?, ?, ?)", [
  "ng-legacy", "n-1", "user", "u-1", "write", now,
]);
db.run("INSERT INTO remote_devices (id, name, slug, token_hash, token_prefix, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", [
  "dev-1", "sentinel device", "sentinel-device", "hash", "rdev_x", "u-1", now, now,
]);
db.run("INSERT INTO user_preferences (id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?)", [
  "pref-1", "u-1", now, now,
]);
db.run("INSERT INTO knowledge_collections (id, name, slug, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)", [
  "kc-1", "sentinel collection", "sentinel", "p-1", now, now,
]);
db.run("INSERT INTO knowledge_grants (id, collection_id, principal_type, principal_id, grant_type, tag_id, can_write, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", [
  "kg-1", "kc-1", "user", "u-1", "tag", "tag-1", 1, now,
]);

await runMigrations(db);
const firstCount = (db.prepare("SELECT COUNT(*) AS n FROM __drizzle_migrations").get() as { n: number }).n;
await runMigrations(db);
const secondCount = (db.prepare("SELECT COUNT(*) AS n FROM __drizzle_migrations").get() as { n: number }).n;
if (firstCount !== 1 || secondCount !== 1) throw new Error(`migration count ${firstCount}/${secondCount}`);

const tables = ["narrator_grants", "acl_grants", "acl_events", "chat_rooms", "chat_room_members", "chat_messages", "chat_attachments"];
for (const table of tables) {
  const exists = db.prepare("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?").get(table);
  if (!exists) throw new Error(`missing table ${table}`);
}
const cols = (table: string) => new Set((db.prepare(`PRAGMA table_info(\"${table}\")`).all() as Array<{ name: string }>).map((r) => r.name));
const required: Record<string, string[]> = {
  projects: ["traits", "owner_user_id", "visibility"],
  chapters: ["graph_x", "graph_y", "dock_layout_json", "detached_panels_json"],
  narrators: ["owner_user_id", "visibility"],
  remote_devices: ["path_rules_json", "reported_path_rules_json", "owner_scope", "enrolled_at", "enrolled_from_ip", "enrolled_user_agent"],
  user_preferences: ["traits"],
  knowledge_collections: ["inherit_project_gate"],
};
for (const [table, names] of Object.entries(required)) for (const name of names) if (!cols(table).has(name)) throw new Error(`missing column ${table}.${name}`);
for (const name of ["idx_narrators_owner", "idx_narrators_visibility"]) {
  if (!db.prepare("SELECT 1 FROM sqlite_master WHERE type='index' AND name=?").get(name)) throw new Error(`missing index ${name}`);
}
const project = db.prepare("SELECT name, traits, visibility FROM projects WHERE id='p-1'").get() as { name: string; traits: string; visibility: string };
const narrator = db.prepare("SELECT traits, visibility FROM narrators WHERE id='n-1'").get() as { traits: string; visibility: string };
const device = db.prepare("SELECT owner_scope, path_rules_json, reported_path_rules_json FROM remote_devices WHERE id='dev-1'").get() as { owner_scope: string; path_rules_json: string | null; reported_path_rules_json: string | null };
const pref = db.prepare("SELECT traits FROM user_preferences WHERE id='pref-1'").get() as { traits: string };
const collection = db.prepare("SELECT inherit_project_gate FROM knowledge_collections WHERE id='kc-1'").get() as { inherit_project_gate: number };
if (project.name !== "sentinel project" || project.traits !== "[]" || project.visibility !== "public") throw new Error(`project values ${JSON.stringify(project)}`);
if (narrator.traits !== "[]" || narrator.visibility !== "public") throw new Error(`narrator values ${JSON.stringify(narrator)}`);
if (device.owner_scope !== "shared" || device.path_rules_json !== null || device.reported_path_rules_json !== null) throw new Error(`device values ${JSON.stringify(device)}`);
if (pref.traits !== "[]" || collection.inherit_project_gate !== 0) throw new Error(`defaults ${JSON.stringify({ pref, collection })}`);
if ((db.prepare("SELECT COUNT(*) AS n FROM narrator_grants").get() as { n: number }).n !== 1) throw new Error("legacy narrator grant lost");
if ((db.prepare("SELECT COUNT(*) AS n FROM knowledge_grants").get() as { n: number }).n !== 1) throw new Error("legacy knowledge grant lost");
for (const row of db.prepare("SELECT traits FROM projects UNION ALL SELECT traits FROM narrators UNION ALL SELECT traits FROM user_preferences").all() as Array<{ traits: string }>) {
  if (db.prepare("SELECT json_valid(?) AS ok").get(row.traits) as { ok: number } && !(db.prepare("SELECT json_valid(?) AS ok").get(row.traits) as { ok: number }).ok) throw new Error("invalid traits JSON");
}
const fk = db.prepare("PRAGMA foreign_key_check").all();
if (fk.length !== 0) throw new Error(`foreign key violations ${JSON.stringify(fk)}`);
console.log(JSON.stringify({ firstCount, secondCount, tables: tables.length, project, narrator, device, pref, collection, foreignKeyViolations: fk.length }));
db.close();
