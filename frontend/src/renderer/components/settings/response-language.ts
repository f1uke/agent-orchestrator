// Shared options for the human-facing response-language setting, used by both the
// Global scope (the shipped default) and the Project scope (a per-project
// override). The value stored is the language NAME string; the backend injects it
// verbatim into the always-on directive that forces each agent's human-facing
// prose into that language while keeping code/commits/PRs in English.
//
// English is the shipped default and injects no directive (behavior unchanged).
// The list is a sensible common set; the backend accepts any free-form value, so
// this can grow without a server change.
export const RESPONSE_LANGUAGE_OPTIONS = [
	"English",
	"Thai",
	"Japanese",
	"Korean",
	"Chinese (Simplified)",
	"Chinese (Traditional)",
	"Vietnamese",
	"Indonesian",
	"Spanish",
	"Portuguese",
	"French",
	"German",
	"Italian",
	"Russian",
	"Hindi",
	"Arabic",
] as const;

// The value used for "no per-project override" in the Project scope select. Empty
// string maps to an omitted config field, so the project inherits the global
// default at prompt-assembly time.
export const INHERIT_GLOBAL_VALUE = "";
