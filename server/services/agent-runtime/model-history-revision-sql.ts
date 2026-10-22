/** Pure migration input. No runtime DDL and no imports of a live database. */
/**
 * The contract the runtime trusts: v3 lets a state-only write (`state_revision` moves) change
 * an envelope's body columns without a bump (docs/BLOCK_STORAGE-DESIGN.md §5.4).
 */
export const MODEL_HISTORY_REVISION_CONTRACT = "model-history-v3";
/** Frozen: the published block-storage migration (0213, PG 0029) was rendered from it. */
export const MODEL_HISTORY_REVISION_CONTRACT_V2 = "model-history-v2";
/** Frozen: the published v1 migration (0196) and its install proof are rendered from it. */
export const MODEL_HISTORY_REVISION_CONTRACT_V1 = "model-history-v1";

const messageColumns = [
	"id",
	"narrator_id",
	"sdk_message_uuid",
	"parent_tool_use_id",
	"role",
	"content_json",
	"content_text",
	"created_by",
	"origin",
	"origin_label",
];
const toolColumns = [
	"id",
	"message_id",
	"narrator_id",
	"tool_use_id",
	"tool_name",
	"input_json",
	"output_json",
	"status",
	"execution_attempt",
	"created_at",
];
const refColumns = ["id", "narrator_id", "message_id", "seq", "is_compact", "segment_compact_id"];

function sqliteChanged(columns: string[]): string {
	return columns.map((column) => `OLD.${column} IS NOT NEW.${column}`).join(" OR ");
}
function pgChanged(columns: string[]): string {
	return columns.map((column) => `OLD.${column} IS DISTINCT FROM NEW.${column}`).join(" OR ");
}
function holderUpdate(messageIds: string[], narratorIds: string[]): string {
	return `UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (${narratorIds.join(", ")}) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (${messageIds.join(", ")}));`;
}

/**
 * The v1 contract, frozen byte for byte: the published v1 migration was rendered from it,
 * and the migrator proves an installed v1 database against this exact text.
 */
