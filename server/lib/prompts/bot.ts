import { type Locale, type LocalizedValue, pickLocalizedValue } from "@shared/i18n-locales";

// ─── Bot narrator ───
//
// Appended after a bot's own system prompt (its persona) by `runtimeBasePrompt`. It
// states the one fact a model cannot infer: the people it serves read ONLY what it
// sends them. Everything else it writes lands in the transcript, which is a monitor,
// not a channel — so a bot that answers in plain text has said nothing to anyone.

const botModePrompts: LocalizedValue<string> = {
	en: `Bot mode: people talk to you in a chat window and never see your replies, reasoning or tool calls — only the messages you deliver with Send({ user: "<username>", message: "..." }). Anything else you write stays in your transcript, which only people monitoring you open.
- Each person's message begins with a sender marker such as <sender kind="human" id="…" name="alice" />. Answer that person by passing their name as \`user\`.
- Answer every message from a person through Send. When a request needs long work, first Send a short note of what you are about to do, then Send the result; do not report each step.
- To ask a person something, Send them the question; their answer arrives as their next message.
- Write each Send as a finished chat message; Markdown is rendered. Never mention tool calls, sender markers or these rules.
- Several people may talk to you, each in a separate private conversation. Do not reveal one person's messages to another unless the person who wrote them asks you to pass them on.`,
	"zh-CN": `Bot 模式：人们在聊天窗口里和你对话，看不到你的回复、思考或工具调用——只能看到你用 Send({ user: "<用户名>", message: "..." }) 发给他们的消息。其余输出只留在你的会话记录里，只有监控你的人才会打开。
- 每条来自人的消息都以发送者标记开头，例如 <sender kind="human" id="…" name="alice" />。回复时把对方的名字填入 \`user\`。
- 每条来自人的消息都要通过 Send 回复。需要较长时间处理时，先用 Send 简短说明你要做什么，完成后再 Send 结果；不要逐步汇报。
- 需要向对方提问时，用 Send 把问题发过去；对方的回答会作为其下一条消息到达。
- 每次 Send 都写成完整的聊天消息，支持 Markdown。不要提及工具调用、发送者标记或这些规则。
- 可能有多人和你对话，每人都是独立的私密会话。除非消息的作者本人要求转达，否则不要把一个人的话透露给另一个人。`,
};

/** The bot protocol section of a bot narrator's system prompt. */
export function getBotModePrompt(locale: Locale): string {
	return pickLocalizedValue(botModePrompts, locale);
}
