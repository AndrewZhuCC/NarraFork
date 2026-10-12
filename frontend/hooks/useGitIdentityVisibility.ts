/**
 * 哪些界面显示 Git 提交身份入口。
 *
 * 这是"本人在这个浏览器上想看到什么"的显示偏好，不是提交身份本身：身份存在服务端并按
 * 用户 × 会话 绑定，这里只决定入口出现在哪些界面。默认全部关闭，两个开关互相独立，可以
 * 同时开启；改动立刻广播给所有正在渲染的组件。
 */

import { useSyncExternalStore } from "react";

export interface GitIdentityVisibility {
	/** 叙述者列表（左侧边栏）中会话的右键菜单。 */
	contextMenu: boolean;
	/** 新建会话弹窗右侧、默认模型下方。 */
	createModal: boolean;
}

const STORAGE_KEY = "narrafork_git_identity_visibility";

const DEFAULT_VISIBILITY: GitIdentityVisibility = { contextMenu: false, createModal: false };

/** 解析存储值：缺失、损坏或类型不符一律回落到"全部关闭"。 */
export function parseGitIdentityVisibility(raw: string | null): GitIdentityVisibility {
	if (!raw) return DEFAULT_VISIBILITY;
	try {
		const parsed = JSON.parse(raw) as Partial<GitIdentityVisibility> | null;
		return {
			contextMenu: parsed?.contextMenu === true,
			createModal: parsed?.createModal === true,
		};
	} catch {
		return DEFAULT_VISIBILITY;
	}
}

function readStored(): GitIdentityVisibility {
	try {
		return parseGitIdentityVisibility(localStorage.getItem(STORAGE_KEY));
	} catch {
		// 无存储环境（测试、隐私模式）按默认关闭处理。
		return DEFAULT_VISIBILITY;
	}
}

let snapshot: GitIdentityVisibility | undefined;
const listeners = new Set<() => void>();

function getSnapshot(): GitIdentityVisibility {
	snapshot ??= readStored();
	return snapshot;
}

function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/** 部分更新：两个开关各自独立，点一次只改一个。 */
export function setGitIdentityVisibility(patch: Partial<GitIdentityVisibility>): void {
	const next = { ...getSnapshot(), ...patch };
	snapshot = next;
	try {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
	} catch {
		// 存储不可用时保留内存态，本次会话仍然生效。
	}
	for (const listener of listeners) listener();
}

export function useGitIdentityVisibility(): GitIdentityVisibility {
	return useSyncExternalStore(subscribe, getSnapshot, () => DEFAULT_VISIBILITY);
}
