/**
 * Compare two app build version strings, e.g. "0.9.0+fe6657f" vs
 * "0.9.0+fe6657f5a48086b931639c1a03bebdc61c87e9b4".
 *
 * Both the frontend bundle and the server append the git commit as semver
 * build metadata, but the build paths disagree on hash length: vite.config.ts
 * and local builds use `git rev-parse --short HEAD`, while release-CI server
 * binaries embed the full 40-char commit (build-cross-platform.ts
 * --release-ci). A short hash is by definition a prefix of the full one, so
 * commit parts match when either is a prefix of the other.
 *
 * A missing commit part only matches a missing one: a build without commit
 * metadata cannot be proven identical to one that has it.
 */
export function appVersionsMatch(a: string, b: string): boolean {
	const parse = (version: string) => {
		const [base, commit] = version.trim().replace(/^v/, "").split("+");
		return { base, commit: commit || undefined };
	};
	const va = parse(a);
	const vb = parse(b);
	if (va.base !== vb.base) return false;
	if (va.commit === undefined || vb.commit === undefined) return va.commit === vb.commit;
	return (
		va.commit === vb.commit || va.commit.startsWith(vb.commit) || vb.commit.startsWith(va.commit)
	);
}
