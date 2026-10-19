import { describe, expect, test } from "bun:test";
import {
	compileDelegationPolicy,
	isRuntimeToolAllowed,
	resolveRuntimePolicy,
} from "@server/services/agent-runtime/policy";
import {
	projectAskUserQuestionInput,
	projectAskUserQuestionMessagesForModel,
} from "../../question-model-input";
import {
	askUserQuestionTool,
	isAsyncAskRequest,
	isWithdrawOnlyAskRequest,
	readWithdrawIds,
} from "../ask-user-question";
import { questionTool } from "../question";

describe("Question lifecycle capability", () => {
	test("ids, cursors, page size and notes have hard character bounds before SQLite", () => {
		for (const value of [
			{ id: "" },
			{ id: "x".repeat(201) },
			{ ids: [""] },
			{ answerMessageId: "x".repeat(201) },
			{ cursor: "x".repeat(2049) },
			{ limit: 33 },
			{ note: "x".repeat(2049) },
			{ reason: "x".repeat(2049) },
		]) {
			expect(questionTool.parameters.safeParse({ action: "list", ...value }).success).toBe(false);
		}
	});
	test("get/list/resolve/withdraw form one tool without granting creation to subagents", () => {
		for (const action of ["get", "list", "resolve", "withdraw"])
			expect(questionTool.parameters.safeParse({ action }).success).toBe(true);
		for (const subagentType of ["general", "explore", "plan", "review", "search"]) {
			const policy = resolveRuntimePolicy([compileDelegationPolicy({ template: subagentType })]);
			expect(isRuntimeToolAllowed(policy, "Question")).toBe(true);
			expect(isRuntimeToolAllowed(policy, "AskUserQuestion")).toBe(false);
		}
		expect(questionTool.parameters.safeParse({ action: "create" }).success).toBe(false);
	});
});

describe("AskUserQuestion", () => {
	test("does not advertise permission-collected replies as model arguments", () => {
		const schema = askUserQuestionTool.rawJsonSchema as { properties: Record<string, unknown> };
		expect(schema.properties).not.toHaveProperty("answers");
		expect(schema.properties).not.toHaveProperty("annotations");
	});
	test("advertises only header/description on questions and options", () => {
		const schema = askUserQuestionTool.rawJsonSchema as {
			properties?: {
				questions?: {
					items?: {
						required?: string[];
						properties?: Record<string, unknown>;
						additionalProperties?: boolean;
					};
				};
			};
		};
		const items = schema.properties?.questions?.items;
		expect(items?.required ?? []).toEqual(["header", "options"]);
		expect(Object.keys(items?.properties ?? {}).sort()).toEqual([
			"description",
			"header",
			"multiSelect",
			"options",
		]);
		const questionProps = items?.properties as Record<string, { description?: string }>;
		expect(questionProps.header?.description).toContain("SHORT title");
		expect(questionProps.description?.description).toContain("FULL question text");
		expect(items?.additionalProperties).toBe(false);
		const optionProps = (
			items?.properties as {
				options?: { items?: { properties?: Record<string, unknown>; required?: string[] } };
			}
		)?.options?.items;
		expect(optionProps?.required ?? []).toEqual(["header"]);
		expect(Object.keys(optionProps?.properties ?? {}).sort()).toEqual([
			"description",
			"header",
			"preview",
		]);
	});

	test("accepts short header + full description and option headers", () => {
		expect(
			askUserQuestionTool.parameters.safeParse({
				questions: [
					{
						header: "写路径权限策略",
						description: "背景：内网已全放行；云端 key 逐点授予。写路径权限模型选哪个？",
						options: [{ header: "挂权限点（推荐）", description: "推荐" }, { header: "维持现状" }],
						multiSelect: false,
					},
				],
			}).success,
		).toBe(true);
	});

	test("rejects questions without a header", () => {
		expect(
			askUserQuestionTool.parameters.safeParse({
				questions: [
					{
						description: "只有描述没有正文",
						options: [{ header: "A" }, { header: "B" }],
					},
				],
			}).success,
		).toBe(false);
	});

	test("keeps multiSelect optional", () => {
		expect(
			askUserQuestionTool.parameters.safeParse({
				questions: [
					{
						header: "测试策略",
						options: [{ header: "坚持全修" }, { header: "另立 spec" }],
					},
				],
			}).success,
		).toBe(true);
	});
});

