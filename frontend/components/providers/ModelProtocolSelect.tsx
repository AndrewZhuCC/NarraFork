/**
 * 逐模型协议选择器。
 *
 * 协议是模型级的全局覆盖（`settings.agent.modelProtocols`，键为带前缀的完整模型 ID），
 * 与 catalog 的模型资料无关。设置页的模型行与「编辑此连接覆盖」对话框共用这一个组件，
 * 以保证两处选项、文案与「跟随默认」的语义完全一致。
 */

import { type MantineSize, Select } from "@mantine/core";
import { useTranslation } from "react-i18next";
import type { CustomApiProtocol } from "./types";

/** 可选的协议；数组顺序即下拉顺序。 */
export const MODEL_PROTOCOL_OPTIONS = [
	"anthropic-messages",
	"openai-responses",
	"completions-compatible",
	"gemini-compatible",
] as const satisfies readonly CustomApiProtocol[];

const PROTOCOL_LABEL_KEYS: Record<CustomApiProtocol, string> = {
	"anthropic-messages": "customApiProtocolAnthropicMessages",
	"openai-responses": "customApiProtocolOpenAIResponses",
	"completions-compatible": "customApiProtocolCompletionsCompatible",
	"gemini-compatible": "customApiProtocolGeminiCompatible",
};

/**
 * 选项数据（含「跟随默认」）。下拉与无浮层控件（SegmentedControl）共用同一份，
 * 保证两处的取值集合和文案永远一致。
 */
export function modelProtocolOptionsData(
	t: (key: string) => string,
): Array<{ value: string; label: string }> {
	return [
		{ value: "inherit", label: t("modelProtocolInherit") },
		...MODEL_PROTOCOL_OPTIONS.map((protocol) => ({
			value: protocol,
			label: t(PROTOCOL_LABEL_KEYS[protocol]),
		})),
	];
}

/** 单个协议的显示文案（「当前有效值」那类摘要行要用）。 */
export function modelProtocolLabel(
	t: (key: string) => string,
	protocol: CustomApiProtocol,
): string {
	return t(PROTOCOL_LABEL_KEYS[protocol]);
}

export function ModelProtocolSelect({
	value,
	onChange,
	size = "xs",
	width,
	disabled,
	ariaLabel,
	dropdownZIndex,
}: {
	/** 当前协议；`null` 表示「跟随默认」，即 modelProtocols 里没有这个键。 */
	value: CustomApiProtocol | null;
	onChange: (protocol: CustomApiProtocol | null) => void;
	size?: MantineSize;
	width?: number | string;
	disabled?: boolean;
	ariaLabel?: string;
	/** 下拉层 zIndex；嵌在被显式抬高的 Modal 里时必须高于该 Modal，否则下拉会被遮住。 */
	dropdownZIndex?: number;
}) {
	const { t } = useTranslation("settings");
	return (
		<Select
			size={size}
			w={width}
			disabled={disabled}
			allowDeselect={false}
			placeholder={t("modelProtocolInherit")}
			aria-label={ariaLabel ?? t("modelProtocol")}
			comboboxProps={{ withinPortal: true, zIndex: dropdownZIndex }}
			value={value ?? "inherit"}
			data={modelProtocolOptionsData(t)}
			onChange={(next) => onChange(next && next !== "inherit" ? (next as CustomApiProtocol) : null)}
		/>
	);
}
