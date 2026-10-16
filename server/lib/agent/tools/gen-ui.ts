import { GEN_UI_DIRECTORY } from "@shared/gen-ui-files";
import { UI_MARKUP_LIMITS } from "@shared/gen-ui-markup";
import { GEN_UI_ACTIONS, GEN_UI_TOOL_NAME, isGenUiReadAction } from "@shared/gen-ui-tool-policy";
import { z } from "zod/v4";
import { targetPathSemantics } from "../execution/path-semantics";
import type { ToolDefinition } from "../types";

// Field order is the order the model emits them, and `source` streams into the chat
// while it is still being written: everything that sizes or labels the card must be
// declared (and therefore arrive) before it.
export const genUiParameters = z
	.object({
		action: z.enum(GEN_UI_ACTIONS),
		prefix: z
			.string()
			.regex(/^[\p{L}\p{N}][\p{L}\p{N}_-]{0,47}$/u)
			.optional(),
		path: z.string().min(1).max(4096).optional(),
		title: z.string().min(1).max(80).optional(),
		size: z.enum(["sm", "md", "lg"]).optional(),
		// Retired: still parsed so a call already in flight is answered, never honoured.
		skip_build: z.boolean().optional(),
		source: z.string().min(1).max(UI_MARKUP_LIMITS.sourceChars).optional(),
	})
	.strict()
	.superRefine((input, ctx) => {
		const given = (key: keyof typeof input) => input[key] !== undefined;
		const only = (...allowed: Array<keyof typeof input>) =>
			(["prefix", "path", "title", "size", "skip_build", "source"] as const).every(
				(key) => allowed.includes(key) || !given(key),
			);
		const valid =
			input.action === "create"
				? given("prefix") && only("prefix")
				: input.action === "show"
					? given("path") && only("path")
					: input.action === "render"
						? given("source") && only("source", "title", "size")
						: input.action === "kit_build"
							? only("skip_build")
							: only();
		if (!valid)
			ctx.addIssue({
				code: "custom",
				message:
					"create takes only prefix; show takes only path; render takes source (+ title, size); kit_build and kit_status take nothing",
			});
	});
export type GenUiInput =
	| { action: "create"; prefix: string }
	| { action: "show"; path: string }
	| { action: "render"; source: string; title?: string; size?: "sm" | "md" | "lg" }
	| { action: "kit_status" }
	| { action: "kit_build"; skip_build?: boolean };

export const genUiTool: ToolDefinition = {
	name: GEN_UI_TOOL_NAME,
	description: `Show the user UI in the conversation. Two ways:

render({title, size, source}) — the default. Draws markup inline in the chat while you write it: no build, no click. Use it for cards, tables, comparisons, forms, tabs, accordions.
- Components: every export of @mantine/core (Stack, Group, Card, Text, Title, Badge, Button, Table, Tabs, Accordion, TextInput, Textarea, NumberInput, Select, Checkbox, Switch, Radio, Slider, SegmentedControl, Progress, Alert…), dotted members such as <Tabs.List>, and plain HTML (div, p, ul, li, table, img, a…). With a project component kit its components replace Mantine; kit_status lists them. A misspelt tag is drawn as a placeholder and the result names the closest real one.
- source is JSX-like DATA, not code. Attributes are string/number/boolean literals, {[…]} / {{…}} literals, or JSX ({<Icon />}). No variables, expressions, map(), hooks, imports or onChange: write repeated items out.
- Controls keep their own state. Use defaultValue / defaultChecked; a literal value= or checked= freezes the control.
- The only handlers: onClick={sendPrompt("text")} sends that fixed text as the user's next message; onClick={openLink("https://…")} opens a link after the user confirms. What the user types or picks in a card is NOT sent to you. To collect a choice, give one button per option, each with its own sendPrompt text.
- Put title and size before source: "sm" (240px), "md" (400px, default) or "lg" (580px).
Example: <Stack gap="sm"><Title order={4}>Plans</Title><Tabs defaultValue="a"><Tabs.List><Tabs.Tab value="a">Basic</Tabs.Tab><Tabs.Tab value="b">Pro</Tabs.Tab></Tabs.List><Tabs.Panel value="a"><Text>$0</Text></Tabs.Panel><Tabs.Panel value="b"><Text>$9</Text></Tabs.Panel></Tabs><Group><Button onClick={sendPrompt("I'll take Basic")}>Basic</Button><Button onClick={sendPrompt("I'll take Pro")}>Pro</Button></Group></Stack>

create({prefix}) then show({path}) — a full React app, only when markup cannot do it (controls that drive each other, computed values, live updates). create writes ${GEN_UI_DIRECTORY}/<prefix>-<id>/App.tsx. Edit it with the normal file tools: default-export a component; import react, @mantine/core, @mantine/hooks and relative files, or "kit" when the project has a kit; no other packages and no host API. Then show the directory. The user sees a card with an "Open preview" button: the app is built and runs only when they click it, and nothing in it reaches you.

kit_status() — optional: the project kit's components with props and examples, or how to author one. kit_build() — builds the project kit from ${GEN_UI_DIRECTORY}/kit/kit.json with the project's own Vite once the user confirms in the chat. The confirmation is requested for you; never ask for it repeatedly. A bundle you build yourself is never used.

Never put secrets in UI.`,
	parameters: genUiParameters,
	executionRouting: {
		kind: "single",
		resolve(input, config) {
			const workspace = config.workspaceContext;
			const cwd = workspace?.cwd ?? config.cwd;
			const paths = targetPathSemantics(workspace?.pathFlavor ?? "posix");
			return {
				key: "primary",
				operation: isGenUiReadAction(input.action) ? "read" : "write",
				deviceId: workspace?.deviceId ?? config.defaultDeviceId ?? "local",
				workdir: cwd,
				path:
					input.action === "show" && typeof input.path === "string"
						? input.path
						: paths.resolve(workspace?.git?.rootPath ?? cwd, GEN_UI_DIRECTORY),
			};
		},
	},
	formatValidationError: () =>
		"Use {action:'render',title,size,source}, {action:'kit_status'}, {action:'kit_build'}, {action:'create',prefix:'safe-name'} or {action:'show',path:'directory-or-App.tsx'}.",
	async execute(args, ctx) {
		try {
			const { executeGenUiTool } = await import("@server/services/gen-ui/runtime");
			return await executeGenUiTool(genUiParameters.parse(args) as GenUiInput, ctx);
		} catch (error) {
			return {
				output: error instanceof Error ? error.message.slice(0, 600) : "UI request failed",
				isError: true,
			};
		}
	},
};
