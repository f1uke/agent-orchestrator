// The two pieces of Monaco's service layer the app touches, which the package
// ships without type declarations. Kept to exactly what is used; see
// `markInlineEditsOnboarded` in lib/inline-completion/provider.ts.

declare module "monaco-editor/editor/standalone/browser/standaloneServices" {
	export const StandaloneServices: {
		get<T>(id: unknown): T;
	};
}

declare module "monaco-editor/platform/storage/common/storage" {
	export const IStorageService: unknown;
}
