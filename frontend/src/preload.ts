import { contextBridge, ipcRenderer } from "electron";
import type { BrowserNavState, BrowserRect } from "./main/browser-view-host";
import type { DaemonStatus } from "./shared/daemon-status";
import type { TelemetryBootstrap } from "./shared/telemetry";
import type { MigrationState } from "./main/app-state";
import type { NativeNotificationClickPayload, NativeNotificationInput } from "./main/native-notifications";
import type { OpenInTargets } from "./main/open-in-targets";
import type { RunXcodegenResult } from "./main/run-xcodegen";
import type { UpdateSettings, UpdateStatus } from "./main/update-settings";
import type { CompanionSettings } from "./main/companion-settings";

export type BrowserBoundsInput = {
	viewId: string;
	rect: BrowserRect;
	visible: boolean;
};

export type BrowserNavigateInput = {
	viewId: string;
	url: string;
};

export type ImportFolderMode = "project" | "workspace";

export type ImportRepoScan = {
	name: string;
	path: string;
	relativePath: string;
	branch: string;
	remote: string;
	hasRemote: boolean;
	status?: "ok" | "error";
	reason?: string;
};

export type ImportFolderScan = {
	path: string;
	repos: ImportRepoScan[];
};

const api = {
	app: {
		getVersion: () => ipcRenderer.invoke("app:getVersion") as Promise<string>,
		chooseDirectory: (title?: string) => ipcRenderer.invoke("app:chooseDirectory", title) as Promise<string | null>,
		scanImportFolder: (input: { path: string; mode: ImportFolderMode }) =>
			ipcRenderer.invoke("app:scanImportFolder", input) as Promise<ImportFolderScan>,
	},
	clipboard: {
		writeText: (text: string) => ipcRenderer.invoke("clipboard:writeText", text) as Promise<void>,
		readText: () => ipcRenderer.invoke("clipboard:readText") as Promise<string>,
	},
	openIn: {
		detectTargets: (dir: string) => ipcRenderer.invoke("openIn:detectTargets", dir) as Promise<OpenInTargets>,
		finder: (dir: string) => ipcRenderer.invoke("openIn:finder", dir) as Promise<void>,
		terminal: (dir: string) => ipcRenderer.invoke("openIn:terminal", dir) as Promise<void>,
		editor: (dir: string) => ipcRenderer.invoke("openIn:editor", dir) as Promise<void>,
		xcode: (targetPath: string) => ipcRenderer.invoke("openIn:xcode", targetPath) as Promise<void>,
		androidStudio: (dir: string) => ipcRenderer.invoke("openIn:androidStudio", dir) as Promise<void>,
		xcodegen: (dir: string) => ipcRenderer.invoke("openIn:xcodegen", dir) as Promise<RunXcodegenResult>,
	},
	shell: {
		openExternal: (url: string) => ipcRenderer.invoke("shell:openExternal", url) as Promise<void>,
		// Reveal a local file in Finder (selects it); Open launches it in the OS
		// default app for its type. Used by the Tests tab to surface stored evidence.
		showItemInFolder: (path: string) => ipcRenderer.invoke("shell:showItemInFolder", path) as Promise<void>,
		openPath: (path: string) => ipcRenderer.invoke("shell:openPath", path) as Promise<void>,
	},
	daemon: {
		getStatus: () => ipcRenderer.invoke("daemon:getStatus") as Promise<DaemonStatus>,
		start: () => ipcRenderer.invoke("daemon:start") as Promise<DaemonStatus>,
		stop: () => ipcRenderer.invoke("daemon:stop") as Promise<DaemonStatus>,
		onStatus: (listener: (status: DaemonStatus) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, status: DaemonStatus) => listener(status);
			ipcRenderer.on("daemon:status", wrapped);
			return () => {
				ipcRenderer.off("daemon:status", wrapped);
			};
		},
	},
	telemetry: {
		getBootstrap: () => ipcRenderer.invoke("telemetry:getBootstrap") as Promise<TelemetryBootstrap | null>,
	},
	browser: {
		ensure: (sessionId: string) => ipcRenderer.invoke("browser:ensure", sessionId) as Promise<BrowserNavState>,
		setBounds: (input: BrowserBoundsInput) => ipcRenderer.send("browser:setBounds", input),
		navigate: (input: BrowserNavigateInput) =>
			ipcRenderer.invoke("browser:navigate", input) as Promise<BrowserNavState>,
		clear: (viewId: string) => ipcRenderer.invoke("browser:clear", viewId) as Promise<BrowserNavState>,
		goBack: (viewId: string) => ipcRenderer.invoke("browser:goBack", viewId) as Promise<BrowserNavState>,
		goForward: (viewId: string) => ipcRenderer.invoke("browser:goForward", viewId) as Promise<BrowserNavState>,
		reload: (viewId: string) => ipcRenderer.invoke("browser:reload", viewId) as Promise<BrowserNavState>,
		stop: (viewId: string) => ipcRenderer.invoke("browser:stop", viewId) as Promise<BrowserNavState>,
		destroy: (viewId: string) => ipcRenderer.send("browser:destroy", viewId),
		onNavState: (listener: (state: BrowserNavState) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, state: BrowserNavState) => listener(state);
			ipcRenderer.on("browser:navState", wrapped);
			return () => {
				ipcRenderer.off("browser:navState", wrapped);
			};
		},
	},
	notifications: {
		show: (notification: NativeNotificationInput) =>
			ipcRenderer.invoke("notifications:show", notification) as Promise<void>,
		onClick: (listener: (payload: NativeNotificationClickPayload) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, payload: NativeNotificationClickPayload) => listener(payload);
			ipcRenderer.on("notifications:click", wrapped);
			return () => {
				ipcRenderer.off("notifications:click", wrapped);
			};
		},
	},
	appState: {
		getMigration: () => ipcRenderer.invoke("appState:getMigration") as Promise<MigrationState>,
		setMigration: (migration: MigrationState) =>
			ipcRenderer.invoke("appState:setMigration", migration) as Promise<void>,
	},
	updateSettings: {
		get: () => ipcRenderer.invoke("updateSettings:get") as Promise<UpdateSettings>,
		set: (settings: UpdateSettings) => ipcRenderer.invoke("updateSettings:set", settings) as Promise<void>,
	},
	companionSettings: {
		get: () => ipcRenderer.invoke("companionSettings:get") as Promise<CompanionSettings>,
		set: (settings: CompanionSettings) => ipcRenderer.invoke("companionSettings:set", settings) as Promise<void>,
	},
	companion: {
		// Right-clicking a Proc on the desktop asks for THIS window, on that session.
		onOpenPetLibrary: (listener: (sessionId: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, sessionId: string) => listener(sessionId);
			ipcRenderer.on("companion:openPetLibrary", wrapped);
			return () => {
				ipcRenderer.off("companion:openPetLibrary", wrapped);
			};
		},
		// A look was chosen here. The value is already in the localStorage both
		// windows share; this only asks the overlay to go and read it.
		looksChanged: () => ipcRenderer.send("companion:looksChanged"),
	},
	updates: {
		getStatus: () => ipcRenderer.invoke("updates:getStatus") as Promise<UpdateStatus>,
		check: () => ipcRenderer.invoke("updates:check") as Promise<void>,
		download: () => ipcRenderer.invoke("updates:download") as Promise<void>,
		install: () => ipcRenderer.invoke("updates:install") as Promise<void>,
		onStatus: (listener: (status: UpdateStatus) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, status: UpdateStatus) => listener(status);
			ipcRenderer.on("updates:status", wrapped);
			return () => {
				ipcRenderer.off("updates:status", wrapped);
			};
		},
	},
};

contextBridge.exposeInMainWorld("ao", api);

export type AoBridge = typeof api;
