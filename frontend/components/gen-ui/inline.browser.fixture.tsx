import "@mantine/core/styles.css";
import { RenderLodCtx } from "@frontend/components/narrator/lod/RenderLodCtx";
import { setNarratorShortcut } from "@frontend/hooks/useNarratorShortcuts";
import { useFocusComposerShortcut } from "@frontend/hooks/useReadFromStartShortcut";
import { setToken } from "@frontend/lib/api/client";
import { mantineTheme } from "@frontend/lib/mantine-theme";
import { MantineProvider, v8CssVariablesResolver } from "@mantine/core";
import type { GenUiKitNotice } from "@shared/gen-ui-kit";
import { createInstance } from "i18next";
import { useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { GenUICard } from "./GenUICard";

const i18n = createInstance();
await i18n.init({
	lng: "en",
	// As in the app: React escapes on render, so interpolated values are passed through.
	interpolation: { escapeValue: false },
	resources: {
		en: {
			narrator: {
				genUi: {
					title: "UI",
					loading: "Loading runtime",
					loadFailed: "Load failed",
					renderFailed: "Render failed",
					staticPreview: "Shared preview unavailable",
					streaming: "Generating {{title}}",
					expand: "Expand",
					sendFailed: "Send failed",
					linkConfirm: "Open {{url}}?",
					linkOpen: "Open",
					promptConfirm: "Send this to the chat? {{text}}",
					promptSend: "Send",
					cancel: "Cancel",
					formWaiting: "Waiting for your answer: {{title}}",
					formClose: "Close form",
					kit: {
						title: "Component kit",
						buildPrompt: "Build a component kit?",
						updatePrompt: "Update the component kit?",
						confirm: "Confirm",
						dismiss: "Not now",
						approved: "Confirmed",
						failed: "Request failed",
						state: { builtin: "Built-in", unbuilt: "Unbuilt", stale: "Stale", ready: "Ready" },
					},
				},
			},
		},
	},
});
setToken("fixture-private-token");

/** The props a `render` tool row passes down, driven by the test like a stream would. */
function Fixture() {
	const composerRef = useRef<HTMLTextAreaElement>(null);
	useFocusComposerShortcut(composerRef, true);
	const [inline, setInline] = useState({ source: "", live: true });
	const [kit, setKit] = useState<GenUiKitNotice | undefined>(undefined);
	const [form, setForm] = useState<string | undefined>(undefined);
	Object.assign(window, {
		inlineFixture: {
			stream: (source: string, live: boolean) => setInline({ source, live }),
			kit: setKit,
			form: setForm,
			focusBinding: (value: string) => setNarratorShortcut("focusComposer", value),
		},
	});
	return (
		<I18nextProvider i18n={i18n}>
			<MantineProvider
				theme={mantineTheme}
				cssVariablesResolver={v8CssVariablesResolver}
				forceColorScheme="light"
			>
				<RenderLodCtx.Provider value={{ lod: 5, interactive: true }}>
					<div id="card" style={{ width: "min(720px, 100%)" }}>
						<GenUICard
							narratorId="n1"
							file={null}
							inline={{ ...inline, title: "Plans", formToolUseId: form, stateKey: "use-1" }}
							kit={kit}
							height={400}
						/>
					</div>
					<button type="button" id="after-preview">
						After preview
					</button>
					<textarea
						id="composer"
						ref={composerRef}
						data-narrator-composer="n1"
						aria-label="Composer"
					/>
				</RenderLodCtx.Provider>
			</MantineProvider>
		</I18nextProvider>
	);
}
createRoot(document.getElementById("root") as HTMLElement).render(<Fixture />);
