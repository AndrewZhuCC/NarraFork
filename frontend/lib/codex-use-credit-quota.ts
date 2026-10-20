export interface CodexCredentialEditForm {
	displayName: string;
	priority: number;
	useCreditQuota: boolean;
}

export function codexCredentialEditForm(entry: {
	displayName?: string;
	priority: number;
	useCreditQuota?: boolean;
}): CodexCredentialEditForm {
	return {
		displayName: entry.displayName ?? "",
		priority: entry.priority,
		useCreditQuota: entry.useCreditQuota ?? false,
	};
}

/** Only a transition to paid-credit usage requires explicit consent. */
export async function saveCodexCredentialEdit(
	currentUseCreditQuota: boolean | undefined,
	form: CodexCredentialEditForm,
	confirm: () => Promise<boolean>,
	save: (form: CodexCredentialEditForm) => void,
): Promise<boolean> {
	if (form.useCreditQuota && !currentUseCreditQuota && !(await confirm())) return false;
	save(form);
	return true;
}
