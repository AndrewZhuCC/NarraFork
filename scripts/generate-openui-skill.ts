import { narratorOpenUIChatLibrary as openuiChatLibrary } from "../frontend/components/narrator/openui/openui-library";
import { estimateTokens } from "../server/lib/agent/estimate-tokens";

// Build-time only: builtin-routines imports the generated text, never this React library.
const [uiPackage, langPackage] = await Promise.all([
	Bun.file(
		new URL("../../package.json", import.meta.resolve("@openuidev/react-ui/genui-lib")),
	).json(),
	Bun.file(new URL("../package.json", import.meta.resolve("@openuidev/react-lang"))).json(),
]);
const versions = `@openuidev/react-ui@${uiPackage.version} / @openuidev/react-lang@${langPackage.version}`;

const prompt = openuiChatLibrary
	.prompt({
		inlineMode: true,
		bindings: true,
		tools: [],
		toolCalls: false,
		preamble:
			"The following component reference describes NarraFork's in-chat UI. The NarraFork output and execution rules take precedence over generic library examples.",
		additionalRules: [
			"Prefer components for parallel information, comparisons, steps, and data so the content is easier to scan and understand: cards for grouped facts, tables for comparisons, expandable steps for processes, charts for patterns and trends, and forms for collecting input or choices. Use Markdown for brief answers, connective prose, and copyable code.",
			"Wrap every UI program in a triple-backtick openui-lang code block. Each block must be self-contained: root = Card(...) first, then all referenced statements and local data. For changes, emit the complete updated program, not a partial patch; other chat blocks do not supply definitions.",
			"Use only the registered components and declarative language below. Do not generate a webpage, HTML/JSX, executable JavaScript, a frontend application, or user files to display this UI; do not install anything for it.",
			"NarraFork supplies no renderer tool provider: never emit Query(), Mutation(), @Run, MCP calls, network requests, shell commands, file operations, or invented execution actions. Available renderer tools are empty; UI is not an execution permission.",
			"Local $bindings, calculations, @Set, and @Reset change only this UI's local state. They do not persist edits, modify project data, call a tool, or send a message automatically. EditableTable supports editing cells and explicitly sending edited rows to the assistant with Save Changes; this does not itself save a project file or database.",
			'The only outward actions are explicit user clicks to continue this conversation or submit a form: use Action([@ToAssistant("clear message describing the user\'s choice or submission")]) or { type: "continue_conversation", context: "clear message" }. Give buttons an explicit action and an honest label; do not rely on implicit/default actions or submit on render or field changes.',
			"NarraFork's host handles these conversation/form actions through its existing message API and includes the submitted form data. Generate the declarative action only; do not construct, call, or claim to have called an API yourself. A submission asks the assistant to continue; it does not itself execute or save a business operation.",
			"Use the registered form controls with meaningful names and FormControl labels. Explicit submissions can include strings, numbers, booleans, dates, selections, and edited table rows. NarraFork projects the rendered fields for that submission; unrelated hidden bindings and arbitrary action params are not sent.",
			"Never emit @OpenUrl, open_url, redirects, navigation actions, unknown action types, or arbitrary action params. Generic library action/navigation examples in the reference are not permitted by NarraFork. If an action is unsupported, use a clearly labelled conversation action or a Markdown explanation instead.",
			"Data components such as Slice, Series, and Point are element nodes with a props object. Project their fields through props: for slices = [Slice(...), Slice(...)], use slices.props.category and slices.props.value for chart labels and values; slices.category or slices.value yields nulls. Plain object data uses its own actual field names. Never infer plural field aliases.",
			"Use user-provided or verified tool-result data. Do not invent factual metrics, sources, or URLs; label requested sample data as illustrative. Do not claim that local edits or a rendered button have executed or persisted anything.",
		],
		// NarraFork's Markdown/component composition rules replace the library's UI-edit workflow.
	})
	.replace(/^## Inline Mode\n[\s\S]*?(?=^## Important Rules\n)/m, "");

const content = `# nf-openui

Prefer NarraFork chat components for parallel information, comparisons, steps, and data; combine them with Markdown explanations and use forms to collect input.

## Loading and context

- Before generating UI, load this skill with the existing Skill tool using name \`nf-openui\` unless its full rules and component reference are already present in the current context.
- Do not reload while these rules are available. After compaction, if the rules or component reference are missing and UI is needed again, reload \`nf-openui\` or reread its SKILL.md before generating; do not guess component signatures from memory.
- Combine Markdown narration and copyable text or code with component blocks for visualization, structure, and interaction. UI code belongs in \`\`\`openui-lang fences inside the chat reply, with Markdown allowed before and after.

## NarraFork renderer boundary

The library prompt is generated with \`inlineMode: true\`, \`bindings: true\`, \`tools: []\`, and \`toolCalls: false\`. Bindings are local UI interaction only. NarraFork grants the renderer no Query, Mutation, or MCP execution permission. Only explicit conversation continuations and form submissions reach the host's existing message API; the additional NarraFork rules below govern all actions.

## Installed component reference

Generated from ${versions}; ${Object.keys(openuiChatLibrary.components).length} registered components. This fixed reference is loaded only with the skill, not injected into every ordinary conversation.

${prompt}
`;

// Escape only TypeScript template-literal syntax; keep the generated prompt readable.
const literal = content.replaceAll("\\", "\\\\").replaceAll("`", "\\`").replaceAll("${", "\\${");
const source = `// Generated by scripts/generate-openui-skill.ts. Do not edit manually.
// ${versions}
export const OPENUI_SKILL_CONTENT = \`${literal}\`;
`;
await Bun.write(new URL("../server/generated/openui-skill.ts", import.meta.url), source);
console.log(
	JSON.stringify({
		versions,
		components: Object.keys(openuiChatLibrary.components).length,
		characters: content.length,
		bytes: Buffer.byteLength(content, "utf8"),
		estimatedTokens: estimateTokens(content),
		tokenEstimateNote: "NarraFork character heuristic, not model billing tokens",
	}),
);
