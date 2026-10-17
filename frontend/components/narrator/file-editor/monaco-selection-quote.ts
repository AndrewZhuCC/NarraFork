import type { FileSelection } from "@shared/file-reference";
import type { editor, IRange } from "monaco-editor/editor/editor.api";
import { createEditorLocalId } from "./editor-local-id";
import type { MonacoAPI } from "./monaco-loader";
import { monacoFileSelection } from "./monaco-navigation";

interface QuoteState {
	owner: string;
	label: string;
	onQuoteSelection?: (selection: FileSelection) => void;
	disabledReason?: string | null;
}

/** A native glyph widget: no content decorations, view zones, keybindings or React selection state. */
export class MonacoSelectionQuote {
	private readonly node: HTMLDivElement;
	private readonly button: HTMLButtonElement;
	private readonly widget: editor.IGlyphMarginWidget;
	private range: IRange = { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 1 };
	private attached = false;
	private disposed = false;
	private owner: string;
	private ownerSelectionValid = true;
	private revision = -1;
	private marginReserved: boolean;

	constructor(
		api: MonacoAPI,
		private readonly view: editor.IStandaloneCodeEditor,
		private readonly model: editor.ITextModel,
		document: Document,
		private readonly state: () => QuoteState,
	) {
		const initial = state();
		this.owner = initial.owner;
		this.marginReserved = !!initial.onQuoteSelection;
		this.node = document.createElement("div");
		this.node.className = "nf-monaco-selection-quote";
		this.button = document.createElement("button");
		this.button.type = "button";
		this.button.className = "nf-monaco-selection-quote-button";
		this.button.setAttribute("data-monaco-quote-selection", "");
		const icon = document.createElement("span");
		icon.setAttribute("aria-hidden", "true");
		icon.textContent = "“";
		this.button.appendChild(icon);
		this.node.appendChild(this.button);
		const id = `selection-quote-${createEditorLocalId()}`;
		this.widget = {
			getId: () => id,
			getDomNode: () => this.node,
			getPosition: () => ({
				lane: api.editor.GlyphMarginLane.Center,
				zIndex: 100,
				range: this.range,
			}),
		};
		this.button.addEventListener("pointerdown", this.preserveSelection);
		this.button.addEventListener("mousedown", this.preserveSelection);
		this.button.addEventListener("click", this.quote);
	}

	private readonly preserveSelection = (event: Event) => {
		event.preventDefault();
		event.stopPropagation();
	};

	private readonly quote = (event: Event) => {
		event.preventDefault();
		event.stopPropagation();
		const current = this.state();
		// Never quote a cached range, another model/file, or a model changed since the widget synced.
		if (
			this.disposed ||
			!this.attached ||
			current.owner !== this.owner ||
			!current.onQuoteSelection ||
			current.disabledReason ||
			this.view.getModel() !== this.model ||
			this.model.getVersionId() !== this.revision
		)
			return;
		const selection = monacoFileSelection(this.view.getSelection());
		if (selection) current.onQuoteSelection(selection);
	};

	/** Explicit file reloads must not inherit a selection belonging to the previous bytes. */
	invalidate(): void {
		this.ownerSelectionValid = false;
		this.remove();
	}

	/** Called directly on cursor movement, never publishing high-frequency selection into React. */
	sync(selectionChanged = false): void {
		if (this.disposed) return;
		const current = this.state();
		if (this.owner !== current.owner) {
			this.owner = current.owner;
			this.invalidate();
		}
		if (selectionChanged) this.ownerSelectionValid = true;
		// Callback availability may change after mount. Reserve once, never resize while selecting.
		if (current.onQuoteSelection && !this.marginReserved) {
			this.marginReserved = true;
			this.view.updateOptions({ glyphMargin: true });
		}
		const selection = monacoFileSelection(this.view.getSelection());
		if (
			!current.onQuoteSelection ||
			!this.ownerSelectionValid ||
			this.view.getModel() !== this.model ||
			!selection
		) {
			this.remove();
			return;
		}
		this.button.disabled = !!current.disabledReason;
		this.button.title = current.disabledReason || current.label;
		this.button.setAttribute("aria-label", current.label);
		this.range = selection;
		this.revision = this.model.getVersionId();
		if (!this.attached) {
			this.attached = true;
			this.view.addGlyphMarginWidget(this.widget);
		} else this.view.layoutGlyphMarginWidget(this.widget);
	}

	private remove(): void {
		if (!this.attached) return;
		this.attached = false;
		this.view.removeGlyphMarginWidget(this.widget);
	}

	dispose(): void {
		this.disposed = true;
		this.remove();
		this.button.removeEventListener("pointerdown", this.preserveSelection);
		this.button.removeEventListener("mousedown", this.preserveSelection);
		this.button.removeEventListener("click", this.quote);
		this.node.remove();
	}
}
