/** Pure migration input. No runtime DDL and no imports of a live database. */
export const MODEL_HISTORY_REVISION_CONTRACT = "model-history-v1";

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

/** Appended ONLY by the official generator to the fresh, unpublished schema migration. */
export function renderModelHistoryRevisionSql(dialect: "sqlite" | "postgres"): string {
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
			`INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', '${MODEL_HISTORY_REVISION_CONTRACT}');`,
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
		`INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', '${MODEL_HISTORY_REVISION_CONTRACT}');`,
	);
	return statements.join("\n--> statement-breakpoint\n");
}
