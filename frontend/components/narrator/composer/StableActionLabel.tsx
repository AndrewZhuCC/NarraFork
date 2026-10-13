import classes from "./StableActionLabel.module.css";

/** Keep translated idle candidates in one intrinsic-width slot, not a guessed px minimum. */
export function StableActionLabel({
	label,
	candidates,
}: {
	label: string;
	candidates: readonly string[];
}) {
	return (
		<span className={classes.label} data-stable-action-label>
			{[...new Set(candidates)].map((candidate) => (
				<span key={candidate} className={classes.sizer} data-label={candidate} aria-hidden="true" />
			))}
			<span className={classes.actual}>{label}</span>
		</span>
	);
}
