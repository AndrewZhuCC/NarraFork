/**
 * 哪些界面显示 Git 提交身份入口。
 *
 * 这是「本人在这个浏览器上想看到什么」的显示偏好，不是提交身份本身：身份存在服务端并按
 * 用户 × 会话绑定，这里只决定入口出现在哪些界面。默认全部关闭，两个开关互相独立。
 * 存储走 useLocalPref：每个开关一条 key，跨窗口通过 storage 事件更新，写的时候不会用
 * 过期的内存快照把另一个开关盖掉。
 */

import { useLocalPref } from "./useLocalPref";

export interface GitIdentityVisibility {
	/** 叙述者列表（左侧边栏）中会话的右键菜单。 */
	contextMenu: boolean;
	/** 新建会话弹窗右侧、默认模型下方。 */
	createModal: boolean;
}

export function useGitIdentityVisibility(): GitIdentityVisibility & {
	setContextMenu: (value: boolean) => void;
	setCreateModal: (value: boolean) => void;
} {
	const [contextMenu, setContextMenu] = useLocalPref("narrafork_git_identity_context_menu");
	const [createModal, setCreateModal] = useLocalPref("narrafork_git_identity_create_modal");
	return { contextMenu, createModal, setContextMenu, setCreateModal };
}
