import "@mantine/core/styles.css";
import { MantineProvider } from "@mantine/core";
import i18next from "i18next";
import { createRef } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import enCommon from "../../../locales/en/common.json";
import enNarrator from "../../../locales/en/narrator.json";
import zhCommon from "../../../locales/zh-CN/common.json";
import zhNarrator from "../../../locales/zh-CN/narrator.json";
import { NarratorComposerRow, type NarratorComposerRowProps } from "./NarratorComposerRow";

const root = createRoot(document.getElementById("root") as HTMLElement);
const instance = i18next.createInstance();
await instance.use(initReactI18next).init({
	lng: "en",
	resources: {
		en: { narrator: enNarrator, common: enCommon },
		"zh-CN": { narrator: zhNarrator, common: zhCommon },
	},
	react: { useSuspense: false },
});
let generation = 0;
let action: "send" | "retry" | "continue" | "queue" = "send";
let hasInput = false;
const calls = { send: 0, retry: 0, continue: 0 };
const noop = () => {};
const props: NarratorComposerRowProps = {
	fileInputRef: createRef(),
	composerRef: createRef(),
	sendingRef: { current: false },
	appendInputRef: undefined,
	narratorId: "fixture",
	isActive: false,
	composerHasText: false,
	composerHasAttachments: false,
	setComposerHasText: (value) => {
		hasInput = value;
		render();
	},
	effectiveFocusIndex: null,
	enterQueueMode: "turn",
	ctrlEnterQueueMode: "tool",
	onFileInputChange: noop,
	onComposerPasteImages: noop,
	onSendWithMode: noop,
	onSend: () => {
		calls.send++;
	},
	onRetry: () => {
		calls.retry++;
	},
	onContinue: () => {
		calls.continue++;
	},
	onTakeover: noop,
	onStopTakeover: noop,
	queuedMessagesCount: 0,
	showCompactQueueChoice: false,
	canCutInLine: false,
	hasCutInMessage: false,
	canTakeover: false,
	isTakenOver: false,
	canRetryLastUserMessage: false,
	canContinueNarrator: false,
	retryRecoveryAllowsInterrupt: true,
	isSending: false,
	takeoverMutationPending: false,
	stopTakeoverMutationPending: false,
	interruptMutationPending: false,
	interruptProgress: 0,
	queueHoldProgress: 0,
	startInterruptPress: noop,
	handleInterruptMouseUp: noop,
	clearInterruptTimer: noop,
	startQueueHold: noop,
	handleQueuePointerUp: noop,
	cancelQueueHold: noop,
	handleQueueClick: noop,
	onUpdateEnterQueueMode: noop,
	onUpdateCtrlEnterQueueMode: noop,
};
function render() {
	root.render(
		<I18nextProvider i18n={instance}>
			<MantineProvider>
				<div style={{ height: "100dvh", display: "flex", flexDirection: "column" }}>
					<div id="messages" style={{ flex: 1, minHeight: 0 }}>
						Visible message viewport
					</div>
					<div id="composer">
						<NarratorComposerRow
							key={generation}
							{...props}
							composerHasText={hasInput}
							isActive={action === "queue"}
							canRetryLastUserMessage={action === "retry"}
							canContinueNarrator={action === "continue"}
						/>
					</div>
				</div>
			</MantineProvider>
		</I18nextProvider>,
	);
}
export async function start(language: string) {
	await instance.changeLanguage(language);
	generation++;
	action = "send";
	hasInput = false;
	calls.send = 0;
	calls.retry = 0;
	calls.continue = 0;
	render();
}
export function setAction(value: typeof action) {
	action = value;
	render();
}
export function snapshot() {
	const input = document.querySelector<HTMLTextAreaElement>("textarea");
	const label = document.querySelector<HTMLElement>("[data-stable-action-label]");
	const button = label?.closest("button");
	const viewport = document.getElementById("messages")?.getBoundingClientRect();
	const inputRect = input?.getBoundingClientRect();
	const labelRect = label?.getBoundingClientRect();
	return {
		ready: !!inputRect,
		buttonWidth: button?.getBoundingClientRect().width,
		buttonText: button?.textContent,
		disabled: button?.disabled,
		inputWidth: inputRect?.width,
		inputHeight: inputRect?.height,
		viewportHeight: viewport?.height,
		labelWidth: labelRect?.width,
		actualWidth: label?.lastElementChild?.getBoundingClientRect().width,
		calls: { ...calls },
		value: input?.value,
		sizers: [...(label?.querySelectorAll<HTMLElement>("[data-label]") ?? [])].map((node) => ({
			hidden: node.getAttribute("aria-hidden"),
			visibility: getComputedStyle(node).visibility,
			width: node.getBoundingClientRect().width,
			text: node.textContent,
		})),
	};
}
await start("en");
