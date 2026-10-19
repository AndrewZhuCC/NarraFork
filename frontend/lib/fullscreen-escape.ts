import { type RefObject, useEffect } from "react";
import classes from "./fullscreen-escape.module.css";

/**
 * CSS 全屏浮层的祖先逃逸:全屏期间中和会困住 `position: fixed` 后代的祖先效果。
 *
 * 全屏编辑器不能用 portal(portal 会搬动 DOM,丢掉 ProseMirror 的光标与撤销栈),
 * 只能在原地 `position: fixed`。但祖先上的 `isolation: isolate`、`transform`、
 * `contain`、定位 + `z-index` 等会创建层叠上下文或 fixed 包含块,使浮层画在
 * AppShell 的导航栏/顶栏之下 —— 这正是"全屏被左侧和顶部导航挡住"的根因。
 *
 * 该 hook 在全屏激活时从浮层向上遍历到 body,为产生这些效果的祖先打上中和类
 * (见 fullscreen-escape.module.css),退出/卸载时逐个移除。检测基于 getComputedStyle,
 * 覆盖的祖先来源:isolation、transform、filter、backdrop-filter、perspective、
 * will-change(上列各项)、contain 的 layout/paint 系列、content-visibility、
 * container-type、以及定位/flex/grid 子项上的 z-index。
 *
 * 已知不覆盖的来源(中和它们会改变视觉或布局):opacity < 1、position: fixed/sticky、
 * mix-blend-mode、clip-path/mask、will-change: opacity。独立的 translate/rotate/scale
 * 属性也不覆盖:实测 Chrome 里用样式表类把 rotate/scale 设为 none 反而会让 transform
 * 计算值变成单位矩阵(等价于制造一个包含块),越中和越糟。当前祖先链上不存在这些形态;
 * 若未来引入,浮层仍会困在该祖先内,需要单独评估。
 */
export function useFullscreenEscape(ref: RefObject<HTMLElement | null>, active: boolean) {
	useEffect(() => {
		if (!active) return;
		const element = ref.current;
		// linkedom 等测试 DOM 没有 getComputedStyle:无真实绘制,无需逃逸。
		const view = element?.ownerDocument.defaultView;
		if (!element || !view?.getComputedStyle) return;
		const tagged: HTMLElement[] = [];
		for (
			let node = element.parentElement;
			node && node !== element.ownerDocument.body;
			node = node.parentElement
		) {
			const style = view.getComputedStyle(node);
			// 任一项都会创建层叠上下文或 fixed 包含块(样式细节见 stylesheet 头注释)。
			const traps =
				style.isolation === "isolate" ||
				style.transform !== "none" ||
				style.filter !== "none" ||
				style.backdropFilter !== "none" ||
				style.perspective !== "none" ||
				/\b(?:transform|filter|perspective|contain)\b/.test(style.willChange) ||
				// contain 的 size/style 分量不构成包含块,只中和 layout/paint 系列。
				/\b(?:layout|paint|strict|content)\b/.test(style.contain) ||
				style.contentVisibility !== "visible" ||
				style.containerType !== "normal" ||
				// 定位元素或 flex/grid 子项上的 z-index 也会建层叠上下文;静态元素上的
				// z-index 无效,中和它无副作用。
				style.zIndex !== "auto";
			if (traps) {
				node.classList.add(classes.escape);
				tagged.push(node);
			}
		}
		return () => {
			for (const node of tagged) node.classList.remove(classes.escape);
		};
	}, [ref, active]);
}