/**
 * These three predicates are shared with the permission gate, which uses them to
 * decide whether a call needs a prompt at all. A disagreement between the two sides
 * would show up as the gate rejecting a call the tool handles fine (or worse, blocking
 * on a question that was meant not to block), so they are pinned here.
 */
describe("AskUserQuestion — async mode predicates", () => {
	test("async is opt-in and strictly boolean true", () => {
		expect(isAsyncAskRequest({ questions: [], async: true })).toBe(true);
		expect(isAsyncAskRequest({ questions: [] })).toBe(false);
		expect(isAsyncAskRequest({ questions: [], async: false })).toBe(false);
		// A truthy non-boolean must not silently enable a non-blocking ask.
		expect(isAsyncAskRequest({ questions: [], async: "yes" })).toBe(false);
		expect(isAsyncAskRequest(null)).toBe(false);
	});

	test("withdraw ids ignore malformed entries", () => {
		expect(readWithdrawIds({ withdraw: ["a", "", "  ", 7, null, "b"] })).toEqual(["a", "b"]);
		expect(readWithdrawIds({ withdraw: "a" })).toEqual([]);
		expect(readWithdrawIds({})).toEqual([]);
	});

	test("a withdraw-only call is recognised, and asking alongside it is not", () => {
		expect(isWithdrawOnlyAskRequest({ withdraw: ["q1"] })).toBe(true);
		expect(isWithdrawOnlyAskRequest({ questions: [], withdraw: ["q1"] })).toBe(true);
		// Still asking something → the questions decide how the call is treated.
		expect(
			isWithdrawOnlyAskRequest({
				questions: [{ header: "h", options: [] }],
				withdraw: ["q1"],
			}),
		).toBe(false);
		expect(isWithdrawOnlyAskRequest({ withdraw: [] })).toBe(false);
	});

	test("validation accepts a withdraw-only call but rejects an empty one", () => {
		// A maintenance call carries no questions — the gate validates input with this
		// same schema, so rejecting it here would make withdrawals unreachable.
		expect(askUserQuestionTool.parameters.safeParse({ withdraw: ["q1"] }).success).toBe(true);
		// Neither asking nor withdrawing: nothing for this call to do.
		expect(askUserQuestionTool.parameters.safeParse({}).success).toBe(false);
		expect(askUserQuestionTool.parameters.safeParse({ questions: [], withdraw: [] }).success).toBe(
			false,
		);
	});

	test("async and withdraw are advertised to the model", () => {
		const schema = askUserQuestionTool.rawJsonSchema as {
			properties?: Record<string, { type?: string }>;
		};
		expect(schema.properties?.async?.type).toBe("boolean");
		expect(schema.properties?.withdraw?.type).toBe("array");
	});

	test("the description tells the model header is short and description holds the prompt", () => {
		expect(askUserQuestionTool.description).toContain("ONLY these two field names");
		expect(askUserQuestionTool.description).toContain("SHORT title");
		expect(askUserQuestionTool.description).toContain("FULL prompt");
		expect(askUserQuestionTool.description).toContain("Do not cram the long prompt into `header`");
	});
});