export function renderModelHistoryRevisionSqlV1(dialect: "sqlite" | "postgres"): string {
	if (dialect === "sqlite") {
		const statements: string[] = [];
		const trigger = (name: string, event: string, table: string, body: string, condition = "") => {
			statements.push(
				`CREATE TRIGGER ${name} ${event} ON ${table}${condition ? ` WHEN ${condition}` : ""} BEGIN\n${body}\nEND;`,
			);
		};
		for (const event of ["INSERT", "UPDATE", "DELETE"] as const) {
			const row = event === "DELETE" ? "OLD" : "NEW";
			const both = event === "UPDATE";
			const messageIds = both ? ["OLD.id", "NEW.id"] : [`${row}.id`];
			const owners = both ? ["OLD.narrator_id", "NEW.narrator_id"] : [`${row}.narrator_id`];
			trigger(
				`model_history_v1_message_${event.toLowerCase()}`,
				`${event === "DELETE" ? "BEFORE" : "AFTER"} ${event}`,
				"narrator_messages",
				(event === "DELETE"
					? ""
					: `UPDATE narrator_messages SET body_revision = ${event === "INSERT" ? "NEW.body_revision" : "OLD.body_revision"} + 1 WHERE id = NEW.id;\n`) +
					holderUpdate(messageIds, owners),
				both ? sqliteChanged(messageColumns) : "",
			);
			trigger(
				`model_history_v1_tool_${event.toLowerCase()}`,
				`${event === "DELETE" ? "BEFORE" : "AFTER"} ${event}`,
				"narrator_tool_calls",
				(event === "DELETE"
					? ""
					: `UPDATE narrator_tool_calls SET model_revision = ${event === "INSERT" ? "NEW.model_revision" : "OLD.model_revision"} + 1 WHERE id = NEW.id;\n`) +
					holderUpdate(both ? ["OLD.message_id", "NEW.message_id"] : [`${row}.message_id`], owners),
				both ? sqliteChanged(toolColumns) : "",
			);
			trigger(
				`model_history_v1_ref_${event.toLowerCase()}`,
				`AFTER ${event}`,
				"narrator_message_refs",
				`UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (${owners.join(", ")});`,
				both ? sqliteChanged(refColumns) : "",
			);
		}
		trigger(
			"model_history_v1_sender",
			"AFTER UPDATE",
			"users",
			`UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));`,
			sqliteChanged(["username"]),
		);
		trigger(
			"model_history_v1_narrator",
			"AFTER UPDATE",
			"narrators",
			`UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.id OR id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id);`,
			sqliteChanged(["title", "context_summary"]),
		);
		statements.push(
			`INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', '${MODEL_HISTORY_REVISION_CONTRACT_V1}');`,
		);
		return statements.join("\n--> statement-breakpoint\n");
	}
	const statements: string[] = [];
	const functionAndTrigger = (name: string, table: string, body: string, event: string) => {
		statements.push(
			`CREATE FUNCTION ${name}() RETURNS trigger LANGUAGE plpgsql AS $model_history$\nBEGIN\n${body}\nEND;\n$model_history$;`,
		);
		statements.push(
			`CREATE TRIGGER ${name} ${event} ON ${table} FOR EACH ROW EXECUTE FUNCTION ${name}();`,
		);
	};
	functionAndTrigger(
		"model_history_v1_message",
		"narrator_messages",
		`
		IF TG_OP = 'DELETE' THEN
			${holderUpdate(["OLD.id"], ["OLD.narrator_id"])} RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			NEW.body_revision := NEW.body_revision + 1;
			${holderUpdate(["NEW.id"], ["NEW.narrator_id"])} RETURN NEW;
		ELSIF ${pgChanged(messageColumns)} THEN
			NEW.body_revision := OLD.body_revision + 1;
			${holderUpdate(["OLD.id", "NEW.id"], ["OLD.narrator_id", "NEW.narrator_id"])}
		END IF;
		RETURN NEW;`,
		"BEFORE INSERT OR UPDATE OR DELETE",
	);
	functionAndTrigger(
		"model_history_v1_tool",
		"narrator_tool_calls",
		`
		IF TG_OP = 'DELETE' THEN
			${holderUpdate(["OLD.message_id"], ["OLD.narrator_id"])} RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			NEW.model_revision := NEW.model_revision + 1;
			${holderUpdate(["NEW.message_id"], ["NEW.narrator_id"])} RETURN NEW;
		ELSIF ${pgChanged(toolColumns)} THEN
			NEW.model_revision := OLD.model_revision + 1;
			${holderUpdate(["OLD.message_id", "NEW.message_id"], ["OLD.narrator_id", "NEW.narrator_id"])}
		END IF;
		RETURN NEW;`,
		"BEFORE INSERT OR UPDATE OR DELETE",
	);
	functionAndTrigger(
		"model_history_v1_ref",
		"narrator_message_refs",
		`
		IF TG_OP = 'DELETE' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = OLD.narrator_id; RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.narrator_id;
		ELSIF ${pgChanged(refColumns)} THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
		END IF;
		RETURN NEW;`,
		"AFTER INSERT OR UPDATE OR DELETE",
	);
	functionAndTrigger(
		"model_history_v1_sender",
		"users",
		`
		IF ${pgChanged(["username"])} THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
		END IF;
		RETURN NEW;`,
		"AFTER UPDATE",
	);
	// BEFORE UPDATE avoids updating this same narrator row from its AFTER trigger in PG.
	functionAndTrigger(
		"model_history_v1_narrator",
		"narrators",
		`
		IF ${pgChanged(["title", "context_summary"])} THEN
			NEW.model_history_revision := OLD.model_history_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id <> NEW.id AND id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id);
		END IF;
		RETURN NEW;`,
		"BEFORE UPDATE",
	);
	statements.push(
		`INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', '${MODEL_HISTORY_REVISION_CONTRACT_V1}');`,
	);
	return statements.join("\n--> statement-breakpoint\n");
}

// === v2: block storage (docs/BLOCK_STORAGE-DESIGN.md §4.5) ===

/** Envelope columns whose change alters the model's view of every format. */
const messageIdentityColumnsV2 = [
	"id",
	"narrator_id",
	"sdk_message_uuid",
	"parent_tool_use_id",
	"role",
	"created_by",
	"origin",
	"origin_label",
	"author_narrator_id",
];
/**
 * Envelope body columns. Counted for every format in v2: readers still read `content_json`
 * during P1. The `body_format = 1` gate moves to the contract that switches readers to blocks (P2).
 */
