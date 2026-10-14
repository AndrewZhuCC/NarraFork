import {
	closestCenter,
	DndContext,
	type DragEndEvent,
	MouseSensor,
	TouchSensor,
	useSensor,
	useSensors,
} from "@dnd-kit/core";
import {
	arrayMove,
	horizontalListSortingStrategy,
	SortableContext,
	useSortable,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Box, Group, Menu, Portal, UnstyledButton } from "@mantine/core";
import {
	IconChecklist,
	IconFileText,
	IconLock,
	IconPin,
	IconPinFilled,
	IconPinnedOff,
} from "@tabler/icons-react";
import { type CSSProperties, useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { restrictToHorizontalAxis } from "../../../lib/dnd-modifiers";
import { attachTabStripWheel } from "../../terminal/tab-strip-wheel";
import type { SpecTabGroup } from "./spec-tab-order";

export interface SpecTabChip {
	uri: string;
	path: string;
	label: string;
	kind: "tasks" | "doc";
	/** 文档对助手只读（显示锁图标）。 */
	locked?: boolean;
	/** 用户置顶的文档（显示图钉）。 */
	pinned?: boolean;
	/** 内置的 tasks/行为护栏/index 不可拖拽、不可置顶。 */
	movable: boolean;
}

interface SpecFileTabStripProps {
	chips: SpecTabChip[];
	activeUri: string;
	onSelect: (uri: string) => void;
	/** 拖拽结束时回调该组拖后的完整路径顺序。 */
	onReorder: (group: SpecTabGroup, paths: string[]) => void;
	onTogglePin: (path: string, pinned: boolean) => void;
}

const SPEC_URI_PREFIX = "spec://";

const uriToPath = (uri: string) => uri.slice(SPEC_URI_PREFIX.length);

/** 与 Mantine SegmentedControl(size="xs") 一致的标签外观。 */
function chipStyle(active: boolean, dragging: boolean): CSSProperties {
	return {
		padding: "2px 6px",
		fontSize: "var(--mantine-font-size-xs)",
		fontWeight: 500,
		whiteSpace: "nowrap",
		userSelect: "none",
		borderRadius:
			"max(calc(var(--mantine-radius-default) - 4px), calc(var(--mantine-radius-default) / 4))",
		color: active
			? "light-dark(var(--mantine-color-black), var(--mantine-color-white))"
			: "light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))",
		background: active
			? "light-dark(var(--mantine-color-white), var(--mantine-color-dark-5))"
			: "transparent",
		opacity: dragging ? 0.5 : 1,
	};
}

function chipLabel(chip: SpecTabChip) {
	return (
		<Group gap={4} wrap="nowrap">
			{chip.kind === "tasks" ? (
				<IconChecklist size={13} />
			) : chip.locked ? (
				<IconLock size={12} />
			) : (
				<IconFileText size={13} />
			)}
			<span>{chip.label}</span>
			{chip.pinned && <IconPinFilled size={11} />}
		</Group>
	);
}

function FixedChip({
	chip,
	active,
	onSelect,
}: {
	chip: SpecTabChip;
	active: boolean;
	onSelect: () => void;
}) {
	return (
		<UnstyledButton onClick={onSelect} style={chipStyle(active, false)}>
			{chipLabel(chip)}
		</UnstyledButton>
	);
}

function SortableChip({
	chip,
	active,
	onSelect,
	onOpenMenu,
}: {
	chip: SpecTabChip;
	active: boolean;
	onSelect: () => void;
	onOpenMenu: (x: number, y: number) => void;
}) {
	const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
		id: chip.uri,
	});
	return (
		<UnstyledButton
			ref={setNodeRef}
			{...attributes}
			{...listeners}
			onClick={onSelect}
			onContextMenu={(event) => {
				event.preventDefault();
				event.stopPropagation();
				onOpenMenu(event.clientX, event.clientY);
			}}
			style={{
				...chipStyle(active, isDragging),
				transform: CSS.Transform.toString(transform),
				transition,
			}}
		>
			{chipLabel(chip)}
		</UnstyledButton>
	);
}

/**
 * 大纲文件标签条：内置三项固定在前，其余文档可拖拽排序、右键置顶。
 *
 * 拖拽只在各自组内生效（跨组落点会被忽略），分组边界由右键置顶/取消置顶改变。
 */