describe("AskUserQuestion model input provenance", () => {
	test("removes only top-level response fields without changing the source or nested authored data", () => {
		const authored = {
			questions: [{ header: "Direction", options: [{ header: "A", preview: "answers are data" }] }],
			async: false,
			context: "context",
			metadata: { answers: "authored nested data" },
		};
		const input = Object.freeze({
			...authored,
			answers: { Direction: "HUMAN_REPLY_CANARY" },
			annotations: { Direction: { notes: "human note" } },
			deferredByUser: true,
		});
		expect(projectAskUserQuestionInput(input) as unknown).toEqual(authored);
		expect(input.answers.Direction).toBe("HUMAN_REPLY_CANARY");
		expect(projectAskUserQuestionInput(authored)).toBe(authored);
	});

	test("projects legacy JSON strings once and bounds oversized parsing", () => {
		const input = JSON.stringify({
			questions: [{ header: "Legacy", options: [] }],
			answers: { Legacy: "HUMAN_REPLY_CANARY" },
		});
		expect(JSON.parse(projectAskUserQuestionInput(input))).toEqual({
			questions: [{ header: "Legacy", options: [] }],
		});
		const literal = JSON.stringify(input);
		expect(projectAskUserQuestionInput(literal)).toBe(literal);
		expect(projectAskUserQuestionInput("not JSON")).toBe("not JSON");
		expect(projectAskUserQuestionInput(" ".repeat(1024 * 1024 + 1))).toBe("{}");
	});

	test("original SDK questions win over answered or deferred audit input while results and other tools stay intact", () => {
		const authored = { questions: [{ header: "Direction", options: [] }], async: false };
		const effective = {
			...authored,
			async: true,
			answers: { Direction: "HUMAN_REPLY_CANARY" },
			deferredByUser: true,
		};
		const readInput = { answers: "ordinary tool data", file_path: "example.txt" };
		const source = [
			{
				contentJson: [
					{ type: "tool_use", id: "ask", name: "AskUserQuestion", input: authored },
					{ type: "tool_use", id: "read", name: "Read", input: readInput },
					{ type: "tool_result", tool_use_id: "ask", content: "User answered: HUMAN_REPLY_CANARY" },
				],
				toolCalls: [
					{
						toolUseId: "ask",
						toolName: "AskUserQuestion",
						inputJson: effective,
						outputJson: "User answered: HUMAN_REPLY_CANARY",
					},
					{ toolUseId: "read", toolName: "Read", inputJson: readInput, outputJson: "unchanged" },
				],
			},
		];
		const snapshot = structuredClone(source);
		const projected = projectAskUserQuestionMessagesForModel(source);
		expect(projected[0].toolCalls[0].inputJson as unknown).toEqual(authored);
		expect(projected[0].toolCalls[0].outputJson).toBe("User answered: HUMAN_REPLY_CANARY");
		expect(projected[0].toolCalls[1]).toBe(source[0].toolCalls[1]);
		expect(projected[0].contentJson[1]).toBe(source[0].contentJson[1]);
		expect(projected[0].contentJson[2]).toBe(source[0].contentJson[2]);
		expect(source).toEqual(snapshot);
	});

	test("supports block-only and row-only legacy inputs without conflating nameless other tools", () => {
		const input = {
			questions: [{ header: "Legacy", options: [] }],
			answers: { Legacy: "HUMAN_REPLY_CANARY" },
		};
		const block = { type: "tool_use", id: "ask", name: "AskUserQuestion", input };
		const projected = projectAskUserQuestionMessagesForModel([
			{ contentJson: [block] },
			{ toolCalls: [{ toolName: "AskUserQuestion", inputJson: JSON.stringify(input) }] },
		]);
		expect(projected[0].contentJson?.[0].input as unknown).toEqual({ questions: input.questions });
		expect(projected[1].toolCalls?.[0].inputJson as unknown).toEqual({
			questions: input.questions,
		});
		const other = { type: "tool_use", name: "Read", input: { answers: "keep" } };
		const unrelated = {
			contentJson: [other],
			toolCalls: [{ toolName: "AskUserQuestion", inputJson: input }],
		};
		expect(projectAskUserQuestionMessagesForModel([unrelated])[0].contentJson[0]).toBe(other);
		expect(block.input).toBe(input);
	});
});
