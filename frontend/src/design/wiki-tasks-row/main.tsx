import React from "react";
import { createRoot } from "react-dom/client";
import "../../renderer/styles.css";
import "./preview.css";
import "./wiki-tasks-row-next.css";
import { Preview } from "./Preview";

createRoot(document.getElementById("root") as HTMLElement).render(
	<React.StrictMode>
		<Preview />
	</React.StrictMode>,
);