const messageBodyColumnsV2 = ["content_json", "content_text"];
/** Block columns the model reads; `state` and `revision` are not among them. */
const blockColumnsV2 = [
	"payload",
	"model_text",
	"provider_meta",
	"kind",
	"subtype",
	"ordinal",
	"phase",
	"message_id",
];
/** Stored envelope / ledger columns whose change leaves a block-format envelope to re-sync. */
const syncMessageColumns = ["content_json", "content_text", "role", "origin", "body_format"];
const syncToolColumns = ["input_json", "output_json", "status", "execution_attempt"];
const V1_SQLITE_TRIGGERS = [
	...["message", "tool", "ref"].flatMap((kind) =>
		["insert", "update", "delete"].map((event) => `model_history_v1_${kind}_${event}`),
	),
	"model_history_v1_sender",
	"model_history_v1_narrator",
];
const V1_PG_TRIGGERS = [
	["model_history_v1_message", "narrator_messages"],
	["model_history_v1_tool", "narrator_tool_calls"],
	["model_history_v1_ref", "narrator_message_refs"],
	["model_history_v1_sender", "users"],
	["model_history_v1_narrator", "narrators"],
] as const;

/** A block's envelope is native, so the block (not `content_json`) is what the model reads. */
function nativeEnvelope(messageId: string): string {
	return `EXISTS (SELECT 1 FROM narrator_messages WHERE id = ${messageId} AND body_format = 2)`;
}
function envelopeOwner(messageId: string): string {
	return `(SELECT narrator_id FROM narrator_messages WHERE id = ${messageId})`;
}

/**
 * The block-storage contracts. Self-contained: each drops whatever an older contract left
 * (absent on a fresh database) and upserts the contract row, so the same text installs and
 * reinstalls.
 *
 * Block rows bump their envelope only while it is native (`body_format = 2`) and only when
 * the model's view changes: an `open` block (streaming) is invisible, `state` never counts.
 * The envelope's body columns still count for every format (a missed block sync must never
 * leave a cached history stale; a double bump is only a counter), and flipping the format
 * alone (materialize, backfill) changes no projected byte and bumps nothing.
 *
 * v3 differs in one thing: a write that moves `state_revision` is a state-only write
 * (patchState), which keeps the block rows in sync itself and changes only reader-only
 * fields of `content_json`. Its body-column change neither bumps nor queues a sync.
 */
