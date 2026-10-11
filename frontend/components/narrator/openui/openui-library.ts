import { generatePrompt } from "@openuidev/react-lang";
// Share ThemeContext with OpenUIBlock's ThemeProvider, including portalled controls.
import { openuiChatLibrary } from "@openuidev/react-ui";

// ponytail: 0.17.0 registers SwitchGroup but omits it from FormControl's union.
// Correct the public spec rather than rebuilding the SDK's shared Zod references;
// this becomes a no-op once upstream includes SwitchGroup in the union.
const spec = openuiChatLibrary.toSpec();
const schema = openuiChatLibrary.toJSONSchema();
const formSchema = schema.$defs?.FormControl as {
	properties: { input: { anyOf: { $ref: string }[] } };
};
const inputs = formSchema.properties.input.anyOf;
if (!inputs.some((input) => input.$ref === "#/$defs/SwitchGroup")) {
	inputs.push({ $ref: "#/$defs/SwitchGroup" });
	spec.components.FormControl.signature = spec.components.FormControl.signature.replace(
		", hint?:",
		" | SwitchGroup, hint?:",
	);
}

export const narratorOpenUIChatLibrary: typeof openuiChatLibrary = {
	...openuiChatLibrary,
	toJSONSchema: () => schema,
	toSpec: () => ({ ...spec, schema }),
	prompt: (options) =>
		generatePrompt({
			root: spec.root,
			components: spec.components,
			componentGroups: spec.componentGroups,
			...options,
		}),
};
