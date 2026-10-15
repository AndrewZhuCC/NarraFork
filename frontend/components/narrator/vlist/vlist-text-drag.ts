/**
 * vlist-text-drag.ts — Refuse the browser's native drag of a text selection.
 *
 * Selecting message text and then pressing on that selection starts an HTML5
 * drag, not a new selection. The browser takes the gesture over with
 * `pointercancel` and may never deliver `mouseup` or `dragend` — especially
 * once a virtualized row is replaced under the pointer, or once an ancestor
 * drop target has called `preventDefault` on `dragover`. The page is then
 * stuck inside that drag session: clicks and keystrokes stop reaching the UI
 * until a reload.
 *
 * Cancelling `dragstart` before the drag session begins leaves the pointer
 * sequence intact (`pointerup` / `mouseup` still fire) and leaves copy,
 * caret placement, and an ordinary drag-selection alone.
 */

export interface TextSelectionDragProbe {
	defaultPrevented: boolean;
	/** Event target is inside this list. */
	targetInRoot: boolean;
	/** Press landed on an explicit drag source or an editable control. */
	explicitDragSource: boolean;
	/** A non-collapsed selection touches this list. */
	selectionInRoot: boolean;
}

/**
 * Whether this `dragstart` is the browser dragging a text selection out of the
 * list. Explicit `[draggable]` sources and editable controls keep their drag.
 */
export function shouldCancelTextSelectionDrag(probe: TextSelectionDragProbe): boolean {
	if (probe.defaultPrevented) return false;
	if (!probe.targetInRoot || probe.explicitDragSource) return false;
	return probe.selectionInRoot;
}

const EXPLICIT_DRAG_SOURCE =
	"[draggable='true'], input, textarea, select, [contenteditable='true']";

const ELEMENT_NODE = 1;

function isDomNode(value: EventTarget | null): value is Node {
	return !!value && typeof value === "object" && "nodeType" in value;
}

function nodeInside(root: Node, node: Node | null): boolean {
	if (!node) return false;
	const element = node.nodeType === ELEMENT_NODE ? node : node.parentNode;
	return element != null && root.contains(element);
}

/** Read the live drag and the document selection into the pure probe. */
export function probeTextSelectionDrag(event: Event, root: Node): TextSelectionDragProbe {
	const targetNode = isDomNode(event.target) ? event.target : null;
	const element =
		targetNode == null
			? null
			: targetNode.nodeType === ELEMENT_NODE
				? (targetNode as Element)
				: targetNode.parentElement;
	const doc = root.ownerDocument;
	const selection = doc?.getSelection?.() ?? null;
	const selectionInRoot =
		!!selection &&
		!selection.isCollapsed &&
		selection.rangeCount > 0 &&
		(nodeInside(root, selection.anchorNode) || nodeInside(root, selection.focusNode));
	return {
		defaultPrevented: event.defaultPrevented,
		targetInRoot: nodeInside(root, targetNode),
		explicitDragSource: !!element?.closest(EXPLICIT_DRAG_SOURCE),
		selectionInRoot,
	};
}

/**
 * Install the guard for one list root.
 *
 * Document capture, and not passive: it has to run before a drop target accepts
 * the drag, and `preventDefault` is what keeps the browser from opening the
 * session. Returns a disposer.
 */
export function installVListTextDragGuard(root: Node): () => void {
	const doc = root.ownerDocument;
	if (!doc) return () => {};
	const onDragStart = (event: Event) => {
		if (!shouldCancelTextSelectionDrag(probeTextSelectionDrag(event, root))) return;
		event.preventDefault();
	};
	doc.addEventListener("dragstart", onDragStart, true);
	return () => doc.removeEventListener("dragstart", onDragStart, true);
}