function renderBlockContract(dialect: "sqlite" | "postgres", version: 2 | 3): string {
	const contract = `INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', '${version === 2 ? MODEL_HISTORY_REVISION_CONTRACT_V2 : MODEL_HISTORY_REVISION_CONTRACT}') ON CONFLICT (id) DO UPDATE SET installed_version = excluded.installed_version;`;
	const name = (suffix: string) => `model_history_v${version}_${suffix}`;
	const blockBump = (messageIds: string[]) =>
		`UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (${messageIds.join(", ")});\n${holderUpdate(messageIds, messageIds.map(envelopeOwner))}`;
	if (dialect === "sqlite") {
		const statements = [
			...V1_SQLITE_TRIGGERS,
			...(version === 3 ? MODEL_HISTORY_V2_SQLITE_TRIGGERS : []),
		].map((trigger) => `DROP TRIGGER IF EXISTS ${trigger};`);
		const trigger = (name: string, event: string, table: string, body: string, condition = "") => {
			statements.push(
				`CREATE TRIGGER ${name} ${event} ON ${table}${condition ? ` WHEN ${condition}` : ""} BEGIN\n${body}\nEND;`,
			);
		};
		for (const event of ["INSERT", "UPDATE", "DELETE"] as const) {
			const row = event === "DELETE" ? "OLD" : "NEW";
			const both = event === "UPDATE";
			const messageIds = both ? ["OLD.id", "NEW.id"] : [`${row}.id`];
			const owners = both ? ["OLD.narrator_id", "NEW.narrator_id"] : [`${row}.narrator_id`];
			trigger(
				name(`message_${event.toLowerCase()}`),
				`${event === "DELETE" ? "BEFORE" : "AFTER"} ${event}`,
				"narrator_messages",
				(event === "DELETE"
					? ""
					: `UPDATE narrator_messages SET body_revision = ${event === "INSERT" ? "NEW.body_revision" : "OLD.body_revision"} + 1 WHERE id = NEW.id;\n`) +
					holderUpdate(messageIds, owners),
				both
					? version === 2
						? sqliteChanged([...messageIdentityColumnsV2, ...messageBodyColumnsV2])
						: `(${sqliteChanged(messageIdentityColumnsV2)}) OR ((${sqliteChanged(messageBodyColumnsV2)}) AND NEW.state_revision = OLD.state_revision)`
					: "",
			);
			trigger(
				name(`tool_${event.toLowerCase()}`),
				`${event === "DELETE" ? "BEFORE" : "AFTER"} ${event}`,
				"narrator_tool_calls",
				(event === "DELETE"
					? ""
					: `UPDATE narrator_tool_calls SET model_revision = ${event === "INSERT" ? "NEW.model_revision" : "OLD.model_revision"} + 1 WHERE id = NEW.id;\n`) +
					holderUpdate(both ? ["OLD.message_id", "NEW.message_id"] : [`${row}.message_id`], owners),
				both ? sqliteChanged(toolColumns) : "",
			);
			trigger(
				name(`ref_${event.toLowerCase()}`),
				`AFTER ${event}`,
				"narrator_message_refs",
				`UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (${owners.join(", ")});`,
				both ? sqliteChanged(refColumns) : "",
			);
			trigger(
				name(`block_${event.toLowerCase()}`),
				`${event === "DELETE" ? "BEFORE" : "AFTER"} ${event === "UPDATE" ? `UPDATE OF ${blockColumnsV2.join(", ")}` : event}`,
				"narrator_blocks",
				blockBump(both ? ["OLD.message_id", "NEW.message_id"] : [`${row}.message_id`]),
				both
					? `(${sqliteChanged(blockColumnsV2)}) AND NOT (OLD.phase = 'open' AND NEW.phase = 'open') AND (${nativeEnvelope("OLD.message_id")} OR ${nativeEnvelope("NEW.message_id")})`
					: `${row}.phase <> 'open' AND ${nativeEnvelope(`${row}.message_id`)}`,
			);
		}
		// A tool block's phase follows its newest ledger attempt: ledger status is written by
		// many modules (cleanup, recovery, permission), never all through the store.
		const newestAttempt = `NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id)`;
		const settledPhase = `CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END`;
		trigger(
			name("tool_phase_insert"),
			"AFTER INSERT",
			"narrator_tool_calls",
			"UPDATE narrator_blocks SET phase = 'open' WHERE id = NEW.block_id AND phase <> 'open';",
			newestAttempt,
		);
		trigger(
			name("tool_phase_update"),
			"AFTER UPDATE OF status, output_json",
			"narrator_tool_calls",
			`UPDATE narrator_blocks SET phase = ${settledPhase} WHERE id = NEW.block_id AND phase <> ${settledPhase};`,
			newestAttempt,
		);
		// Block-format envelopes whose stored body or ledger changed are queued for the block
		// sync (narrator-block-sync.ts). Not revision bumps: they only enqueue.
		const enqueue = (messageId: string) =>
			`INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (${messageId});`;
		trigger(
			name("sync_message_insert"),
			"AFTER INSERT",
			"narrator_messages",
			enqueue("NEW.id"),
			"NEW.body_format = 2",
		);
		trigger(
			name("sync_message_update"),
			`AFTER UPDATE OF ${syncMessageColumns.join(", ")}`,
			"narrator_messages",
			enqueue("NEW.id"),
			// A state-only write keeps the rows in sync itself (patchState).
			version === 2
				? "NEW.body_format = 2"
				: "NEW.body_format = 2 AND NEW.state_revision = OLD.state_revision",
		);
		trigger(
			name("sync_tool_insert"),
			"AFTER INSERT",
			"narrator_tool_calls",
			enqueue("NEW.message_id"),
			"(SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2",
		);
		trigger(
			name("sync_tool_update"),
			`AFTER UPDATE OF ${syncToolColumns.join(", ")}`,
			"narrator_tool_calls",
			enqueue("NEW.message_id"),
			"(SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2",
		);
		trigger(
			name("sender"),
			"AFTER UPDATE",
			"users",
			`UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));`,
			sqliteChanged(["username"]),
		);
		trigger(
			name("narrator"),
			"AFTER UPDATE",
			"narrators",
			// A sender label names the author, which a copy keeps while its owner is the holder.
			`UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.id OR id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id OR m.author_narrator_id = NEW.id);`,
			sqliteChanged(["title", "context_summary"]),
		);
		statements.push(contract);
		return statements.join("\n--> statement-breakpoint\n");
	}
	const statements = [
		...V1_PG_TRIGGERS,
		...(version === 3 ? MODEL_HISTORY_V2_PG_TRIGGERS : []),
	].flatMap(([trigger, table]) => [
		`DROP TRIGGER IF EXISTS ${trigger} ON ${table};`,
		`DROP FUNCTION IF EXISTS ${trigger}();`,
	]);
	const functionAndTrigger = (
		name: string,
		table: string,
		body: string,
		event: string,
		declare = "",
	) => {
		statements.push(
			`CREATE FUNCTION ${name}() RETURNS trigger LANGUAGE plpgsql AS $model_history$\n${declare ? `DECLARE\n\t${declare}\n` : ""}BEGIN\n${body}\nEND;\n$model_history$;`,
		);
		statements.push(
			`CREATE TRIGGER ${name} ${event} ON ${table} FOR EACH ROW EXECUTE FUNCTION ${name}();`,
		);
	};
	functionAndTrigger(
		name("message"),
		"narrator_messages",
		`
		IF TG_OP = 'DELETE' THEN
			${holderUpdate(["OLD.id"], ["OLD.narrator_id"])} RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			NEW.body_revision := NEW.body_revision + 1;
			${holderUpdate(["NEW.id"], ["NEW.narrator_id"])} RETURN NEW;
		ELSIF ${
			version === 2
				? pgChanged([...messageIdentityColumnsV2, ...messageBodyColumnsV2])
				: `(${pgChanged(messageIdentityColumnsV2)}) OR ((${pgChanged(messageBodyColumnsV2)}) AND NEW.state_revision IS NOT DISTINCT FROM OLD.state_revision)`
		} THEN
			NEW.body_revision := OLD.body_revision + 1;
			${holderUpdate(["OLD.id", "NEW.id"], ["OLD.narrator_id", "NEW.narrator_id"])}
		END IF;
		RETURN NEW;`,
		"BEFORE INSERT OR UPDATE OR DELETE",
	);
	functionAndTrigger(
		name("tool"),
		"narrator_tool_calls",
		`
		IF TG_OP = 'DELETE' THEN
			${holderUpdate(["OLD.message_id"], ["OLD.narrator_id"])} RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			NEW.model_revision := NEW.model_revision + 1;
			${holderUpdate(["NEW.message_id"], ["NEW.narrator_id"])} RETURN NEW;
		ELSIF ${pgChanged(toolColumns)} THEN
			NEW.model_revision := OLD.model_revision + 1;
			${holderUpdate(["OLD.message_id", "NEW.message_id"], ["OLD.narrator_id", "NEW.narrator_id"])}
		END IF;
		RETURN NEW;`,
		"BEFORE INSERT OR UPDATE OR DELETE",
	);
	functionAndTrigger(
		name("ref"),
		"narrator_message_refs",
		`
		IF TG_OP = 'DELETE' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = OLD.narrator_id; RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.narrator_id;
		ELSIF ${pgChanged(refColumns)} THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
		END IF;
		RETURN NEW;`,
		"AFTER INSERT OR UPDATE OR DELETE",
	);
	// AFTER: the bump writes the parent envelope, never this block row.
	functionAndTrigger(
		name("block"),
		"narrator_blocks",
		`
		IF TG_OP = 'DELETE' THEN
			IF OLD.phase <> 'open' AND ${nativeEnvelope("OLD.message_id")} THEN
				${blockBump(["OLD.message_id"])}
			END IF;
			RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			IF NEW.phase <> 'open' AND ${nativeEnvelope("NEW.message_id")} THEN
				${blockBump(["NEW.message_id"])}
			END IF;
		ELSIF (${pgChanged(blockColumnsV2)}) AND NOT (OLD.phase = 'open' AND NEW.phase = 'open') AND (${nativeEnvelope("OLD.message_id")} OR ${nativeEnvelope("NEW.message_id")}) THEN
			${blockBump(["OLD.message_id", "NEW.message_id"])}
		END IF;
		RETURN NEW;`,
		"AFTER INSERT OR UPDATE OR DELETE",
	);
	// A tool block's phase follows its newest ledger attempt (see the SQLite triggers).
	functionAndTrigger(
		name("tool_phase"),
		"narrator_tool_calls",
		`
		IF NEW.block_id IS NULL OR NEW.is_internal OR NEW.execution_attempt <
			(SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) THEN
			RETURN NEW;
		END IF;
		IF TG_OP = 'INSERT' OR NEW.status NOT IN ('success', 'fail') THEN
			target_phase := 'open';
		ELSE
			target_phase := 'done';
			BEGIN
				IF (NEW.output_json::jsonb ->> '_broken') = 'true' THEN target_phase := 'failed'; END IF;
			EXCEPTION WHEN others THEN
				NULL;
			END;
		END IF;
		UPDATE narrator_blocks SET phase = target_phase WHERE id = NEW.block_id AND phase <> target_phase;
		RETURN NEW;`,
		"AFTER INSERT OR UPDATE OF status, output_json",
		"target_phase text;",
	);
	// Block-format envelopes whose stored body or ledger changed are queued for the block sync.
	functionAndTrigger(
		name("sync_message"),
		"narrator_messages",
		version === 2
			? `
		IF NEW.body_format = 2 THEN
			INSERT INTO narrator_block_sync_queue (message_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
		END IF;
		RETURN NEW;`
			: // A state-only write keeps the rows in sync itself (patchState).
				`
		IF NEW.body_format = 2 THEN
			IF TG_OP = 'INSERT' THEN
				INSERT INTO narrator_block_sync_queue (message_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
			ELSIF NEW.state_revision IS NOT DISTINCT FROM OLD.state_revision THEN
				INSERT INTO narrator_block_sync_queue (message_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
			END IF;
		END IF;
		RETURN NEW;`,
		`AFTER INSERT OR UPDATE OF ${syncMessageColumns.join(", ")}`,
	);
	functionAndTrigger(
		name("sync_tool"),
		"narrator_tool_calls",
		`
		IF ${nativeEnvelope("NEW.message_id")} THEN
			INSERT INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id) ON CONFLICT DO NOTHING;
		END IF;
		RETURN NEW;`,
		`AFTER INSERT OR UPDATE OF ${syncToolColumns.join(", ")}`,
	);
	functionAndTrigger(
		name("sender"),
		"users",
		`
		IF ${pgChanged(["username"])} THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
		END IF;
		RETURN NEW;`,
		"AFTER UPDATE",
	);
	// BEFORE UPDATE avoids updating this same narrator row from its AFTER trigger in PG.
	functionAndTrigger(
		name("narrator"),
		"narrators",
		`
		IF ${pgChanged(["title", "context_summary"])} THEN
			NEW.model_history_revision := OLD.model_history_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id <> NEW.id AND id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id OR m.author_narrator_id = NEW.id);
		END IF;
		RETURN NEW;`,
		"BEFORE UPDATE",
	);
	statements.push(contract);
	return statements.join("\n--> statement-breakpoint\n");
}