export function SpecFileTabStrip({
	chips,
	activeUri,
	onSelect,
	onReorder,
	onTogglePin,
}: SpecFileTabStripProps) {
	const { t } = useTranslation("narrator");
	const scrollRef = useRef<HTMLDivElement>(null);
	const [menu, setMenu] = useState<{ x: number; y: number; chip: SpecTabChip } | null>(null);
	// 鼠标需要拖动 5px 才算拖拽；触摸需要长按 300ms，避免和横向滚动抢手势。
	const sensors = useSensors(
		useSensor(MouseSensor, { activationConstraint: { distance: 5 } }),
		useSensor(TouchSensor, { activationConstraint: { delay: 300, tolerance: 5 } }),
	);

	// 隐藏滚动条后仍可用滚轮横向浏览溢出的标签。
	useEffect(() => {
		const element = scrollRef.current;
		if (!element) return;
		return attachTabStripWheel(element);
	}, []);

	const fixedChips = chips.filter((chip) => !chip.movable);
	const pinnedChips = chips.filter((chip) => chip.movable && chip.pinned);
	const restChips = chips.filter((chip) => chip.movable && !chip.pinned);
	const pinnedIds = pinnedChips.map((chip) => chip.uri);
	const restIds = restChips.map((chip) => chip.uri);

	const handleDragEnd = useCallback(
		(event: DragEndEvent) => {
			const activeId = String(event.active.id);
			const overId = event.over ? String(event.over.id) : null;
			if (!overId || overId === activeId) return;
			const group: SpecTabGroup | null = pinnedIds.includes(activeId)
				? "pinned"
				: restIds.includes(activeId)
					? "order"
					: null;
			if (!group) return;
			const groupIds = group === "pinned" ? pinnedIds : restIds;
			if (!groupIds.includes(overId)) return;
			const next = arrayMove(groupIds, groupIds.indexOf(activeId), groupIds.indexOf(overId));
			onReorder(
				group,
				next.map((uri) => uriToPath(uri)),
			);
		},
		[pinnedIds, restIds, onReorder],
	);

	const renderSortable = (chip: SpecTabChip) => (
		<SortableChip
			key={chip.uri}
			chip={chip}
			active={chip.uri === activeUri}
			onSelect={() => onSelect(chip.uri)}
			onOpenMenu={(x, y) => setMenu({ x, y, chip })}
		/>
	);

	return (
		<>
			<Box ref={scrollRef} style={{ overflowX: "auto", scrollbarWidth: "none" }}>
				<Box
					style={{
						display: "inline-flex",
						padding: 4,
						borderRadius: "var(--mantine-radius-default)",
						background: "light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-8))",
					}}
				>
					{fixedChips.map((chip) => (
						<FixedChip
							key={chip.uri}
							chip={chip}
							active={chip.uri === activeUri}
							onSelect={() => onSelect(chip.uri)}
						/>
					))}
					<DndContext
						sensors={sensors}
						collisionDetection={closestCenter}
						modifiers={[restrictToHorizontalAxis]}
						onDragEnd={handleDragEnd}
					>
						<SortableContext items={pinnedIds} strategy={horizontalListSortingStrategy}>
							{pinnedChips.map(renderSortable)}
						</SortableContext>
						<SortableContext items={restIds} strategy={horizontalListSortingStrategy}>
							{restChips.map(renderSortable)}
						</SortableContext>
					</DndContext>
				</Box>
			</Box>
			{menu && (
				<Menu
					opened
					onChange={(opened) => {
						if (!opened) setMenu(null);
					}}
					position="bottom-start"
					withinPortal
				>
					{/* 用点击坐标做锚点：client 坐标是视口坐标，必须穿过 Portal 才不会受祖先变换影响。 */}
					<Portal>
						<Menu.Target>
							<span
								style={{
									position: "fixed",
									left: menu.x,
									top: menu.y,
									width: 0,
									height: 0,
									pointerEvents: "none",
								}}
							/>
						</Menu.Target>
					</Portal>
					<Menu.Dropdown>
						<Menu.Item
							leftSection={menu.chip.pinned ? <IconPinnedOff size={14} /> : <IconPin size={14} />}
							onClick={() => {
								onTogglePin(menu.chip.path, !menu.chip.pinned);
								setMenu(null);
							}}
						>
							{menu.chip.pinned ? t("spec.unpinTab") : t("spec.pinTab")}
						</Menu.Item>
					</Menu.Dropdown>
				</Menu>
			)}
		</>
	);
}
