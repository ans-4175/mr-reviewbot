// reviewbot guard for pi: pi's tools have no path restriction, so block every call that could
// reach outside the checkout (pi's cwd). Fails closed: anything unexpected is blocked.
import { realpathSync } from "node:fs";
import { dirname, basename, join, resolve, sep } from "node:path";

const ALLOWED = new Set(["read", "grep", "find", "ls"]);

function real(p: string): string {
	try {
		return realpathSync(p);
	} catch {
		const parent = dirname(p);
		return parent === p ? p : join(real(parent), basename(p)); // a missing file: resolve its parents
	}
}

export default function (pi: any) {
	const root = realpathSync(process.cwd());
	const inside = (p: string) => {
		const r = real(resolve(root, p));
		return r === root || r.startsWith(root + sep);
	};
	const deny = (why: string) => ({ block: true, reason: "reviewbot: " + why });

	pi.on("tool_call", async (event: any) => {
		try {
			const tool = event.toolName;
			if (!ALLOWED.has(tool)) return deny(`tool ${tool} is not allowed`);
			const input = event.input ?? {};
			if (input.path !== undefined) {
				const p = String(input.path).replace(/^@/, "");
				if (p.startsWith("~") || !inside(p)) return deny("path outside the checkout");
			}
			// Globs can climb out too; grep's pattern is a regex, not a path.
			for (const key of tool === "grep" ? ["glob"] : ["pattern", "glob"]) {
				const v = input[key];
				if (typeof v === "string" && (v.includes("..") || v.startsWith("/") || v.startsWith("~"))) {
					return deny(`${key} outside the checkout`);
				}
			}
			return undefined;
		} catch (err) {
			return deny("guard error: " + String(err));
		}
	});
}
