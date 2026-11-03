import { createContext, type ReactNode, useContext } from "react";

/** Opens the inspector's Tasks tab on one job, by its call id or task id, or on the list when given null. */
export type OpenTasks = (ref: string | null) => void;

const TasksContext = createContext<OpenTasks | undefined>(undefined);

/** Where a `Ran agent` row, the status line's count and the Waiting on row send a click. Absent, they stay inert. */
export function ChatTasksProvider({ onOpen, children }: { onOpen?: OpenTasks; children: ReactNode }) {
	return <TasksContext.Provider value={onOpen}>{children}</TasksContext.Provider>;
}

export const useOpenTasks = (): OpenTasks | undefined => useContext(TasksContext);