/**
 * The block-storage contracts (v2, and v3 for state-only writes). Self-contained: each drops
 * whatever an older contract left (absent on a fresh database) and upserts the contract row,
 * so the same text installs and reinstalls.
 */
export function renderModelHistoryRevisionSql(dialect: "sqlite" | "postgres"): string {
	return renderBlockContract(dialect, 3);
}

/** Frozen: the published v2 migration and its install proof are rendered from it. */
export function renderModelHistoryRevisionSqlV2(dialect: "sqlite" | "postgres"): string {
	return renderBlockContract(dialect, 2);
}

/** Trigger names of the v2 contract, by dialect: what a v3 install drops. */
export const MODEL_HISTORY_V2_SQLITE_TRIGGERS = [
	...["message", "tool", "ref", "block"].flatMap((kind) =>
		["insert", "update", "delete"].map((event) => `model_history_v2_${kind}_${event}`),
	),
	"model_history_v2_tool_phase_insert",
	"model_history_v2_tool_phase_update",
	"model_history_v2_sync_message_insert",
	"model_history_v2_sync_message_update",
	"model_history_v2_sync_tool_insert",
	"model_history_v2_sync_tool_update",
	"model_history_v2_sender",
	"model_history_v2_narrator",
] as const;
export const MODEL_HISTORY_V2_PG_TRIGGERS = [
	["model_history_v2_message", "narrator_messages"],
	["model_history_v2_tool", "narrator_tool_calls"],
	["model_history_v2_ref", "narrator_message_refs"],
	["model_history_v2_block", "narrator_blocks"],
	["model_history_v2_tool_phase", "narrator_tool_calls"],
	["model_history_v2_sync_message", "narrator_messages"],
	["model_history_v2_sync_tool", "narrator_tool_calls"],
	["model_history_v2_sender", "users"],
	["model_history_v2_narrator", "narrators"],
] as const;

/** Trigger names of the installed (v3) contract, by dialect (runtime signature checks). */
export const MODEL_HISTORY_SQLITE_TRIGGERS = MODEL_HISTORY_V2_SQLITE_TRIGGERS.map((name) =>
	name.replace("model_history_v2_", "model_history_v3_"),
);
export const MODEL_HISTORY_PG_TRIGGERS = MODEL_HISTORY_V2_PG_TRIGGERS.map(
	([name, table]) => [name.replace("model_history_v2_", "model_history_v3_"), table] as const,
);
