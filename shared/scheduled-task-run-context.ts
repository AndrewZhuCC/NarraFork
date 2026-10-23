/** Where a scheduled task's narrator runs. */
export type ScheduledRunContext = "standalone" | "project";

/**
 * Rows written before chapters were removed may carry `"chapter"`; those tasks keep
 * their `projectId`, so they run as project tasks.
 */
export function normalizeRunContext(value: string): ScheduledRunContext {
	return value === "standalone" ? "standalone" : "project";
}
